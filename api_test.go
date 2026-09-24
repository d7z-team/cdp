package cdp

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"gopkg.d7z.net/cdp/internal/engine"
)

func TestTimeoutDefaultsAndValidation(t *testing.T) {
	defaults, err := (Timeouts{}).normalized()
	if err != nil || defaults.Action <= 0 || defaults.Connect <= 0 || defaults.Shutdown <= 0 {
		t.Fatalf("defaults: %+v %v", defaults, err)
	}
	custom, err := (Timeouts{Action: time.Second}).normalized()
	if err != nil || custom.Action != time.Second || defaults.Action == custom.Action {
		t.Fatalf("independent values: %+v %v", custom, err)
	}
	if _, err := (Timeouts{Print: -time.Second}).normalized(); err == nil {
		t.Fatal("negative timeout accepted")
	}
}

func TestLocatorPlanIsImmutable(t *testing.T) {
	p := &Page{}
	base := p.Locator("#first", "#fallback")
	scoped := base.Nth(2).Locator("span")
	frame := base.ContentFrame().Locator("button")
	if len(base.plan.Layers) != 1 || base.plan.Terminal != nil {
		t.Fatal("base mutated")
	}
	if len(scoped.plan.Layers) != 3 || scoped.plan.Layers[1].Filter.Index != 2 {
		t.Fatalf("terminal scope: %+v", scoped.plan)
	}
	if len(frame.plan.Layers) != 3 || frame.plan.Layers[1].Options[0].Kind != engine.SelectorPlanOptionKindFrameEnter {
		t.Fatalf("frame: %+v", frame.plan)
	}
	if len(base.plan.Layers[0].Options) != 2 {
		t.Fatal("fallback merged")
	}
	root := &Element{page: p}
	desc := root.Locator("td").Last().Locator("button")
	if desc.root != root {
		t.Fatal("exact root lost")
	}
}

func TestPublicErrorsPreserveCause(t *testing.T) {
	cause := context.DeadlineExceeded
	backend := engine.NewBrowserError("runtime", "javascript_exception", "bad script", cause)
	err := (&Locator{page: &Page{}}).wrapError("click", backend)
	var operation *OperationError
	var browser *BrowserError
	var locator *LocatorError
	if !errors.Is(err, cause) || !errors.As(err, &operation) || !errors.As(err, &browser) || !errors.As(err, &locator) || locator.Op != "locator.click" || browser.Kind != "javascript_exception" {
		t.Fatalf("error chain: %#v", err)
	}
}

func TestUninitializedHandlesReturnErrors(t *testing.T) {
	ctx := context.Background()
	var browser *Browser
	var page *Page
	var locator *Locator
	var element *Element
	checks := []func() error{
		func() error { _, err := browser.NewPage(ctx); return err },
		func() error { _, err := browser.Pages(ctx); return err },
		func() error { _, err := page.EvalJSON(ctx, "return 1"); return err },
		func() error { return locator.Click(ctx) },
		func() error { return element.Click(ctx) },
		func() error { _, err := page.WatchDownloads(ctx, 1); return err },
		func() error { _, err := page.WatchPrint(ctx); return err },
	}
	for i, check := range checks {
		if err := check(); !errors.Is(err, ErrClosed) {
			t.Fatalf("check %d: %v", i, err)
		}
	}
	if err := (&Browser{}).Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWatcherCachedResultsAreIndependent(t *testing.T) {
	done := make(chan struct{})
	close(done)
	download := &DownloadWatcher{done: done, results: []Download{{Name: "same.txt", Data: []byte("one")}, {Name: "same.txt", Data: []byte("two")}}, err: context.DeadlineExceeded}
	first, err := download.Wait(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) || len(first) != 2 {
		t.Fatalf("partial: %+v %v", first, err)
	}
	first[0].Data[0] = 'x'
	second, _ := download.Wait(context.Background())
	if string(second[0].Data) != "one" {
		t.Fatal("download cache exposed")
	}
	print := &PrintWatcher{done: done, result: PrintArtifact{MIMEType: "application/pdf", Data: []byte("pdf")}}
	a, err := print.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a.Data[0] = 'x'
	b, _ := print.Wait(context.Background())
	if string(b.Data) != "pdf" {
		t.Fatal("print cache exposed")
	}
}

func TestRouteMatching(t *testing.T) {
	for _, test := range []struct {
		pattern, url string
		want         bool
	}{
		{"**/api/*", "http://example.test/api/hello", true},
		{"/api/hello", "http://example.test/api/hello", true},
		{"**/api/*", "http://example.test/api/nested/hello", false},
		{"**/image?.png", "http://example.test/image1.png", true},
	} {
		if got := routeMatches(RoutePattern{URL: test.pattern}, test.url, "Fetch"); got != test.want {
			t.Errorf("%q %q: %v", test.pattern, test.url, got)
		}
	}
	if routeMatches(RoutePattern{ResourceType: "Image"}, "http://example.test", "Fetch") {
		t.Fatal("resource filter ignored")
	}
}

func TestLocatorPreservesOrderedDuplicateFallbacks(t *testing.T) {
	p := &Page{}
	if _, ok := p.CurrentSnapshot(); ok {
		t.Fatal("unexpected snapshot")
	}
	// Plan alternatives are ordered and not deduplicated by the Go API.
	plan := p.Locator("a", "a", "b").plan
	got := []string{}
	for _, option := range plan.Layers[0].Options {
		got = append(got, option.Raw)
	}
	if !reflect.DeepEqual(got, []string{"a", "a", "b"}) {
		t.Fatalf("order: %v", got)
	}
}
