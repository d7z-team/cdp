package engine

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"log/slog"
	"math"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"gopkg.d7z.net/cdp/internal/imageutil"
	"gopkg.d7z.net/cdp/internal/syncutil"
)

type ScreencastOptions struct {
	Format        string `json:"format,omitempty"`
	Quality       int    `json:"quality,omitempty"`
	MaxWidth      int    `json:"maxWidth,omitempty"`
	MaxHeight     int    `json:"maxHeight,omitempty"`
	EveryNthFrame int    `json:"everyNthFrame,omitempty"`
}

type ScreencastOwner uint64

const (
	ScreencastOwnerPublic     ScreencastOwner = 1
	ScreencastOwnerDebug      ScreencastOwner = 3
	ScreencastOwnerScreenshot ScreencastOwner = 4
)

const screencastAckTimeout = time.Second

type ScreencastFrameMetadata struct {
	OffsetTop       float64 `json:"offsetTop"`
	PageScaleFactor float64 `json:"pageScaleFactor"`
	DeviceWidth     float64 `json:"deviceWidth"`
	DeviceHeight    float64 `json:"deviceHeight"`
	ScrollOffsetX   float64 `json:"scrollOffsetX"`
	ScrollOffsetY   float64 `json:"scrollOffsetY"`
	Timestamp       float64 `json:"timestamp,omitempty"`
}

type ScreencastFrame struct {
	Data     []byte
	Metadata ScreencastFrameMetadata
	Sequence uint64
}

type ScreencastCropOptions struct {
	MaxEdge           int     `json:"maxEdge,omitempty"`
	Quality           int     `json:"quality,omitempty"`
	CSSViewportWidth  float64 `json:"cssViewportWidth,omitempty"`
	CSSViewportHeight float64 `json:"cssViewportHeight,omitempty"`
}

type pageScreencastFrameEvent struct {
	Data      string                  `json:"data"`
	Metadata  ScreencastFrameMetadata `json:"metadata"`
	SessionID int                     `json:"sessionId"`
}

type pageScreencastState struct {
	running     bool
	ready       bool
	generation  uint64
	options     ScreencastOptions
	lastFrame   []byte
	lastMeta    ScreencastFrameMetadata
	lastFrameAt time.Time
	lastSeq     uint64
	waiters     []chan struct{}
	cancelSub   func()
}

func normalizeScreencastOptions(opts ScreencastOptions) ScreencastOptions {
	opts.Format = strings.ToLower(strings.TrimSpace(opts.Format))
	if opts.Format != "png" {
		opts.Format = "jpeg"
	}
	if opts.Quality <= 0 || opts.Quality > 100 {
		opts.Quality = 72
	}
	if opts.MaxWidth < 0 {
		opts.MaxWidth = 0
	}
	if opts.MaxHeight < 0 {
		opts.MaxHeight = 0
	}
	if opts.EveryNthFrame <= 0 {
		opts.EveryNthFrame = 1
	}
	return opts
}

func normalizeScreencastCropOptions(opts ScreencastCropOptions) ScreencastCropOptions {
	if opts.Quality <= 0 || opts.Quality > 100 {
		opts.Quality = 72
	}
	return opts
}

func sameScreencastOptions(a, b ScreencastOptions) bool {
	return a.Format == b.Format &&
		a.Quality == b.Quality &&
		a.MaxWidth == b.MaxWidth &&
		a.MaxHeight == b.MaxHeight &&
		a.EveryNthFrame == b.EveryNthFrame
}

func (p *Page) EnsureScreencastFor(ctx context.Context, owner ScreencastOwner, opts ScreencastOptions) error {
	if owner == 0 {
		return errors.New("screencast owner is required")
	}
	p.screencastOpMu.Lock()
	defer p.screencastOpMu.Unlock()

	opts = normalizeScreencastOptions(opts)
	p.screencastMu.Lock()
	if p.screencastOwners == nil {
		p.screencastOwners = make(map[ScreencastOwner]ScreencastOptions)
	}
	previous, existed := p.screencastOwners[owner]
	p.screencastOwners[owner] = opts
	state := p.screencast
	ready := state != nil && state.running && state.ready
	ownerCount := len(p.screencastOwners)
	p.screencastMu.Unlock()

	if ready {
		if !existed || sameScreencastOptions(previous, opts) || ownerCount > 1 {
			return nil
		}
	}
	if err := p.ensureScreencast(ctx, opts); err != nil {
		p.screencastMu.Lock()
		if existed {
			p.screencastOwners[owner] = previous
		} else {
			delete(p.screencastOwners, owner)
		}
		p.screencastMu.Unlock()
		return err
	}
	return nil
}

func (p *Page) ensureScreencast(ctx context.Context, opts ScreencastOptions) error {
	opts = normalizeScreencastOptions(opts)
	if ctx == nil {
		ctx = p.ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := p.checkConnContext(ctx); err != nil {
		return err
	}

	p.screencastMu.Lock()
	current := p.screencast
	if current != nil && current.running && sameScreencastOptions(current.options, opts) {
		ready := current.ready
		p.screencastMu.Unlock()
		if ready {
			return nil
		}
		return p.waitScreencastReady(ctx, current)
	}
	p.screencastMu.Unlock()

	if current != nil {
		if err := p.stopScreencast(ctx); err != nil {
			return err
		}
	}

	p.lock.RLock()
	conn := p.CdpConn
	p.lock.RUnlock()
	if conn == nil {
		return errors.New("页面未连接")
	}

	resps, cancelSub := conn.SubscribeReliable("Page.screencastFrame")
	p.screencastMu.Lock()
	p.screencastGen++
	state := &pageScreencastState{
		running:    true,
		generation: p.screencastGen,
		options:    opts,
		cancelSub:  cancelSub,
	}
	p.screencast = state
	p.signalScreencastChangeLocked()
	p.screencastMu.Unlock()

	syncutil.Go(func() {
		defer p.failScreencastState(state)
		for resp := range resps {
			var event pageScreencastFrameEvent
			if err := resp.ParamsUnmarshal(&event); err != nil {
				if sessionID, ok := SafeGet[float64](resp.Params, "sessionId"); ok && sessionID != 0 {
					ackCtx, cancel := context.WithTimeout(conn.Context, screencastAckTimeout)
					_, _ = conn.SendMessageContext(ackCtx, "Page.screencastFrameAck", map[string]any{"sessionId": int(sessionID)})
					cancel()
				}
				return
			}

			frameBytes, err := base64.StdEncoding.DecodeString(event.Data)
			current := false
			validFrame := err == nil && len(frameBytes) > 0
			if validFrame {
				now := time.Now()
				p.screencastMu.Lock()
				current = p.screencast == state && state.running && state.generation == p.screencastGen
				if current {
					state.lastFrame = append(state.lastFrame[:0], frameBytes...)
					state.lastMeta = event.Metadata
					state.lastFrameAt = now
					state.ready = true
					p.screencastSeq++
					state.lastSeq = p.screencastSeq
					waiters := state.waiters
					state.waiters = nil
					p.screencastMu.Unlock()
					for _, waiter := range waiters {
						close(waiter)
					}
				} else {
					p.screencastMu.Unlock()
				}
			}

			if event.SessionID != 0 {
				ackCtx, cancel := context.WithTimeout(conn.Context, screencastAckTimeout)
				_, ackErr := conn.SendMessageContext(ackCtx, "Page.screencastFrameAck", map[string]any{"sessionId": event.SessionID})
				cancel()
				if ackErr != nil && !errors.Is(ackErr, ErrBrowserClosed) {
					slog.Debug("ack screencast frame failed", "page_id", p.ID, "error", ackErr)
				}
			}
			if !validFrame || !current {
				return
			}
		}
	})

	rollback := func() {
		p.screencastMu.Lock()
		if p.screencast != state {
			p.screencastMu.Unlock()
			return
		}
		failed := p.takeScreencastState()
		p.screencastMu.Unlock()
		if failed.cancelSub != nil {
			failed.cancelSub()
		}
		for _, waiter := range failed.waiters {
			close(waiter)
		}
	}
	if _, err := conn.SendMessageContext(ctx, "Page.enable", nil); err != nil {
		rollback()
		return BrowserErrorFromCDP("Page.enable", topPageExecutionTarget(), err)
	}
	if _, err := conn.SendMessageContext(ctx, "Page.startScreencast", map[string]any{
		"format":        opts.Format,
		"quality":       opts.Quality,
		"maxWidth":      opts.MaxWidth,
		"maxHeight":     opts.MaxHeight,
		"everyNthFrame": opts.EveryNthFrame,
	}); err != nil {
		rollback()
		return BrowserErrorFromCDP("Page.startScreencast", topPageExecutionTarget(), err)
	}
	if err := p.waitScreencastReady(ctx, state); err != nil {
		rollback()
		stopCtx, cancel := context.WithTimeout(conn.Context, screencastAckTimeout)
		_, _ = conn.SendMessageContext(stopCtx, "Page.stopScreencast", nil)
		cancel()
		return fmt.Errorf("screencast readiness: %w", err)
	}
	return nil
}

func (p *Page) waitScreencastReady(ctx context.Context, state *pageScreencastState) error {
	for {
		p.screencastMu.Lock()
		if p.screencast != state || !state.running {
			p.screencastMu.Unlock()
			return errors.New("screencast stopped before becoming ready")
		}
		if state.ready && len(state.lastFrame) > 0 {
			p.screencastMu.Unlock()
			return nil
		}
		waiter := make(chan struct{})
		state.waiters = append(state.waiters, waiter)
		p.screencastMu.Unlock()
		select {
		case <-waiter:
		case <-p.Done():
			p.removeScreencastWaiter(waiter)
			return ErrBrowserClosed
		case <-ctx.Done():
			p.removeScreencastWaiter(waiter)
			return ctx.Err()
		}
	}
}

func (p *Page) failScreencastState(state *pageScreencastState) {
	p.screencastMu.Lock()
	if p.screencast != state || !state.running || state.generation != p.screencastGen {
		p.screencastMu.Unlock()
		return
	}
	failed := p.takeScreencastState()
	p.screencastMu.Unlock()
	if failed.cancelSub != nil {
		failed.cancelSub()
	}
	for _, waiter := range failed.waiters {
		close(waiter)
	}
}

func (p *Page) StopScreencast() error {
	return p.StopScreencastFor(p.ctx, ScreencastOwnerPublic)
}

func (p *Page) StopScreencastFor(ctx context.Context, owner ScreencastOwner) error {
	if owner == 0 {
		return errors.New("screencast owner is required")
	}
	p.screencastOpMu.Lock()
	defer p.screencastOpMu.Unlock()
	p.screencastMu.Lock()
	delete(p.screencastOwners, owner)
	hasOwners := len(p.screencastOwners) > 0
	p.screencastMu.Unlock()
	if hasOwners {
		return nil
	}
	return p.stopScreencast(ctx)
}

func (p *Page) stopScreencast(ctx context.Context) error {
	if ctx == nil {
		ctx = p.ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	p.lock.RLock()
	conn := p.CdpConn
	p.lock.RUnlock()

	p.screencastMu.Lock()
	state := p.takeScreencastState()
	p.screencastMu.Unlock()

	if state == nil {
		return nil
	}
	if state.cancelSub != nil {
		state.cancelSub()
	}
	for _, waiter := range state.waiters {
		close(waiter)
	}
	if conn == nil {
		return nil
	}
	if _, err := conn.SendMessageContext(ctx, "Page.stopScreencast", nil); err != nil && !errors.Is(err, ErrBrowserClosed) {
		return BrowserErrorFromCDP("Page.stopScreencast", topPageExecutionTarget(), err)
	}
	return nil
}

func (p *Page) takeScreencastState() *pageScreencastState {
	if p.screencast == nil {
		return nil
	}
	state := p.screencast
	state.running = false
	state.ready = false
	p.screencast = nil
	p.signalScreencastChangeLocked()
	return state
}

func (p *Page) signalScreencastChangeLocked() {
	if p.screencastChanged != nil {
		close(p.screencastChanged)
	}
	p.screencastChanged = make(chan struct{})
}

func (p *Page) removeScreencastWaiter(waiter chan struct{}) {
	p.screencastMu.Lock()
	if state := p.screencast; state != nil {
		state.waiters = slices.DeleteFunc(state.waiters, func(candidate chan struct{}) bool {
			return candidate == waiter
		})
	}
	p.screencastMu.Unlock()
}

func (p *Page) screencastSequence() uint64 {
	p.screencastMu.Lock()
	defer p.screencastMu.Unlock()
	if p.screencast == nil || !p.screencast.running || !p.screencast.ready {
		return 0
	}
	return p.screencast.lastSeq
}

func (p *Page) ScreencastSequence() uint64 {
	return p.screencastSequence()
}

func (p *Page) WaitScreencastFrame(ctx context.Context, afterSeq uint64, allowStale bool) (ScreencastFrame, error) {
	if ctx == nil {
		return ScreencastFrame{}, errors.New("screencast frame context is nil")
	}
	for {
		var (
			frame  []byte
			meta   ScreencastFrameMetadata
			seq    uint64
			waiter chan struct{}
		)

		p.screencastMu.Lock()
		state := p.screencast
		if state == nil || !state.running {
			if p.screencastChanged == nil {
				p.screencastChanged = make(chan struct{})
			}
			changed := p.screencastChanged
			p.screencastMu.Unlock()
			select {
			case <-changed:
				continue
			case <-p.Done():
				return ScreencastFrame{}, ErrBrowserClosed
			case <-ctx.Done():
				return ScreencastFrame{}, ctx.Err()
			}
		}
		if len(state.lastFrame) > 0 {
			frame = append([]byte(nil), state.lastFrame...)
			meta = state.lastMeta
			seq = state.lastSeq
		}
		if len(frame) == 0 || seq <= afterSeq {
			waiter = make(chan struct{})
			state.waiters = append(state.waiters, waiter)
		}
		p.screencastMu.Unlock()

		if waiter == nil {
			return ScreencastFrame{Data: frame, Metadata: meta, Sequence: seq}, nil
		}
		select {
		case <-waiter:
		case <-p.Done():
			p.removeScreencastWaiter(waiter)
			return ScreencastFrame{}, ErrBrowserClosed
		case <-ctx.Done():
			p.removeScreencastWaiter(waiter)
			if len(frame) > 0 && allowStale {
				return ScreencastFrame{Data: frame, Metadata: meta, Sequence: seq}, nil
			}
			return ScreencastFrame{}, ctx.Err()
		}
	}
}

func cropScreencastFrame(frame []byte, meta ScreencastFrameMetadata, rect Rect, opts ScreencastCropOptions) ([]byte, error) {
	opts = normalizeScreencastCropOptions(opts)
	cropped, err := cropScreencastFrameImage(frame, meta, rect, opts)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	err = jpeg.Encode(&buf, cropped, &jpeg.Options{Quality: opts.Quality})
	return buf.Bytes(), err
}

func (p *Page) CaptureScreencastCropImageAfterContext(ctx context.Context, rect Rect, opts ScreencastCropOptions, afterSeq uint64, allowStale bool) (*image.RGBA, error) {
	if rect.Width <= 0 || rect.Height <= 0 {
		return nil, errors.New("invalid screencast rect")
	}
	opts = normalizeScreencastCropOptions(opts)
	frame, err := p.WaitScreencastFrame(ctx, afterSeq, allowStale)
	if err != nil {
		return nil, err
	}
	return cropScreencastFrameImage(frame.Data, frame.Metadata, rect, opts)
}

func cropScreencastFrameImage(frame []byte, meta ScreencastFrameMetadata, rect Rect, opts ScreencastCropOptions) (*image.RGBA, error) {
	opts = normalizeScreencastCropOptions(opts)

	decoded, err := imageutil.Decode(frame)
	if err != nil {
		return nil, fmt.Errorf("decode screencast frame: %w", err)
	}

	frameWidth := decoded.Bounds().Dx()
	frameHeight := decoded.Bounds().Dy()
	if frameWidth <= 0 || frameHeight <= 0 {
		return nil, errors.New("empty screencast frame")
	}

	pageScaleFactor := meta.PageScaleFactor
	if pageScaleFactor <= 0 {
		pageScaleFactor = 1
	}

	cssViewportWidth := opts.CSSViewportWidth
	if cssViewportWidth <= 0 {
		deviceWidth := meta.DeviceWidth
		if deviceWidth <= 0 {
			deviceWidth = float64(frameWidth)
		}
		cssViewportWidth = deviceWidth / pageScaleFactor
		if cssViewportWidth <= 0 {
			cssViewportWidth = deviceWidth
		}
	}

	cssViewportHeight := opts.CSSViewportHeight
	if cssViewportHeight <= 0 {
		deviceHeight := meta.DeviceHeight
		if deviceHeight <= 0 {
			deviceHeight = float64(frameHeight)
		}
		cssViewportHeight = deviceHeight / pageScaleFactor
		if cssViewportHeight <= 0 {
			cssViewportHeight = deviceHeight
		}
	}

	scaleX := float64(frameWidth) / cssViewportWidth
	scaleY := float64(frameHeight) / cssViewportHeight

	cropX := max(0, int(math.Round(rect.X*scaleX)))
	cropY := max(0, int(math.Round(rect.Y*scaleY)))
	cropRight := min(frameWidth, int(math.Round((rect.X+rect.Width)*scaleX)))
	cropBottom := min(frameHeight, int(math.Round((rect.Y+rect.Height)*scaleY)))
	cropWidth := cropRight - cropX
	cropHeight := cropBottom - cropY
	if cropWidth <= 0 || cropHeight <= 0 {
		return nil, errors.New("screencast rect is outside the visible frame")
	}

	cropped, err := imageutil.Crop(decoded, image.Rect(cropX, cropY, cropX+cropWidth, cropY+cropHeight))
	if err != nil {
		return nil, fmt.Errorf("crop screencast frame: %w", err)
	}

	if maxEdge := opts.MaxEdge; maxEdge > 0 {
		width := cropped.Bounds().Dx()
		height := cropped.Bounds().Dy()
		if width > maxEdge || height > maxEdge {
			ratio := math.Min(float64(maxEdge)/float64(width), float64(maxEdge)/float64(height))
			targetWidth := max(1, int(math.Round(float64(width)*ratio)))
			targetHeight := max(1, int(math.Round(float64(height)*ratio)))
			resized, resizeErr := imageutil.Resize(cropped, targetWidth, targetHeight)
			if resizeErr == nil {
				cropped = resized
			}
		}
	}

	return cropped, nil
}

var nextScreencastOwner atomic.Uint64

func (p *Page) NewScreencastOwner() ScreencastOwner {
	return ScreencastOwner(nextScreencastOwner.Add(1) + 4)
}
