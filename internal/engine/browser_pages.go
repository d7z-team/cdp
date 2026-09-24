package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (r *BrowserManager) ListPages(ctx context.Context) (map[string]*ListTarget, error) {
	targetsInfo := make([]ListTarget, 0)
	if err := r.HTTPGet(ctx, "/json/list", &targetsInfo); err != nil {
		return nil, err
	}
	result := make(map[string]*ListTarget)
	for _, info := range targetsInfo {
		if info.Type == "page" {
			result[info.ID] = &info
		}
	}
	return result, nil
}

// LoadPageContext waits for the target's current bind cycle. It returns the
// original websocket/domain/init error when binding reaches a terminal failure.
func (r *BrowserManager) LoadPageContext(ctx context.Context, id string) (*Page, error) {
	if r == nil || r.ctx == nil || r.sessions == nil {
		return nil, ErrBrowserClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("page id is empty")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// A later explicit operation may recover a failed bind without waiting for
	// another target event. Failures observed by this request are returned as-is.
	r.requestPageBind(id, true)

	observed := false
	for {
		if page, ok := r.GetPage(id); ok && page != nil {
			if err := page.waitInitContext(ctx); err != nil {
				return nil, fmt.Errorf("initialize page %s: %w", id, err)
			}
			return page, nil
		}

		r.pageBindMu.Lock()
		state, exists := r.pageBinds[id]
		if exists {
			observed = true
			if state.page != nil && !state.inFlight && state.lastErr == nil {
				page := state.page
				r.pageBindMu.Unlock()
				if err := page.waitInitContext(ctx); err != nil {
					return nil, fmt.Errorf("initialize page %s: %w", id, err)
				}
				return page, nil
			}
			if !state.inFlight && state.lastErr != nil {
				bindErr := state.lastErr
				r.pageBindMu.Unlock()
				return nil, fmt.Errorf("initialize page %s: %w", id, bindErr)
			}
		} else if observed {
			r.pageBindMu.Unlock()
			return nil, fmt.Errorf("%w: %s", errPageUnavailable, id)
		}
		changed := r.pageBindChangedLocked()
		r.pageBindMu.Unlock()

		select {
		case <-changed:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-r.Done():
			return nil, ErrBrowserClosed
		}
	}
}

func (r *BrowserManager) GetPage(id string) (*Page, bool) {
	return r.sessions.Load(id)
}

func (r *BrowserManager) managedPage(id string) (*Page, bool) {
	if r == nil || strings.TrimSpace(id) == "" {
		return nil, false
	}
	if page, ok := r.GetPage(id); ok && page != nil {
		return page, true
	}
	r.pageBindMu.Lock()
	defer r.pageBindMu.Unlock()
	state := r.pageBinds[id]
	if state == nil || state.page == nil {
		return nil, false
	}
	return state.page, true
}

func (r *BrowserManager) isCurrentManagedPage(page *Page) bool {
	if r == nil || page == nil || page.ctx == nil || page.ctx.Err() != nil {
		return false
	}
	current, ok := r.managedPage(page.ID)
	return ok && current == page
}

func (r *BrowserManager) updateManagedPage(id string, update func(*Page)) (*Page, bool) {
	if r == nil || strings.TrimSpace(id) == "" {
		return nil, false
	}
	r.pageBindMu.Lock()
	defer r.pageBindMu.Unlock()
	page, ok := r.GetPage(id)
	if !ok || page == nil {
		state := r.pageBinds[id]
		if state == nil {
			return nil, false
		}
		page = state.page
		if page == nil {
			page = state.runtimeCarrier
		}
		if page == nil {
			return nil, false
		}
	}
	if update != nil {
		update(page)
	}
	return page, true
}

func (r *BrowserManager) hasManagedPageTarget(id string) bool {
	if r == nil || strings.TrimSpace(id) == "" {
		return false
	}
	if r.sessions != nil {
		if _, ok := r.GetPage(id); ok {
			return true
		}
	}
	r.pageBindMu.Lock()
	defer r.pageBindMu.Unlock()
	state, ok := r.pageBinds[id]
	return ok && state != nil
}

func (r *BrowserManager) managedPagesSnapshot() []*Page {
	if r == nil {
		return nil
	}
	seen := make(map[*Page]struct{})
	for _, page := range r.BoundPages() {
		seen[page] = struct{}{}
	}
	r.pageBindMu.Lock()
	for _, state := range r.pageBinds {
		if state != nil && state.page != nil {
			seen[state.page] = struct{}{}
		}
	}
	r.pageBindMu.Unlock()
	pages := make([]*Page, 0, len(seen))
	for page := range seen {
		pages = append(pages, page)
	}
	return pages
}

// BoundPages returns the currently published top-level pages.
func (r *BrowserManager) BoundPages() []*Page {
	if r == nil || r.sessions == nil {
		return nil
	}
	pages := make([]*Page, 0)
	r.sessions.Range(func(_ string, page *Page) bool {
		if page != nil {
			pages = append(pages, page)
		}
		return true
	})
	return pages
}

func (r *BrowserManager) NewPage(url string) (*Page, error) { return r.CreatePage(r.ctx, url) }

func (r *BrowserManager) CreatePage(ctx context.Context, url string) (*Page, error) {
	var target ListTarget
	timeoutCtx, cancel := context.WithTimeout(ctx, pageLoadTimeout)
	defer cancel()
	err := r.HTTPPut(timeoutCtx, "/json/new", nil, &target)
	if err != nil {
		return nil, err
	}
	page, err := r.LoadPageContext(timeoutCtx, target.ID)
	if err != nil {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		closeErr := r.HTTPGet(cleanup, "/json/close/"+target.ID, nil)
		return nil, errors.Join(fmt.Errorf("initialize new page %s: %w", target.ID, err), closeErr)
	}
	if url != "" && url != "about:blank" {
		if err := page.Navigate(url); err != nil {
			return nil, fmt.Errorf("导航到 %s 失败: %w", url, err)
		}
	}
	return page, nil
}
