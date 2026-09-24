package mcpapp

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

const (
	corsAllowMethods = "GET, POST, DELETE, OPTIONS"
	corsAllowHeaders = "Content-Type, Accept, Authorization, MCP-Protocol-Version, Mcp-Session-Id, Last-Event-ID"
)

var corsAllowedRequestHeaders = map[string]struct{}{
	"accept": {}, "authorization": {}, "content-type": {}, "last-event-id": {},
	"mcp-protocol-version": {}, "mcp-session-id": {},
}

// HTTPOptions configures the MCP Streamable HTTP endpoint. AllowedOrigins
// contains exact browser origins or a single "*" for explicitly insecure test use.
type HTTPOptions struct {
	AllowedOrigins []string
}

type corsPolicy struct {
	origins  map[string]struct{}
	wildcard bool
}

// NormalizeHTTPOptions validates origins, canonicalizes their scheme and host,
// removes duplicates, and rejects mixing the wildcard with exact origins.
func NormalizeHTTPOptions(options HTTPOptions) (HTTPOptions, error) {
	normalized := make([]string, 0, len(options.AllowedOrigins))
	seen := make(map[string]struct{}, len(options.AllowedOrigins))
	wildcard := false
	for _, raw := range options.AllowedOrigins {
		origin := strings.TrimSpace(raw)
		if origin == "*" {
			wildcard = true
		} else {
			parsed, err := url.Parse(origin)
			if err != nil {
				return HTTPOptions{}, fmt.Errorf("invalid CORS origin %q: %w", raw, err)
			}
			if origin == "" || strings.EqualFold(origin, "null") || parsed.Scheme == "" || parsed.Host == "" || parsed.Opaque != "" {
				return HTTPOptions{}, fmt.Errorf("invalid CORS origin %q: expected scheme://host[:port]", raw)
			}
			if parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.ForceQuery ||
				parsed.Fragment != "" || strings.Contains(origin, "#") || strings.HasSuffix(parsed.Host, ":") {
				return HTTPOptions{}, fmt.Errorf("invalid CORS origin %q: userinfo, path, query, fragment, and empty ports are not allowed", raw)
			}
			parsed.Scheme = strings.ToLower(parsed.Scheme)
			hostname := strings.ToLower(parsed.Hostname())
			port := parsed.Port()
			if (parsed.Scheme == "http" && port == "80") || (parsed.Scheme == "https" && port == "443") {
				port = ""
			}
			switch {
			case port != "":
				parsed.Host = net.JoinHostPort(hostname, port)
			case strings.Contains(hostname, ":"):
				parsed.Host = "[" + hostname + "]"
			default:
				parsed.Host = hostname
			}
			origin = parsed.String()
		}
		if _, ok := seen[origin]; ok {
			continue
		}
		seen[origin] = struct{}{}
		normalized = append(normalized, origin)
	}
	if wildcard && len(normalized) != 1 {
		return HTTPOptions{}, fmt.Errorf("CORS origin %q cannot be combined with exact origins", "*")
	}
	return HTTPOptions{AllowedOrigins: normalized}, nil
}

func newMCPHTTPHandler(handler http.Handler, options HTTPOptions) (http.Handler, error) {
	policy := corsPolicy{origins: make(map[string]struct{}, len(options.AllowedOrigins))}
	protection := http.NewCrossOriginProtection()
	for _, origin := range options.AllowedOrigins {
		if origin == "*" {
			policy.wildcard = true
			protection.AddInsecureBypassPattern("/mcp")
			continue
		}
		policy.origins[origin] = struct{}{}
		if err := protection.AddTrustedOrigin(origin); err != nil {
			return nil, fmt.Errorf("trust CORS origin %q: %w", origin, err)
		}
	}
	protected := protection.Handler(handler)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			protected.ServeHTTP(w, r)
			return
		}
		w.Header().Add("Vary", "Origin")
		allowed := policy.wildcard
		if !allowed {
			_, allowed = policy.origins[origin]
		}
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			w.Header().Add("Vary", "Access-Control-Request-Method")
			w.Header().Add("Vary", "Access-Control-Request-Headers")
			requestedMethod := strings.ToUpper(strings.TrimSpace(r.Header.Get("Access-Control-Request-Method")))
			methodAllowed := requestedMethod == http.MethodGet || requestedMethod == http.MethodPost ||
				requestedMethod == http.MethodDelete || requestedMethod == http.MethodOptions
			headersAllowed := true
			for header := range strings.SplitSeq(r.Header.Get("Access-Control-Request-Headers"), ",") {
				header = strings.ToLower(strings.TrimSpace(header))
				if header == "" {
					continue
				}
				if _, ok := corsAllowedRequestHeaders[header]; !ok {
					headersAllowed = false
					break
				}
			}
			if !allowed || !methodAllowed || !headersAllowed {
				http.Error(w, "CORS preflight rejected", http.StatusForbidden)
				return
			}
			if policy.wildcard {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			}
			w.Header().Set("Access-Control-Allow-Methods", corsAllowMethods)
			w.Header().Set("Access-Control-Allow-Headers", corsAllowHeaders)
			w.Header().Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if allowed {
			if policy.wildcard {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			}
			w.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id")
		}
		protected.ServeHTTP(w, r)
	}), nil
}

// Debug GET endpoints include an interactive WebSocket, so apply the same
// origin policy to safe methods as to POST before reaching any debug handler.
func newDebugHTTPHandler(handler http.Handler) http.Handler {
	protection := http.NewCrossOriginProtection()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		check := *r
		check.Method = http.MethodPost
		if err := protection.Check(&check); err != nil {
			http.Error(w, "cross-origin debug request rejected", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
		w.Header().Set("X-Frame-Options", "DENY")
		handler.ServeHTTP(w, r)
	})
}
