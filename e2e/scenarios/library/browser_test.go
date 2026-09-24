package library

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gopkg.d7z.net/cdp"
	harnessruntime "gopkg.d7z.net/cdp/e2e/harness/runtime"
	"gopkg.d7z.net/cdp/mcpserver"
)

func TestConnectCancellationDuringDiscovery(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	started, aborted := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(aborted) }))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := cdp.Connect(ctx, server.URL, cdp.ConnectOptions{}); result <- err }()
	<-started
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("discovery: %v", err)
	}
	select {
	case <-aborted:
	case <-time.After(3 * time.Second):
		t.Fatal("discovery request continued")
	}
}

func TestConnectInitializationAndIndependentLifetime(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/home")
	setup, cancel := context.WithCancel(context.Background())
	browser := must(cdp.Connect(setup, execBrowser.APIBrowser.Endpoint(), cdp.ConnectOptions{Initialize: func(i *cdp.Initializer) error {
		return i.RegisterInitScript(cdp.InitScript{Name: "public-probe.js", World: cdp.WorldCore, Source: `globalThis.__publicProbe = 'ready';`, Probe: `globalThis.__publicProbe === 'ready'`})
	}}))
	defer browser.Close()
	cancel()
	attached := must(browser.Page(context.Background(), page.ID()))
	world := must(attached.ExecutionContext(context.Background(), cdp.ExecutionContextOptions{World: cdp.WorldCore}))
	if got := evalText(world, `return globalThis.__publicProbe`); got != "ready" {
		t.Fatalf("initialization: %s", got)
	}
	server := must(mcpserver.New(browser, mcpserver.Options{}))
	mustOK(server.Close())
	if !browser.Alive() {
		t.Fatal("MCP closed borrowed browser")
	}
	mustOK(browser.Close())
	mustOK(browser.Close())
	if _, err := browser.Pages(context.Background()); !errors.Is(err, cdp.ErrClosed) {
		t.Fatalf("closed operation: %v", err)
	}
	if !execBrowser.APIBrowser.Alive() {
		t.Fatal("Connect killed external process")
	}
	if must(page.Title(context.Background())) == "" {
		t.Fatal("owner connection lost")
	}
}

func TestBrowserPagesAndExplicitSelection(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	ctx := context.Background()
	browser := execBrowser.APIBrowser
	page := must(browser.NewPage(ctx))
	defer page.Close(ctx)
	mustOK(page.Navigate(ctx, fixtureRT.Server.URL+"/home", cdp.NavigateOptions{}))
	pages := must(browser.Pages(ctx))
	found := false
	for _, p := range pages {
		if p.ID() == page.ID() {
			found = true
		}
	}
	if !found {
		t.Fatal("new page missing")
	}
	mustOK(page.Activate(ctx))
	if must(browser.ActivePage(ctx)).ID() != page.ID() {
		t.Fatal("active page incorrect")
	}
	if must(browser.Page(ctx, page.ID())).ID() != page.ID() {
		t.Fatal("page lookup incorrect")
	}
	if _, err := browser.FindPage(ctx, cdp.PageMatch{URLPrefix: "https://missing.invalid"}); !errors.Is(err, cdp.ErrNotFound) {
		t.Fatalf("find: %v", err)
	}
}

func TestBrowserInitializationFailurePreservesCause(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	cause := errors.New("initializer deliberately failed")
	browser, err := cdp.Connect(context.Background(), execBrowser.APIBrowser.Endpoint(), cdp.ConnectOptions{Initialize: func(*cdp.Initializer) error { return cause }})
	if browser != nil || !errors.Is(err, cause) {
		t.Fatalf("initialization error: %v %v", browser, err)
	}
	if !execBrowser.APIBrowser.Alive() {
		t.Fatal("failed attach killed owner")
	}
}

func TestBrowserTimeoutsAreIndependent(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/home")
	fast := configuredPage(t, page, cdp.ConnectOptions{Timeouts: cdp.Timeouts{Read: 500 * time.Millisecond}})
	if err := fast.Eval(context.Background(), `await new Promise(r=>setTimeout(r,1000));return 1`, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("short timeout: %v", err)
	}
	if got := evalValue(page, `await new Promise(r=>setTimeout(r,100));return 2`); got != "2" {
		t.Fatalf("timeout leaked: %s", got)
	}
}

func TestLaunchOwnershipAndProfileProtection(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	ctx := context.Background()
	profile := t.TempDir()
	options := harnessruntime.BuildBrowserConfig(execBrowser.Config, profile)
	owner, err := cdp.Launch(ctx, options)
	if err != nil {
		t.Fatalf("launch profile owner: %v", err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	other, err := cdp.Launch(ctx, options)
	if other != nil {
		t.Cleanup(func() { _ = other.Close() })
	}
	if other != nil || !errors.Is(err, cdp.ErrProfileInUse) {
		t.Fatalf("profile collision: %v %v", other, err)
	}
	if !owner.Alive() {
		t.Fatal("profile collision killed owner")
	}
	borrowed := must(cdp.Connect(ctx, owner.Endpoint(), cdp.ConnectOptions{}))
	mustOK(borrowed.Close())
	if !owner.Alive() {
		t.Fatal("borrowed close killed process")
	}
	mustOK(owner.Close())
	if owner.Alive() {
		t.Fatal("owned process still alive")
	}
}
