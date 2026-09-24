package cdp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
)

type RoutePattern struct{ URL, ResourceType string }
type RouteRequest struct {
	URL, Method, PostData, ResourceType string
	Headers                             http.Header
}
type RouteContinueOptions struct {
	URL, Method string
	Headers     http.Header
	PostData    []byte
}
type RouteFulfillOptions struct {
	Status      int
	Headers     http.Header
	Body        []byte
	ContentType string
}
type Route struct {
	Request   RouteRequest
	page      *Page
	id        string
	mu        sync.Mutex
	completed bool
}

func (r *Route) complete(ctx context.Context, method string, params map[string]any) error {
	if r == nil || r.page == nil {
		return ErrClosed
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.completed {
		return errors.New("route already completed")
	}
	params["requestId"] = r.id
	err := r.page.Session().Call(ctx, method, params, nil)
	if err == nil {
		r.completed = true
	}
	return err
}
func headerEntries(headers http.Header) []map[string]string {
	out := []map[string]string{}
	for name, values := range headers {
		for _, value := range values {
			out = append(out, map[string]string{"name": name, "value": value})
		}
	}
	return out
}
func (r *Route) Continue(ctx context.Context, opts RouteContinueOptions) error {
	p := map[string]any{}
	if opts.URL != "" {
		p["url"] = opts.URL
	}
	if opts.Method != "" {
		p["method"] = opts.Method
	}
	if opts.Headers != nil {
		p["headers"] = headerEntries(opts.Headers)
	}
	if opts.PostData != nil {
		p["postData"] = base64.StdEncoding.EncodeToString(opts.PostData)
	}
	return r.complete(ctx, "Fetch.continueRequest", p)
}
func (r *Route) Fulfill(ctx context.Context, opts RouteFulfillOptions) error {
	if opts.Status == 0 {
		opts.Status = http.StatusOK
	}
	headers := opts.Headers.Clone()
	if headers == nil {
		headers = http.Header{}
	}
	if opts.ContentType != "" {
		headers.Set("Content-Type", opts.ContentType)
	}
	return r.complete(ctx, "Fetch.fulfillRequest", map[string]any{"responseCode": opts.Status, "responseHeaders": headerEntries(headers), "body": base64.StdEncoding.EncodeToString(opts.Body)})
}
func (r *Route) Abort(ctx context.Context, reason string) error {
	if reason == "" {
		reason = "BlockedByClient"
	}
	return r.complete(ctx, "Fetch.failRequest", map[string]any{"errorReason": reason})
}

type RouteRegistration struct {
	page    *Page
	pattern RoutePattern
	handler func(context.Context, *Route) error
	once    sync.Once
	err     error
	ctx     context.Context
	cancel  context.CancelFunc
}

func (p *Page) Route(ctx context.Context, pattern RoutePattern, handler func(context.Context, *Route) error) (*RouteRegistration, error) {
	_, stop, err := p.operation(ctx, p.timeouts().Read)
	if err != nil {
		return nil, err
	}
	stop()

	if ctx == nil || handler == nil {
		return nil, errors.New("route requires context and handler")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.routeMu.Lock()
	defer p.routeMu.Unlock()
	life, cancel := context.WithCancel(ctx)
	r := &RouteRegistration{page: p, pattern: pattern, handler: handler, ctx: life, cancel: cancel}
	if p.routeSubscription == nil {
		sub, err := p.Session().Subscribe(context.Background(), "Fetch.requestPaused")
		if err != nil {
			cancel()
			return nil, err
		}
		p.routeSubscription = sub
		go p.processRoutes(sub)
	}
	p.routes = append(p.routes, r)
	if err := p.updateRoutes(ctx); err != nil {
		p.routes = p.routes[:len(p.routes)-1]
		cancel()
		return nil, err
	}
	go func() {
		select {
		case <-life.Done():
		case <-p.Done():
		}
		_ = r.Close()
	}()
	return r, nil
}
func (p *Page) updateRoutes(ctx context.Context) error {
	if len(p.routes) == 0 {
		err := p.Session().Call(ctx, "Fetch.disable", nil, nil)
		if p.routeSubscription != nil {
			_ = p.routeSubscription.Close()
			p.routeSubscription = nil
		}
		return err
	}
	return p.Session().Call(ctx, "Fetch.enable", map[string]any{"patterns": []map[string]string{{"urlPattern": "*", "requestStage": "Request"}}}, nil)
}
func (r *RouteRegistration) Close() error {
	if r == nil || r.page == nil {
		return nil
	}
	r.once.Do(func() {
		r.cancel()
		p := r.page
		p.routeMu.Lock()
		defer p.routeMu.Unlock()
		for i, v := range p.routes {
			if v == r {
				p.routes = append(p.routes[:i], p.routes[i+1:]...)
				break
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), p.timeouts().Shutdown)
		defer cancel()
		r.err = p.updateRoutes(ctx)
	})
	return r.err
}
func routeMatches(pattern RoutePattern, rawURL, resource string) bool {
	if pattern.ResourceType != "" && pattern.ResourceType != resource {
		return false
	}
	s := pattern.URL
	if s == "" || s == "*" || s == "**/*" {
		return true
	}
	if !strings.ContainsAny(s, "*?") {
		return strings.HasSuffix(rawURL, s)
	}
	value := rawURL
	if strings.HasPrefix(s, "/") {
		if parsed, err := url.Parse(rawURL); err == nil {
			value = parsed.RequestURI()
		}
	}
	re := regexp.QuoteMeta(s)
	re = strings.ReplaceAll(re, `\*\*`, ".*")
	re = strings.ReplaceAll(re, `\*`, "[^/]*")
	re = strings.ReplaceAll(re, `\?`, ".")
	ok, _ := regexp.MatchString("^"+re+"$", value)
	return ok
}
func (p *Page) processRoutes(sub *Subscription) {
	defer sub.Close()
	for event := range sub.Events() {
		var payload struct {
			RequestID    string
			ResourceType string
			Request      struct {
				URL, Method, PostData string
				Headers               map[string]string
			}
		}
		data, _ := json.Marshal(event.Params)
		if json.Unmarshal(data, &payload) != nil {
			continue
		}
		request := RouteRequest{URL: payload.Request.URL, Method: payload.Request.Method, PostData: payload.Request.PostData, ResourceType: payload.ResourceType, Headers: make(http.Header)}
		for k, v := range payload.Request.Headers {
			request.Headers.Set(k, v)
		}
		r := &Route{page: p, id: payload.RequestID, Request: request}
		p.routeMu.Lock()
		var handler *RouteRegistration
		for i := len(p.routes) - 1; i >= 0; i-- {
			if routeMatches(p.routes[i].pattern, request.URL, request.ResourceType) {
				handler = p.routes[i]
				break
			}
		}
		p.routeMu.Unlock()
		go func() {
			parent := context.Background()
			if handler != nil {
				parent = handler.ctx
			}
			ctx, cancel := context.WithTimeout(parent, p.timeouts().Action)
			defer cancel()
			if handler == nil {
				_ = r.Continue(ctx, RouteContinueOptions{})
				return
			}
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				defer func() {
					if recover() != nil {
						cancel()
					}
				}()
				_ = handler.handler(ctx, r)
			}()
			select {
			case <-finished:
			case <-ctx.Done():
			}
			r.mu.Lock()
			completed := r.completed
			r.mu.Unlock()
			if !completed {
				cleanup, stop := context.WithTimeout(context.Background(), p.timeouts().Shutdown)
				defer stop()
				_ = r.Abort(cleanup, "Failed")
			}
		}()
	}
}
