package runtime

import (
	"context"
	"fmt"
	"strings"

	"gopkg.d7z.net/cdp"
)

// BrowserSession combines a leased page with browser and fixture resources.
type BrowserSession struct {
	Lease   *PageLease
	Browser *BrowserRuntime
	Fixture *FixtureRuntime
}

// Page returns the high-level API page held by the session lease.
func (s *BrowserSession) Page() *cdp.Page {
	if s == nil || s.Lease == nil || s.Lease.Worker == nil {
		return nil
	}
	return s.Lease.Worker.Page
}

// Release returns the leased page to the worker pool.
func (s *BrowserSession) Release() {
	if s == nil {
		return
	}
	s.Lease.Release()
}

// FixtureURL resolves path against the session fixture server.
func (s *BrowserSession) FixtureURL(path string) string {
	if s == nil || s.Fixture == nil || s.Fixture.Server == nil {
		return ""
	}
	return s.Fixture.Server.URL + normalizeFixturePath(path)
}

// Open navigates the leased page to a fixture path.
func (s *BrowserSession) Open(path string) (*cdp.Page, error) {
	page := s.Page()
	if page == nil {
		return nil, fmt.Errorf("browser session page is nil")
	}
	return page, page.Navigate(context.Background(), s.FixtureURL(path), cdp.NavigateOptions{})
}

func normalizeFixturePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || path == "/" {
		return "/"
	}
	if strings.HasPrefix(path, "/") {
		return path
	}
	return "/" + path
}
