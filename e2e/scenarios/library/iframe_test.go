package library

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.d7z.net/cdp"
)

type highlightProjectionProbe struct {
	ExpectedX float64 `json:"expectedX"`
	ExpectedY float64 `json:"expectedY"`
	LocalX    float64 `json:"localX"`
	LocalY    float64 `json:"localY"`
}

func readRectProbe(pageJS evaluator, expr string) (highlightProjectionProbe, error) {
	raw := evalValue(pageJS, expr)
	probe := highlightProjectionProbe{}
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		return highlightProjectionProbe{}, err
	}
	return probe, nil
}

func readCanvasAlphaAt(page *cdp.Page, x, y float64) (float64, error) {
	raw := evalValue(page, fmt.Sprintf(`
		const host = document.getElementById('__cdp_overlay_canvas_host_core');
		const canvas = host?.shadowRoot?.getElementById('__cdp_box_drawer_canvas_core');
		if (!(canvas instanceof HTMLCanvasElement)) {
			return -1;
		}
		const ctx = canvas.getContext('2d');
		if (!ctx) {
			return -1;
		}
		const rect = canvas.getBoundingClientRect();
		const dpr = window.devicePixelRatio || 1;
		let best = 0;
		for (let dy = -3; dy <= 3; dy++) {
			for (let dx = -3; dx <= 3; dx++) {
				const px = Math.round(((%f + dx) - rect.left) * dpr);
				const py = Math.round(((%f + dy) - rect.top) * dpr);
				if (px < 0 || py < 0 || px >= canvas.width || py >= canvas.height) {
					continue;
				}
				const alpha = ctx.getImageData(px, py, 1, 1).data[3];
				if (alpha > best) {
					best = alpha;
				}
			}
		}
		return best;
	`, x, y))
	return strconv.ParseFloat(strings.Trim(raw, `"`), 64)
}

func TestIframeContentFrameClick(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/iframe")
	frame := page.FrameLocator("#demo-frame")
	mustOK(frame.Locator("[data-testid='frame-btn']").Click(context.Background()))

	got := must(frame.Locator("[data-testid='frame-btn']").TextContent(context.Background()))

	if got != "frame-clicked" {
		t.Fatalf("unexpected iframe button text: %q", got)
	}
}

func TestImplicitIframeFallback(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/iframe")
	text := must(page.Locator("#demo-frame").Locator("[data-testid='frame-btn']").TextContent(context.Background()))

	if text != "frame-button" {
		t.Fatalf("unexpected implicit iframe fallback text: %q", text)
	}
}

func TestIframeSelectorQueryIndexedRectsSkipMissingLayouts(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/iframe")
	coreRuntime := must(page.ExecutionContext(context.Background(), cdp.ExecutionContextOptions{World: cdp.WorldCore}))
	raw := evalValue(coreRuntime, `
		const ffi = window.__cdp_ffi;
		const plan = {
			layers: [
				{options: [{kind: 'selector', raw: '#demo-frame'}]},
				{options: [{kind: 'frame_enter'}]},
				{options: [{kind: 'selector', raw: '.rect-item'}]},
			],
		};
		const session = await ffi.startSelectorQuery(document, plan, {mode: 'load', resultMode: 'full'});
		try {
			const first = ffi.selectorQueryNodeInfo(session.token, 'ids', 0);
			const last = ffi.selectorQueryNodeInfo(session.token, 'ids', 2);
			const rects = await ffi.selectorQueryRects(session.token, 'ids', [0, 2], 'top');
			return {
				count: session.ids.count,
				kinds: [first?.kind || '', last?.kind || ''],
				rectCount: rects.length,
				widths: rects.map((rect) => Math.round(rect.width)),
			};
		} finally {
			ffi.disposeSelectorQuery(session.token);
		}
	`)
	var probe struct {
		Count     int      `json:"count"`
		Kinds     []string `json:"kinds"`
		RectCount int      `json:"rectCount"`
		Widths    []int    `json:"widths"`
	}
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		t.Fatalf("unmarshal iframe rect probe: %v raw=%s", err, raw)
	}
	if probe.Count != 3 {
		t.Fatalf("selector count = %d, want 3", probe.Count)
	}
	if len(probe.Kinds) != 2 || probe.Kinds[0] != "remote" || probe.Kinds[1] != "remote" {
		t.Fatalf("expected remote iframe entries, got %+v", probe.Kinds)
	}
	if probe.RectCount != 2 {
		t.Fatalf("indexed rect count = %d, want 2; probe=%+v", probe.RectCount, probe)
	}
	if len(probe.Widths) != 2 || probe.Widths[0] != 40 || probe.Widths[1] != 70 {
		t.Fatalf("indexed rect widths = %+v, want [40 70]", probe.Widths)
	}
}

func TestNestedIframeLocatorContentFrameEquivalence(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/iframe-nested")
	child := "[data-testid='deep-btn']"
	outerFrame := page.Locator("#outer-frame").ContentFrame()
	implicit := outerFrame.Locator("#inner-frame").Locator(child)
	explicitFrame := outerFrame.Locator("#inner-frame").ContentFrame()
	explicit := explicitFrame.Locator(child)

	if text := must(implicit.TextContent(context.Background())); text != "deep-button" {
		t.Fatalf("unexpected implicit nested iframe text: %q", text)
	}
	if text := must(explicit.TextContent(context.Background())); text != "deep-button" {
		t.Fatalf("unexpected explicit content frame text: %q", text)
	}
	mustOK(implicit.Click(context.Background()))

	if text := must(explicit.TextContent(context.Background())); text != "deep-clicked" {
		t.Fatalf("explicit content frame did not observe implicit click text: %q", text)
	}
	if state := must(explicitFrame.Locator("[data-testid='click-state']").TextContent(context.Background())); state != "primary-clicked" {
		t.Fatalf("explicit content frame did not observe implicit click state: %q", state)
	}
}

func TestWaitForSelectorInFrame(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/iframe-list")
	frame := page.FrameLocator("#list-frame")
	sel := frame.Locator("[data-testid='frame-item']")
	mustOK(sel.Wait(context.Background(), cdp.StateAttached))

	if must(sel.Count(context.Background())) !=
		2 {
		t.Fatalf("unexpected frame item count: %d", must(sel.Count(context.Background())))
	}
	if text := must(sel.First().TextContent(context.Background())); text != "alpha" {
		t.Fatalf("unexpected first frame item text: %q", text)
	}
}

func TestWaitForFrameSelectorFullPath(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/iframe-nested")
	frame := page.FrameLocator("#frame-stage >> #outer-frame")
	if text := must(frame.Locator("#inner-frame").ContentFrame().Locator("[data-testid='deep-btn']").TextContent(context.Background())); text != "deep-button" {
		t.Fatalf("unexpected full path frame selector text: %q", text)
	}
	inner := page.FrameLocator("#frame-stage >> #outer-frame").FrameLocator("#inner-frame")
	if text := must(inner.Locator("[data-testid='deep-btn']").TextContent(context.Background())); text != "deep-button" {
		t.Fatalf("unexpected explicit nested frame selector text: %q", text)
	}
}

func TestContentFrameRequiresFrameElement(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/iframe-nested")
	msg := expectPanicMessage(func() {
		must(page.Locator("#frame-stage").ContentFrame().Locator("#outer-frame").TextContent(context.Background()))

	})
	if msg == "" {
		t.Fatal("expected ContentFrame on non-frame element to fail")
	}
	if !strings.Contains(msg, "#frame-stage") && !strings.Contains(msg, "#outer-frame") {
		t.Fatalf("unexpected ContentFrame failure message: %s", msg)
	}
}

func TestExplicitFrameSelectorSemantics(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/iframe-nested")
	deepCards := page.Locator("#frame-stage").FrameLocator("#outer-frame").FrameLocator("#inner-frame").Locator("#deep-cards")
	if count := must(deepCards.Count(context.Background())); count != 1 {
		t.Fatalf("unexpected explicit frame locator count: %d", count)
	}
	if text := must(deepCards.Locator(".deep-card").First().Locator(".title").TextContent(context.Background())); text != "alpha" {
		t.Fatalf("unexpected explicit frame locator text: %q", text)
	}

}

func TestNestedIframeClick(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/iframe-nested")
	inner := nestedInnerFrame(page)
	sel := inner.Locator("[data-testid='deep-btn']")
	mustOK(sel.Wait(context.Background(), cdp.StateVisible))
	mustOK(sel.Click(context.Background()))

	got := must(sel.TextContent(context.Background()))

	if got != "deep-clicked" {
		t.Fatalf("unexpected nested iframe button text: %q", got)
	}
	if state := must(inner.Locator("[data-testid='click-state']").TextContent(context.Background())); state != "primary-clicked" {
		t.Fatalf("unexpected nested iframe click state: %q", state)
	}
}

func TestIframeSelectorHighlightProjectsToTopCanvas(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/highlight-iframe")
	frame := page.FrameLocator("#highlight-frame")
	if text := must(frame.Locator("[data-testid='highlight-inside']").TextContent(context.Background())); text != "Inside Frame" {
		t.Fatalf("unexpected iframe text content: %q", text)
	}
	time.Sleep(time.Duration(100) *
		time.Millisecond)

	frameRect, err := readRectProbe(page, `
		const frame = document.querySelector('#highlight-frame');
		if (!(frame instanceof HTMLIFrameElement)) {
			return { expectedX: -1, expectedY: -1, localX: -1, localY: -1 };
		}
		const rect = frame.getBoundingClientRect();
		return {
			expectedX: rect.left + frame.clientLeft,
			expectedY: rect.top + frame.clientTop,
			localX: 0,
			localY: 0,
		};
	`)
	if err != nil {
		t.Fatal(err)
	}
	targetRect, err := readRectProbe(frame.Locator("[data-testid='highlight-inside']"), `
		const rect = this instanceof HTMLElement ? this.getBoundingClientRect() : null;
		if (!rect) {
			return { expectedX: -1, expectedY: -1, localX: -1, localY: -1 };
		}
		return {
			expectedX: 0,
			expectedY: 0,
			localX: rect.left + 2,
			localY: rect.top + 2,
		};
	`)
	if err != nil {
		t.Fatal(err)
	}
	probe := highlightProjectionProbe{
		ExpectedX: frameRect.ExpectedX + targetRect.LocalX,
		ExpectedY: frameRect.ExpectedY + targetRect.LocalY,
		LocalX:    targetRect.LocalX,
		LocalY:    targetRect.LocalY,
	}
	mustOK(frame.Locator("[data-testid=highlight-inside]").Highlight(context.Background(), "iframe target"))
	expectedAlpha, err := readCanvasAlphaAt(page, probe.ExpectedX, probe.ExpectedY)
	if err != nil {
		t.Fatal(err)
	}
	localAlpha, err := readCanvasAlphaAt(page, probe.LocalX, probe.LocalY)
	if err != nil {
		t.Fatal(err)
	}
	if expectedAlpha < 16 {
		t.Fatalf("expected projected iframe highlight to paint near top-space target, alpha=%.0f probe=%+v", expectedAlpha, probe)
	}
	if localAlpha >= expectedAlpha {
		t.Fatalf("expected local iframe coordinates not to dominate top canvas highlight, expectedAlpha=%.0f localAlpha=%.0f probe=%+v", expectedAlpha, localAlpha, probe)
	}
}

func TestNestedIframeInput(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/iframe-nested")
	inner := nestedInnerFrame(page)
	input := inner.Locator("[data-testid='deep-input']")
	mustOK(input.Fill(context.Background(), "sample-nested-value"))
	if value := must(input.Value(context.Background())); value != "sample-nested-value" {
		t.Fatalf("unexpected nested iframe input value: %q", value)
	}
	if mirror := must(inner.Locator("[data-testid='input-mirror']").TextContent(context.Background())); mirror != "sample-nested-value" {
		t.Fatalf("unexpected nested iframe input mirror: %q", mirror)
	}
}

func TestNestedIframeInputRepeatSubmit(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/iframe-nested")
	inner := nestedInnerFrame(page)
	input := inner.Locator("[data-testid='repeat-input']")
	submit := inner.Locator("[data-testid='repeat-submit']")
	count := inner.Locator("[data-testid='repeat-count']")
	last := inner.Locator("[data-testid='repeat-last']")
	log := inner.Locator("[data-testid='repeat-log']")
	mustOK(input.Fill(context.Background(), "alpha"))
	mustOK(submit.Click(context.Background()))

	if err := assertPointerDeltaWithin(inner); err != nil {
		t.Fatalf("first repeat submit click pointer mismatch: %v", err)
	}
	if got := must(count.TextContent(context.Background())); got != "1" {
		t.Fatalf("unexpected repeat submit count after first click: %q", got)
	}
	if got := must(last.TextContent(context.Background())); got != "alpha" {
		t.Fatalf("unexpected repeat submit last value after first click: %q", got)
	}
	if got := must(log.TextContent(context.Background())); got != "submit:1:alpha" {
		t.Fatalf("unexpected repeat submit log after first click: %q", got)
	}
	mustOK(input.Fill(context.Background(), "beta"))
	mustOK(submit.Click(context.Background()))

	if err := assertPointerDeltaWithin(inner); err != nil {
		t.Fatalf("second repeat submit click pointer mismatch: %v", err)
	}
	if got := must(count.TextContent(context.Background())); got != "2" {
		t.Fatalf("unexpected repeat submit count after second click: %q", got)
	}
	if got := must(last.TextContent(context.Background())); got != "beta" {
		t.Fatalf("unexpected repeat submit last value after second click: %q", got)
	}
	if got := must(log.TextContent(context.Background())); got != "submit:2:beta" {
		t.Fatalf("unexpected repeat submit log after second click: %q", got)
	}
	mustOK(input.Fill(context.Background(), "gamma"))
	mustOK(submit.Click(context.Background()))

	if err := assertPointerDeltaWithin(inner); err != nil {
		t.Fatalf("third repeat submit click pointer mismatch: %v", err)
	}
	if got := must(count.TextContent(context.Background())); got != "3" {
		t.Fatalf("unexpected repeat submit count after third click: %q", got)
	}
	if got := must(last.TextContent(context.Background())); got != "gamma" {
		t.Fatalf("unexpected repeat submit last value after third click: %q", got)
	}
	if got := must(log.TextContent(context.Background())); got != "submit:3:gamma" {
		t.Fatalf("unexpected repeat submit log after third click: %q", got)
	}
}

func TestNestedIframeClickPointerAccuracy(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/iframe-nested")
	inner := nestedInnerFrame(page)
	input := inner.Locator("[data-testid='repeat-input']")
	submit := inner.Locator("[data-testid='repeat-submit']")
	values := []string{"delta-a", "delta-b", "delta-c", "delta-d", "delta-e"}
	for index, value := range values {
		mustOK(input.Fill(context.Background(), value))
		mustOK(submit.Click(context.Background()))

		if err := assertPointerDeltaWithin(inner); err != nil {
			t.Fatalf("repeat click %d pointer mismatch: %v", index+1, err)
		}
	}
	if got := must(inner.Locator("[data-testid='repeat-count']").TextContent(context.Background())); got != "5" {
		t.Fatalf("unexpected pointer accuracy repeat count: %q", got)
	}
}

func TestNestedIframeNodeActions(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/iframe-nested")
	values := []string{"node-a", "node-b", "node-c"}
	inner := nestedInnerFrame(page)
	for index, value := range values {
		input := must(inner.ByTestID("repeat-input").All(context.Background()))[0]
		submit := must(inner.ByTestID("repeat-submit").All(context.Background()))[0]
		if err := input.Fill(context.Background(), value); err != nil {
			t.Fatalf("node input %d failed: %v", index+1, err)
		}
		if err := submit.Click(context.Background()); err != nil {
			t.Fatalf("node click %d failed: %v", index+1, err)
		}
		if err := assertPointerDeltaWithin(inner); err != nil {
			t.Fatalf("node click %d pointer mismatch: %v", index+1, err)
		}
	}
	if got := must(inner.Locator("[data-testid='repeat-count']").TextContent(context.Background())); got != "3" {
		t.Fatalf("unexpected repeat count: %q", got)
	}
	if got := must(inner.Locator("[data-testid='repeat-last']").TextContent(context.Background())); got != "node-c" {
		t.Fatalf("unexpected repeat last value: %q", got)
	}
}

func TestNestedIframeActionClick(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/iframe-nested")
	inner := nestedInnerFrame(page)
	mustOK(inner.Locator("[data-testid='action-btn']").Click(context.Background()))

	if log := must(inner.Locator("[data-testid='action-log']").TextContent(context.Background())); log != "action-clicked" {
		t.Fatalf("unexpected nested iframe action log: %q", log)
	}
}

func TestNestedIframeHover(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/iframe-nested")
	inner := nestedInnerFrame(page)
	mustOK(inner.Locator("[data-testid='hover-target']").Hover(context.Background()))

	if state := must(inner.Locator("[data-testid='hover-state']").TextContent(context.Background())); state != "hovered" {
		t.Fatalf("unexpected nested iframe hover state: %q", state)
	}
}

func TestNestedIframePress(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/iframe-nested")
	inner := nestedInnerFrame(page)
	input := inner.Locator("[data-testid='press-input']")
	mustOK(input.Fill(context.Background(), "sample-value"))
	mustOK(input.Press(context.Background(), "Enter", cdp.KeyOptions{}))

	if state := must(inner.Locator("[data-testid='press-state']").TextContent(context.Background())); state != "sample-value:enter" {
		t.Fatalf("unexpected nested iframe press state: %q", state)
	}
}

func TestNestedIframeWaitStates(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/iframe-nested")
	inner := nestedInnerFrame(page)
	target := inner.Locator("[data-testid='toggle-target']")
	mustOK(inner.Locator("[data-testid='disable-trigger']").Click(context.Background()))
	mustOK(target.Wait(context.Background(), cdp.StateDisabled))
	mustOK(target.Wait(context.Background(), cdp.StateEnabled))
	mustOK(target.Wait(context.Background(), cdp.StateHidden))
	mustOK(inner.Locator("[data-testid='remove-trigger']").Click(context.Background()))
	mustOK(inner.Locator("[data-testid='remove-target']").Wait(context.Background(), cdp.StateDetached))

}

func TestNestedIframeClickNativeDisabledWaits(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/iframe-nested")
	inner := nestedInnerFrame(page)
	mustOK(inner.Locator("[data-testid='native-disabled-release']").Click(context.Background()))
	mustOK(inner.Locator("[data-testid='native-disabled-target']").Click(context.Background()))

	if state := must(inner.Locator("[data-testid='native-disabled-state']").TextContent(context.Background())); state != "clicked" {
		t.Fatalf("expected native disabled target click after recovery, got %q", state)
	}
}

func TestNestedIframeClickAriaDisabledStillRunsWhenDOMAllows(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/iframe-nested")
	inner := nestedInnerFrame(page)
	mustOK(inner.Locator("[data-testid='aria-disabled-target']").Click(context.Background()))

	if state := must(inner.Locator("[data-testid='aria-disabled-state']").TextContent(context.Background())); state != "clicked" {
		t.Fatalf("aria-disabled target should click when DOM allows it, got %q", state)
	}
}

func TestNestedIframeClickAncestorAriaDisabledStillRunsWhenDOMAllows(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/iframe-nested")
	inner := nestedInnerFrame(page)
	mustOK(inner.Locator("[data-testid='ancestor-aria-disabled-target']").Click(context.Background()))

	if state := must(inner.Locator("[data-testid='ancestor-aria-disabled-state']").TextContent(context.Background())); state != "clicked" {
		t.Fatalf("ancestor aria-disabled target should click when DOM allows it, got %q", state)
	}
}

func TestNestedIframeUpload(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/iframe-nested")
	inner := nestedInnerFrame(page)

	uploadAndAssert := func(targetSelector string, stateTestID string, fileName string) {
		mustOK(inner.Locator(targetSelector).Upload(context.Background(), fileName, []byte("hello-upload")))
		state := must(inner.ByTestID(stateTestID).TextContent(context.Background()))

		if !strings.Contains(state, fileName) {
			t.Fatalf("unexpected nested iframe upload state for %s: %q", targetSelector, state)
		}
	}

	cases := []struct {
		targetSelector string
		stateTestID    string
		fileName       string
	}{
		{targetSelector: "[data-testid='upload-input']", stateTestID: "upload-state", fileName: "payload-direct.txt"},
		{targetSelector: "[data-testid='upload-wrapper']", stateTestID: "upload-wrapper-state", fileName: "payload-wrapper.txt"},
		{targetSelector: "[data-testid='upload-label']", stateTestID: "upload-label-state", fileName: "payload-label.txt"},
		{targetSelector: "[data-testid='upload-label-child']", stateTestID: "upload-nested-label-state", fileName: "payload-label-child.txt"},
		{targetSelector: "[data-testid='upload-prev-sibling']", stateTestID: "upload-prev-sibling-state", fileName: "payload-prev-sibling.txt"},
		{targetSelector: "[data-testid='upload-next-sibling']", stateTestID: "upload-next-sibling-state", fileName: "payload-next-sibling.txt"},
	}
	for _, tc := range cases {
		uploadAndAssert(tc.targetSelector, tc.stateTestID, tc.fileName)
	}

	msg := expectPanicMessage(func() {
		mustOK(inner.ByTestID("upload-ambiguous").Upload(context.Background(), "ambiguous.txt", []byte("ambiguous")))
	})
	if msg == "" || !strings.Contains(msg, "不是唯一的文件输入框") {
		t.Fatalf("unexpected ambiguous upload panic: %q", msg)
	}
	msg = expectPanicMessage(func() {
		mustOK(inner.ByTestID("upload-missing").Upload(context.Background(), "missing.txt", []byte("missing")))
	})
	if msg == "" || !strings.Contains(msg, "未找到关联的文件输入框") {
		t.Fatalf("unexpected missing upload panic: %q", msg)
	}
}

func TestNestedIframeForm(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/iframe-nested")
	inner := nestedInnerFrame(page)
	check := inner.Locator("[data-testid='deep-check']")
	mustOK(check.SetChecked(context.Background(), true))

	if !must(check.IsChecked(context.Background())) {
		t.Fatal("expected nested iframe checkbox to be checked")
	}
	selectBox := inner.Locator("[data-testid='deep-city']")
	mustOK(selectBox.Select(context.Background(), []string{"shanghai"}))
	if value := must(selectBox.Value(context.Background())); value != "shanghai" {
		t.Fatalf("unexpected nested iframe select value: %q", value)
	}
	if state := must(inner.Locator("[data-testid='form-state']").TextContent(context.Background())); state != `{"agree":true,"city":"shanghai"}` {
		t.Fatalf("unexpected nested iframe form state: %q", state)
	}
	mustOK(check.SetChecked(context.Background(), false))

	if must(check.IsChecked(context.Background())) {
		t.Fatal("expected nested iframe checkbox to be unchecked")
	}
}

func TestNestedIframeScreenshot(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/iframe-nested")
	inner := nestedInnerFrame(page)
	img := must(inner.Locator("[data-testid='shot-target']").Screenshot(context.Background()))

	if img == nil {
		t.Fatal("nested iframe screenshot returned nil image")
	}
	if img.Bounds().Dx() < 100 || img.Bounds().Dy() < 40 {
		t.Fatalf("unexpected nested iframe screenshot size: %dx%d", img.Bounds().Dx(), img.Bounds().Dy())
	}
	if err := assertScreenshotMarkerBorder("nested iframe element", img); err != nil {
		t.Fatal(err)
	}
}

func TestNestedIframeActionabilityObscured(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/iframe-nested")
	msg := expectPanicMessage(func() {
		mustOK(nestedInnerFrame(page).Locator("[data-testid='covered-btn']").Click(context.Background()))

	})
	if !strings.Contains(msg, "obscured") && !strings.Contains(msg, "Obscured") {
		t.Fatalf("expected obscured diagnostic, got: %s", msg)
	}
	if !strings.Contains(msg, "covered-btn") {
		t.Fatalf("expected covered button in actionability message, got: %s", msg)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func nestedInnerFrame(page *cdp.Page) *cdp.Locator {
	outer := page.FrameLocator("#outer-frame")
	return outer.FrameLocator("#inner-frame")
}

func assertPointerDeltaWithin(inner *cdp.Locator) error {
	if inner == nil {
		return fmt.Errorf("inner frame selector is nil")
	}
	raw := strings.TrimSpace(must(inner.Locator("[data-testid='repeat-click-delta']").TextContent(context.Background())))
	if raw == "" {
		return fmt.Errorf("repeat click delta is empty")
	}
	parts := strings.Split(raw, ",")
	if len(parts) != 2 {
		return fmt.Errorf("invalid repeat click delta payload: %q", raw)
	}
	dx, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	if err != nil {
		return fmt.Errorf("parse delta x: %w", err)
	}
	dy, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err != nil {
		return fmt.Errorf("parse delta y: %w", err)
	}
	if math.Abs(dx) > 1 || math.Abs(dy) > 1 {
		point := must(inner.Locator("[data-testid='repeat-click-point']").TextContent(context.Background()))

		return fmt.Errorf("delta=(%.2f,%.2f) tolerance=1 point=%s", dx, dy, point)
	}
	return nil
}
