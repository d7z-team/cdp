package library

import (
	"context"
	"fmt"
	"testing"
	"time"

	"gopkg.d7z.net/cdp"
)

func assertScrollColor(sel *cdp.Locator, cssW, cssH, cssX, cssY float64, want screenshotRGBRange, label string) error {
	img := must(sel.Screenshot(context.Background()))

	if img == nil {
		return fmt.Errorf("screenshot returned nil for %s", label)
	}
	scaleX := float64(img.Bounds().Dx()) / cssW
	scaleY := float64(img.Bounds().Dy()) / cssH
	if err := assertScreenshotNativeScale("scroll-next "+label, scaleX, scaleY); err != nil {
		return err
	}
	return assertScreenshotCSSRGB("scroll-next "+label, img, cssX, cssY, scaleX, scaleY, want)
}

func TestScrollNextVerticalDown(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/scroll-next")
	time.Sleep(time.Duration(500) *
		time.Millisecond)

	sel := page.ByTestID("scroll-v")
	mustOK(sel.Wait(context.Background(), cdp.StateVisible))

	if err := assertScrollColor(sel, 200, 600, 100, 100,
		screenshotRGBRange{minR: 180, maxR: 255, minG: 0, maxG: 80, minB: 0, maxB: 80},
		"red"); err != nil {
		t.Fatal(err)
	}

	ok := must(sel.ScrollNext(context.Background(), cdp.DirectionDown))

	if !ok {
		t.Fatal("first NextScrollDown() should return true")
	}

	if err := assertScrollColor(sel, 200, 600, 100, 300,
		screenshotRGBRange{minR: 0, maxR: 80, minG: 140, maxG: 220, minB: 0, maxB: 80},
		"green"); err != nil {
		t.Fatal(err)
	}

	ok = must(sel.ScrollNext(context.Background(), cdp.DirectionDown))

	if !ok {
		t.Fatal("second NextScrollDown() should return true")
	}

	if err := assertScrollColor(sel, 200, 600, 100, 500,
		screenshotRGBRange{minR: 0, maxR: 80, minG: 0, maxG: 120, minB: 180, maxB: 255},
		"blue"); err != nil {
		t.Fatal(err)
	}
}

func TestScrollNextUpDownRoundtrip(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/scroll-next")
	time.Sleep(time.Duration(500) *
		time.Millisecond)

	sel := page.ByTestID("scroll-v")
	mustOK(sel.Wait(context.Background(), cdp.StateVisible))

	if !must(sel.ScrollNext(context.Background(), cdp.DirectionDown)) {
		t.Fatal("NextScrollDown() should return true")
	}
	if !must(sel.ScrollNext(context.Background(), cdp.DirectionUp)) {
		t.Fatal("NextScrollUp() should return true")
	}
	if must(sel.ScrollNext(context.Background(), cdp.DirectionUp)) {
		t.Fatal("NextScrollUp() at top should return false")
	}
}

func TestScrollNextBottomReturnsFalse(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/scroll-next")
	time.Sleep(time.Duration(500) *
		time.Millisecond)

	sel := page.ByTestID("scroll-v")
	mustOK(sel.Wait(context.Background(), cdp.StateVisible))

	if !must(sel.ScrollNext(context.Background(), cdp.DirectionDown)) {
		t.Fatal("1st NextScrollDown() should return true")
	}
	if !must(sel.ScrollNext(context.Background(), cdp.DirectionDown)) {
		t.Fatal("2nd NextScrollDown() should return true")
	}
	if must(sel.ScrollNext(context.Background(), cdp.DirectionDown)) {
		t.Fatal("3rd NextScrollDown() at bottom should return false")
	}
}

func TestScrollNextHorizontalRL(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/scroll-next")
	time.Sleep(time.Duration(500) *
		time.Millisecond)

	sel := page.ByTestID("scroll-h")
	mustOK(sel.Wait(context.Background(), cdp.StateVisible))

	if !must(sel.ScrollNext(context.Background(), cdp.DirectionRight)) {
		t.Fatal("first NextScrollRight() should return true")
	}
	if !must(sel.ScrollNext(context.Background(), cdp.DirectionLeft)) {
		t.Fatal("NextScrollLeft() should return true")
	}
	if must(sel.ScrollNext(context.Background(), cdp.DirectionLeft)) {
		t.Fatal("NextScrollLeft() at left edge should return false")
	}
}

func TestScrollToVerticalTop(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/scroll-next")
	time.Sleep(time.Duration(500) *
		time.Millisecond)

	sel := page.ByTestID("scroll-v")
	mustOK(sel.Wait(context.Background(), cdp.StateVisible))
	must(sel.ScrollNext(context.Background(), cdp.DirectionDown))
	must(sel.ScrollNext(context.Background(), cdp.DirectionDown))
	mustOK(sel.ScrollIntoViewAt(context.Background(), -1, 0))

	if err := assertScrollColor(sel, 200, 600, 100, 100,
		screenshotRGBRange{minR: 180, maxR: 255, minG: 0, maxG: 80, minB: 0, maxB: 80},
		"top-red"); err != nil {
		t.Fatal(err)
	}
}

func TestScrollToVerticalBottom(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/scroll-next")
	time.Sleep(time.Duration(500) *
		time.Millisecond)

	sel := page.ByTestID("scroll-v")
	mustOK(sel.Wait(context.Background(), cdp.StateVisible))
	mustOK(sel.ScrollIntoViewAt(context.Background(), -1, 1))

	if err := assertScrollColor(sel, 200, 600, 100, 500,
		screenshotRGBRange{minR: 0, maxR: 80, minG: 0, maxG: 120, minB: 180, maxB: 255},
		"bottom-blue"); err != nil {
		t.Fatal(err)
	}
}

func TestScrollToVerticalMiddle(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/scroll-next")
	time.Sleep(time.Duration(500) *
		time.Millisecond)

	sel := page.ByTestID("scroll-v")
	mustOK(sel.Wait(context.Background(), cdp.StateVisible))
	mustOK(sel.ScrollIntoViewAt(context.Background(), 0.5, 0.5))

	if err := assertScrollColor(sel, 200, 600, 100, 300,
		screenshotRGBRange{minR: 0, maxR: 80, minG: 140, maxG: 220, minB: 0, maxB: 80},
		"middle-green"); err != nil {
		t.Fatal(err)
	}
}

func TestScrollToHorizontalLeft(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/scroll-next")
	time.Sleep(time.Duration(500) *
		time.Millisecond)

	sel := page.ByTestID("scroll-h")
	mustOK(sel.Wait(context.Background(), cdp.StateVisible))
	must(sel.ScrollNext(context.Background(), cdp.DirectionRight))
	must(sel.ScrollNext(context.Background(), cdp.DirectionRight))
	mustOK(sel.ScrollIntoViewAt(context.Background(), 0, -1))

	if err := assertScrollColor(sel, 600, 200, 100, 100,
		screenshotRGBRange{minR: 180, maxR: 255, minG: 0, maxG: 80, minB: 0, maxB: 80},
		"left-red"); err != nil {
		t.Fatal(err)
	}
}

func TestScrollToHorizontalRight(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/scroll-next")
	time.Sleep(time.Duration(500) *
		time.Millisecond)

	sel := page.ByTestID("scroll-h")
	mustOK(sel.Wait(context.Background(), cdp.StateVisible))
	mustOK(sel.ScrollIntoViewAt(context.Background(), 1, -1))

	if err := assertScrollColor(sel, 600, 200, 500, 100,
		screenshotRGBRange{minR: 0, maxR: 80, minG: 0, maxG: 120, minB: 180, maxB: 255},
		"right-blue"); err != nil {
		t.Fatal(err)
	}
}
