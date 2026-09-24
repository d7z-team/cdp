package library

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gopkg.d7z.net/cdp"
)

func TestWaitForURLExact(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	sess := acquireSession(t)
	page, err := sess.Open("/navigation")
	if err != nil {
		t.Fatal(err)
	}
	mustOK(page.ByTestID("exact-link").Click(context.Background()))

	want := sess.FixtureURL("/navigation-done?step=exact")
	mustOK(page.WaitForURL(context.Background(), want))
	if got := must(page.ByTestID("done-state").TextContent(context.Background())); got != "done" {
		t.Fatalf("unexpected navigation done state: %q", got)
	}
}

func TestWaitForURLPrefixMatch(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	sess := acquireSession(t)
	page, err := sess.Open("/navigation")
	if err != nil {
		t.Fatal(err)
	}
	mustOK(page.ByTestID("prefix-btn").Click(context.Background()))

	mustOK(page.WaitForURLMatch(context.Background(), func(url string) bool { return strings.HasPrefix(url, sess.FixtureURL("/navigation-done?step=prefix")) }))
	if got := must(page.ByTestID("done-state").TextContent(context.Background())); got != "done" {
		t.Fatalf("unexpected navigation done state: %q", got)
	}
}

func TestNetworkIdleIncludesExistingRequests(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/home")
	evalText(page, `window.__slowFinished=false;fetch('/api/slow-image?delay_ms=1500').finally(()=>window.__slowFinished=true)`)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := page.WaitForNetworkIdle(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("existing request not tracked: %v", err)
	}
	mustOK(page.WaitForNetworkIdle(context.Background()))
	if evalValue(page, `return window.__slowFinished`) != "true" {
		t.Fatal("idle returned before request finished")
	}
	evalText(page, `fetch('/api/slow-image?delay_ms=2000').catch(() => {})`)
	mustOK(page.Navigate(context.Background(), fixtureRT.Server.URL+"/eval", cdp.NavigateOptions{}))
	afterNavigation, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err := page.WaitForNetworkIdle(afterNavigation); err != nil {
		t.Fatalf("new document retained a previous document's request: %v", err)
	}
}
