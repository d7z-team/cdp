package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"gopkg.d7z.net/cdp/internal/syncutil"
)

func newActivePageTestManager() *BrowserManager {
	return &BrowserManager{
		sessions:        syncutil.NewSyncMap[string, *Page](),
		ctx:             context.Background(),
		firstPageNotify: make(chan struct{}),
		pageLifecycle:   map[string]*pageLifecycleState{},
	}
}

func addActivePageTestPage(manager *BrowserManager, id string, pageURL string) {
	page := &Page{
		ID:       id,
		ctx:      context.Background(),
		initDone: make(chan struct{}),
	}
	close(page.initDone)
	page.CdpConn = &CdpConn{}
	manager.sessions.Store(id, page)
	manager.withPageLifecycleState(id, func(state *pageLifecycleState) {
		state.url = pageURL
		state.targetInfo = TargetInfoSnapshot{TargetID: id, Type: "page", URL: pageURL}
	})
}

func TestBrowserManagerGetLastActivePageIDSkipsPlaceholder(t *testing.T) {
	manager := newActivePageTestManager()
	addActivePageTestPage(manager, "newtab", "chrome://newtab/")

	if got := manager.GetLastActivePageID(); got != "" {
		t.Fatalf("GetLastActivePageID() = %q, want empty", got)
	}
}

func TestBrowserManagerGetLastActivePageIDFallsBackToExecutable(t *testing.T) {
	manager := newActivePageTestManager()
	addActivePageTestPage(manager, "newtab", "chrome://newtab/")
	addActivePageTestPage(manager, "page", "https://example.com")
	manager.SetLastActivePageID("newtab")

	if got := manager.GetLastActivePageID(); got != "page" {
		t.Fatalf("GetLastActivePageID() = %q, want page", got)
	}
}

func TestBrowserManagerGetLastActivePageReturnsClearNoUsablePageError(t *testing.T) {
	manager := newActivePageTestManager()
	addActivePageTestPage(manager, "devtools", "devtools://devtools/bundled/inspector.html")

	_, err := manager.GetLastActivePage()
	if err == nil {
		t.Fatal("expected no usable page error")
	}
	if got := err.Error(); got != "当前没有可操作页面，请打开业务页面或调用 NewPage/AutoPage" {
		t.Fatalf("error = %q", got)
	}
}

func TestBrowserManagersForSameEndpointShareCancelableForegroundGate(t *testing.T) {
	endpoint := "http://foreground-interaction.test/"
	first := loadBrowserServiceState(endpoint)
	second := loadBrowserServiceState(endpoint)
	if first != second {
		t.Fatal("same CDP endpoint should share browser service state")
	}

	first.foregroundGateMu.Lock()
	first.foregroundGate = make(chan struct{}, 1)
	first.foregroundGate <- struct{}{}
	gate := first.foregroundGate
	first.foregroundGateMu.Unlock()
	<-gate

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := second.acquireForeground(canceled, make(chan struct{})); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled acquisition error = %v", err)
	}

	gate <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	release, err := second.acquireForeground(ctx, make(chan struct{}))
	if err != nil {
		t.Fatalf("acquire after cancellation: %v", err)
	}
	release()
}
