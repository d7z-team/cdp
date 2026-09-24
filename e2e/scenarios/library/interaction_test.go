package library

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"strings"
	"testing"
	"time"

	"gopkg.d7z.net/cdp"
)

type formEvent struct {
	Type      string `json:"type"`
	IsTrusted bool   `json:"isTrusted"`
	Value     string `json:"value"`
}

type interactionPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

func readInteractionPoint(t *testing.T, target evaluator) interactionPoint {
	t.Helper()
	raw := evalValue(target, `
		const rect = this.getBoundingClientRect();
		return {x: rect.left + rect.width / 2, y: rect.top + rect.height / 2};
	`)
	var point interactionPoint
	if err := json.Unmarshal([]byte(raw), &point); err != nil {
		t.Fatalf("decode interaction point: %v; raw=%s", err, raw)
	}
	return point
}

func readFormEvents(t *testing.T, page evaluator, name string) []formEvent {
	t.Helper()
	raw := evalValue(page, fmt.Sprintf(`return window.__formEvents[%q] || []`, name))
	var events []formEvent
	if err := json.Unmarshal([]byte(raw), &events); err != nil {
		t.Fatalf("unmarshal form events %s: %v raw=%q", name, err, raw)
	}
	return events
}

func hasFormEvent(events []formEvent, eventType string) bool {
	for _, event := range events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}

func TestClickButtonCounter(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/")
	mustOK(page.ByTestID("counter").Click(context.Background()))

	got := must(page.ByTestID("counter").TextContent(context.Background()))

	if got != "clicked:1" {
		t.Fatalf("unexpected counter text: %q", got)
	}
}

func TestBackgroundPageForegroundInteractions(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	session := acquireSession(t)
	pageA, err := session.Open("/mcp-test")
	if err != nil {
		t.Fatal(err)
	}
	pageB := must(execBrowser.APIBrowser.NewPage(context.Background()))
	t.Cleanup(func() { mustOK(pageB.Close(context.Background())) })
	mustOK(pageB.Navigate(context.Background(), session.FixtureURL("/mcp-test"), cdp.NavigateOptions{}))

	if active := must(execBrowser.APIBrowser.ActivePage(context.Background())).ID(); active != pageB.ID() {
		t.Fatalf("new foreground page = %q, want %q", active, pageB.ID())
	}
	mustOK(pageA.ByTestID("btn-counter").Click(context.Background()))

	if got := must(pageA.ByTestID("btn-counter").TextContent(context.Background())); got != "Clicked: 1" {
		t.Fatalf("background selector click result = %q", got)
	}
	if active := must(execBrowser.APIBrowser.ActivePage(context.Background())).ID(); active != pageA.ID() {
		t.Fatalf("selector click active page = %q, want %q", active, pageA.ID())
	}
	mustOK(pageB.Activate(context.Background()))

	pointA := readInteractionPoint(t, pageA.ByTestID("btn-counter"))
	if err := pageA.MouseClick(context.Background(), pointA.X, pointA.Y); err != nil {
		t.Fatalf("direct core background click: %v", err)
	}
	if got := must(pageA.ByTestID("btn-counter").TextContent(context.Background())); got != "Clicked: 2" {
		t.Fatalf("direct core background click result = %q", got)
	}
	mustOK(pageB.Activate(context.Background()))

	inputA := pageA.Locator("#text-input")
	mustOK(inputA.Fill(context.Background(), "foreground"))
	mustOK(inputA.Press(context.Background(), "K", cdp.KeyOptions{}))

	if got := must(inputA.Value(context.Background())); got != "foregroundK" {
		t.Fatalf("background input and press value = %q", got)
	}
	if active := must(execBrowser.APIBrowser.ActivePage(context.Background())).ID(); active != pageA.ID() {
		t.Fatalf("keyboard active page = %q, want %q", active, pageA.ID())
	}

	pointB := readInteractionPoint(t, pageB.ByTestID("btn-counter"))
	start := make(chan struct{})
	errs := make(chan error, 2)
	go func() {
		<-start
		errs <- pageA.MouseClick(context.Background(), pointA.X, pointA.Y)
	}()
	go func() {
		<-start
		errs <- pageB.MouseClick(context.Background(), pointB.X, pointB.Y)
	}()
	close(start)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent background click: %v", err)
		}
	}
	if got := must(pageA.ByTestID("btn-counter").TextContent(context.Background())); got != "Clicked: 3" {
		t.Fatalf("page A concurrent click result = %q", got)
	}
	if got := must(pageB.ByTestID("btn-counter").TextContent(context.Background())); got != "Clicked: 1" {
		t.Fatalf("page B concurrent click result = %q", got)
	}
	mustOK(pageA.Activate(context.Background()))

	if active := must(execBrowser.APIBrowser.ActivePage(context.Background())).ID(); active != pageA.ID() {
		t.Fatalf("explicit BringToFront active page = %q, want %q", active, pageA.ID())
	}
	mustOK(pageB.Activate(context.Background()))
	mustOK(pageA.Navigate(context.Background(), session.FixtureURL("/events"), cdp.NavigateOptions{}))
	mustOK(pageA.ByTestID("drag-source").DragTo(context.Background(), pageA.ByTestID("drag-target")))
	if got := attribute(pageA.ByTestID("drag-target"), "data-drop"); got != "received" {
		t.Fatalf("background drag result = %q", got)
	}

}

func TestMouseVisualActionPipeline(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/")
	if got := page.ActionMode(); got != "fast" {
		t.Fatalf("e2e page action mode = %q, want fast", got)
	}
	coreRuntime := must(page.ExecutionContext(context.Background(), cdp.ExecutionContextOptions{World: cdp.WorldCore}))
	evalText(coreRuntime, `
		const ffi = window.__cdp_ffi;
		if (!ffi?.showMouseAction) throw new Error('semantic mouse visual API unavailable');
		ffi.__mouseVisualProbe = { actions: [] };
		const original = ffi.showMouseAction.bind(ffi);
		ffi.showMouseAction = function(action) {
			ffi.__mouseVisualProbe.actions.push(structuredClone(action));
			return original(action);
		};
	`)
	mustOK(page.ByTestID("counter").Click(context.Background()))

	if got := evalValue(coreRuntime, `return window.__cdp_ffi.__mouseVisualProbe.actions.length`); got != "1" {
		t.Fatalf("selector click visual action count = %s, want 1", got)
	}
	if got := evalValue(coreRuntime, `return window.__cdp_ffi.__mouseVisualProbe.actions[0].kind === 'click' && window.__cdp_ffi.__mouseVisualProbe.actions[0].mode === 'fast'`); got != "true" {
		t.Fatalf("unexpected selector click visual action: %q", got)
	}

	raw := evalValue(page.ByTestID("counter"), `
		const rect = this.getBoundingClientRect();
		return {
			x: rect.left + rect.width / 2,
			y: rect.top + rect.height / 2,
			edgeX: rect.left + Math.min(8, rect.width / 2),
			edgeY: rect.top + 1
		};
	`)
	var point struct {
		X     float64 `json:"x"`
		Y     float64 `json:"y"`
		EdgeX float64 `json:"edgeX"`
		EdgeY float64 `json:"edgeY"`
	}
	if err := json.Unmarshal([]byte(raw), &point); err != nil {
		t.Fatalf("decode counter point: %v; raw=%s", err, raw)
	}
	mustOK(page.MouseClick(context.Background(), point.X, point.Y))
	if got := evalValue(coreRuntime, `return window.__cdp_ffi.__mouseVisualProbe.actions.length`); got != "2" {
		t.Fatalf("coordinate click visual action count = %s, want 2", got)
	}
	if got := evalValue(coreRuntime, `return window.__cdp_ffi.__mouseVisualProbe.actions[1].kind === 'click' && window.__cdp_ffi.__mouseVisualProbe.actions[1].mode === 'fast'`); got != "true" {
		t.Fatalf("unexpected coordinate click visual action: %q", got)
	}

	page = configuredPage(t, page, cdp.ConnectOptions{ActionMode: "strict"})
	mustOK(page.MouseMove(context.Background(), point.EdgeX, point.EdgeY))
	if got := evalValue(coreRuntime, `return window.__cdp_ffi.__mouseVisualProbe.actions.length === 3 && window.__cdp_ffi.__mouseVisualProbe.actions[2].kind === 'move' && window.__cdp_ffi.__mouseVisualProbe.actions[2].mode === 'strict'`); got != "true" {
		t.Fatalf("strict mouse move should emit one semantic visual action, got %q", got)
	}
	if got := evalValue(coreRuntime, `
		const host = document.getElementById('__cdp_pointer_overlay_host');
		return !!host?.shadowRoot && host.shadowRoot.querySelectorAll('#__cdp_pointer_canvas').length === 1;
	`); got != "true" {
		t.Fatalf("expected exactly one pointer canvas, got %q", got)
	}
	time.Sleep(time.Duration(1600) *
		time.Millisecond)

	if got := evalValue(coreRuntime, `return document.getElementById('__cdp_pointer_overlay_host')?.shadowRoot?.querySelector('#__cdp_pointer_canvas')?.style.display === 'none'`); got != "true" {
		t.Fatalf("pointer should automatically hide in released state, got %q", got)
	}

	got := must(page.ByTestID("counter").TextContent(context.Background()))

	if got != "clicked:2" {
		t.Fatalf("unexpected counter text after fast click paths: %q", got)
	}

	page = configuredPage(t, page, cdp.ConnectOptions{ActionMode: "fast"})
	mustOK(page.Navigate(context.Background(), fixtureRT.Server.URL+"/form", cdp.NavigateOptions{}))
	time.Sleep(time.Duration(1400) *
		time.Millisecond)

	newRuntime := must(page.ExecutionContext(context.Background(), cdp.ExecutionContextOptions{World: cdp.WorldCore}))
	if got := evalValue(newRuntime, `return document.getElementById('__cdp_pointer_overlay_host') === null`); got != "true" {
		t.Fatalf("old pointer timeline restored after navigation: %q", got)
	}
}

func TestInputMirror(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/form")
	mustOK(page.ByTestID("name-input").Fill(context.Background(), "sample-value"))
	value := must(page.ByTestID("name-input").Value(context.Background()))

	mirror := must(page.ByTestID("mirror").TextContent(context.Background()))

	if value != "sample-value" {
		t.Fatalf("unexpected input value: %q", value)
	}
	if mirror != "sample-value" {
		t.Fatalf("unexpected mirrored text: %q", mirror)
	}
}

func TestMouseVisualPolicyDistinguishesExplicitAndInternalActions(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/form")
	coreRuntime := must(page.ExecutionContext(context.Background(), cdp.ExecutionContextOptions{World: cdp.WorldCore}))
	evalText(coreRuntime, `
		const ffi = window.__cdp_ffi;
		if (!ffi?.showMouseAction) throw new Error('semantic mouse visual API unavailable');
		ffi.__mouseVisualProbe = { actions: [] };
		const original = ffi.showMouseAction.bind(ffi);
		ffi.showMouseAction = function(action) {
			ffi.__mouseVisualProbe.actions.push(structuredClone(action));
			return original(action);
		};
	`)
	mustOK(page.ByTestID("name-input").Fill(context.Background(), "sample-value"))
	if got := evalValue(coreRuntime, `return window.__cdp_ffi.__mouseVisualProbe.actions.length`); got != "0" {
		t.Fatalf("internal input focus emitted mouse visual action, count=%s", got)
	}
	mustOK(page.ByTestID("name-input").Hover(context.Background()))

	if got := evalValue(coreRuntime, `return window.__cdp_ffi.__mouseVisualProbe.actions.length === 1 && window.__cdp_ffi.__mouseVisualProbe.actions[0].kind === 'move'`); got != "true" {
		t.Fatalf("explicit hover should emit one move visual action, got %q", got)
	}
	mustOK(page.ByTestID("name-input").Click(context.Background()))

	if got := evalValue(coreRuntime, `return window.__cdp_ffi.__mouseVisualProbe.actions.length === 2 && window.__cdp_ffi.__mouseVisualProbe.actions[1].kind === 'click'`); got != "true" {
		t.Fatalf("explicit click should emit one click visual action, got %q", got)
	}
}

func TestVisibilitySemantics(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/visibility")
	visible := must(page.ByTestID("visible-box").IsVisible(context.Background()))

	hidden := must(page.ByTestID("hidden-box").IsVisible(context.Background()))

	if !visible {
		t.Fatal("expected visible-box to be visible")
	}
	if hidden {
		t.Fatal("expected hidden-box to be hidden")
	}
	mustOK(page.ByTestID("hidden-box").Wait(context.Background(), cdp.StateHidden))

	if scrollY := evalValue(page, `return window.scrollY`); scrollY != "0" {
		t.Fatalf("expected WaitForHidden not to scroll page, got scrollY=%q", scrollY)
	}
	mustOK(page.ByTestID("visible-box").Wait(context.Background(), cdp.StateVisible))

	requireNodeVisible := func(selector string, want bool) {
		t.Helper()
		element := must(page.Locator(selector).All(context.Background()))[0]
		got, err := element.IsVisible(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("Page.IsVisible(%s) = %v, want %v", selector, got, want)
		}
	}

	if must(page.ByTestID("opacity-child").IsVisible(context.Background())) {
		t.Fatal("expected child under opacity:0 ancestor to be hidden")
	}
	requireNodeVisible(`[data-testid="opacity-child"]`, false)
	if must(page.ByTestID("visibility-child").IsVisible(context.Background())) {
		t.Fatal("expected child under visibility:hidden ancestor to be hidden")
	}
	requireNodeVisible(`[data-testid="visibility-child"]`, false)
	if must(page.ByTestID("contents-parent").IsVisible(context.Background())) {
		t.Fatal("expected display:contents parent with no own layout box to be hidden")
	}
	requireNodeVisible(`[data-testid="contents-parent"]`, false)
	if !must(page.ByTestID("contents-child").IsVisible(context.Background())) {
		t.Fatal("expected child inside display:contents parent to be visible")
	}
	requireNodeVisible(`[data-testid="contents-child"]`, true)

	offscreen := page.ByTestID("offscreen-button")
	mustOK(page.ScrollTo(context.Background(), 0, 0))
	if scrollY := evalValue(page, `return window.scrollY`); scrollY != "0" {
		t.Fatalf("expected initial scrollY to be 0, got %q", scrollY)
	}
	if !must(offscreen.IsVisible(context.Background())) {
		t.Fatal("expected offscreen-button to be visible by DOM/CSS semantics")
	}
	if scrollY := evalValue(page, `return window.scrollY`); scrollY != "0" {
		t.Fatalf("expected IsVisible not to scroll page for offscreen-button, got scrollY=%q", scrollY)
	}
	requireNodeVisible(`[data-testid="offscreen-button"]`, true)
	if scrollY := evalValue(page, `return window.scrollY`); scrollY != "0" {
		t.Fatalf("expected Page.IsVisible not to scroll page for offscreen-button, got scrollY=%q", scrollY)
	}
	mustOK(page.ScrollTo(context.Background(), 0, 0))
	mustOK(offscreen.Wait(context.Background(), cdp.StateVisible))

	if scrollY := evalValue(page, `return window.scrollY`); scrollY != "0" {
		t.Fatal("visible-state wait must not scroll an offscreen element")
	}
	mustOK(page.ScrollTo(context.Background(), 0, 0))
	mustOK(offscreen.Click(context.Background()))

	if got := must(offscreen.TextContent(context.Background())); got != "offscreen-clicked" {
		t.Fatalf("unexpected offscreen button text after click: %q", got)
	}
	if scrollY := evalValue(page, `return window.scrollY`); scrollY == "0" {
		t.Fatal("expected Click to scroll page for offscreen-button")
	}
}

func TestAutoSelectSingleVisible(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/visibility")

	// 3 buttons with class .dup-visible-btn, only B is visible
	btn := page.Locator(".dup-visible-btn")
	mustOK( // Click: should auto-select the single visible button B
		btn.Click(context.Background()))

	if got := must(page.ByTestID("dup-btn-b").TextContent(context.Background())); got != "B" {
		t.Fatalf("expected B button text, got %q", got)
	}

	// TextContent: should read from the single visible button
	if got := must(page.Locator(".dup-visible-btn").TextContent(context.Background())); got != "B" {
		t.Fatalf("expected TextContent 'B' from visible button, got %q", got)
	}

	// IsVisible: should return true for the visible one
	if !must(page.Locator(".dup-visible-btn").IsVisible(context.Background())) {
		t.Fatal("expected IsVisible true when exactly 1 of 3 is visible")
	}

	// GetAttribute: should read from the visible one
	if got := attribute(page.Locator(".dup-visible-btn"), "data-testid"); got != "dup-btn-b" {
		t.Fatalf("expected GetAttribute from visible button, got %q", got)
	}

	// Count: should count all 3 (no visibility filtering)
	if got := must(page.Locator(".dup-visible-btn").Count(context.Background())); got != 3 {
		t.Fatalf("expected Count 3, got %d", got)
	}
}

func TestAutoSelectSingleVisibleAllHidden(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/visibility")
	evalText( // Hide the only visible button, now all 3 are hidden
		page, `document.querySelector('[data-testid="dup-btn-b"]').style.display = 'none'`)

	err := page.Locator(".dup-visible-btn").Click(context.Background())
	var detail *cdp.LocatorError
	if !errors.As(err, &detail) || detail.Counts.IDs != 3 || detail.Counts.Actionable != 0 {
		t.Fatalf("hidden matches: %v", err)
	}

}

func TestCheckUncheck(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/form-advanced")
	box := page.ByTestID("agree")
	mustOK(box.SetChecked(context.Background(), true))

	if !must(box.IsChecked(context.Background())) {
		t.Fatal("expected checkbox to be checked")
	}
	events := readFormEvents(t, page, "agree")
	if !hasFormEvent(events, "click") {
		t.Fatalf("Check should use native click before fallback, events=%+v", events)
	}
	mustOK(box.SetChecked(context.Background(), false))

	if must(box.IsChecked(context.Background())) {
		t.Fatal("expected checkbox to be unchecked")
	}
	events = readFormEvents(t, page, "agree")
	clicks := 0
	for _, event := range events {
		if event.Type == "click" {
			clicks++
		}
	}
	if clicks < 2 {
		t.Fatalf("Uncheck should use native click before fallback, events=%+v", events)
	}
}

func TestCheckFallsBackWhenNativeClickDoesNotChangeState(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/form-advanced")
	box := page.ByTestID("blocked-check")
	mustOK(box.SetChecked(context.Background(), true))

	if !must(box.IsChecked(context.Background())) {
		t.Fatal("expected blocked checkbox fallback to set checked")
	}
	events := readFormEvents(t, page, "blocked")
	if !hasFormEvent(events, "click") {
		t.Fatalf("fallback should still attempt native click first, events=%+v", events)
	}
	if !hasFormEvent(events, "change") {
		t.Fatalf("fallback should dispatch change for compatibility, events=%+v", events)
	}
}

func TestSelectOption(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/form-advanced")
	mustOK(page.ByTestID("city").Select(context.Background(), []string{"shanghai"}))
	value := must(page.ByTestID("city").Value(context.Background()))

	if value != "shanghai" {
		t.Fatalf("unexpected select value: %q", value)
	}
	state := must(page.ByTestID("state").TextContent(context.Background()))

	if state == "" || state == `{"agree":false,"city":""}` {
		t.Fatalf("unexpected state text: %q", state)
	}
	events := readFormEvents(t, page, "city")
	if !hasFormEvent(events, "change") {
		t.Fatalf("Select should dispatch change and verify value, events=%+v", events)
	}
}

func TestRecentScreencastImage(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/screencast-image")
	mustOK(page.Activate(context.Background()))

	stream := must(page.Screencast(context.Background(), cdp.ScreencastOptions{}))
	defer stream.Close()
	frame := must(stream.Next(context.Background(), 0))
	decoded := must(jpeg.Decode(bytes.NewReader(frame.Data)))
	img := image.NewRGBA(decoded.Bounds())
	draw.Draw(img, img.Bounds(), decoded, decoded.Bounds().Min, draw.Src)
	if img == nil {
		t.Fatal("recent screencast image returned nil")
	}
	if img.Bounds().Dx() < 200 || img.Bounds().Dy() < 120 {
		t.Fatalf("unexpected screencast image size: %dx%d", img.Bounds().Dx(), img.Bounds().Dy())
	}

	assertColor := func(name string, x, y int, check func(r, g, b int) bool) {
		t.Helper()
		pixel := img.RGBAAt(x, y)
		r, g, b := int(pixel.R), int(pixel.G), int(pixel.B)
		if !check(r, g, b) {
			t.Fatalf("%s color mismatch at (%d,%d): got rgb(%d,%d,%d)", name, x, y, r, g, b)
		}
	}

	width := img.Bounds().Dx()
	height := img.Bounds().Dy()
	assertColor("top-left", width/4, height/3, func(r, g, b int) bool {
		return r >= 180 && g <= 100 && b <= 100
	})
	assertColor("top-right", width*3/4, height/3, func(r, g, b int) bool {
		return g >= 180 && r <= 100 && b <= 120
	})
	assertColor("bottom-left", width/4, height*2/3, func(r, g, b int) bool {
		return b >= 180 && r <= 100 && g <= 100
	})
	assertColor("bottom-right", width*3/4, height*2/3, func(r, g, b int) bool {
		return r >= 180 && g >= 180 && b <= 120
	})
}

func TestFullscreenVirtualState(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/fullscreen")
	if must(page.IsFullscreen(context.Background())) {
		t.Fatal("expected initial fullscreen state to be false")
	}
	if enabled := evalValue(page, `return String(document.fullscreenEnabled)`); enabled != `"true"` {
		t.Fatalf("unexpected fullscreenEnabled value: %q", enabled)
	}
	evalText(page, `enterFullscreen()`)
	if got := must(page.ByTestID("fullscreen-result").TextContent(context.Background())); got != "enter-resolved" {
		t.Fatalf("unexpected enter result: %q", got)
	}
	if got := must(page.ByTestID("fullscreen-active").TextContent(context.Background())); got != "true" {
		t.Fatalf("unexpected fullscreen active text after enter: %q", got)
	}
	if got := must(page.ByTestID("fullscreen-element").TextContent(context.Background())); got != "fullscreen-panel" {
		t.Fatalf("unexpected fullscreen element text after enter: %q", got)
	}
	if got := must(page.ByTestID("fullscreen-count").TextContent(context.Background())); got != "1" {
		t.Fatalf("unexpected fullscreen change count after enter: %q", got)
	}
	if !must(page.IsFullscreen(context.Background())) {
		t.Fatal("expected page fullscreen state to be true after enter")
	}
	evalText(page, `exitFullscreen()`)
	if got := must(page.ByTestID("fullscreen-result").TextContent(context.Background())); got != "exit-resolved" {
		t.Fatalf("unexpected exit result: %q", got)
	}
	if got := must(page.ByTestID("fullscreen-active").TextContent(context.Background())); got != "false" {
		t.Fatalf("unexpected fullscreen active text after exit: %q", got)
	}
	if got := must(page.ByTestID("fullscreen-element").TextContent(context.Background())); got != "" {
		t.Fatalf("unexpected fullscreen element text after exit: %q", got)
	}
	if got := must(page.ByTestID("fullscreen-count").TextContent(context.Background())); got != "2" {
		t.Fatalf("unexpected fullscreen change count after exit: %q", got)
	}
	if must(page.IsFullscreen(context.Background())) {
		t.Fatal("expected page fullscreen state to be false after exit")
	}
}

func TestPrintInterceptPDF(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/print")
	wait := must(page.WatchPrint(context.Background()))
	defer wait.Close()
	mustOK(page.ByTestID("print-trigger").Click(context.Background()))

	artifact := must(wait.Wait(context.Background()))
	if len(artifact.Data) == 0 {
		t.Fatal("print artifact is nil")
	}
	if artifact.MIMEType != "application/pdf" {
		t.Fatalf("unexpected print mime type: %q", artifact.MIMEType)
	}
	if len(artifact.Data) <= 0 {
		t.Fatal("expected print artifact size > 0")
	}
	events := must(page.ByTestID("print-events").TextContent(context.Background()))

	if !strings.Contains(events, "beforeprint") || !strings.Contains(events, "afterprint") || !strings.HasSuffix(events, "afterprint") {
		t.Fatalf("unexpected print events order: %q", events)
	}
	state := attribute(page.ByTestID("print-target"), "data-print-state")
	if state != "after" {
		t.Fatalf("unexpected print target state after print: %q", state)
	}
}
