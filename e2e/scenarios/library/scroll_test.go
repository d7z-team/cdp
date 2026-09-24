package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"gopkg.d7z.net/cdp"
)

type actionabilityProbe struct {
	Actionable     bool   `json:"actionable"`
	Kind           string `json:"kind"`
	Culprit        string `json:"culprit"`
	HitTarget      string `json:"hitTarget"`
	DelegateReason string `json:"delegateReason"`
	Retriable      bool   `json:"retriable"`
	RetryAction    string `json:"retryAction"`
}

func readActionabilityProbe(page *cdp.Page, testID string, purpose ...string) (actionabilityProbe, error) {
	options := "undefined"
	if len(purpose) > 0 && strings.TrimSpace(purpose[0]) != "" {
		options = fmt.Sprintf(`{purpose:%q}`, purpose[0])
	}
	raw := evalValue(must(page.ExecutionContext(context.Background(), cdp.ExecutionContextOptions{World: cdp.WorldCore})), fmt.Sprintf(`
		return await window.__cdp_ffi?.actionabilityDiagnostic(
			document.querySelector('[data-testid=%q]'),
			%s
		)
	`, testID, options))
	probe := actionabilityProbe{}
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		return actionabilityProbe{}, fmt.Errorf("unmarshal actionability probe for %s: %w (raw=%q)", testID, err, raw)
	}
	return probe, nil
}

func assertEvalTrue(page *cdp.Page, expr string, message string) error {
	if got := evalValue(page, expr); got != "true" {
		return fmt.Errorf("%s: %s returned %q", message, expr, got)
	}
	return nil
}

func resetScrollActionsPage(page *cdp.Page) {
	evalText(page, `(() => {
		const frame = document.querySelector('#scroll-frame');
		if (frame && frame.contentWindow) {
			frame.contentWindow.scrollTo(0, 0);
		}
		window.scrollTo(0, 0);
	})()`)
}

func assertIframeScrolledToTarget(page *cdp.Page, testID string, actionName string) error {
	if err := assertEvalTrue(page, `return window.scrollY > 0`, "expected "+actionName+" to scroll outer document"); err != nil {
		return err
	}
	if err := assertEvalTrue(page, `return document.querySelector('#scroll-frame').contentWindow.scrollY > 0`, "expected "+actionName+" to scroll iframe document"); err != nil {
		return err
	}
	if err := assertEvalTrue(page, `
		const frame = document.querySelector('#scroll-frame');
		const rect = frame.getBoundingClientRect();
		return rect.top >= 0 && rect.bottom <= window.innerHeight;
	`, "expected "+actionName+" to keep iframe inside outer viewport"); err != nil {
		return err
	}
	if err := assertEvalTrue(page, fmt.Sprintf(`
		const frame = document.querySelector('#scroll-frame');
		const target = frame.contentDocument.querySelector('[data-testid="%s"]');
		const rect = target.getBoundingClientRect();
		return rect.top >= 0 && rect.bottom <= frame.contentWindow.innerHeight;
	`, testID), "expected "+actionName+" target to be inside iframe viewport"); err != nil {
		return err
	}
	return nil
}

func TestDocumentAutoScrollControls(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/scroll-actions")
	mustOK(page.ScrollTo(context.Background(), 0, 0))
	if err := assertEvalTrue(page, `return window.scrollY === 0`, "expected document page to start at top"); err != nil {
		t.Fatal(err)
	}

	if got := must(page.ByTestID("below-fold-button").TextContent(context.Background())); got != "below-fold-button" {
		t.Fatalf("unexpected offscreen button text before interaction: %q", got)
	}
	if err := assertEvalTrue(page, `return window.scrollY === 0`, "expected TextContent not to scroll document"); err != nil {
		t.Fatal(err)
	}
	if got := must(page.ByTestID("below-fold-input").Value(context.Background())); got != "" {
		t.Fatalf("unexpected offscreen input value before interaction: %q", got)
	}
	if err := assertEvalTrue(page, `return window.scrollY === 0`, "expected GetValue not to scroll document"); err != nil {
		t.Fatal(err)
	}
	mustOK(page.ByTestID("below-fold-button").Click(context.Background()))

	if err := assertEvalTrue(page, `return window.scrollY > 0`, "expected button click to scroll document"); err != nil {
		t.Fatal(err)
	}
	if err := assertEvalTrue(page, `
		const target = document.querySelector('[data-testid="below-fold-button"]');
		const rect = target.getBoundingClientRect();
		return rect.top >= 0 && rect.bottom <= window.innerHeight;
	`, "expected clicked document button to be inside viewport"); err != nil {
		t.Fatal(err)
	}
	if got := must(page.ByTestID("button-state").TextContent(context.Background())); got != "page-button-clicked" {
		t.Fatalf("unexpected document button state: %q", got)
	}
	mustOK(page.ScrollTo(context.Background(), 0, 0))
	mustOK(page.ByTestID("below-fold-input").Fill(context.Background(), "page-input-value"))
	if err := assertEvalTrue(page, `return window.scrollY > 0`, "expected input fill to scroll document"); err != nil {
		t.Fatal(err)
	}
	if err := assertEvalTrue(page, `
		const target = document.querySelector('[data-testid="below-fold-input"]');
		const rect = target.getBoundingClientRect();
		return rect.top >= 0 && rect.bottom <= window.innerHeight;
	`, "expected filled document input to be inside viewport"); err != nil {
		t.Fatal(err)
	}
	if got := must(page.ByTestID("below-fold-input").Value(context.Background())); got != "page-input-value" {
		t.Fatalf("unexpected document input value: %q", got)
	}
	if got := must(page.ByTestID("input-state").TextContent(context.Background())); got != "page-input-value" {
		t.Fatalf("unexpected document input state: %q", got)
	}
	mustOK(page.ScrollTo(context.Background(), 0, 0))
	mustOK(page.ByTestID("below-fold-notes").Fill(context.Background(), "page-textarea-value"))
	if err := assertEvalTrue(page, `return window.scrollY > 0`, "expected textarea fill to scroll document"); err != nil {
		t.Fatal(err)
	}
	if got := must(page.ByTestID("textarea-state").TextContent(context.Background())); got != "page-textarea-value" {
		t.Fatalf("unexpected document textarea state: %q", got)
	}
	mustOK(page.ScrollTo(context.Background(), 0, 0))
	mustOK(page.ByTestID("below-fold-city").Select(context.Background(), []string{"shanghai"}))
	if err := assertEvalTrue(page, `return window.scrollY > 0`, "expected select to scroll document"); err != nil {
		t.Fatal(err)
	}
	if err := assertEvalTrue(page, `
		const target = document.querySelector('[data-testid="below-fold-city"]');
		const rect = target.getBoundingClientRect();
		return rect.top >= 0 && rect.bottom <= window.innerHeight;
	`, "expected selected document control to be inside viewport"); err != nil {
		t.Fatal(err)
	}
	if got := must(page.ByTestID("below-fold-city").Value(context.Background())); got != "shanghai" {
		t.Fatalf("unexpected document select value: %q", got)
	}
	if got := must(page.ByTestID("select-state").TextContent(context.Background())); got != "shanghai" {
		t.Fatalf("unexpected document select state: %q", got)
	}
	mustOK(page.ScrollTo(context.Background(), 0, 0))
	mustOK(page.ByTestID("below-fold-check").SetChecked(context.Background(), true))

	if err := assertEvalTrue(page, `return window.scrollY > 0`, "expected checkbox check to scroll document"); err != nil {
		t.Fatal(err)
	}
	if !must(page.ByTestID("below-fold-check").IsChecked(context.Background())) {
		t.Fatal("expected below-fold checkbox to be checked")
	}
	if got := must(page.ByTestID("check-state").TextContent(context.Background())); got != "true" {
		t.Fatalf("unexpected document checkbox state after check: %q", got)
	}
	mustOK(page.ScrollTo(context.Background(), 0, 0))
	mustOK(page.ByTestID("below-fold-check").SetChecked(context.Background(), false))

	if err := assertEvalTrue(page, `return window.scrollY > 0`, "expected checkbox uncheck to scroll document"); err != nil {
		t.Fatal(err)
	}
	if must(page.ByTestID("below-fold-check").IsChecked(context.Background())) {
		t.Fatal("expected below-fold checkbox to be unchecked")
	}
	if got := must(page.ByTestID("check-state").TextContent(context.Background())); got != "false" {
		t.Fatalf("unexpected document checkbox state after uncheck: %q", got)
	}
	mustOK(page.ScrollTo(context.Background(), 0, 0))
	mustOK(page.ByTestID("below-fold-secondary").Click(context.Background()))

	if err := assertEvalTrue(page, `return window.scrollY > 0`, "expected secondary button click to scroll document"); err != nil {
		t.Fatal(err)
	}
	if got := must(page.ByTestID("secondary-state").TextContent(context.Background())); got != "secondary-clicked" {
		t.Fatalf("unexpected document secondary button state: %q", got)
	}
}

func TestIframeAutoScrollControls(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/scroll-actions-iframe")
	frame := page.FrameLocator("#scroll-frame")
	resetScrollActionsPage(page)
	if err := assertEvalTrue(page, `return window.scrollY === 0`, "expected iframe host page to start at top"); err != nil {
		t.Fatal(err)
	}
	if err := assertEvalTrue(page, `return document.querySelector('#scroll-frame').contentWindow.scrollY === 0`, "expected iframe document to start at top"); err != nil {
		t.Fatal(err)
	}
	mustOK(frame.ByTestID("frame-below-fold-button").Click(context.Background()))

	if err := assertIframeScrolledToTarget(page, "frame-below-fold-button", "iframe button click"); err != nil {
		t.Fatal(err)
	}
	if got := must(frame.ByTestID("frame-button-state").TextContent(context.Background())); got != "frame-button-clicked" {
		t.Fatalf("unexpected iframe button state: %q", got)
	}

	resetScrollActionsPage(page)
	mustOK(frame.ByTestID("frame-below-fold-input").Fill(context.Background(), "frame-input-value"))
	if err := assertIframeScrolledToTarget(page, "frame-below-fold-input", "iframe input fill"); err != nil {
		t.Fatal(err)
	}
	if got := must(frame.ByTestID("frame-below-fold-input").Value(context.Background())); got != "frame-input-value" {
		t.Fatalf("unexpected iframe input value: %q", got)
	}
	if got := must(frame.ByTestID("frame-input-state").TextContent(context.Background())); got != "frame-input-value" {
		t.Fatalf("unexpected iframe input state: %q", got)
	}

	resetScrollActionsPage(page)
	mustOK(frame.ByTestID("frame-below-fold-notes").Fill(context.Background(), "frame-textarea-value"))
	if err := assertIframeScrolledToTarget(page, "frame-below-fold-notes", "iframe textarea fill"); err != nil {
		t.Fatal(err)
	}
	if got := must(frame.ByTestID("frame-textarea-state").TextContent(context.Background())); got != "frame-textarea-value" {
		t.Fatalf("unexpected iframe textarea state: %q", got)
	}
}

func TestDocumentHoverObscured(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/scroll-actions-obscured")
	mustOK(page.ScrollTo(context.Background(), 0, 0))

	msg := expectPanicMessage(func() {
		mustOK(page.ByTestID("hover-obscured-button").Click(context.Background()))

	})
	if msg == "" {
		t.Fatal("expected click on hover-obscured-button to fail")
	}
	if !strings.Contains(msg, "obscured") {
		t.Fatalf("expected obscured panic, got %q", msg)
	}
	err := expectPanicError(func() {
		mustOK(page.ByTestID("hover-obscured-button").Click(context.Background()))

	})
	var selectorErr *cdp.LocatorError
	if !errors.As(err, &selectorErr) {
		t.Fatalf("expected LocatorError, got %#v", err)
	}
	var browserErr *cdp.BrowserError
	if !errors.As(err, &browserErr) || browserErr.Kind != "obscured" {
		t.Fatalf("expected obscured BrowserError, got %#v", err)
	}
	if err := assertEvalTrue(page, `return window.scrollY > 0`, "expected obscured click attempt to scroll document"); err != nil {
		t.Fatal(err)
	}
	if got := must(page.ByTestID("hover-obscured-state").TextContent(context.Background())); got != "hovered" {
		t.Fatalf("unexpected hover obscured state: %q", got)
	}
}

func TestDocumentClickObscuredByFixedFooter(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/actionability-obscured-scroll")

	probe, err := readActionabilityProbe(page, "footer-covered-target", "click")
	if err != nil {
		t.Fatal(err)
	}
	if probe.Actionable || probe.Kind != "obscured" || !strings.Contains(probe.Culprit, "dialog-footer") {
		t.Fatalf("expected target to start obscured by dialog footer, got %+v", probe)
	}
	mustOK(page.ByTestID("footer-covered-target").Click(context.Background()))

	if got := must(page.ByTestID("obscured-state").TextContent(context.Background())); got != "footer-clicked" {
		t.Fatalf("unexpected footer recovery state: %q", got)
	}
	if err := assertEvalTrue(page, `return document.querySelector('[data-testid="footer-scroll"]').scrollTop > 0`, "expected footer recovery to reposition the dialog body"); err != nil {
		t.Fatal(err)
	}
}

func TestDocumentClickPermanentOverlayRemainsObscured(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/actionability-obscured-scroll")
	evalText(page, `window.activateObscuredScenario('permanent')`)
	page = configuredPage(t, page, cdp.ConnectOptions{ActionMode: cdp.ActionFast, Timeouts: cdp.Timeouts{Action: 600 * time.Millisecond, Read: 600 * time.Millisecond}})

	err := expectPanicError(func() {
		mustOK(page.ByTestID("permanent-target").Click(context.Background()))

	})
	var browserErr *cdp.BrowserError
	if !errors.As(err, &browserErr) || browserErr.Kind != "obscured" {
		t.Fatalf("expected permanent overlay to remain obscured, got %#v", err)
	}
	if got := must(page.ByTestID("obscured-state").TextContent(context.Background())); got != "idle" {
		t.Fatalf("permanent overlay allowed a click: %q", got)
	}
}

func TestDocumentClickWaitsForTransientOverlay(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/actionability-obscured-scroll")
	page = configuredPage(t, page, cdp.ConnectOptions{ActionMode: cdp.ActionFast, Timeouts: cdp.Timeouts{Action: 2500 * time.Millisecond, Read: 2500 * time.Millisecond}})
	evalText(page, `window.activateObscuredScenario('transient')`)
	mustOK(page.ByTestID("transient-target").Click(context.Background()))

	if got := must(page.ByTestID("obscured-state").TextContent(context.Background())); got != "transient-clicked" {
		t.Fatalf("unexpected transient overlay state: %q", got)
	}
}

func TestIframeClickObscuredByInnerFooter(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/actionability-obscured-iframe")
	frame := page.FrameLocator("#obscured-frame")
	mustOK(frame.ByTestID("frame-footer-target").Click(context.Background()))

	if got := must(frame.ByTestID("frame-obscured-state").TextContent(context.Background())); got != "frame-footer-clicked" {
		t.Fatalf("unexpected inner footer recovery state: %q", got)
	}
	if err := assertEvalTrue(page, `return document.querySelector('#obscured-frame').contentDocument.querySelector('.frame-scroll').scrollTop > 0`, "expected iframe body to reposition its covered target"); err != nil {
		t.Fatal(err)
	}
}

func TestIframeClickObscuredByParentFooter(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/actionability-obscured-parent-frame")
	frame := page.FrameLocator("#parent-obscured-frame")
	mustOK(frame.ByTestID("parent-footer-target").Click(context.Background()))

	if got := must(frame.ByTestID("frame-obscured-state").TextContent(context.Background())); got != "parent-footer-clicked" {
		t.Fatalf("unexpected parent footer recovery state: %q", got)
	}
	if err := assertEvalTrue(page, `return window.scrollY > 0`, "expected parent frame recovery to reposition the iframe"); err != nil {
		t.Fatal(err)
	}
}

func TestDocumentClickStabilityLifecycle(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/actionability-stability")
	page = configuredPage(t, page, cdp.ConnectOptions{ActionMode: "strict"})

	transition := page.ByTestID("transition-target")
	mustOK(transition.Click(context.Background()))
	mustOK(transition.Click(context.Background()))

	if got := must(page.ByTestID("transition-count").TextContent(context.Background())); got != "2" {
		t.Fatalf("transition click count = %q, want 2", got)
	}
	if got := must(page.ByTestID("scroll-count").TextContent(context.Background())); got != "0" {
		t.Fatalf("visible target caused unnecessary scroll events: %q", got)
	}
	mustOK(page.ByTestID("smooth-scroll-target").Click(context.Background()))

	if got := must(page.ByTestID("smooth-scroll-count").TextContent(context.Background())); got != "1" {
		t.Fatalf("smooth-scroll target click count = %q, want 1", got)
	}
	if err := assertEvalTrue(page, `return window.scrollY > 0`, "expected offscreen target to scroll into view"); err != nil {
		t.Fatal(err)
	}

	movingErr := expectPanicError(func() {
		mustOK(page.ByTestID("moving-target").Click(context.Background()))

	})
	var movingBrowserErr *cdp.BrowserError
	if !errors.As(movingErr, &movingBrowserErr) || movingBrowserErr.Kind != "unstable" {
		t.Fatalf("moving target error = %#v, want unstable BrowserError", movingErr)
	}
	stability, ok := movingBrowserErr.Data["stability"].(map[string]any)
	if !ok || stability["kind"] != "timeout" {
		t.Fatalf("moving target stability data = %+v, want timeout", movingBrowserErr.Data["stability"])
	}
	if got := must(page.ByTestID("moving-count").TextContent(context.Background())); got != "0" {
		t.Fatalf("moving target dispatched a click: %q", got)
	}

	detachRaw := evalValue(must(page.ExecutionContext(context.Background(), cdp.ExecutionContextOptions{World: cdp.WorldCore})), `
		const target = document.querySelector('[data-testid="detach-target"]');
		setTimeout(() => target.replaceWith(target.cloneNode(true)), 40);
		return await window.__cdp_ffi.actionabilityDiagnostic(target, {
			purpose: 'click',
			mode: 'strict',
			stabilityTimeoutMs: 400
		});
	`)
	detachProbe := actionabilityProbe{}
	if err := json.Unmarshal([]byte(detachRaw), &detachProbe); err != nil {
		t.Fatalf("unmarshal detached actionability diagnostic: %v raw=%q", err, detachRaw)
	}
	if detachProbe.Kind != "detached" {
		t.Fatalf("detached actionability kind = %q, want detached", detachProbe.Kind)
	}
	if got := must(page.ByTestID("detach-count").TextContent(context.Background())); got != "0" {
		t.Fatalf("detached target dispatched a click: %q", got)
	}
}

func TestDocumentClickDelegatedSVG(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/actionability-svg-delegate")
	mustOK(page.ByTestID("delegate-cell").Click(context.Background()))

	if got := must(page.ByTestID("delegate-state").TextContent(context.Background())); got != "delegate-clicked" {
		t.Fatalf("unexpected delegate state: %q", got)
	}

	probe, err := readActionabilityProbe(page, "delegate-cell")
	if err != nil {
		t.Fatal(err)
	}
	if !probe.Actionable {
		t.Fatalf("expected delegated actionability to be actionable: %+v", probe)
	}
	if probe.Kind != "delegated_clickable" {
		t.Fatalf("expected delegated_clickable kind, got %+v", probe)
	}
	if !strings.Contains(probe.HitTarget, "svg") {
		t.Fatalf("expected svg delegate hit target, got %+v", probe)
	}
}

func TestDocumentClickDelegatedSmallSVGIcon(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/actionability-svg-delegate")
	mustOK(page.ByTestID("icon-cell").Click(context.Background()))

	if got := must(page.ByTestID("icon-state").TextContent(context.Background())); got != "icon-clicked" {
		t.Fatalf("unexpected icon state: %q", got)
	}

	probe, err := readActionabilityProbe(page, "icon-cell")
	if err != nil {
		t.Fatal(err)
	}
	if !probe.Actionable {
		t.Fatalf("expected icon delegate actionability to be actionable: %+v", probe)
	}
	if probe.Kind != "delegated_clickable" {
		t.Fatalf("expected delegated_clickable kind, got %+v", probe)
	}
	if !strings.Contains(probe.HitTarget, "svg") {
		t.Fatalf("expected svg icon hit target, got %+v", probe)
	}
	if !strings.Contains(probe.DelegateReason, "svg icon") {
		t.Fatalf("expected svg icon delegate reason, got %+v", probe)
	}
}

func TestDocumentClickHoverRevealTableAction(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/actionability-hover-reveal")

	probe, err := readActionabilityProbe(page, "audit-action", "click")
	if err != nil {
		t.Fatal(err)
	}
	if probe.Actionable || !probe.Retriable || probe.RetryAction != "hover_priming" {
		t.Fatalf("expected hidden table action to request hover priming before click, got %+v", probe)
	}

	row := page.Locator(`[data-testid="hover-table"] tbody tr`).First()
	mustOK(row.Locator(`xpath=//td[22]/div[1]/a[1]`).Click(context.Background()))

	if got := must(page.ByTestID("hover-state").TextContent(context.Background())); got != "audit-clicked" {
		t.Fatalf("unexpected hover reveal state: %q", got)
	}
}

func TestDocumentClickFallbackSkipsNonActionableMatch(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/actionability-hover-reveal")
	mustOK(page.Locator("#display-none-action", "#visible-fallback-action").Click(context.Background()))

	if got := must(page.ByTestID("fallback-state").TextContent(context.Background())); got != "visible-clicked" {
		t.Fatalf("unexpected fallback state: %q", got)
	}
}

func TestDocumentInputFallbackSkipsNonEditableMatch(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/actionability-hover-reveal")
	mustOK(page.Locator("#display-none-input", "#visible-input-fallback").Fill(context.Background(), "typed"))
	if got := must(page.ByTestID("visible-input-fallback").Value(context.Background())); got != "typed" {
		t.Fatalf("unexpected input fallback value: %q", got)
	}
	if got := must(page.ByTestID("input-fallback-state").TextContent(context.Background())); got != "typed" {
		t.Fatalf("unexpected input fallback state: %q", got)
	}
}

func TestDocumentClickBlockedOverlayRemainsObscured(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/actionability-svg-delegate")

	msg := expectPanicMessage(func() {
		mustOK(page.ByTestID("blocked-cell").Click(context.Background()))

	})
	if msg == "" {
		t.Fatal("expected click on blocked-cell to fail")
	}
	if !strings.Contains(msg, "obscured") {
		t.Fatalf("expected obscured panic, got %q", msg)
	}
	if got := must(page.ByTestID("blocked-state").TextContent(context.Background())); got != "idle" {
		t.Fatalf("unexpected blocked state after failed click: %q", got)
	}

	probe, err := readActionabilityProbe(page, "blocked-cell")
	if err != nil {
		t.Fatal(err)
	}
	if probe.Actionable {
		t.Fatalf("expected blocked actionability to fail: %+v", probe)
	}
	if probe.Kind != "obscured" {
		t.Fatalf("expected obscured kind, got %+v", probe)
	}
	if !strings.Contains(probe.Culprit, "blocked") {
		t.Fatalf("expected blocked overlay culprit, got %+v", probe)
	}
}

func TestDocumentClickSVGInsideBlockerRemainsObscured(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/actionability-svg-delegate")

	msg := expectPanicMessage(func() {
		mustOK(page.ByTestID("spinner-blocked-cell").Click(context.Background()))

	})
	if msg == "" {
		t.Fatal("expected click on spinner-blocked-cell to fail")
	}
	if !strings.Contains(msg, "obscured") {
		t.Fatalf("expected obscured panic, got %q", msg)
	}
	if got := must(page.ByTestID("spinner-blocked-state").TextContent(context.Background())); got != "idle" {
		t.Fatalf("unexpected spinner blocked state after failed click: %q", got)
	}

	probe, err := readActionabilityProbe(page, "spinner-blocked-cell")
	if err != nil {
		t.Fatal(err)
	}
	if probe.Actionable {
		t.Fatalf("expected spinner blocker actionability to fail: %+v", probe)
	}
	if probe.Kind != "obscured" {
		t.Fatalf("expected obscured kind, got %+v", probe)
	}
	if probe.DelegateReason != "" {
		t.Fatalf("expected no delegate reason for spinner blocker, got %+v", probe)
	}
}
