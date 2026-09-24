// Package fixture serves the browser pages and API routes used by E2E tests.
package fixture

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed pages/*.html assets/*.js
var pageFS embed.FS

// Server owns an HTTP fixture server.
type Server struct {
	HTTP *httptest.Server
	URL  string
}

// Start creates and starts a fixture server.
func Start(_ context.Context) *Server {
	mux := http.NewServeMux()
	registerFixtureRoutes(mux)
	server := httptest.NewServer(mux)
	return &Server{
		HTTP: server,
		URL:  server.URL,
	}
}

func registerFixtureRoutes(mux *http.ServeMux) {
	if mux == nil {
		return
	}
	entries, err := fs.Glob(pageFS, "pages/*.html")
	if err != nil {
		panic(fmt.Errorf("glob fixture pages: %w", err))
	}
	sort.Strings(entries)
	for _, entry := range entries {
		name := path.Base(entry)
		route := routeForFixture(name)
		handler := serveFixturePage(name)
		if route == "/" {
			mux.HandleFunc("/", handler)
			mux.HandleFunc("/home", handler)
			continue
		}
		mux.HandleFunc(route, handler)
	}
	assets, err := fs.Sub(pageFS, "assets")
	if err != nil {
		panic(err)
	}
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(assets))))
	registerAPIRoutes(mux)
}

func registerAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/headers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(r.Header)
	})
	mux.HandleFunc("/api/hello", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, `{"message":"hello"}`)
	})
	mux.HandleFunc("/api/echo", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body map[string]any
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		w.WriteHeader(http.StatusOK)
		if body == nil {
			_, _ = fmt.Fprint(w, `{"echo":null}`)
			return
		}
		resp, err := json.Marshal(body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = fmt.Fprint(w, string(resp))
	})
	mux.HandleFunc("/api/data", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, `{"updated":true}`)
	})
	mux.HandleFunc("/api/slow-image", func(w http.ResponseWriter, r *http.Request) {
		delayMs, _ := strconv.Atoi(r.URL.Query().Get("delay_ms"))
		if delayMs <= 0 {
			delayMs = 2500
		}
		time.Sleep(time.Duration(delayMs) * time.Millisecond)
		w.Header().Set("Content-Type", "image/svg+xml")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, `<svg xmlns="http://www.w3.org/2000/svg" width="12" height="12"><rect width="12" height="12" fill="#4f46e5"/></svg>`)
	})
	mux.HandleFunc("/api/item", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, `{"deleted":true}`)
	})
	mux.HandleFunc("/api/download/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, path.Base(r.URL.Path)))
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "downloaded-content")
	})
	mux.HandleFunc("/api/download-not-found", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
}

func routeForFixture(name string) string {
	base := strings.TrimSpace(strings.TrimSuffix(name, ".html"))
	if base == "" || base == "home" {
		return "/"
	}
	return "/" + base
}

// Close stops the fixture server.
func (s *Server) Close() {
	if s == nil || s.HTTP == nil {
		return
	}
	s.HTTP.Close()
}

func serverBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func serveFixturePage(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(name, "identity") {
			w.Header().Set("Accept-CH", "Sec-CH-UA-Full-Version-List, Sec-CH-UA-Arch, Sec-CH-UA-Bitness")
		}
		body, err := pageFS.ReadFile("pages/" + name)
		if err != nil {
			http.Error(w, fmt.Sprintf("fixture page %s not found", name), http.StatusInternalServerError)
			return
		}
		content := strings.ReplaceAll(string(body), "{{BASE_URL}}", serverBaseURL(r))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, content)
	}
}
