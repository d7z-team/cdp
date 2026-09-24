package cdp

import (
	"context"
	"errors"
	"fmt"
)

type LoadState string

const (
	LoadComplete         LoadState = "complete"
	LoadDOMContentLoaded LoadState = "domcontentloaded"
	LoadCommit           LoadState = "commit"
	LoadNetworkIdle      LoadState = "networkidle"
)

type NavigateOptions struct{ WaitUntil LoadState }

func (p *Page) Navigate(ctx context.Context, url string, opts NavigateOptions) error {
	c, cancel, err := p.operation(ctx, p.timeouts().Navigation)
	if err != nil {
		return err
	}
	defer cancel()
	var result struct {
		ErrorText string `json:"errorText"`
	}
	if err = p.Session().Call(c, "Page.navigate", map[string]any{"url": url}, &result); err != nil {
		return err
	}
	if result.ErrorText != "" {
		return operationError("navigate", errors.New(result.ErrorText))
	}
	if err = p.WaitForLoadState(c, opts.WaitUntil); err != nil {
		return err
	}
	return operationError("navigate.initialize", p.engine.EnsureDocumentReady(c))
}
func (p *Page) WaitForLoadState(ctx context.Context, state LoadState) error {
	if state == "" {
		state = LoadComplete
	}
	if state == LoadCommit {
		c, cancel, err := p.operation(ctx, p.timeouts().Navigation)
		if err != nil {
			return err
		}
		defer cancel()
		return c.Err()
	}
	if state != LoadComplete && state != LoadDOMContentLoaded && state != LoadNetworkIdle {
		return fmt.Errorf("invalid load state: %s", state)
	}
	c, cancel, err := p.operation(ctx, p.timeouts().Navigation)
	if err != nil {
		return err
	}
	defer cancel()
	if state == LoadNetworkIdle {
		return p.WaitForNetworkIdle(c)
	}
	return operationError("wait_load", poll(c, func() (bool, error) {
		var s string
		err := p.Eval(c, "return document.readyState", &s)
		// Navigation can invalidate the handle between resolution and evaluation.
		// This read-only readiness probe may poll again; user Eval is never replayed.
		if errors.Is(err, ErrStaleElement) {
			return false, nil
		}
		return s == "complete" || (state == LoadDOMContentLoaded && s == "interactive"), err
	}))
}
func (p *Page) Reload(ctx context.Context) error {
	if err := p.Session().Call(ctx, "Page.reload", nil, nil); err != nil {
		return err
	}
	if err := p.WaitForLoadState(ctx, LoadComplete); err != nil {
		return err
	}
	return operationError("navigation.initialize", p.engine.EnsureDocumentReady(ctx))
}
func (p *Page) history(ctx context.Context, offset int) error {
	var h struct {
		Current int `json:"currentIndex"`
		Entries []struct {
			ID int `json:"id"`
		} `json:"entries"`
	}
	if err := p.Session().Call(ctx, "Page.getNavigationHistory", nil, &h); err != nil {
		return err
	}
	i := h.Current + offset
	if i < 0 || i >= len(h.Entries) {
		return ErrNotFound
	}
	if err := p.Session().Call(ctx, "Page.navigateToHistoryEntry", map[string]any{"entryId": h.Entries[i].ID}, nil); err != nil {
		return err
	}
	if err := p.WaitForLoadState(ctx, LoadComplete); err != nil {
		return err
	}
	return operationError("navigation.initialize", p.engine.EnsureDocumentReady(ctx))
}
func (p *Page) Back(ctx context.Context) error    { return p.history(ctx, -1) }
func (p *Page) Forward(ctx context.Context) error { return p.history(ctx, 1) }
func (p *Page) WaitForURL(ctx context.Context, url string) error {
	return p.WaitForURLMatch(ctx, func(value string) bool { return value == url })
}

// WaitForURLMatch waits until match accepts the current page URL.
func (p *Page) WaitForURLMatch(ctx context.Context, match func(string) bool) error {
	if match == nil {
		return fmt.Errorf("URL matcher is nil")
	}
	c, cancel, err := p.operation(ctx, p.timeouts().Navigation)
	if err != nil {
		return err
	}
	defer cancel()
	return poll(c, func() (bool, error) { v, err := p.URL(c); return err == nil && match(v), err })
}
