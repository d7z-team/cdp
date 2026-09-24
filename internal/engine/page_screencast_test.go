package engine

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"gopkg.d7z.net/cdp/internal/imageutil"
)

func TestWaitScreencastFrameLifecycle(t *testing.T) {
	page := NewPageWithContext(context.Background())
	page.screencast = &pageScreencastState{
		running:   true,
		lastFrame: []byte("first"),
		lastMeta:  ScreencastFrameMetadata{DeviceWidth: 800, DeviceHeight: 600},
		lastSeq:   8,
	}
	page.screencastSeq = 8

	frame, err := page.WaitScreencastFrame(context.Background(), 7, false)
	require.NoError(t, err)
	require.Equal(t, uint64(8), frame.Sequence)
	require.Equal(t, []byte("first"), frame.Data)
	frame.Data[0] = 'x'
	require.Equal(t, []byte("first"), page.screencast.lastFrame)

	result := make(chan ScreencastFrame, 1)
	errorsCh := make(chan error, 1)
	go func() {
		next, waitErr := page.WaitScreencastFrame(context.Background(), 8, false)
		result <- next
		errorsCh <- waitErr
	}()
	require.Eventually(t, func() bool {
		page.screencastMu.Lock()
		defer page.screencastMu.Unlock()
		return len(page.screencast.waiters) == 1
	}, time.Second, time.Millisecond)
	require.NoError(t, page.StopScreencast())
	page.screencastMu.Lock()
	page.screencast = &pageScreencastState{running: true, lastFrame: []byte("second"), lastSeq: 9}
	page.screencastSeq = 9
	page.signalScreencastChangeLocked()
	page.screencastMu.Unlock()
	require.NoError(t, <-errorsCh)
	require.Equal(t, uint64(9), (<-result).Sequence)
}

func TestWaitScreencastFrameCancellationAndPageClose(t *testing.T) {
	page := NewPageWithContext(context.Background())
	page.screencast = &pageScreencastState{running: true, lastFrame: []byte("frame"), lastSeq: 1}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := page.WaitScreencastFrame(ctx, 1, false)
		done <- err
	}()
	require.Eventually(t, func() bool {
		page.screencastMu.Lock()
		defer page.screencastMu.Unlock()
		return len(page.screencast.waiters) == 1
	}, time.Second, time.Millisecond)
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	page.screencastMu.Lock()
	require.Empty(t, page.screencast.waiters)
	page.screencastMu.Unlock()

	pageCtx, closePage := context.WithCancel(context.Background())
	closedPage := NewPageWithContext(pageCtx)
	closePage()
	_, err := closedPage.WaitScreencastFrame(context.Background(), 0, false)
	require.True(t, errors.Is(err, ErrBrowserClosed))
}

func TestScreencastReadinessAndFailureLifecycle(t *testing.T) {
	page := NewPageWithContext(context.Background())
	canceled := make(chan struct{})
	state := &pageScreencastState{
		running:    true,
		generation: 1,
		cancelSub: func() {
			close(canceled)
		},
	}
	page.screencast = state
	page.screencastGen = 1

	ready := make(chan error, 1)
	go func() {
		ready <- page.waitScreencastReady(context.Background(), state)
	}()
	require.Eventually(t, func() bool {
		page.screencastMu.Lock()
		defer page.screencastMu.Unlock()
		return len(state.waiters) == 1
	}, time.Second, time.Millisecond)

	page.screencastMu.Lock()
	state.ready = true
	state.lastFrame = []byte("ready")
	waiters := state.waiters
	state.waiters = nil
	page.screencastMu.Unlock()
	for _, waiter := range waiters {
		close(waiter)
	}
	require.NoError(t, <-ready)

	failureWaiter := make(chan struct{})
	page.screencastMu.Lock()
	state.waiters = append(state.waiters, failureWaiter)
	page.screencastMu.Unlock()
	page.failScreencastState(state)
	require.Nil(t, page.screencast)
	select {
	case <-failureWaiter:
	case <-time.After(time.Second):
		t.Fatal("screencast failure did not wake waiters")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("screencast failure did not cancel the failed subscription")
	}

	replacement := &pageScreencastState{running: true, ready: true, generation: 2, lastFrame: []byte("new")}
	page.screencast = replacement
	page.screencastGen = 2
	page.failScreencastState(state)
	require.Same(t, replacement, page.screencast, "old generation changed the replacement stream")
}

func TestScreencastOwnersSharePhysicalStreamUntilFinalRelease(t *testing.T) {
	page := NewPageWithContext(context.Background())
	debugOptions := ScreencastOptions{Format: "jpeg", Quality: 72, MaxWidth: 1280, EveryNthFrame: 1}
	stream := &pageScreencastState{running: true, ready: true, options: normalizeScreencastOptions(debugOptions)}
	page.screencast = stream
	page.screencastOwners = map[ScreencastOwner]ScreencastOptions{
		ScreencastOwnerDebug: normalizeScreencastOptions(debugOptions),
	}

	require.NoError(t, page.EnsureScreencastFor(context.Background(), ScreencastOwnerDebug, debugOptions))
	for range 2 {
		require.NoError(t, page.EnsureScreencastFor(context.Background(), ScreencastOwnerScreenshot, screenshotScreencastOptions))
		require.Same(t, stream, page.screencast, "a screenshot owner must reuse the debug stream")
		require.Equal(t, 1280, page.screencast.options.MaxWidth)
		require.NoError(t, page.StopScreencastFor(context.Background(), ScreencastOwnerScreenshot))
		require.Same(t, stream, page.screencast, "releasing a screenshot must preserve the debug stream")
	}
	require.NoError(t, page.EnsureScreencastFor(context.Background(), ScreencastOwnerPublic, ScreencastOptions{
		Format: "jpeg", Quality: 72, MaxWidth: 1440, EveryNthFrame: 1,
	}))
	require.Len(t, page.screencastOwners, 2)
	require.Equal(t, 1280, page.screencast.options.MaxWidth, "an additional owner must not restart the shared stream")

	require.NoError(t, page.StopScreencastFor(context.Background(), ScreencastOwnerDebug))
	require.NotNil(t, page.screencast, "public consumer still owns the physical stream")
	require.NotContains(t, page.screencastOwners, ScreencastOwnerDebug)
	require.Contains(t, page.screencastOwners, ScreencastOwnerPublic)

	require.NoError(t, page.StopScreencastFor(context.Background(), ScreencastOwnerPublic))
	require.Nil(t, page.screencast, "the final owner release must stop the physical stream")
	require.Empty(t, page.screencastOwners)
}

func encodePNGForTest(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buffer bytes.Buffer
	require.NoError(t, png.Encode(&buffer, img))
	return buffer.Bytes()
}

func TestCropScreencastFrame(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := range 4 {
		for x := range 4 {
			if x < 2 {
				frame.Set(x, y, color.RGBA{R: 255, A: 255})
				continue
			}
			frame.Set(x, y, color.RGBA{B: 255, A: 255})
		}
	}

	output, err := cropScreencastFrame(encodePNGForTest(t, frame), ScreencastFrameMetadata{
		DeviceWidth:  4,
		DeviceHeight: 4,
	}, Rect{
		X:      2,
		Y:      0,
		Width:  2,
		Height: 4,
	}, ScreencastCropOptions{
		MaxEdge: 2,
		Quality: 90,
	})
	require.NoError(t, err)

	decoded, err := imageutil.Decode(output)
	require.NoError(t, err)
	require.Equal(t, 1, decoded.Bounds().Dx())
	require.Equal(t, 2, decoded.Bounds().Dy())

	pixel := decoded.RGBAAt(0, 0)

	r, g, b := int(pixel.R), int(pixel.G), int(pixel.B)
	require.Less(t, r, b)
	require.LessOrEqual(t, g, b)
}

func TestCropScreencastFrameImagePreservesNativeSizeWithoutMaxEdge(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 1200, 700))
	for y := range 700 {
		for x := range 1200 {
			switch {
			case x < 200:
				frame.Set(x, y, color.RGBA{R: 255, A: 255})
			case x < 1100:
				frame.Set(x, y, color.RGBA{G: 255, A: 255})
			default:
				frame.Set(x, y, color.RGBA{B: 255, A: 255})
			}
		}
	}

	output, err := cropScreencastFrameImage(encodePNGForTest(t, frame), ScreencastFrameMetadata{
		DeviceWidth:  1200,
		DeviceHeight: 700,
	}, Rect{
		X:      200,
		Y:      0,
		Width:  900,
		Height: 700,
	}, ScreencastCropOptions{})
	require.NoError(t, err)
	require.Equal(t, 900, output.Bounds().Dx())
	require.Equal(t, 700, output.Bounds().Dy())

	pixel := output.RGBAAt(0, 0)

	r, g, b := int(pixel.R), int(pixel.G), int(pixel.B)
	require.Less(t, r, g)
	require.Less(t, b, g)
}

func TestCropScreencastFrameImageClipsToVisibleFrame(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 4, 4))
	output, err := cropScreencastFrameImage(encodePNGForTest(t, frame), ScreencastFrameMetadata{
		DeviceWidth:  4,
		DeviceHeight: 4,
	}, Rect{
		X:      -2,
		Y:      1,
		Width:  4,
		Height: 5,
	}, ScreencastCropOptions{})
	require.NoError(t, err)
	require.Equal(t, 2, output.Bounds().Dx())
	require.Equal(t, 3, output.Bounds().Dy())

	_, err = cropScreencastFrameImage(encodePNGForTest(t, frame), ScreencastFrameMetadata{
		DeviceWidth:  4,
		DeviceHeight: 4,
	}, Rect{X: 5, Y: 0, Width: 2, Height: 2}, ScreencastCropOptions{})
	require.ErrorContains(t, err, "outside the visible frame")
}

func TestCropScreencastFrameImageUsesViewportYWithoutOffsetTop(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := range 4 {
		for x := range 4 {
			switch y {
			case 0:
				frame.Set(x, y, color.RGBA{R: 255, A: 255})
			case 1:
				frame.Set(x, y, color.RGBA{G: 255, A: 255})
			case 2:
				frame.Set(x, y, color.RGBA{B: 255, A: 255})
			default:
				frame.Set(x, y, color.RGBA{R: 255, G: 255, A: 255})
			}
		}
	}

	output, err := cropScreencastFrameImage(encodePNGForTest(t, frame), ScreencastFrameMetadata{
		DeviceWidth:     4,
		DeviceHeight:    4,
		OffsetTop:       1,
		ScrollOffsetY:   100,
		PageScaleFactor: 1,
	}, Rect{
		X:      0,
		Y:      1,
		Width:  4,
		Height: 1,
	}, ScreencastCropOptions{
		CSSViewportWidth:  4,
		CSSViewportHeight: 4,
	})
	require.NoError(t, err)
	require.Equal(t, 4, output.Bounds().Dx())
	require.Equal(t, 1, output.Bounds().Dy())

	pixel := output.RGBAAt(0, 0)

	r, g, b := int(pixel.R), int(pixel.G), int(pixel.B)
	require.Less(t, r, g)
	require.Less(t, b, g)
}

func TestCropScreencastFrameImagePrefersExplicitCSSViewport(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 8, 4))
	for y := range 4 {
		for x := range 8 {
			switch {
			case x < 2:
				frame.Set(x, y, color.RGBA{R: 255, A: 255})
			case x < 4:
				frame.Set(x, y, color.RGBA{G: 255, A: 255})
			default:
				frame.Set(x, y, color.RGBA{B: 255, A: 255})
			}
		}
	}

	output, err := cropScreencastFrameImage(encodePNGForTest(t, frame), ScreencastFrameMetadata{
		DeviceWidth:     8,
		DeviceHeight:    4,
		PageScaleFactor: 1,
	}, Rect{
		X:      1,
		Y:      0,
		Width:  1,
		Height: 4,
	}, ScreencastCropOptions{
		CSSViewportWidth:  4,
		CSSViewportHeight: 4,
	})
	require.NoError(t, err)
	require.Equal(t, 2, output.Bounds().Dx())
	require.Equal(t, 4, output.Bounds().Dy())

	pixel := output.RGBAAt(0, 0)

	r, g, b := int(pixel.R), int(pixel.G), int(pixel.B)
	require.Less(t, r, g)
	require.Less(t, b, g)
}

func TestCropScreencastFrameFallsBackToMetadataPageScaleFactor(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 8, 4))
	for y := range 4 {
		for x := range 8 {
			if x < 4 {
				frame.Set(x, y, color.RGBA{R: 255, A: 255})
				continue
			}
			frame.Set(x, y, color.RGBA{G: 255, A: 255})
		}
	}

	output, err := cropScreencastFrame(encodePNGForTest(t, frame), ScreencastFrameMetadata{
		DeviceWidth:     4,
		DeviceHeight:    2,
		PageScaleFactor: 2,
	}, Rect{
		X:      1,
		Y:      0,
		Width:  1,
		Height: 2,
	}, ScreencastCropOptions{
		MaxEdge: 8,
		Quality: 90,
	})
	require.NoError(t, err)

	decoded, err := imageutil.Decode(output)
	require.NoError(t, err)
	require.Equal(t, 4, decoded.Bounds().Dx())
	require.Equal(t, 4, decoded.Bounds().Dy())

	pixel := decoded.RGBAAt(0, 0)

	r, g, b := int(pixel.R), int(pixel.G), int(pixel.B)
	require.Less(t, r, g)
	require.LessOrEqual(t, b, g)
}
