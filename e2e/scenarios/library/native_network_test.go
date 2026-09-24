package library

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestNativeNetworkCacheRedirectAndPreflight(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	var cached, validated, preflight, posted atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		switch r.URL.Path {
		case "/cache":
			cached.Add(1)
			w.Header().Set("Cache-Control", "max-age=3600")
			_, _ = w.Write([]byte("cached"))
		case "/validate":
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("ETag", `"stable"`)
			if r.Header.Get("If-None-Match") == `"stable"` {
				validated.Add(1)
				w.WriteHeader(http.StatusNotModified)
				return
			}
			_, _ = w.Write([]byte("validated"))
		case "/redirect":
			http.Redirect(w, r, "/final", http.StatusFound)
		case "/final":
			_, _ = w.Write([]byte("redirected"))
		case "/cors":
			if r.Method == http.MethodOptions {
				preflight.Add(1)
				w.Header().Set("Access-Control-Allow-Methods", "POST")
				w.Header().Set("Access-Control-Allow-Headers", "X-Probe")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			if r.Method == http.MethodPost && r.Header.Get("X-Probe") == "yes" {
				posted.Add(1)
			}
			_, _ = w.Write([]byte("posted"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	p := openFixture(t, "/home")
	var results []string
	mustOK(p.Eval(t.Context(), fmt.Sprintf(`
		const base = %q;
		const results = [];
		for (const path of ['/cache', '/cache', '/validate', '/validate', '/redirect']) {
			results.push(await (await fetch(base + path)).text());
		}
		results.push(await (await fetch(base + '/cors', {method:'POST', headers:{'X-Probe':'yes'}})).text());
		return results;
	`, server.URL), &results))
	if fmt.Sprint(results) != "[cached cached validated validated redirected posted]" {
		t.Fatalf("native fetch results: %v", results)
	}
	if cached.Load() != 1 || validated.Load() != 1 || preflight.Load() != 1 || posted.Load() != 1 {
		t.Fatalf("requests: cached=%d, 304=%d, OPTIONS=%d, POST=%d", cached.Load(), validated.Load(), preflight.Load(), posted.Load())
	}
}
