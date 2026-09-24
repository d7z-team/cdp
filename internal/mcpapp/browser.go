package mcpapp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"gopkg.d7z.net/cdp"
	"gopkg.d7z.net/cdp/snapshot"
)

type BrowserService struct {
	browser *cdp.Browser
	maxTabs int
	mu      sync.Mutex
}
type TabInfo struct {
	Diagnostics      cdp.DiagnosticsMode `json:"diagnostics"`
	ConsoleCollected bool                `json:"console_collected"`
	ID               string              `json:"id"`
	URL              string              `json:"url"`
	Title            string              `json:"title"`
	Active           bool                `json:"active"`
	CreatedAt        time.Time           `json:"created_at,omitempty"`
	LastNavAt        time.Time           `json:"last_nav_at,omitempty"`
}

func BorrowBrowser(browser *cdp.Browser, maxTabs int) *BrowserService {
	if maxTabs <= 0 {
		maxTabs = 10
	}
	return &BrowserService{browser: browser, maxTabs: maxTabs}
}
func (s *BrowserService) Page(ctx context.Context, id string) (*cdp.Page, error) {
	if id == "" {
		return nil, NewToolError("invalid_argument", "tab.page", "tab_id is required", nil)
	}
	p, err := s.browser.Page(ctx, id)
	if err != nil {
		return nil, err
	}
	_ = p.SetDialogPolicy(cdp.DialogPassthrough)
	return p, nil
}
func (s *BrowserService) NewPage(ctx context.Context) (*cdp.Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pages, err := s.browser.Pages(ctx)
	if err != nil {
		return nil, err
	}
	if len(pages) >= s.maxTabs {
		return nil, NewToolError("invalid_argument", "tab.new", fmt.Sprintf("tab limit reached (%d)", s.maxTabs), nil)
	}
	p, err := s.browser.NewPage(ctx)
	if err != nil {
		return nil, err
	}
	_ = p.SetDialogPolicy(cdp.DialogPassthrough)
	return p, nil
}
func (s *BrowserService) Navigate(ctx context.Context, tabID, url string) (*cdp.Page, snapshot.Document, []string, error) {
	if url == "" {
		return nil, snapshot.Document{}, nil, NewToolError("invalid_argument", "navigate", "url is required", nil)
	}
	var (
		page *cdp.Page
		err  error
	)
	if tabID == "" {
		page, err = s.NewPage(ctx)
	} else {
		page, err = s.Page(ctx, tabID)
	}
	if err != nil {
		return nil, snapshot.Document{}, nil, err
	}
	sub, err := page.Session().Subscribe(ctx, "Page.domContentEventFired", "Page.loadEventFired")
	if err != nil {
		return page, snapshot.Document{}, nil, err
	}
	defer sub.Close()
	events := sub.Events()
	if err := page.Navigate(ctx, url, cdp.NavigateOptions{WaitUntil: cdp.LoadCommit}); err != nil {
		return page, snapshot.Document{}, nil, fmt.Errorf("navigate: %w", err)
	}
	warnings, err := waitForDocument(ctx, page, events, 30*time.Second)
	if err != nil {
		return page, snapshot.Document{}, warnings, err
	}
	document, err := page.Snapshot(ctx)
	if err != nil {
		return page, snapshot.Document{}, warnings, fmt.Errorf("capture after navigate: %w", err)
	}
	return page, document, warnings, nil
}

func waitForDocument(ctx context.Context, page *cdp.Page, events <-chan cdp.Event, timeout time.Duration) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	domReady := false
	if handle, err := page.ExecutionContext(ctx, cdp.ExecutionContextOptions{World: cdp.WorldMain}); err == nil {
		if raw, evalErr := handle.EvalJSON(ctx, "return document.readyState"); evalErr == nil {
			domReady = string(raw) == `"interactive"` || string(raw) == `"complete"`
			if string(raw) == `"complete"` {
				return nil, nil
			}
		}
	}
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return nil, cdp.ErrClosed
			}
			switch event.Method {
			case "Page.domContentEventFired":
				domReady = true
			case "Page.loadEventFired":
				return nil, nil
			}
		case <-page.Done():
			return nil, cdp.ErrClosed
		case <-ctx.Done():
			if parentErr := context.Cause(ctx); parentErr != context.DeadlineExceeded {
				return nil, parentErr
			}
			if domReady {
				return []string{"load event did not complete before navigation timeout"}, nil
			}
			return nil, NewToolError("timeout", "navigate.wait", "DOMContentLoaded did not complete before navigation timeout", ctx.Err())
		}
	}
}

func (s *BrowserService) ListTabs(ctx context.Context, activeID string) ([]TabInfo, error) {
	pages, err := s.browser.Pages(ctx)
	if err != nil {
		return nil, err
	}
	tabs := make([]TabInfo, 0, len(pages))
	for _, p := range pages {
		info, err := p.Info(ctx)
		if errors.Is(err, cdp.ErrClosed) {
			continue
		}
		if err != nil {
			return nil, err
		}
		tabs = append(tabs, TabInfo{Diagnostics: s.browser.Diagnostics(), ConsoleCollected: s.browser.Diagnostics() == cdp.DiagnosticsRuntime, ID: p.ID(), URL: info.URL, Title: info.Title, Active: p.ID() == activeID, CreatedAt: info.CreatedAt, LastNavAt: info.LastNavigatedAt})
	}
	sort.Slice(tabs, func(i, j int) bool { return tabs[i].CreatedAt.Before(tabs[j].CreatedAt) })
	return tabs, nil
}
func (s *BrowserService) CloseTab(ctx context.Context, id string) error {
	p, err := s.Page(ctx, id)
	if err != nil {
		return err
	}
	return p.Close(ctx)
}
