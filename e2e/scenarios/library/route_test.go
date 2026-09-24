package library

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gopkg.d7z.net/cdp"
)

func TestRoutePrecedenceAndUnregister(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	session := acquireSession(t)
	page := must(session.Open("/route-test"))
	ctx := context.Background()
	first := must(page.Route(ctx, cdp.RoutePattern{URL: "/api/**"}, func(ctx context.Context, r *cdp.Route) error {
		return r.Fulfill(ctx, cdp.RouteFulfillOptions{Body: []byte("first"), ContentType: "text/plain"})
	}))
	defer first.Close()
	second := must(page.Route(ctx, cdp.RoutePattern{URL: "/api/hello"}, func(ctx context.Context, r *cdp.Route) error {
		return r.Fulfill(ctx, cdp.RouteFulfillOptions{Status: 201, Body: []byte(`{"custom":"yes"}`), ContentType: "application/json"})
	}))
	defer second.Close()
	response := must(page.Fetch(ctx, cdp.FetchRequest{URL: session.FixtureURL("/api/hello")}))
	if response.Status != 201 || string(response.Body) != `{"custom":"yes"}` {
		t.Fatalf("last handler precedence: %+v", response)
	}
	mustOK(second.Close())
	for range 3 {
		response = must(page.Fetch(ctx, cdp.FetchRequest{URL: session.FixtureURL("/api/hello")}))
		if string(response.Body) != "first" {
			t.Fatalf("fallback: %+v", response)
		}
	}
	mustOK(first.Close())
	response = must(page.Fetch(ctx, cdp.FetchRequest{URL: session.FixtureURL("/api/hello")}))
	if !strings.Contains(string(response.Body), "hello") {
		t.Fatalf("unregister: %+v", response)
	}
}

func TestRouteContinueAndAbort(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	session := acquireSession(t)
	page := must(session.Open("/route-test"))
	ctx := context.Background()
	registration := must(page.Route(ctx, cdp.RoutePattern{URL: "/api/hello"}, func(ctx context.Context, r *cdp.Route) error { return r.Continue(ctx, cdp.RouteContinueOptions{}) }))
	defer registration.Close()
	response := must(page.Fetch(ctx, cdp.FetchRequest{URL: session.FixtureURL("/api/hello")}))
	if !strings.Contains(string(response.Body), "hello") {
		t.Fatalf("continue: %+v", response)
	}
	mustOK(registration.Close())
	registration = must(page.Route(ctx, cdp.RoutePattern{URL: "/api/hello"}, func(ctx context.Context, r *cdp.Route) error { return r.Abort(ctx, "") }))
	defer registration.Close()
	if _, err := page.Fetch(ctx, cdp.FetchRequest{URL: session.FixtureURL("/api/hello")}); err == nil {
		t.Fatal("aborted fetch succeeded")
	}
	// Unmatched requests still proceed while interception is active.
	response = must(page.Fetch(ctx, cdp.FetchRequest{URL: session.FixtureURL("/api/data"), Method: "DELETE"}))
	if response.Status != 200 {
		t.Fatalf("unmatched: %+v", response)
	}
}

func TestRouteCancellationReleasesPausedRequest(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	session := acquireSession(t)
	page := must(session.Open("/route-test"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	registration := must(page.Route(ctx, cdp.RoutePattern{URL: "/api/hello"}, func(ctx context.Context, r *cdp.Route) error { close(entered); <-ctx.Done(); return ctx.Err() }))
	defer registration.Close()
	fetchCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	done := make(chan error, 1)
	go func() {
		_, err := page.Fetch(fetchCtx, cdp.FetchRequest{URL: session.FixtureURL("/api/hello")})
		done <- err
	}()
	select {
	case <-entered:
	case <-fetchCtx.Done():
		t.Fatal("handler not invoked")
	}
	cancel()
	if err := <-done; errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("request remained paused: %v", err)
	}
}
