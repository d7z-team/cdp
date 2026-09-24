package library

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gopkg.d7z.net/cdp"
)

func TestPageContent(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/page-ops")
	content := must(page.Content(context.Background()))

	if !strings.Contains(content, "<h1>PageOps Test</h1>") {
		t.Fatal("Content should contain page HTML")
	}
}

func TestPageGetURL(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	sess := acquireSession(t)
	page, err := sess.Open("/page-ops")
	if err != nil {
		t.Fatal(err)
	}
	url := must(page.URL(context.Background()))

	if !strings.Contains(url, "/page-ops") {
		t.Fatalf("GetURL = %q, want URL containing /page-ops", url)
	}
}

func TestPageGetTitle(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/page-ops")
	title := must(page.Title(context.Background()))

	if title != "PageOps Test" {
		t.Fatalf("GetTitle = %q, want PageOps Test", title)
	}
}

func TestPageAlertShowHide(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/page-ops")
	mustOK(page.ShowAlert(context.Background(), "Test message"))
	time.Sleep(time.Duration(200) *
		time.Millisecond)
	mustOK(page.HideAlert(context.Background()))
	time.Sleep(time.Duration(100) *
		time.Millisecond)

}

func TestPageRefresh(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/page-ops")
	evalText(page, `document.body.setAttribute('data-refresh-test', 'before-refresh')`)
	mustOK(page.Reload(context.Background()))

	bodyVal := evalValue(page, "document.body && document.body.getAttribute('data-refresh-test')")
	if bodyVal != "null" && bodyVal != "undefined" {
		t.Fatalf("after Refresh, data-refresh-test = %q, want null/undefined", bodyVal)
	}
}

func TestPageScrollTo(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/page-ops")
	mustOK(page.ScrollTo(context.Background(), 0, 2000))
	time.Sleep(time.Duration(300) *
		time.Millisecond)

	scrollY := evalValue(page, "window.scrollY")
	t.Logf("scrollTo(0,2000) -> scrollY=%s", scrollY)
}

func TestPageHighlight(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/page-ops")
	mustOK(page.Highlight(context.Background(), "[data-testid=highlight-target]"))
}

func TestPageHighlightUsesMainFrameRuntimeWithIframeContexts(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/screenshot-scroll-stitch")
	coreRuntime := must(page.ExecutionContext(context.Background(), cdp.ExecutionContextOptions{World: cdp.WorldCore}))
	evalText(coreRuntime, `
		const ffi = window.__cdp_ffi;
		if (!ffi?.canvas?.drawMultiple) throw new Error('canvas drawMultiple unavailable');
		ffi.__highlightMainFrameProbe = {draws: []};
		const originalDrawMultiple = ffi.canvas.drawMultiple.bind(ffi.canvas);
		ffi.canvas.drawMultiple = function(boxes, options) {
			ffi.__highlightMainFrameProbe.draws.push({
				count: boxes?.length || 0,
				owner: options?.owner || '',
				modeName: options?.modeName || '',
			});
			return originalDrawMultiple(boxes, options);
		};
	`)
	mustOK(page.Highlight(context.Background(), ".page-marker"))

	raw := evalValue(coreRuntime, `return window.__cdp_ffi.__highlightMainFrameProbe`)
	var probe struct {
		Draws []struct {
			Count    int    `json:"count"`
			Owner    string `json:"owner"`
			ModeName string `json:"modeName"`
		} `json:"draws"`
	}
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		t.Fatalf("unmarshal main-frame highlight probe: %v raw=%s", err, raw)
	}
	if len(probe.Draws) == 0 {
		t.Fatalf("expected highlight draw in main-frame runtime, probe=%+v", probe)
	}
	lastDraw := probe.Draws[len(probe.Draws)-1]
	if lastDraw.Owner != "page-highlight" {
		t.Fatalf("highlight owner = %q, want page-highlight", lastDraw.Owner)
	}
	if lastDraw.Count != 4 {
		t.Fatalf("highlight draw count = %d, want 4", lastDraw.Count)
	}
}

func TestPageHighlightUsesDedicatedLayerAndDedupesRects(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/page-ops")
	coreRuntime := must(page.ExecutionContext(context.Background(), cdp.ExecutionContextOptions{World: cdp.WorldCore}))
	evalText(coreRuntime, `
		const ffi = window.__cdp_ffi;
		if (!ffi?.canvas?.drawMultiple) throw new Error('canvas drawMultiple unavailable');
		ffi.__highlightProbe = { draws: [], clears: [] };
		const originalDrawMultiple = ffi.canvas.drawMultiple.bind(ffi.canvas);
		ffi.canvas.drawMultiple = function(boxes, options) {
			ffi.__highlightProbe.draws.push({count: boxes?.length || 0, owner: options?.owner || '', revision: options?.revision || 0});
			return originalDrawMultiple(boxes, options);
		};
		const originalClear = ffi.canvas.clear.bind(ffi.canvas);
		ffi.canvas.clear = function(options) {
			ffi.__highlightProbe.clears.push({owner: options?.owner || '', revision: options?.revision || 0});
			return originalClear(options);
		};
	`)
	mustOK(page.Highlight(context.Background(), "[data-testid=highlight-target]", "[data-testid=highlight-target]"))

	raw := evalValue(coreRuntime, `return window.__cdp_ffi.__highlightProbe`)
	var probe struct {
		Draws []struct {
			Count    int    `json:"count"`
			Owner    string `json:"owner"`
			Revision int    `json:"revision"`
		} `json:"draws"`
		Clears []struct {
			Owner    string `json:"owner"`
			Revision int    `json:"revision"`
		} `json:"clears"`
	}
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		t.Fatalf("unmarshal highlight probe: %v raw=%s", err, raw)
	}
	if len(probe.Clears) == 0 {
		t.Fatalf("expected highlight to clear its layer, probe=%+v", probe)
	}
	lastClear := probe.Clears[len(probe.Clears)-1]
	if lastClear.Owner != "page-highlight" || lastClear.Revision == 0 {
		t.Fatalf("unexpected highlight clear metadata: %+v", lastClear)
	}
	if len(probe.Draws) == 0 {
		t.Fatalf("expected highlight draw, probe=%+v", probe)
	}
	lastDraw := probe.Draws[len(probe.Draws)-1]
	if lastDraw.Owner != "page-highlight" || lastDraw.Revision == 0 {
		t.Fatalf("unexpected highlight draw metadata: %+v", lastDraw)
	}
	if lastDraw.Count != 1 {
		t.Fatalf("expected duplicate selectors to draw one unique rect, got %d", lastDraw.Count)
	}
	mustOK(page.Highlight(context.Background(), "[data-testid=highlight-target]", "h1"))
	raw = evalValue(coreRuntime, `return window.__cdp_ffi.__highlightProbe`)
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		t.Fatalf("unmarshal multi-target highlight probe: %v raw=%s", err, raw)
	}
	lastDraw = probe.Draws[len(probe.Draws)-1]
	if lastDraw.Count != 2 {
		t.Fatalf("expected independent selectors to draw two rects, got %d", lastDraw.Count)
	}

}

func TestPageBackForward(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	sess := acquireSession(t)
	page, err := sess.Open("/page-ops")
	if err != nil {
		t.Fatal(err)
	}
	mustOK(page.Locator("[data-testid=nav-next-link]").Click(context.Background()))

	mustOK(page.WaitForURLMatch(context.Background(), func(url string) bool { return strings.HasPrefix(url, sess.FixtureURL("/navigation")) }))
	mustOK(page.Back(context.Background()))

	mustOK(page.WaitForURLMatch(context.Background(), func(url string) bool { return strings.HasPrefix(url, sess.FixtureURL("/page-ops")) }))
	if !strings.Contains(must(page.URL(context.Background())), "/page-ops") {
		t.Fatalf("Back() URL = %q, want /page-ops", must(page.URL(context.Background())))
	}
	mustOK(page.Forward(context.Background()))

	mustOK(page.WaitForURLMatch(context.Background(), func(url string) bool { return strings.HasPrefix(url, sess.FixtureURL("/navigation")) }))
	if !strings.Contains(must(page.URL(context.Background())), "/navigation") {
		t.Fatalf("Forward() URL = %q, want /navigation", must(page.URL(context.Background())))
	}
}
