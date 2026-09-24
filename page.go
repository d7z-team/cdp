package cdp

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	engine "gopkg.d7z.net/cdp/internal/engine"
)

type Page struct {
	routeMu           sync.Mutex
	routes            []*RouteRegistration
	routeSubscription *Subscription
	engine            *engine.Page
	browser           *Browser
}

func (p *Page) ID() string {
	if p == nil || p.engine == nil {
		return ""
	}
	return p.engine.ID
}
func (p *Page) Done() <-chan struct{} {
	if p == nil || p.engine == nil {
		c := make(chan struct{})
		close(c)
		return c
	}
	return p.engine.Done()
}
func (p *Page) operation(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc, error) {
	if p == nil || p.engine == nil || p.browser == nil {
		return nil, nil, ErrClosed
	}
	select {
	case <-p.engine.Done():
		return nil, nil, ErrClosed
	default:
	}
	return p.browser.operation(ctx, timeout)
}
func (p *Page) Close(ctx context.Context) error {
	return p.Session().Call(ctx, "Target.closeTarget", map[string]any{"targetId": p.ID()}, nil)
}
func (p *Page) Eval(ctx context.Context, script string, result any) error {
	raw, err := p.EvalJSON(ctx, script)
	if err != nil {
		return err
	}
	if result == nil {
		return nil
	}
	return operationError("eval.decode", json.Unmarshal(raw, result))
}
func (p *Page) EvalJSON(ctx context.Context, script string) (json.RawMessage, error) {
	c, cancel, err := p.operation(ctx, p.timeouts().Read)
	if err != nil {
		return nil, err
	}
	defer cancel()
	h, err := p.engine.ContextWithContext(c, engine.RuntimeContextOptions{Namespace: engine.NamespacePageMain})
	if err != nil {
		return nil, operationError("eval", err)
	}
	raw, err := h.EvalJSONContext(c, script)
	if err != nil {
		return nil, operationError("eval", err)
	}
	if raw == "" || raw == "undefined" {
		raw = "null"
	}
	return json.RawMessage(raw), nil
}
func (p *Page) URL(ctx context.Context) (string, error) {
	var v string
	err := p.Eval(ctx, "return location.href", &v)
	return v, err
}
func (p *Page) Title(ctx context.Context) (string, error) {
	var v string
	err := p.Eval(ctx, "return document.title", &v)
	return v, err
}
func (p *Page) Content(ctx context.Context) (string, error) {
	var v string
	err := p.Eval(ctx, "return document.documentElement.outerHTML", &v)
	return v, err
}
func (p *Page) SetContent(ctx context.Context, html string) error {
	raw, _ := json.Marshal(html)
	return p.Eval(ctx, "document.open(); document.write("+string(raw)+"); document.close();", nil)
}
func (p *Page) Activate(ctx context.Context) error {
	c, cancel, err := p.operation(ctx, p.timeouts().Action)
	if err != nil {
		return err
	}
	defer cancel()
	return operationError("activate", p.engine.ActivateContext(c))
}
func poll(ctx context.Context, check func() (bool, error)) error {
	timer := time.NewTicker(100 * time.Millisecond)
	defer timer.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		ok, err := check()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
}

type PageInfo struct {
	ID, URL, Title             string
	CreatedAt, LastNavigatedAt time.Time
}

func (p *Page) Info(ctx context.Context) (PageInfo, error) {
	var raw struct {
		TargetInfo struct {
			TargetID   string `json:"targetId"`
			URL, Title string
		} `json:"targetInfo"`
	}
	err := p.Session().Call(ctx, "Target.getTargetInfo", map[string]any{"targetId": p.ID()}, &raw)
	created, navigated := p.engine.Timestamps()
	return PageInfo{ID: p.ID(), URL: raw.TargetInfo.URL, Title: raw.TargetInfo.Title, CreatedAt: created, LastNavigatedAt: navigated}, err
}

func (p *Page) timeouts() Timeouts {
	if p != nil && p.browser != nil {
		return p.browser.timeouts
	}
	t, _ := (Timeouts{}).normalized()
	return t
}

// ActionMode returns the immutable interaction policy of this page's browser.
func (p *Page) ActionMode() ActionMode {
	if p == nil || p.engine == nil {
		return ""
	}
	return ActionMode(p.engine.ActionMode())
}
