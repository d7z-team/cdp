package mcpapp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const mcpInitializeRequest = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"cors-test","version":"1"}}}`

func TestNormalizeHTTPOptions(t *testing.T) {
	tests := []struct {
		name    string
		origins []string
		want    []string
		wantErr bool
	}{
		{name: "empty"},
		{name: "exact and duplicate", origins: []string{"HTTPS://EXAMPLE.COM:443", "https://example.com"}, want: []string{"https://example.com"}},
		{name: "multiple", origins: []string{"http://localhost:5173", "https://example.com:8443"}, want: []string{"http://localhost:5173", "https://example.com:8443"}},
		{name: "ipv6", origins: []string{"HTTP://[::1]:80"}, want: []string{"http://[::1]"}},
		{name: "wildcard", origins: []string{"*", "*"}, want: []string{"*"}},
		{name: "wildcard mixed", origins: []string{"*", "https://example.com"}, wantErr: true},
		{name: "null", origins: []string{"null"}, wantErr: true},
		{name: "empty value", origins: []string{" "}, wantErr: true},
		{name: "missing scheme", origins: []string{"example.com"}, wantErr: true},
		{name: "missing host", origins: []string{"https://"}, wantErr: true},
		{name: "userinfo", origins: []string{"https://user@example.com"}, wantErr: true},
		{name: "path", origins: []string{"https://example.com/"}, wantErr: true},
		{name: "query", origins: []string{"https://example.com?x=1"}, wantErr: true},
		{name: "empty query", origins: []string{"https://example.com?"}, wantErr: true},
		{name: "fragment", origins: []string{"https://example.com#x"}, wantErr: true},
		{name: "empty fragment", origins: []string{"https://example.com#"}, wantErr: true},
		{name: "empty port", origins: []string{"https://example.com:"}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizeHTTPOptions(HTTPOptions{AllowedOrigins: test.origins})
			if test.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(got.AllowedOrigins, "|") != strings.Join(test.want, "|") {
				t.Fatalf("origins=%v want=%v", got.AllowedOrigins, test.want)
			}
		})
	}
}

func TestMCPHTTPPreflight(t *testing.T) {
	handler, err := newTestApp(t, HTTPOptions{AllowedOrigins: []string{"https://client.example"}})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		origin  string
		method  string
		headers string
		status  int
	}{
		{name: "allowed", origin: "https://client.example", method: http.MethodPost, headers: "content-type, MCP-Protocol-Version, MCP-Session-Id, Last-Event-ID, Authorization, Accept", status: http.StatusNoContent},
		{name: "other origin", origin: "https://other.example", method: http.MethodPost, headers: "content-type", status: http.StatusForbidden},
		{name: "other port", origin: "https://client.example:8443", method: http.MethodPost, headers: "content-type", status: http.StatusForbidden},
		{name: "unsupported method", origin: "https://client.example", method: http.MethodPut, headers: "content-type", status: http.StatusForbidden},
		{name: "unsupported header", origin: "https://client.example", method: http.MethodPost, headers: "x-test", status: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodOptions, "http://mcp.test/mcp", nil)
			request.Header.Set("Origin", test.origin)
			request.Header.Set("Access-Control-Request-Method", test.method)
			request.Header.Set("Access-Control-Request-Headers", test.headers)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			vary := strings.Join(response.Header().Values("Vary"), ",")
			for _, value := range []string{"Origin", "Access-Control-Request-Method", "Access-Control-Request-Headers"} {
				if !strings.Contains(vary, value) {
					t.Fatalf("Vary=%q does not contain %q", vary, value)
				}
			}
			if test.status != http.StatusNoContent {
				if value := response.Header().Get("Access-Control-Allow-Origin"); value != "" {
					t.Fatalf("rejected origin was allowed: %q", value)
				}
				return
			}
			if response.Header().Get("Access-Control-Allow-Origin") != test.origin ||
				response.Header().Get("Access-Control-Allow-Methods") != corsAllowMethods ||
				response.Header().Get("Access-Control-Allow-Headers") != corsAllowHeaders ||
				response.Header().Get("Access-Control-Max-Age") != "600" {
				t.Fatalf("unexpected CORS headers: %v", response.Header())
			}
		})
	}
}

func TestMCPHTTPActualRequestOriginProtection(t *testing.T) {
	handler, err := newTestApp(t, HTTPOptions{AllowedOrigins: []string{"https://client.example"}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://mcp.test/mcp", strings.NewReader(mcpInitializeRequest))
	request.Header.Set("Origin", "https://client.example")
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("initialize status=%d body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("Access-Control-Allow-Origin") != "https://client.example" ||
		response.Header().Get("Access-Control-Expose-Headers") != "Mcp-Session-Id" ||
		response.Header().Get("Mcp-Session-Id") == "" {
		t.Fatalf("unexpected initialize headers: %v", response.Header())
	}

	request = httptest.NewRequest(http.MethodPost, "http://mcp.test/mcp", strings.NewReader(mcpInitializeRequest))
	request.Header.Set("Origin", "https://other.example")
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || response.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("rejected status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		request = httptest.NewRequest(method, "http://mcp.test/mcp", nil)
		request.Header.Set("Origin", "https://client.example")
		request.Header.Set("Sec-Fetch-Site", "cross-site")
		request.Header.Set("Accept", "application/json, text/event-stream")
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Header().Get("Access-Control-Allow-Origin") != "https://client.example" ||
			response.Header().Get("Access-Control-Expose-Headers") != "Mcp-Session-Id" {
			t.Fatalf("%s response missing CORS headers: status=%d headers=%v", method, response.Code, response.Header())
		}
	}

	defaultHandler, err := newTestApp(t, HTTPOptions{})
	if err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodPost, "http://mcp.test/mcp", strings.NewReader(mcpInitializeRequest))
	request.Header.Set("Origin", "http://mcp.test")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	response = httptest.NewRecorder()
	defaultHandler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("same-origin status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
}

func TestMCPHTTPWildcardAndRouteScope(t *testing.T) {
	handler, err := newTestApp(t, HTTPOptions{AllowedOrigins: []string{"*"}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodOptions, "http://mcp.test/mcp", nil)
	request.Header.Set("Origin", "https://anything.example")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	request.Header.Set("Access-Control-Request-Headers", "content-type")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("wildcard status=%d headers=%v", response.Code, response.Header())
	}
	if response.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Fatalf("wildcard enabled credentials: %v", response.Header())
	}
	request = httptest.NewRequest(http.MethodPost, "http://mcp.test/mcp", strings.NewReader(mcpInitializeRequest))
	request.Header.Set("Origin", "https://anything.example")
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("wildcard request status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	if response.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Fatalf("wildcard request enabled credentials: %v", response.Header())
	}

	request = httptest.NewRequest(http.MethodOptions, "http://mcp.test/mcp", nil)
	request.Header.Set("Accept", "application/json, text/event-stream")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("non-preflight OPTIONS status=%d body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("Allow") != "GET, POST, DELETE" {
		t.Fatalf("non-preflight OPTIONS Allow=%q", response.Header().Get("Allow"))
	}

	for _, route := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/debug"},
		{method: http.MethodPost, path: "/api/debug/tools/not_a_tool"},
		{method: http.MethodGet, path: "/health"},
	} {
		request = httptest.NewRequest(route.method, "http://mcp.test"+route.path, strings.NewReader(`{}`))
		request.Header.Set("Origin", "https://anything.example")
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("route %s unexpectedly enabled CORS: status=%d headers=%v", route.path, response.Code, response.Header())
		}
	}
}

func TestDebugToolCrossOriginProtection(t *testing.T) {
	for _, origins := range [][]string{nil, {"https://client.example"}, {"*"}} {
		app, err := newTestApp(t, HTTPOptions{AllowedOrigins: origins})
		if err != nil {
			t.Fatal(err)
		}
		for _, contentType := range []string{"text/plain", "application/json", "application/x-www-form-urlencoded"} {
			req := httptest.NewRequest(http.MethodPost, "http://mcp.test/api/debug/tools/probe", strings.NewReader(`{}`))
			req.Header.Set("Origin", "https://client.example")
			req.Header.Set("Sec-Fetch-Site", "cross-site")
			req.Header.Set("Content-Type", contentType)
			res := httptest.NewRecorder()
			app.ServeHTTP(res, req)
			if res.Code != http.StatusForbidden {
				t.Fatalf("origins=%v content-type=%s: status=%d body=%s", origins, contentType, res.Code, res.Body.String())
			}
		}
		req := httptest.NewRequest(http.MethodPost, "http://mcp.test/api/debug/tools/probe", strings.NewReader(`{}`))
		req.Header.Set("Origin", "http://mcp.test")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		res := httptest.NewRecorder()
		app.ServeHTTP(res, req)
		if res.Code != http.StatusNotFound || !strings.Contains(res.Body.String(), "unknown tool") {
			t.Fatalf("same-origin debug request: status=%d body=%s", res.Code, res.Body.String())
		}
	}
}

func TestDebugReadAndWebSocketOriginProtection(t *testing.T) {
	app, err := newTestApp(t, HTTPOptions{AllowedOrigins: []string{"*"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/debug", "/api/debug/current", "/api/debug/screenshot", "/api/debug/stream"} {
		for _, fetchSite := range []string{"cross-site", ""} {
			req := httptest.NewRequest(http.MethodGet, "http://mcp.test"+path, nil)
			req.Header.Set("Origin", "https://untrusted.example")
			req.Header.Set("Sec-Fetch-Site", fetchSite)
			req.Header.Set("Connection", "Upgrade")
			req.Header.Set("Upgrade", "websocket")
			res := httptest.NewRecorder()
			app.ServeHTTP(res, req)
			if res.Code != http.StatusForbidden {
				t.Fatalf("%s fetch-site=%q: %d", path, fetchSite, res.Code)
			}
		}
	}
	req := httptest.NewRequest(http.MethodGet, "http://mcp.test/debug", nil)
	req.Header.Set("Sec-Fetch-Site", "none")
	res := httptest.NewRecorder()
	app.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Header().Get("X-Frame-Options") != "DENY" || res.Header().Get("Content-Security-Policy") != "frame-ancestors 'none'" {
		t.Fatalf("local debug UI: %d %v", res.Code, res.Header())
	}
}
