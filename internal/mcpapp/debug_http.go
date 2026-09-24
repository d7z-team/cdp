package mcpapp

import (
	"embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

//go:embed debugui/*
var debugAssets embed.FS

func registerDebugRoutes(mux *http.ServeMux, service *Service, catalog *toolCatalog) {
	hub := newDebugHub(service, catalog)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/debug", http.StatusTemporaryRedirect)
	})
	mux.HandleFunc("/debug", serveDebugAsset("debugui/index.html", "text/html; charset=utf-8"))
	mux.HandleFunc("/debug/", serveDebugAsset("debugui/index.html", "text/html; charset=utf-8"))
	mux.HandleFunc("/debug/assets/app.js", serveDebugAsset("debugui/app.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("/debug/assets/app.css", serveDebugAsset("debugui/app.css", "text/css; charset=utf-8"))
	mux.HandleFunc("/api/debug/tools/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeDebugError(w, http.StatusMethodNotAllowed, NewToolError("invalid_argument", "debug.tool", "method must be POST", nil))
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/api/debug/tools/")
		if name == "" || strings.Contains(name, "/") {
			writeDebugError(w, http.StatusNotFound, NewToolError("invalid_argument", "debug.tool", "tool not found", nil))
			return
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			writeDebugError(w, http.StatusBadRequest, normalizeToolError("debug.tool."+name, "", err))
			return
		}
		invocation, err := catalog.invoke(r.Context(), name, raw)
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, errUnknownTool) {
				status = http.StatusNotFound
			}
			writeDebugError(w, status, normalizeToolError("debug.tool."+name, "", err))
			return
		}
		writeDebugJSON(w, http.StatusOK, invocation.Output)
	})
	mux.HandleFunc("/api/debug/current", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeDebugError(w, http.StatusMethodNotAllowed, NewToolError("invalid_argument", "debug.current", "method must be GET", nil))
			return
		}
		tabID := r.URL.Query().Get("tab_id")
		runtime, err := service.runtime(r.Context(), tabID)
		if err != nil {
			writeDebugError(w, http.StatusBadRequest, normalizeToolError("debug.current", "", err))
			return
		}
		document, ok := runtime.page.CurrentSnapshot()
		if !ok {
			writeDebugError(w, http.StatusConflict, NewToolError("stale_target", "debug.current", "tab has no current snapshot", nil))
			return
		}
		view, err := renderSnapshot(document, "", 0)
		if err != nil {
			writeDebugError(w, http.StatusInternalServerError, normalizeToolError("debug.current", "", err))
			return
		}
		status, _ := service.debugStatus(r.Context(), tabID)
		writeDebugJSON(w, http.StatusOK, map[string]any{"ok": true, "snapshot": view, "state": status})
	})
	mux.HandleFunc("/api/debug/screenshot", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeDebugError(w, http.StatusMethodNotAllowed, NewToolError("invalid_argument", "debug.screenshot", "method must be GET", nil))
			return
		}
		input := ScreenshotInput{TabID: r.URL.Query().Get("tab_id"), Ref: r.URL.Query().Get("ref"), Format: r.URL.Query().Get("format")}
		result, output, _ := service.screenshot(r.Context(), nil, input)
		if result == nil || result.IsError || !output.OK || len(result.Content) == 0 {
			writeDebugError(w, http.StatusBadRequest, output.Error)
			return
		}
		imageContent, ok := result.Content[0].(*mcp.ImageContent)
		if !ok {
			writeDebugError(w, http.StatusInternalServerError, NewToolError("browser_error", "debug.screenshot", "screenshot did not return image content", nil))
			return
		}
		w.Header().Set("Content-Type", imageContent.MIMEType)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Image-Width", strconv.Itoa(output.Width))
		w.Header().Set("X-Image-Height", strconv.Itoa(output.Height))
		_, _ = w.Write(imageContent.Data)
	})
	mux.HandleFunc("/api/debug/upload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeDebugError(w, http.StatusMethodNotAllowed, NewToolError("invalid_argument", "debug.upload", "method must be POST", nil))
			return
		}
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			writeDebugError(w, http.StatusBadRequest, normalizeToolError("debug.upload", "", err))
			return
		}
		defer func() { _ = r.MultipartForm.RemoveAll() }()
		root, err := os.MkdirTemp("", "cdp-debug-upload-")
		if err != nil {
			writeDebugError(w, http.StatusInternalServerError, normalizeToolError("debug.upload", "", err))
			return
		}
		defer func() { _ = os.RemoveAll(root) }()
		paths := []string{}
		for index, header := range r.MultipartForm.File["files"] {
			file, openErr := header.Open()
			if openErr != nil {
				writeDebugError(w, http.StatusBadRequest, normalizeToolError("debug.upload", "", openErr))
				return
			}
			directory := filepath.Join(root, strconv.Itoa(index))
			if err := os.MkdirAll(directory, 0o700); err != nil {
				_ = file.Close()
				writeDebugError(w, http.StatusInternalServerError, normalizeToolError("debug.upload", "", err))
				return
			}
			path := filepath.Join(directory, filepath.Base(header.Filename))
			destination, createErr := os.Create(path)
			if createErr == nil {
				_, createErr = io.Copy(destination, file)
			}
			createErr = errors.Join(createErr, file.Close())
			if destination != nil {
				createErr = errors.Join(createErr, destination.Close())
			}
			if createErr != nil {
				writeDebugError(w, http.StatusInternalServerError, normalizeToolError("debug.upload", "", createErr))
				return
			}
			paths = append(paths, path)
		}
		if len(paths) == 0 {
			writeDebugError(w, http.StatusBadRequest, NewToolError("invalid_argument", "debug.upload", "at least one file is required", nil))
			return
		}
		_, output, _ := service.upload(r.Context(), nil, UploadInput{TabID: r.FormValue("tab_id"), Ref: r.FormValue("ref"), Files: paths})
		writeDebugJSON(w, http.StatusOK, output)
	})
	mux.HandleFunc("/api/debug/stream", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeDebugError(w, http.StatusMethodNotAllowed, NewToolError("invalid_argument", "debug.stream", "method must be GET", nil))
			return
		}
		hub.stream(w, r)
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeDebugError(w, http.StatusMethodNotAllowed, NewToolError("invalid_argument", "health", "method must be GET", nil))
			return
		}
		tabs := []TabInfo{}
		browserAlive := false
		if service.browser != nil {
			browserAlive = service.browser.browser != nil && service.browser.browser.Alive()
			if listed, err := service.browser.ListTabs(r.Context(), service.activeID()); err == nil {
				tabs = listed
			}
		}
		writeDebugJSON(w, http.StatusOK, map[string]any{
			"ok": true, "browser_alive": browserAlive, "tabs": len(tabs), "viewers": hub.viewerCount(),
			"uptime_ms": time.Since(hub.started).Milliseconds(), "tools": catalog.names(),
		})
	})
}

func serveDebugAsset(name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := debugAssets.ReadFile(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(data)
	}
}

func writeDebugJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeDebugError(w http.ResponseWriter, status int, err *ToolError) {
	if err == nil {
		err = NewToolError("browser_error", "debug", "operation failed", nil)
	}
	writeDebugJSON(w, status, map[string]any{"ok": false, "error": err})
}
