package library

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"gopkg.d7z.net/cdp"
)

func TestExactElementDoesNotRequeryAfterReplacement(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/mcp-test")
	ctx := context.Background()
	element := must(page.ByTestID("btn-counter").All(ctx))[0]
	mustOK(element.Click(ctx))
	if got := must(page.ByTestID("btn-counter").TextContent(ctx)); got != "Clicked: 1" {
		t.Fatalf("single click: %s", got)
	}
	evalText(page, `const old=document.querySelector('[data-testid="btn-counter"]');const replacement=old.cloneNode(true);replacement.textContent='replacement';replacement.onclick=()=>replacement.textContent='unexpected click';old.replaceWith(replacement)`)
	if err := element.Click(ctx); err == nil {
		t.Fatal("detached exact element clicked")
	}
	if got := must(page.ByTestID("btn-counter").TextContent(ctx)); got != "replacement" {
		t.Fatalf("exact element silently re-queried: %s", got)
	}
	mustOK(page.Navigate(ctx, fixtureRT.Server.URL+"/mcp-test", cdp.NavigateOptions{}))
	for name, operation := range map[string]func() error{
		"click":      func() error { return element.Click(ctx) },
		"text":       func() error { _, err := element.TextContent(ctx); return err },
		"visibility": func() error { _, err := element.IsVisible(ctx); return err },
		"eval":       func() error { return element.Eval(ctx, "return this.textContent", nil) },
		"screenshot": func() error { _, err := element.Screenshot(ctx); return err },
	} {
		if err := operation(); !errors.Is(err, cdp.ErrStaleElement) {
			t.Errorf("%s after navigation: %v", name, err)
		}
	}
}

func TestCanceledOperationsDoNotPoisonPage(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/home")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	operations := []func() error{
		func() error { _, err := page.Locator("body").Count(ctx); return err },
		func() error { return page.Locator("missing").Wait(ctx, cdp.StateVisible) },
		func() error { return page.Navigate(ctx, "https://invalid.invalid", cdp.NavigateOptions{}) },
		func() error { _, err := page.Screenshot(ctx); return err },
		func() error { return page.Press(ctx, "A", cdp.KeyOptions{}) },
		func() error { _, err := page.WatchDownloads(ctx, 1); return err },
		func() error { _, err := page.WatchPrint(ctx); return err },
	}
	for i, operation := range operations {
		if err := operation(); !errors.Is(err, context.Canceled) {
			t.Fatalf("operation %d: %v", i, err)
		}
	}
	if got := evalValue(page, "return 6*7"); got != "42" {
		t.Fatal(got)
	}
}

func TestSnapshotElementLifetime(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/mcp-test")
	ctx := context.Background()
	first := must(page.Snapshot(ctx))
	var element *cdp.Element
	for _, node := range first.Nodes {
		if node.Role == "button" && node.Name == "Clicked: 0" {
			element = must(page.Element(ctx, first.Ref(node.ID)))
			break
		}
	}
	if element == nil {
		t.Fatal("counter button missing from snapshot")
	}
	if got := must(element.TextContent(ctx)); got != "Clicked: 0" {
		t.Fatalf("current snapshot text: %q", got)
	}
	second := must(page.Snapshot(ctx))
	if second.ID == first.ID {
		t.Fatal("new snapshot must advance the reference generation")
	}
	for name, operation := range map[string]func() error{
		"click":      func() error { return element.Click(ctx) },
		"text":       func() error { _, err := element.TextContent(ctx); return err },
		"visibility": func() error { _, err := element.IsVisible(ctx); return err },
		"eval":       func() error { return element.Eval(ctx, "return this.textContent", nil) },
		"screenshot": func() error { _, err := element.Screenshot(ctx); return err },
	} {
		if err := operation(); !errors.Is(err, cdp.ErrStaleElement) {
			t.Errorf("%s with outdated snapshot: %v", name, err)
		}
	}
	if got := must(page.ByTestID("btn-counter").TextContent(ctx)); got != "Clicked: 0" {
		t.Fatalf("outdated snapshot changed the counter: %q", got)
	}
}

func TestPrintWaitCancellationDoesNotConsumeResult(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/print")
	lifetime, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	watcher := must(page.WatchPrint(lifetime))
	defer watcher.Close()
	short, stop := context.WithCancel(context.Background())
	stop()
	if _, err := watcher.Wait(short); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait cancellation: %v", err)
	}
	mustOK(page.ByTestID("print-trigger").Click(lifetime))
	artifact := must(watcher.Wait(lifetime))
	if len(artifact.Data) < 4 || string(artifact.Data[:4]) != "%PDF" {
		t.Fatal("invalid PDF")
	}
	again := must(watcher.Wait(lifetime))
	if string(again.Data) != string(artifact.Data) {
		t.Fatal("cached print changed")
	}
}

func TestConcurrentShutdownAndSubscriptions(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/home")
	ctx := context.Background()
	connection := must(cdp.Connect(ctx, execBrowser.APIBrowser.Endpoint(), cdp.ConnectOptions{}))
	attached := must(connection.Page(ctx, page.ID()))
	subscription := must(attached.Session().Subscribe(ctx, "Network.requestWillBeSent"))
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() { errs <- connection.Close() })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	select {
	case _, open := <-subscription.Events():
		if open {
			t.Fatal("subscription not closed")
		}
	case <-time.After(time.Second):
		t.Fatal("subscription leaked")
	}
	if !errors.Is(subscription.Err(), cdp.ErrClosed) {
		t.Fatalf("subscription close: %v", subscription.Err())
	}
	if !execBrowser.APIBrowser.Alive() {
		t.Fatal("borrowed shutdown killed browser")
	}
}
