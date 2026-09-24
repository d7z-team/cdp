package library

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"math"
	"strings"
	"testing"
	"time"

	"gopkg.d7z.net/cdp"
)

const (
	screenshotBorderWidth    = 1
	screenshotMinNativeScale = 0.95
)

type screenshotRGBRange struct {
	minR int
	maxR int
	minG int
	maxG int
	minB int
	maxB int
}

func screenshotRGBMatches(r, g, b int, want screenshotRGBRange) bool {
	return r >= want.minR && r <= want.maxR && g >= want.minG && g <= want.maxG && b >= want.minB && b <= want.maxB
}

func assertScreenshotRGB(name string, img *image.RGBA, x, y int, want screenshotRGBRange) error {
	if img == nil {
		return fmt.Errorf("%s screenshot returned nil image", name)
	}
	if x < 0 || y < 0 || x >= img.Bounds().Dx() || y >= img.Bounds().Dy() {
		return fmt.Errorf("%s sample point out of bounds at (%d,%d), image=%dx%d", name, x, y, img.Bounds().Dx(), img.Bounds().Dy())
	}
	pixel := img.RGBAAt(x, y)
	r, g, b := int(pixel.R), int(pixel.G), int(pixel.B)
	if !screenshotRGBMatches(r, g, b, want) {
		return fmt.Errorf("%s color mismatch at (%d,%d): got rgb(%d,%d,%d), image=%dx%d", name, x, y, r, g, b, img.Bounds().Dx(), img.Bounds().Dy())
	}
	return nil
}

func assertScreenshotCSSRGB(name string, img *image.RGBA, cssX, cssY, scaleX, scaleY float64, want screenshotRGBRange) error {
	return assertScreenshotRGB(name, img, int(math.Round(cssX*scaleX)), int(math.Round(cssY*scaleY)), want)
}

func assertScreenshotNativeScale(name string, scaleX, scaleY float64) error {
	if scaleX < screenshotMinNativeScale || scaleY < screenshotMinNativeScale {
		return fmt.Errorf("%s screenshot was downscaled below native resolution: %.2fx%.2f", name, scaleX, scaleY)
	}
	return nil
}

func assertScreenshotMarkerBorder(name string, img *image.RGBA) error {
	return assertScreenshotMarkerEdges(name, img, true)
}

func assertScreenshotMarkerLeadingEdges(name string, img *image.RGBA) error {
	return assertScreenshotMarkerEdges(name, img, false)
}

func assertScreenshotMarkerEdges(name string, img *image.RGBA, includeTrailingEdges bool) error {
	if img == nil {
		return fmt.Errorf("%s screenshot returned nil image", name)
	}
	width := img.Bounds().Dx()
	height := img.Bounds().Dy()
	if width < screenshotBorderWidth*4 || height < screenshotBorderWidth*4 {
		return fmt.Errorf("%s screenshot too small for border validation: %dx%d", name, width, height)
	}
	band := max(3, screenshotBorderWidth*3)
	type edgeMarker struct {
		name  string
		fromX int
		toX   int
		fromY int
		toY   int
	}
	edgeMarkers := []edgeMarker{
		{name: "top", fromX: width/2 - band, toX: width/2 + band, fromY: 0, toY: band},
		{name: "left", fromX: 0, toX: band, fromY: height/2 - band, toY: height/2 + band},
	}
	if includeTrailingEdges {
		edgeMarkers = append(edgeMarkers,
			edgeMarker{name: "right", fromX: width - band, toX: width, fromY: height/2 - band, toY: height/2 + band},
			edgeMarker{name: "bottom", fromX: width/2 - band, toX: width/2 + band, fromY: height - band, toY: height},
		)
	}
	want := screenshotRGBRange{minR: 120, maxR: 255, minG: 0, maxG: 150, minB: 120, maxB: 255}
	for _, marker := range edgeMarkers {
		found := false
		for y := max(0, marker.fromY); y < min(height, marker.toY) && !found; y++ {
			for x := max(0, marker.fromX); x < min(width, marker.toX); x++ {
				pixel := img.RGBAAt(x, y)
				r, g, b := int(pixel.R), int(pixel.G), int(pixel.B)
				if screenshotRGBMatches(r, g, b, want) {
					found = true
					break
				}
			}
		}
		if !found {
			return fmt.Errorf("%s %s border color missing in band x=%d..%d y=%d..%d, size=%dx%d", name, marker.name, marker.fromX, marker.toX, marker.fromY, marker.toY, width, height)
		}
	}
	return nil
}

func assertScreenshotTrailingEdgeProbes(name string, img *image.RGBA, cssWidth, cssHeight, scaleX, scaleY float64) error {
	probes := []struct {
		name string
		x    float64
		y    float64
		want screenshotRGBRange
	}{
		{name: "right outer probe", x: cssWidth - 3, y: cssHeight * 0.35, want: screenshotRGBRange{minR: 0, maxR: 70, minG: 170, maxG: 255, minB: 170, maxB: 255}},
		{name: "right middle probe", x: cssWidth - 6, y: cssHeight * 0.35, want: screenshotRGBRange{minR: 220, maxR: 255, minG: 80, maxG: 160, minB: 0, maxB: 70}},
		{name: "right inner probe", x: cssWidth - 9, y: cssHeight * 0.35, want: screenshotRGBRange{minR: 100, maxR: 180, minG: 0, maxG: 90, minB: 180, maxB: 255}},
		{name: "bottom outer probe", x: cssWidth * 0.35, y: cssHeight - 3, want: screenshotRGBRange{minR: 100, maxR: 190, minG: 190, maxG: 255, minB: 0, maxB: 90}},
		{name: "bottom middle probe", x: cssWidth * 0.35, y: cssHeight - 6, want: screenshotRGBRange{minR: 220, maxR: 255, minG: 40, maxG: 130, minB: 120, maxB: 220}},
		{name: "bottom inner probe", x: cssWidth * 0.35, y: cssHeight - 9, want: screenshotRGBRange{minR: 0, maxR: 90, minG: 40, maxG: 120, minB: 120, maxB: 210}},
		{name: "corner outer probe", x: cssWidth - 3, y: cssHeight - 3, want: screenshotRGBRange{minR: 0, maxR: 60, minG: 80, maxG: 160, minB: 90, maxB: 170}},
		{name: "corner middle probe", x: cssWidth - 6, y: cssHeight - 6, want: screenshotRGBRange{minR: 150, maxR: 230, minG: 0, maxG: 90, minB: 0, maxB: 90}},
		{name: "corner inner probe", x: cssWidth - 9, y: cssHeight - 9, want: screenshotRGBRange{minR: 30, maxR: 110, minG: 30, maxG: 110, minB: 190, maxB: 255}},
	}
	for _, probe := range probes {
		if err := assertScreenshotCSSRGB(name+" "+probe.name, img, probe.x, probe.y, scaleX, scaleY, probe.want); err != nil {
			return err
		}
	}
	return nil
}

type screenshotEvaluator evaluator

func evalScreenshotFloat(eval screenshotEvaluator, js string) (float64, error) {
	var value float64
	if err := json.Unmarshal([]byte(evalValue(eval, "return ("+js+");")), &value); err != nil {
		return 0, fmt.Errorf("parse screenshot eval float: %w", err)
	}
	return value, nil
}

func evalScreenshotFloatSlice(eval screenshotEvaluator, js string) ([]float64, error) {
	var value []float64
	if err := json.Unmarshal([]byte(evalValue(eval, "return ("+js+");")), &value); err != nil {
		return nil, fmt.Errorf("parse screenshot eval float slice: %w", err)
	}
	return value, nil
}

func assertCoreScreenshotOverlayVisible(page screenshotEvaluator, label string) error {
	got := evalValue(page, `
return (() => {
	const host = document.getElementById('__cdp_overlay_canvas_host_core');
	if (!host || getComputedStyle(host).visibility === 'hidden') {
		return false;
	}
	const canvas = host.shadowRoot && host.shadowRoot.getElementById('__cdp_box_drawer_canvas_core');
	if (!(canvas instanceof HTMLCanvasElement)) {
		return false;
	}
	const ctx = canvas.getContext('2d');
	if (!ctx) {
		return false;
	}
	const dpr = window.devicePixelRatio || 1;
	const data = ctx.getImageData(Math.round(50 * dpr), Math.round(50 * dpr), 1, 1).data;
	return data[3] > 0;
})()
`)
	if got != "true" {
		return fmt.Errorf("%s expected core overlay to remain visible after screenshot, got %q", label, got)
	}
	return nil
}

func TestElementScreenshotZoomedPageClip(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/screenshot-zoom")

	if err := page.Session().Call(context.Background(), "Emulation.setPageScaleFactor", map[string]any{
		"pageScaleFactor": 1.5,
	}, nil); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = page.Session().Call(context.Background(), "Emulation.resetPageScaleFactor", nil, nil)
	}()
	mustOK(page.ScrollTo(context.Background(), 0, 360))
	time.Sleep(time.Duration(200) *
		time.Millisecond)

	metrics, err := layoutMetrics(page)
	if err != nil {
		t.Fatal(err)
	}
	if metrics.VisualViewport.Scale <= 1.01 {
		t.Fatalf("page scale was not applied, got scale %.2f", metrics.VisualViewport.Scale)
	}

	img := must(page.ByTestID("zoom-shot-target").Screenshot(context.Background()))

	if img == nil {
		t.Fatal("zoomed element screenshot returned nil image")
	}
	afterMetrics, err := layoutMetrics(page)
	if err != nil {
		t.Fatal(err)
	}
	if afterMetrics.VisualViewport.Scale <= 1.01 {
		t.Fatalf("element screenshot changed page scale, got scale %.2f", afterMetrics.VisualViewport.Scale)
	}
	if img.Bounds().Dx() < 100 || img.Bounds().Dy() < 70 {
		t.Fatalf("unexpected zoomed element screenshot size: %dx%d", img.Bounds().Dx(), img.Bounds().Dy())
	}
	if err := assertScreenshotMarkerBorder("zoomed element", img); err != nil {
		t.Fatal(err)
	}

	pixel := img.RGBAAt(img.Bounds().Dx()/2, img.Bounds().Dy()/2)

	r, g, b := int(pixel.R), int(pixel.G), int(pixel.B)
	if g < 150 || r > 90 || b > 120 {
		t.Fatalf("zoomed element screenshot center color mismatch: got rgb(%d,%d,%d), size=%dx%d", r, g, b, img.Bounds().Dx(), img.Bounds().Dy())
	}
}

func TestPageScreenshotZoomedPreservesScale(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/screenshot-zoom")

	if err := page.Session().Call(context.Background(), "Emulation.setPageScaleFactor", map[string]any{
		"pageScaleFactor": 1.5,
	}, nil); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = page.Session().Call(context.Background(), "Emulation.resetPageScaleFactor", nil, nil)
	}()
	mustOK(page.ScrollTo(context.Background(), 0, 0))
	time.Sleep(time.Duration(200) *
		time.Millisecond)

	metrics, err := layoutMetrics(page)
	if err != nil {
		t.Fatal(err)
	}
	if metrics.VisualViewport.Scale <= 1.01 {
		t.Fatalf("page scale was not applied, got scale %.2f", metrics.VisualViewport.Scale)
	}
	contentWidth := metrics.CSSContentSize.Width
	contentHeight := metrics.CSSContentSize.Height
	if contentWidth <= 0 || contentHeight <= 0 {
		contentWidth = metrics.ContentSize.Width
		contentHeight = metrics.ContentSize.Height
	}
	if contentWidth <= 0 || contentHeight <= 0 {
		t.Fatalf("invalid content size: %.2fx%.2f", contentWidth, contentHeight)
	}
	img := must(page.Screenshot(context.Background()))

	if img == nil {
		t.Fatal("zoomed page screenshot returned nil image")
	}
	afterMetrics, err := layoutMetrics(page)
	if err != nil {
		t.Fatal(err)
	}
	if afterMetrics.VisualViewport.Scale <= 1.01 {
		t.Fatalf("page screenshot changed page scale, got scale %.2f", afterMetrics.VisualViewport.Scale)
	}
	scaleX := float64(img.Bounds().Dx()) / contentWidth
	scaleY := float64(img.Bounds().Dy()) / contentHeight
	if err := assertScreenshotNativeScale("zoomed page", scaleX, scaleY); err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() < 500 || img.Bounds().Dy() < 500 {
		t.Fatalf("zoomed page screenshot too small: %dx%d for content %.2fx%.2f scale=%.2fx%.2f", img.Bounds().Dx(), img.Bounds().Dy(), contentWidth, contentHeight, scaleX, scaleY)
	}

	points := []struct {
		name string
		x    float64
		y    float64
		want screenshotRGBRange
	}{
		{name: "blue marker", x: 170, y: 155, want: screenshotRGBRange{minR: 0, maxR: 80, minG: 30, maxG: 110, minB: 180, maxB: 255}},
		{name: "target center", x: 360, y: 602, want: screenshotRGBRange{minR: 0, maxR: 80, minG: 150, maxG: 255, minB: 20, maxB: 120}},
	}
	for _, point := range points {
		if err := assertScreenshotCSSRGB(point.name, img, point.x, point.y, scaleX, scaleY, point.want); err != nil {
			t.Fatal(err)
		}
	}
}

func TestScreenshotHidesInjectedOverlays(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/screencast-image")
	evalText(must(page.ExecutionContext(context.Background(), cdp.ExecutionContextOptions{World: cdp.WorldCore})), `
		window.__cdp_ffi?.canvas.setMode('#003cff','截图覆盖层');window.__cdp_ffi?.canvas.setHighlightTimeout(0);window.__cdp_ffi?.canvas.draw(0,0,240,240,0);
	`)
	time.Sleep(time.Duration(100) *
		time.Millisecond)

	if err := assertCoreScreenshotOverlayVisible(page, "before screenshot"); err != nil {
		t.Fatal(err)
	}

	pageImg := must(page.Screenshot(context.Background()))

	if pageImg == nil {
		t.Fatal("overlay page screenshot returned nil image")
	}
	viewportWidth, err := evalScreenshotFloat(page, `window.visualViewport ? window.visualViewport.width : window.innerWidth`)
	if err != nil {
		t.Fatal(err)
	}
	viewportHeight, err := evalScreenshotFloat(page, `window.visualViewport ? window.visualViewport.height : window.innerHeight`)
	if err != nil {
		t.Fatal(err)
	}
	if err := assertScreenshotCSSRGB("overlay hidden in page screenshot", pageImg, 40, 40, float64(pageImg.Bounds().Dx())/viewportWidth, float64(pageImg.Bounds().Dy())/viewportHeight, screenshotRGBRange{minR: 220, maxR: 255, minG: 0, maxG: 40, minB: 0, maxB: 40}); err != nil {
		t.Fatal(err)
	}
	if err := assertCoreScreenshotOverlayVisible(page, "after page screenshot"); err != nil {
		t.Fatal(err)
	}

	locatorImg := must(page.ByTestID("tile-red").Screenshot(context.Background()))

	if locatorImg == nil {
		t.Fatal("overlay locator screenshot returned nil image")
	}
	tileWidth, err := evalScreenshotFloat(page, `document.querySelector('[data-testid="tile-red"]').getBoundingClientRect().width`)
	if err != nil {
		t.Fatal(err)
	}
	tileHeight, err := evalScreenshotFloat(page, `document.querySelector('[data-testid="tile-red"]').getBoundingClientRect().height`)
	if err != nil {
		t.Fatal(err)
	}
	if err := assertScreenshotCSSRGB("overlay hidden in locator screenshot", locatorImg, 40, 40, float64(locatorImg.Bounds().Dx())/tileWidth, float64(locatorImg.Bounds().Dy())/tileHeight, screenshotRGBRange{minR: 220, maxR: 255, minG: 0, maxG: 40, minB: 0, maxB: 40}); err != nil {
		t.Fatal(err)
	}
	if err := assertCoreScreenshotOverlayVisible(page, "after locator screenshot"); err != nil {
		t.Fatal(err)
	}
	evalText(must(page.ExecutionContext(context.Background(), cdp.ExecutionContextOptions{World: cdp.WorldCore})), `window.__cdp_ffi?.canvas.clear();`)
}

func TestPageScreenshotScrollStitch(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/screenshot-scroll-stitch")
	mustOK(page.ScrollTo(context.Background(), 180, 220))
	time.Sleep(time.Duration(100) *
		time.Millisecond)

	img := must(page.Screenshot(context.Background()))

	if img == nil {
		t.Fatal("scroll stitched page screenshot returned nil image")
	}
	viewportWidth, err := evalScreenshotFloat(page, `window.visualViewport ? window.visualViewport.width : window.innerWidth`)
	if err != nil {
		t.Fatal(err)
	}
	viewportHeight, err := evalScreenshotFloat(page, `window.visualViewport ? window.visualViewport.height : window.innerHeight`)
	if err != nil {
		t.Fatal(err)
	}
	scaleX := float64(img.Bounds().Dx()) / math.Max(1400, viewportWidth)
	scaleY := float64(img.Bounds().Dy()) / math.Max(1300, viewportHeight)
	if err := assertScreenshotNativeScale("scroll stitched page", scaleX, scaleY); err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() < 500 || img.Bounds().Dy() < 500 {
		t.Fatalf("scroll stitched page screenshot too small: %dx%d", img.Bounds().Dx(), img.Bounds().Dy())
	}
	points := []struct {
		name string
		x    float64
		y    float64
		want screenshotRGBRange
	}{
		{name: "top-left marker", x: 60, y: 50, want: screenshotRGBRange{minR: 180, maxR: 255, minG: 0, maxG: 70, minB: 0, maxB: 80}},
		{name: "top-right tail marker", x: 1398, y: 50, want: screenshotRGBRange{minR: 0, maxR: 80, minG: 50, maxG: 140, minB: 180, maxB: 255}},
		{name: "bottom-left tail marker", x: 60, y: 1298, want: screenshotRGBRange{minR: 200, maxR: 255, minG: 170, maxG: 245, minB: 0, maxB: 90}},
		{name: "bottom-right tail marker", x: 1398, y: 1298, want: screenshotRGBRange{minR: 0, maxR: 90, minG: 130, maxG: 220, minB: 20, maxB: 120}},
	}
	for _, point := range points {
		if err := assertScreenshotCSSRGB(point.name, img, point.x, point.y, scaleX, scaleY, point.want); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLocatorScreenshotTopBodyLifecycle(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	session := acquireSession(t)
	page, err := session.Open("/screenshot-scroll-stitch")
	if err != nil {
		t.Fatal(err)
	}
	mustOK(page.ScrollTo(context.Background(), 180, 220))
	time.Sleep(time.Duration(100) *
		time.Millisecond)

	img := must(page.Locator("body").Screenshot(context.Background()))

	if img == nil {
		t.Fatal("body screenshot returned nil image")
	}
	viewportWidth, err := evalScreenshotFloat(page, `window.visualViewport ? window.visualViewport.width : window.innerWidth`)
	if err != nil {
		t.Fatal(err)
	}
	viewportHeight, err := evalScreenshotFloat(page, `window.visualViewport ? window.visualViewport.height : window.innerHeight`)
	if err != nil {
		t.Fatal(err)
	}
	scaleX := float64(img.Bounds().Dx()) / math.Max(1400, viewportWidth)
	scaleY := float64(img.Bounds().Dy()) / math.Max(1300, viewportHeight)
	if err := assertScreenshotNativeScale("body page screenshot", scaleX, scaleY); err != nil {
		t.Fatal(err)
	}
	if err := assertScreenshotCSSRGB("body top-left marker", img, 60, 50, scaleX, scaleY, screenshotRGBRange{minR: 180, maxR: 255, minG: 0, maxG: 70, minB: 0, maxB: 80}); err != nil {
		t.Fatal(err)
	}
	if err := assertScreenshotCSSRGB("body bottom-right marker", img, 1398, 1298, scaleX, scaleY, screenshotRGBRange{minR: 0, maxR: 90, minG: 130, maxG: 220, minB: 20, maxB: 120}); err != nil {
		t.Fatal(err)
	}

	repeated := must(page.Locator("body").Screenshot(context.Background()))

	if repeated == nil || repeated.Bounds().Dx() != img.Bounds().Dx() || repeated.Bounds().Dy() != img.Bounds().Dy() {
		if repeated == nil {
			t.Fatalf("repeated body screenshot returned nil, want %dx%d", img.Bounds().Dx(), img.Bounds().Dy())
		}
		t.Fatalf("repeated body screenshot size = %dx%d, want %dx%d", repeated.Bounds().Dx(), repeated.Bounds().Dy(), img.Bounds().Dx(), img.Bounds().Dy())
	}

	foreground := must(execBrowser.APIBrowser.NewPage(context.Background()))
	t.Cleanup(func() { mustOK(foreground.Close(context.Background())) })
	mustOK(foreground.Navigate(context.Background(), session.FixtureURL("/mcp-test"), cdp.NavigateOptions{}))

	if active := must(execBrowser.APIBrowser.ActivePage(context.Background())).ID(); active != foreground.ID() {
		t.Fatalf("foreground page = %q, want %q", active, foreground.ID())
	}
	backgroundShot := must(page.Locator("body").Screenshot(context.Background()))

	if backgroundShot == nil || backgroundShot.Bounds().Dx() != img.Bounds().Dx() || backgroundShot.Bounds().Dy() != img.Bounds().Dy() {
		if backgroundShot == nil {
			t.Fatalf("background body screenshot returned nil, want %dx%d", img.Bounds().Dx(), img.Bounds().Dy())
		}
		t.Fatalf("background body screenshot size = %dx%d, want %dx%d", backgroundShot.Bounds().Dx(), backgroundShot.Bounds().Dy(), img.Bounds().Dx(), img.Bounds().Dy())
	}
	if visibility := evalValue(page, `return document.visibilityState`); visibility != `"visible"` {
		t.Fatalf("screenshot page visibility = %q, want visible; tracked active=%q", visibility, must(execBrowser.APIBrowser.ActivePage(context.Background())).ID())
	}
	if visibility := evalValue(foreground, `return document.visibilityState`); visibility == `"visible"` {
		t.Fatalf("previous foreground page remained visible after screenshot; tracked active=%q", must(execBrowser.APIBrowser.ActivePage(context.Background())).ID())
	}
}

func TestLocatorScreenshotTopHtmlUsesPageScreenshot(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/screenshot-scroll-stitch")
	img := must(page.Locator("html").Screenshot(context.Background()))

	if img == nil {
		t.Fatal("html screenshot returned nil image")
	}
	viewportWidth, err := evalScreenshotFloat(page, `window.visualViewport ? window.visualViewport.width : window.innerWidth`)
	if err != nil {
		t.Fatal(err)
	}
	viewportHeight, err := evalScreenshotFloat(page, `window.visualViewport ? window.visualViewport.height : window.innerHeight`)
	if err != nil {
		t.Fatal(err)
	}
	scaleX := float64(img.Bounds().Dx()) / math.Max(1400, viewportWidth)
	scaleY := float64(img.Bounds().Dy()) / math.Max(1300, viewportHeight)
	if err := assertScreenshotNativeScale("html page screenshot", scaleX, scaleY); err != nil {
		t.Fatal(err)
	}
	if err := assertScreenshotCSSRGB("html bottom-left marker", img, 60, 1298, scaleX, scaleY, screenshotRGBRange{minR: 200, maxR: 255, minG: 170, maxG: 245, minB: 0, maxB: 90}); err != nil {
		t.Fatal(err)
	}
}

func TestPageScreenshotWideScrollStitch(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/screenshot-complex-scroll")
	mustOK(page.ScrollTo(context.Background(), 640, 520))
	time.Sleep(time.Duration(100) *
		time.Millisecond)

	img := must(page.Screenshot(context.Background()))

	if img == nil {
		t.Fatal("wide stitched page screenshot returned nil image")
	}
	scaleX := float64(img.Bounds().Dx()) / 2400
	scaleY := float64(img.Bounds().Dy()) / 1800
	if err := assertScreenshotNativeScale("wide stitched page", scaleX, scaleY); err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() < 900 || img.Bounds().Dy() < 700 {
		t.Fatalf("wide stitched page screenshot too small: %dx%d", img.Bounds().Dx(), img.Bounds().Dy())
	}
	if math.Abs(scaleX-scaleY) > 0.08 {
		t.Fatalf("wide stitched page screenshot scale mismatch: %.2fx%.2f image=%dx%d", scaleX, scaleY, img.Bounds().Dx(), img.Bounds().Dy())
	}

	points := []struct {
		name string
		x    float64
		y    float64
		want screenshotRGBRange
	}{
		{name: "wide top-left marker", x: 76, y: 56, want: screenshotRGBRange{minR: 180, maxR: 255, minG: 0, maxG: 80, minB: 0, maxB: 90}},
		{name: "wide top-right tail marker", x: 2398, y: 56, want: screenshotRGBRange{minR: 0, maxR: 90, minG: 50, maxG: 140, minB: 180, maxB: 255}},
		{name: "wide bottom-left tail marker", x: 76, y: 1798, want: screenshotRGBRange{minR: 200, maxR: 255, minG: 170, maxG: 250, minB: 0, maxB: 100}},
		{name: "wide bottom-right tail marker", x: 2398, y: 1798, want: screenshotRGBRange{minR: 0, maxR: 90, minG: 130, maxG: 230, minB: 20, maxB: 130}},
	}
	for _, point := range points {
		if err := assertScreenshotCSSRGB(point.name, img, point.x, point.y, scaleX, scaleY, point.want); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPageScreenshotOverflowXHiddenAxis(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/screenshot-complex-scroll")
	evalText(page, `document.documentElement.style.overflowX = 'hidden'; document.body.style.overflowX = 'hidden'; window.scrollTo(0, 420);`)
	time.Sleep(time.Duration(100) *
		time.Millisecond)

	viewportWidth, err := evalScreenshotFloat(page, `window.visualViewport ? window.visualViewport.width : window.innerWidth`)
	if err != nil {
		t.Fatal(err)
	}
	img := must(page.Screenshot(context.Background()))

	if img == nil {
		t.Fatal("overflow-x hidden page screenshot returned nil image")
	}
	scaleY := float64(img.Bounds().Dy()) / 1800
	scaleX := float64(img.Bounds().Dx()) / math.Min(2400, viewportWidth)
	if err := assertScreenshotNativeScale("overflow-x hidden page", scaleX, scaleY); err != nil {
		t.Fatal(err)
	}
	expectedWidth := int(math.Round(math.Min(2400, viewportWidth) * scaleY))
	if math.Abs(float64(img.Bounds().Dx()-expectedWidth)) > 10 {
		t.Fatalf("overflow-x hidden page screenshot width mismatch: got %d want about %d, image=%dx%d viewport=%.2f scaleY=%.2f", img.Bounds().Dx(), expectedWidth, img.Bounds().Dx(), img.Bounds().Dy(), viewportWidth, scaleY)
	}
	if math.Abs(scaleX-scaleY) > 0.08 {
		t.Fatalf("overflow-x hidden page screenshot scale mismatch: %.2fx%.2f image=%dx%d", scaleX, scaleY, img.Bounds().Dx(), img.Bounds().Dy())
	}
	if err := assertScreenshotCSSRGB("overflow-x hidden top-left marker", img, 76, 56, scaleX, scaleY, screenshotRGBRange{minR: 180, maxR: 255, minG: 0, maxG: 80, minB: 0, maxB: 90}); err != nil {
		t.Fatal(err)
	}
	if err := assertScreenshotCSSRGB("overflow-x hidden bottom-left tail marker", img, 76, 1798, scaleX, scaleY, screenshotRGBRange{minR: 200, maxR: 255, minG: 170, maxG: 250, minB: 0, maxB: 100}); err != nil {
		t.Fatal(err)
	}
}

func TestLocatorScreenshotScrollParentStitch(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/screenshot-scroll-stitch")
	img := must(page.ByTestID("scroll-stitch-target").Screenshot(context.Background()))

	if img == nil {
		t.Fatal("scroll parent locator screenshot returned nil image")
	}
	scaleX := float64(img.Bounds().Dx()) / 320
	scaleY := float64(img.Bounds().Dy()) / 260
	if math.Abs(scaleX-scaleY) > 0.08 {
		t.Fatalf("unexpected locator screenshot scale: %.2fx%.2f, image=%dx%d", scaleX, scaleY, img.Bounds().Dx(), img.Bounds().Dy())
	}
	if err := assertScreenshotMarkerLeadingEdges("scroll parent locator", img); err != nil {
		t.Fatal(err)
	}
	points := []struct {
		name string
		x    float64
		y    float64
		want screenshotRGBRange
	}{
		{name: "top-left quadrant", x: 50, y: 50, want: screenshotRGBRange{minR: 180, maxR: 255, minG: 0, maxG: 90, minB: 0, maxB: 90}},
		{name: "center quadrant", x: 235, y: 200, want: screenshotRGBRange{minR: 0, maxR: 90, minG: 130, maxG: 230, minB: 20, maxB: 130}},
	}
	for _, point := range points {
		if err := assertScreenshotCSSRGB(point.name, img, point.x, point.y, scaleX, scaleY, point.want); err != nil {
			t.Fatal(err)
		}
	}
	if err := assertScreenshotTrailingEdgeProbes("scroll parent locator", img, 320, 260, scaleX, scaleY); err != nil {
		t.Fatal(err)
	}
}

func TestLocatorScreenshotSelfScrollStitch(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/screenshot-scroll-stitch")
	scrollStateJS := `(() => {
		const target = document.querySelector('[data-testid="self-scroll-box"]');
		return [target.scrollLeft, target.scrollTop];
	})()`
	evalText(page, `const target = document.querySelector('[data-testid="self-scroll-box"]'); target.scrollLeft = 44; target.scrollTop = 58;`)
	before, err := evalScreenshotFloatSlice(page, scrollStateJS)
	if err != nil {
		t.Fatal(err)
	}

	img := must(page.ByTestID("self-scroll-box").Screenshot(context.Background()))

	if img == nil {
		t.Fatal("self scroll locator screenshot returned nil image")
	}
	scaleX := float64(img.Bounds().Dx()) / 320
	scaleY := float64(img.Bounds().Dy()) / 260
	if err := assertScreenshotNativeScale("self scroll locator", scaleX, scaleY); err != nil {
		t.Fatal(err)
	}
	if math.Abs(scaleX-scaleY) > 0.08 {
		t.Fatalf("self scroll locator scale mismatch: %.2fx%.2f image=%dx%d", scaleX, scaleY, img.Bounds().Dx(), img.Bounds().Dy())
	}
	dpr, err := evalScreenshotFloat(page, `window.devicePixelRatio || 1`)
	if err != nil {
		t.Fatal(err)
	}
	expectedHeight := int(math.Round(260 * dpr))
	if math.Abs(float64(img.Bounds().Dy()-expectedHeight)) > 2 {
		t.Fatalf("self scroll locator height mismatch: got %d want about %d, image=%dx%d dpr=%.2f", img.Bounds().Dy(), expectedHeight, img.Bounds().Dx(), img.Bounds().Dy(), dpr)
	}
	points := []struct {
		name string
		x    float64
		y    float64
		want screenshotRGBRange
	}{
		{name: "self scroll top-left quadrant", x: 50, y: 50, want: screenshotRGBRange{minR: 180, maxR: 255, minG: 0, maxG: 90, minB: 0, maxB: 90}},
		{name: "self scroll center quadrant", x: 235, y: 200, want: screenshotRGBRange{minR: 0, maxR: 90, minG: 130, maxG: 230, minB: 20, maxB: 130}},
	}
	for _, point := range points {
		if err := assertScreenshotCSSRGB(point.name, img, point.x, point.y, scaleX, scaleY, point.want); err != nil {
			t.Fatal(err)
		}
	}
	if err := assertScreenshotTrailingEdgeProbes("self scroll locator", img, 320, 260, scaleX, scaleY); err != nil {
		t.Fatal(err)
	}

	after, err := evalScreenshotFloatSlice(page, scrollStateJS)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatalf("self scroll state length mismatch: before=%v after=%v", before, after)
	}
	for i := range before {
		if math.Abs(before[i]-after[i]) > 1 {
			t.Fatalf("self scroll state was not restored: before=%v after=%v", before, after)
		}
	}
}

func TestLocatorScreenshotLargeSelfScrollStitch(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/screenshot-large-self-scroll")
	stateJS := `(() => {
		const target = document.querySelector('[data-testid="large-self-scroll-list"]');
		return [target.clientWidth, target.clientHeight, target.scrollHeight, target.scrollTop, window.scrollY];
	})()`
	evalText(page, `const target = document.querySelector('[data-testid="large-self-scroll-list"]'); target.scrollTop = 312; window.scrollTo(0, 0);`)
	before, err := evalScreenshotFloatSlice(page, stateJS)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 5 {
		t.Fatalf("large self scroll state length mismatch: %v", before)
	}
	dpr, err := evalScreenshotFloat(page, `window.devicePixelRatio || 1`)
	if err != nil {
		t.Fatal(err)
	}

	img := must(page.ByTestID("large-self-scroll-list").Screenshot(context.Background()))

	if img == nil {
		t.Fatal("large self scroll locator screenshot returned nil image")
	}
	contentWidth := before[0]
	contentHeight := before[2]
	scaleX := float64(img.Bounds().Dx()) / contentWidth
	expectedHeight := int(math.Round(contentHeight * dpr))
	if math.Abs(float64(img.Bounds().Dy()-expectedHeight)) > 2 {
		t.Fatalf("large self scroll locator height mismatch: got %d want about %d, image=%dx%d dpr=%.2f", img.Bounds().Dy(), expectedHeight, img.Bounds().Dx(), img.Bounds().Dy(), dpr)
	}
	if err := assertScreenshotNativeScale("large self scroll locator", scaleX, dpr); err != nil {
		t.Fatal(err)
	}
	points := []struct {
		name string
		x    float64
		y    float64
		want screenshotRGBRange
	}{
		{name: "large self scroll top marker", x: 180, y: 60, want: screenshotRGBRange{minR: 190, maxR: 255, minG: 0, maxG: 90, minB: 0, maxB: 90}},
		{name: "large self scroll first middle marker", x: 180, y: 1100, want: screenshotRGBRange{minR: 0, maxR: 90, minG: 60, maxG: 140, minB: 190, maxB: 255}},
		{name: "large self scroll second middle marker", x: 180, y: 2430, want: screenshotRGBRange{minR: 200, maxR: 255, minG: 170, maxG: 250, minB: 0, maxB: 100}},
		{name: "large self scroll before-tail marker", x: 180, y: 3680, want: screenshotRGBRange{minR: 0, maxR: 90, minG: 130, maxG: 230, minB: 20, maxB: 130}},
		{name: "large self scroll tail marker", x: 180, y: 4940, want: screenshotRGBRange{minR: 0, maxR: 70, minG: 170, maxG: 255, minB: 170, maxB: 255}},
	}
	for _, point := range points {
		if err := assertScreenshotCSSRGB(point.name, img, point.x, point.y, scaleX, dpr, point.want); err != nil {
			t.Fatal(err)
		}
	}

	after, err := evalScreenshotFloatSlice(page, stateJS)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("large self scroll state length mismatch after screenshot: before=%v after=%v", before, after)
	}
	for _, index := range []int{3, 4} {
		if math.Abs(before[index]-after[index]) > 1 {
			t.Fatalf("large self scroll state was not restored: before=%v after=%v", before, after)
		}
	}
}

func TestLocatorScreenshotContainedSelfScrollFullContent(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/screenshot-contained-self-scroll")
	stateJS := `(() => {
		const target = document.querySelector('[data-testid="contained-self-scroll-list"]');
		return [target.clientWidth, target.clientHeight, target.scrollHeight, target.scrollTop, window.scrollY];
	})()`
	evalText(page, `const target = document.querySelector('[data-testid="contained-self-scroll-list"]'); target.scrollTop = 277; window.scrollTo(0, 0);`)
	before, err := evalScreenshotFloatSlice(page, stateJS)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 5 {
		t.Fatalf("contained self scroll state length mismatch: %v", before)
	}
	dpr, err := evalScreenshotFloat(page, `window.devicePixelRatio || 1`)
	if err != nil {
		t.Fatal(err)
	}

	img := must(page.ByTestID("contained-self-scroll-list").Screenshot(context.Background()))

	if img == nil {
		t.Fatal("contained self scroll locator screenshot returned nil image")
	}
	contentWidth := before[0]
	contentHeight := before[2]
	scaleX := float64(img.Bounds().Dx()) / contentWidth
	expectedHeight := int(math.Round(contentHeight * dpr))
	if math.Abs(float64(img.Bounds().Dy()-expectedHeight)) > 2 {
		t.Fatalf("contained self scroll locator height mismatch: got %d want about %d, image=%dx%d dpr=%.2f", img.Bounds().Dy(), expectedHeight, img.Bounds().Dx(), img.Bounds().Dy(), dpr)
	}
	if err := assertScreenshotNativeScale("contained self scroll locator", scaleX, dpr); err != nil {
		t.Fatal(err)
	}
	points := []struct {
		name string
		x    float64
		y    float64
		want screenshotRGBRange
	}{
		{name: "contained self scroll top marker", x: 180, y: 55, want: screenshotRGBRange{minR: 190, maxR: 255, minG: 0, maxG: 90, minB: 0, maxB: 90}},
		{name: "contained self scroll middle marker", x: 180, y: 900, want: screenshotRGBRange{minR: 0, maxR: 90, minG: 60, maxG: 140, minB: 190, maxB: 255}},
		{name: "contained self scroll low marker", x: 180, y: 1720, want: screenshotRGBRange{minR: 200, maxR: 255, minG: 170, maxG: 250, minB: 0, maxB: 100}},
		{name: "contained self scroll tail marker", x: 180, y: 2140, want: screenshotRGBRange{minR: 0, maxR: 70, minG: 170, maxG: 255, minB: 170, maxB: 255}},
	}
	for _, point := range points {
		if err := assertScreenshotCSSRGB(point.name, img, point.x, point.y, scaleX, dpr, point.want); err != nil {
			t.Fatal(err)
		}
	}

	after, err := evalScreenshotFloatSlice(page, stateJS)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("contained self scroll state length mismatch after screenshot: before=%v after=%v", before, after)
	}
	for _, index := range []int{3, 4} {
		if math.Abs(before[index]-after[index]) > 1 {
			t.Fatalf("contained self scroll state was not restored: before=%v after=%v", before, after)
		}
	}
}

func TestLocatorScreenshotDeepNestedScrollStitch(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/screenshot-complex-scroll")
	scrollStateJS := `(() => {
		const outer = document.querySelector('[data-testid="deep-outer"]');
		const middle = document.querySelector('[data-testid="deep-middle"]');
		const inner = document.querySelector('[data-testid="deep-inner"]');
		return [outer.scrollLeft, outer.scrollTop, middle.scrollLeft, middle.scrollTop, inner.scrollLeft, inner.scrollTop];
	})()`
	before, err := evalScreenshotFloatSlice(page, scrollStateJS)
	if err != nil {
		t.Fatal(err)
	}

	img := must(page.ByTestID("deep-scroll-target").Screenshot(context.Background()))

	if img == nil {
		t.Fatal("deep nested scroll locator screenshot returned nil image")
	}
	scaleX := float64(img.Bounds().Dx()) / 460
	scaleY := float64(img.Bounds().Dy()) / 360
	if math.Abs(scaleX-scaleY) > 0.08 {
		t.Fatalf("deep nested scroll locator scale mismatch: %.2fx%.2f image=%dx%d", scaleX, scaleY, img.Bounds().Dx(), img.Bounds().Dy())
	}
	if err := assertScreenshotMarkerLeadingEdges("deep nested scroll locator", img); err != nil {
		t.Fatal(err)
	}
	points := []struct {
		name string
		x    float64
		y    float64
		want screenshotRGBRange
	}{
		{name: "deep nested top-left quadrant", x: 70, y: 60, want: screenshotRGBRange{minR: 180, maxR: 255, minG: 0, maxG: 90, minB: 0, maxB: 90}},
		{name: "deep nested center quadrant", x: 365, y: 285, want: screenshotRGBRange{minR: 0, maxR: 90, minG: 130, maxG: 230, minB: 20, maxB: 130}},
	}
	for _, point := range points {
		if err := assertScreenshotCSSRGB(point.name, img, point.x, point.y, scaleX, scaleY, point.want); err != nil {
			t.Fatal(err)
		}
	}
	if err := assertScreenshotTrailingEdgeProbes("deep nested scroll locator", img, 460, 360, scaleX, scaleY); err != nil {
		t.Fatal(err)
	}

	after, err := evalScreenshotFloatSlice(page, scrollStateJS)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatalf("deep nested scroll state length mismatch: before=%v after=%v", before, after)
	}
	for i := range before {
		if math.Abs(before[i]-after[i]) > 1 {
			t.Fatalf("deep nested scroll state was not restored: before=%v after=%v", before, after)
		}
	}
}

func TestLocatorScreenshotOverflowXHiddenYScroll(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/screenshot-complex-scroll")
	visibleWidth, err := evalScreenshotFloat(page, `document.querySelector('[data-testid="axis-box"]').clientWidth`)
	if err != nil {
		t.Fatal(err)
	}
	if visibleWidth <= 0 {
		t.Fatalf("invalid axis hidden client width: %.2f", visibleWidth)
	}

	img := must(page.ByTestID("axis-hidden-target").Screenshot(context.Background()))

	if img == nil {
		t.Fatal("overflow-x hidden y-scroll locator screenshot returned nil image")
	}
	scaleX := float64(img.Bounds().Dx()) / visibleWidth
	scaleY := float64(img.Bounds().Dy()) / 330
	if math.Abs(scaleX-scaleY) > 0.08 {
		t.Fatalf("overflow-x hidden y-scroll locator scale mismatch: %.2fx%.2f image=%dx%d visibleWidth=%.2f", scaleX, scaleY, img.Bounds().Dx(), img.Bounds().Dy(), visibleWidth)
	}
	if visibleWidth > 230 {
		t.Fatalf("overflow-x hidden fixture did not constrain the horizontal axis enough: clientWidth=%.2f", visibleWidth)
	}
	if err := assertScreenshotRGB("overflow-x hidden y-scroll top border", img, img.Bounds().Dx()/2, screenshotBorderWidth/2, screenshotRGBRange{minR: 220, maxR: 255, minG: 0, maxG: 60, minB: 160, maxB: 240}); err != nil {
		t.Fatal(err)
	}
	if err := assertScreenshotRGB("overflow-x hidden y-scroll left border", img, screenshotBorderWidth/2, img.Bounds().Dy()/2, screenshotRGBRange{minR: 220, maxR: 255, minG: 0, maxG: 60, minB: 160, maxB: 240}); err != nil {
		t.Fatal(err)
	}
	if err := assertScreenshotCSSRGB("overflow-x hidden y-scroll upper visible quadrant", img, 50, 50, scaleX, scaleY, screenshotRGBRange{minR: 180, maxR: 255, minG: 0, maxG: 90, minB: 0, maxB: 90}); err != nil {
		t.Fatal(err)
	}
	if err := assertScreenshotCSSRGB("overflow-x hidden y-scroll lower visible quadrant", img, 50, 270, scaleX, scaleY, screenshotRGBRange{minR: 200, maxR: 255, minG: 170, maxG: 250, minB: 0, maxB: 100}); err != nil {
		t.Fatal(err)
	}
	if err := assertScreenshotCSSRGB("overflow-x hidden y-scroll bottom outer probe", img, 50, 327, scaleX, scaleY, screenshotRGBRange{minR: 100, maxR: 190, minG: 190, maxG: 255, minB: 0, maxB: 90}); err != nil {
		t.Fatal(err)
	}
	if err := assertScreenshotCSSRGB("overflow-x hidden y-scroll bottom middle probe", img, 50, 324, scaleX, scaleY, screenshotRGBRange{minR: 220, maxR: 255, minG: 40, maxG: 130, minB: 120, maxB: 220}); err != nil {
		t.Fatal(err)
	}
	if err := assertScreenshotCSSRGB("overflow-x hidden y-scroll bottom inner probe", img, 50, 321, scaleX, scaleY, screenshotRGBRange{minR: 0, maxR: 90, minG: 40, maxG: 120, minB: 120, maxB: 210}); err != nil {
		t.Fatal(err)
	}
	if err := assertScreenshotCSSRGB("overflow-x hidden y-scroll right edge stays left half", img, visibleWidth-18, 50, scaleX, scaleY, screenshotRGBRange{minR: 180, maxR: 255, minG: 0, maxG: 90, minB: 0, maxB: 90}); err != nil {
		t.Fatal(err)
	}
}

func TestLocatorScreenshotFrameParentScrollStitch(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/screenshot-frame-scroll")
	mustOK(page.ScrollTo(context.Background(), 0, 0))
	time.Sleep(time.Duration(100) *
		time.Millisecond)

	img := must(page.FrameLocator("#screenshot-frame").ByTestID("frame-scroll-target").Screenshot(context.Background()))

	if img == nil {
		t.Fatal("frame parent scroll locator screenshot returned nil image")
	}
	scaleX := float64(img.Bounds().Dx()) / 300
	scaleY := float64(img.Bounds().Dy()) / 220
	if math.Abs(scaleX-scaleY) > 0.08 {
		t.Fatalf("frame parent scroll locator scale mismatch: %.2fx%.2f image=%dx%d", scaleX, scaleY, img.Bounds().Dx(), img.Bounds().Dy())
	}
	if err := assertScreenshotMarkerLeadingEdges("frame parent scroll locator", img); err != nil {
		t.Fatal(err)
	}
	points := []struct {
		name string
		x    float64
		y    float64
		want screenshotRGBRange
	}{
		{name: "frame parent top-left quadrant", x: 55, y: 45, want: screenshotRGBRange{minR: 180, maxR: 255, minG: 0, maxG: 90, minB: 0, maxB: 90}},
		{name: "frame parent center quadrant", x: 245, y: 175, want: screenshotRGBRange{minR: 0, maxR: 90, minG: 130, maxG: 230, minB: 20, maxB: 130}},
	}
	for _, point := range points {
		if err := assertScreenshotCSSRGB(point.name, img, point.x, point.y, scaleX, scaleY, point.want); err != nil {
			t.Fatal(err)
		}
	}
	if err := assertScreenshotTrailingEdgeProbes("frame parent scroll locator", img, 300, 220, scaleX, scaleY); err != nil {
		t.Fatal(err)
	}
}

func TestLocatorScreenshotFrameBodyUsesFrameDocumentScreenshot(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/screenshot-frame-scroll")
	mustOK(page.ScrollTo(context.Background(), 0, 0))
	time.Sleep(time.Duration(100) *
		time.Millisecond)

	img := must(page.FrameLocator("#screenshot-frame").Locator("body").Screenshot(context.Background()))

	if img == nil {
		t.Fatal("frame body screenshot returned nil image")
	}
	scaleX := float64(img.Bounds().Dx()) / 720
	scaleY := float64(img.Bounds().Dy()) / 620
	if err := assertScreenshotNativeScale("frame body document screenshot", scaleX, scaleY); err != nil {
		t.Fatal(err)
	}
	if math.Abs(scaleX-scaleY) > 0.08 {
		t.Fatalf("frame body document screenshot scale mismatch: %.2fx%.2f image=%dx%d", scaleX, scaleY, img.Bounds().Dx(), img.Bounds().Dy())
	}
	if err := assertScreenshotCSSRGB("frame body top-left marker", img, 40, 35, scaleX, scaleY, screenshotRGBRange{minR: 180, maxR: 255, minG: 0, maxG: 70, minB: 0, maxB: 80}); err != nil {
		t.Fatal(err)
	}
	if err := assertScreenshotCSSRGB("frame body bottom-right marker", img, 700, 600, scaleX, scaleY, screenshotRGBRange{minR: 0, maxR: 90, minG: 130, maxG: 220, minB: 20, maxB: 120}); err != nil {
		t.Fatal(err)
	}
}

func TestLocatorScreenshotZeroSizeErrorHasTargetDetails(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/screenshot-scroll-stitch")
	evalText(page, `(() => {
		const node = document.createElement('div');
		node.setAttribute('data-testid', 'zero-shot-target');
		node.style.cssText = 'width:0;height:0;overflow:hidden;';
		document.body.appendChild(node);
	})()`)

	msg := expectPanicMessage(func() {
		must(page.ByTestID("zero-shot-target").Screenshot(context.Background()))

	})
	for _, want := range []string{"invalid screenshot target", "target=div", "elementRect=0.00x0.00"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("zero-size screenshot error missing %q: %s", want, msg)
		}
	}
}

func TestLocatorScreenshotOverflowHiddenVisibleOnly(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/screenshot-scroll-stitch")
	full := must(page.ByTestID("scroll-stitch-target").Screenshot(context.Background()))

	if full == nil {
		t.Fatal("reference locator screenshot returned nil image")
	}
	scaleX := float64(full.Bounds().Dx()) / 320
	scaleY := float64(full.Bounds().Dy()) / 260

	img := must(page.ByTestID("hidden-clip-target").Screenshot(context.Background()))

	if img == nil {
		t.Fatal("hidden clip locator screenshot returned nil image")
	}
	expectedWidth := int(math.Round(150 * scaleX))
	expectedHeight := int(math.Round(120 * scaleY))
	if math.Abs(float64(img.Bounds().Dx()-expectedWidth)) > 4 || math.Abs(float64(img.Bounds().Dy()-expectedHeight)) > 4 {
		t.Fatalf("hidden clip screenshot size mismatch: got %dx%d want about %dx%d", img.Bounds().Dx(), img.Bounds().Dy(), expectedWidth, expectedHeight)
	}
	if err := assertScreenshotRGB("hidden clip top border", img, img.Bounds().Dx()/2, screenshotBorderWidth/2, screenshotRGBRange{minR: 220, maxR: 255, minG: 0, maxG: 60, minB: 160, maxB: 240}); err != nil {
		t.Fatal(err)
	}
	if err := assertScreenshotRGB("hidden clip left border", img, screenshotBorderWidth/2, img.Bounds().Dy()/2, screenshotRGBRange{minR: 220, maxR: 255, minG: 0, maxG: 60, minB: 160, maxB: 240}); err != nil {
		t.Fatal(err)
	}
	if err := assertScreenshotCSSRGB("hidden clip visible quadrant", img, 50, 50, scaleX, scaleY, screenshotRGBRange{minR: 180, maxR: 255, minG: 0, maxG: 90, minB: 0, maxB: 90}); err != nil {
		t.Fatal(err)
	}
	x := img.Bounds().Dx() - max(2, int(math.Round(10*scaleX)))
	y := img.Bounds().Dy() - max(2, int(math.Round(10*scaleY)))
	pixel := img.RGBAAt(x, y)
	r, g, b := int(pixel.R), int(pixel.G), int(pixel.B)
	if g > 120 && r < 120 {
		t.Fatalf("hidden clip screenshot leaked bottom-right green quadrant at (%d,%d): rgb(%d,%d,%d)", x, y, r, g, b)
	}
}
