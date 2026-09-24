package library

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"gopkg.d7z.net/cdp"
)

const mainWorldNamespaceProbe = `
	const forbidden = [
		'__cdp_runtime',
		'__cdp_ffi',
		'__cdp_main_runtime'
	];
	const ownNames = Object.getOwnPropertyNames(window);
	return forbidden.every((name) => !(name in window))
		&& Object.keys(window).every((name) => !/cdp/i.test(name))
		&& ownNames.every((name) => !/cdp/i.test(name));
`

func TestRuntimeNamespaceFacade(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/eval")
	if got := evalValue(page, `return window.__fixtureState?.status === 'booted'`); got != "true" {
		t.Fatalf("page main context result = %q", got)
	}
	if got := evalValue(page, "return (() => {"+mainWorldNamespaceProbe+"})()"); got != "true" {
		t.Fatalf("internal runtime exposed in main world before lazy runtime use: %q", got)
	}
	_ = must(page.IsFullscreen(context.Background()))

	if got := evalValue(page, "return (() => {"+mainWorldNamespaceProbe+"})()"); got != "true" {
		t.Fatalf("internal runtime exposed in main world after lazy runtime use: %q", got)
	}
	if got := evalValue(page, `
		return document.querySelector('[__cdp_internal]') === null;
	`); got != "true" {
		t.Fatalf("internal runtime marker exposed in page DOM or symbols: %q", got)
	}

	isolated := must(page.ExecutionContext(context.Background(), cdp.ExecutionContextOptions{World: cdp.WorldCore}))
	if got := evalValue(isolated, `return await window.__cdp_ffi.call.pageId()`); got == "" || got == "undefined" {
		t.Fatalf("isolated ffi page id = %q", got)
	}
	if got := evalValue(isolated, `
		const facade = Object.getOwnPropertyDescriptor(window, '__cdp_ffi');
		const slots = Object.getOwnPropertyNames(window).filter((name) => name.startsWith('__cdp_ffi_slot_bind_'));
		return facade?.enumerable === false
			&& slots.length > 0
			&& slots.every((name) => Object.getOwnPropertyDescriptor(window, name)?.enumerable === false);
	`); got != "true" {
		t.Fatalf("isolated runtime descriptor mismatch: %q", got)
	}
}

func TestRuntimeNamespaceIsolationFromIframeParent(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/iframe")
	if got := evalValue(page, `
		const frame = document.querySelector('iframe');
		if (!frame?.contentWindow) return false;
		const parent = frame.contentWindow.parent;
		const forbidden = [
			'__cdp_runtime',
			'__cdp_ffi',
			'__cdp_main_runtime'
		];
		return forbidden.every((name) => !(name in parent))
			&& frame.contentWindow.Object.keys(parent).every((name) => !/cdp/i.test(name))
			&& frame.contentWindow.Object.getOwnPropertyNames(parent).every((name) => !/cdp/i.test(name));
	`); got != "true" {
		t.Fatalf("internal runtime visible from iframe parent reference: %q", got)
	}
}

func TestFrameBridgeRejectsPendingRequestOnLifecycleEnd(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	t.Run("bridge destroy", func(t *testing.T) {
		page := openFixture(t, "/eval")
		isolated := must(page.ExecutionContext(context.Background(), cdp.ExecutionContextOptions{World: cdp.WorldCore}))
		got := evalValue(isolated, `
			const bridge = window.__cdp_ffi?.bridge;
			if (!bridge) return 'missing-bridge';
			const frame = document.createElement('iframe');
			frame.src = 'data:text/html,<meta charset="utf-8"><title>no bridge</title>';
			document.body.appendChild(frame);
			await new Promise((resolve) => frame.addEventListener('load', resolve, {once: true}));
			const pending = bridge.request('test.never.responds', {}, frame.contentWindow, 10000)
				.then(() => 'resolved', (error) => error instanceof Error ? error.message : String(error));
			bridge.destroy();
			const result = await pending;
			frame.remove();
			return result;
		`)
		if !strings.Contains(got, "FrameBridge destroyed") {
			t.Fatalf("pending bridge request was not rejected on destroy: %q", got)
		}
	})

	t.Run("runtime removed", func(t *testing.T) {
		page := openFixture(t, "/eval")
		isolated := must(page.ExecutionContext(context.Background(), cdp.ExecutionContextOptions{World: cdp.WorldCore}))
		got := evalValue(isolated, `
			const bridge = window.__cdp_ffi?.bridge;
			if (!bridge) return 'missing-bridge';
			const frame = document.createElement('iframe');
			frame.src = 'data:text/html,<meta charset="utf-8"><title>departed runtime</title>';
			document.body.appendChild(frame);
			await new Promise((resolve) => frame.addEventListener('load', resolve, {once: true}));
			const runtimeId = 'runtime_departed_test';
			bridge.runtimeWindows.set(runtimeId, frame.contentWindow);
			bridge.runtimeIdsByWindow.set(frame.contentWindow, runtimeId);
			const pending = bridge.request('test.never.responds', {}, frame.contentWindow, 10000, runtimeId)
				.then(() => 'resolved', (error) => error instanceof Error ? error.message : String(error));
			const removed = bridge.forgetRuntime(runtimeId, frame.contentWindow);
			const result = await pending;
			frame.remove();
			return String(removed) + ':' + result;
		`)
		if !strings.Contains(got, "true:FrameBridge: runtime runtime_departed_test is unavailable") {
			t.Fatalf("pending runtime request was not rejected on removal: %q", got)
		}
	})
}

func TestCoreRuntimeReinstallAndNavigation(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	session := acquireSession(t)
	page, err := session.Open("/eval")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/eval", "/iframe-nested", "/eval?fresh=1"} {
		mustOK(page.Navigate(context.Background(), session.FixtureURL(path), cdp.NavigateOptions{}))

		isolated := must(page.ExecutionContext(context.Background(), cdp.ExecutionContextOptions{World: cdp.WorldCore}))
		if got := evalValue(isolated, `
   window.__testCoreInstance = window.__cdp_ffi;
   return window.__cdp_ffi.runtimeReady();
  `); got != "true" {
			t.Fatalf("core runtime readiness at %s: %s", path, got)
		}
		for range 2 {
			_ = must(page.ExecutionContext(context.Background(), cdp.ExecutionContextOptions{World: cdp.WorldCore}))
		}
		if got := evalValue(isolated, `return window.__cdp_ffi === window.__testCoreInstance && window.__cdp_ffi.runtimeReady()`); got != "true" {
			t.Fatalf("core replaced on repeated ensure: %s", got)
		}
		if must(page.Locator("body").Count(context.Background())) !=
			1 {
			t.Fatal("selector query failed after runtime reconciliation")
		}
	}
}

func TestCoreManagerHandoffPreservesClaimedRuntime(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	session := acquireSession(t)
	page, err := session.Open("/eval")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := session.Browser.APIBrowser.Endpoint()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	first, err := cdp.Connect(ctx, endpoint, cdp.ConnectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := first.Page(ctx, page.ID()); err != nil {
		t.Fatal(err)
	}
	second, err := cdp.Connect(ctx, endpoint, cdp.ConnectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	replacement, err := second.Page(ctx, page.ID())
	if err != nil {
		t.Fatal(err)
	}

	handle, err := replacement.ExecutionContext(ctx, cdp.ExecutionContextOptions{World: cdp.WorldCore})
	if err != nil {
		t.Fatal(err)
	}
	before, err := handle.EvalJSON(ctx, `return window.__cdp_ffi.bridge.runtimeId`)
	if err != nil || len(before) == 0 {
		t.Fatalf("runtime before handoff: %v %v", before, err)
	}
	if err := first.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := handle.EvalJSON(ctx, `return window.__cdp_ffi.bridge.runtimeId`)
	if err != nil || string(before) != string(after) {
		t.Fatalf("runtime changed during old manager cleanup: %v -> %v (%v)", before, after, err)
	}
	result, err := handle.EvalJSON(ctx, `return await window.__cdp_ffi.call.pageId()`)
	if err != nil || string(result) != fmt.Sprintf("%q", page.ID()) {
		t.Fatalf("replacement binding result: %v (%v)", result, err)
	}
	if err := second.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	mustOK(page.Navigate(context.Background(), session.FixtureURL("/eval?handoff=fresh"), cdp.NavigateOptions{}))

	if must(page.Locator("body").Count(context.Background())) !=
		1 {
		t.Fatal("primary manager did not restore core on navigation")
	}
}
