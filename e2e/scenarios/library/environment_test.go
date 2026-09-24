package library

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"reflect"
	"sync"
	"testing"
	"time"

	"gopkg.d7z.net/cdp"
)

func TestHeadlessScreenConfiguration(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	for _, screen := range []cdp.ScreenOptions{{}, {Width: 1600, Height: 1000, ScaleFactor: 2}, {Width: 1920, Height: 1080, ScaleFactor: 1.25}} {
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		browser, err := cdp.Launch(ctx, cdp.LaunchOptions{Screen: screen, WindowSize: cdp.WindowSize{Width: 1440, Height: 960}})
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = browser.Close(); cancel() })
		page := must(browser.NewPage(ctx))
		var actual struct {
			Width, Height, OuterWidth, OuterHeight int
			Scale                                  float64
		}
		mustOK(page.Eval(ctx, `return {Width:screen.width,Height:screen.height,OuterWidth:outerWidth,OuterHeight:outerHeight,Scale:devicePixelRatio}`, &actual))
		width, height, scale := screen.Width, screen.Height, screen.ScaleFactor
		if width == 0 {
			width, height, scale = 1920, 1080, 1
		}
		// Fractional display scaling rounds Chromium window decorations to pixels.
		if actual.Width != width || actual.Height != height || actual.Scale != scale || math.Abs(float64(actual.OuterWidth-1440)) > 2 || math.Abs(float64(actual.OuterHeight-960)) > 2 {
			t.Errorf("screen=%+v", actual)
		}
		mustOK(browser.Close())
		cancel()
	}
}

func TestNativeBrowserIdentity(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	target := must(url.Parse(fixtureRT.Server.URL))
	proxy := httputil.NewSingleHostReverseProxy(target)
	var mu sync.Mutex
	var requests []struct{ Path, UA string }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, struct{ Path, UA string }{r.URL.Path, r.UserAgent()})
		mu.Unlock()
		proxy.ServeHTTP(w, r)
	}))
	defer server.Close()
	page := openFixture(t, "/identity")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	var expected string
	for range 2 {
		mustOK(page.Navigate(ctx, server.URL+"/identity", cdp.NavigateOptions{}))
		var results map[string]struct {
			UA       string         `json:"ua"`
			Platform string         `json:"platform"`
			Metadata map[string]any `json:"metadata"`
			Headers  http.Header    `json:"headers"`
		}
		mustOK(page.Eval(ctx, `return await identityProbe()`, &results))
		main := results["main"]
		expected = main.UA
		if len(results) != 6 || expected == "" || main.Metadata["architecture"] == "" {
			t.Fatalf("incomplete native identity: %v", results)
		}
		for name, result := range results {
			if result.UA != expected || result.Headers.Get("User-Agent") != expected || result.Platform != main.Platform {
				t.Errorf("%s identity differs: %+v", name, result)
			}
			if !reflect.DeepEqual(result.Metadata, main.Metadata) {
				t.Errorf("%s metadata=%v want=%v", name, result.Metadata, main.Metadata)
			}
		}
		if main.Headers.Get("Sec-Ch-Ua-Arch") != `"`+main.Metadata["architecture"].(string)+`"` {
			t.Errorf("negotiated metadata missing: %v", main.Headers)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	seen := map[string]bool{}
	for _, request := range requests {
		seen[request.Path] = true
		if request.UA != expected {
			t.Errorf("request %s UA=%q want=%q", request.Path, request.UA, expected)
		}
	}
	for _, path := range []string{"/identity", "/identity-frame", "/assets/identity-worker.js", "/assets/identity-shared.js", "/assets/identity-service.js"} {
		if !seen[path] {
			t.Errorf("missing initial request for %s", path)
		}
	}
}
