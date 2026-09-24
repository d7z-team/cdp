package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	"gopkg.d7z.net/cdp/internal/syncutil"
)

type orderedLifecycleTestHandler struct {
	mu       sync.Mutex
	seen     []string
	sequence map[string]uint64
	entered  chan struct{}
	release  chan struct{}
	done     chan string
}

func prepareRuntimeReadyTestPage(page *Page, contextID int) {
	page.manager = nil
	page.setMainFrame("top-frame")
	page.executionContexts = map[targetContextKey]executionContextInfo{
		{ContextID: contextID}: {ID: contextID, FrameID: "top-frame", Name: worldNameIsolatedCore},
	}
}

func (h *orderedLifecycleTestHandler) Name() string { return "ordered-lifecycle-test" }

func (h *orderedLifecycleTestHandler) HandleLifecycle(_ LifecycleContext, event LifecycleEvent) error {
	if event.Reason == "page-1-first" {
		close(h.entered)
		<-h.release
	}
	h.mu.Lock()
	h.seen = append(h.seen, event.Reason)
	h.sequence[event.Reason] = event.Sequence
	h.mu.Unlock()
	h.done <- event.Reason
	return nil
}

func TestBrowserManagerTopPageURLUsesLifecycleSnapshot(t *testing.T) {
	manager := &BrowserManager{}
	manager.updatePageTargetInfo(TargetInfo{TargetID: "page-1", Type: "page", URL: "https://example.test/top"})
	if got := manager.TopPageURL("page-1"); got != "https://example.test/top" {
		t.Fatalf("TopPageURL = %q", got)
	}
	if got := manager.TopPageURL("missing"); got != "" {
		t.Fatalf("missing TopPageURL = %q", got)
	}
}

func TestRuntimeReadyLifecycleWaitsForPagePublication(t *testing.T) {
	manager := &BrowserManager{sessions: syncutil.NewSyncMap[string, *Page]()}
	manager.setState(managerStateRunning)
	handler := &orderedLifecycleTestHandler{
		sequence: make(map[string]uint64),
		done:     make(chan string, 1),
	}
	if err := manager.RegisterLifecycle(handler); err != nil {
		t.Fatal(err)
	}
	page := NewPageWithContext(context.Background())
	page.ID = "page-1"
	prepareRuntimeReadyTestPage(page, 7)
	page.bumpTargetEpoch("")
	manager.emitPageRuntimeReady(page, InitScript{Name: "test-runtime.js", Namespace: NamespaceIsolatedCore}, 1, 7)
	if got := manager.lifecycleSeq.Load(); got != 0 {
		t.Fatalf("runtime-ready lifecycle escaped before page publication: sequence=%d", got)
	}

	manager.sessions.Store(page.ID, page)
	manager.publishPageRuntimeReady(page)
	if got := manager.lifecycleSeq.Load(); got != 1 {
		t.Fatalf("published runtime-ready lifecycle sequence = %d, want 1", got)
	}
	select {
	case <-handler.done:
	case <-time.After(time.Second):
		t.Fatal("queued runtime-ready lifecycle was not published")
	}
}

func TestRuntimeReadyLifecycleKeepsNewestPendingGeneration(t *testing.T) {
	manager := &BrowserManager{sessions: syncutil.NewSyncMap[string, *Page]()}
	manager.setState(managerStateRunning)
	page := NewPageWithContext(context.Background())
	page.ID = "page-1"
	prepareRuntimeReadyTestPage(page, 8)
	script := InitScript{Name: "test-runtime.js", Namespace: NamespaceIsolatedCore}

	manager.emitPageRuntimeReady(page, script, 2, 8)
	manager.emitPageRuntimeReady(page, script, 1, 8)

	page.runtimeReadyMu.Lock()
	pending := page.pendingRuntimeReady[script.Name]
	page.runtimeReadyMu.Unlock()
	if pending.generation != 2 {
		t.Fatalf("pending runtime generation = %d, want newest generation 2", pending.generation)
	}
}

func TestRuntimeReadyLifecycleDeduplicatesExecutionContext(t *testing.T) {
	manager := &BrowserManager{sessions: syncutil.NewSyncMap[string, *Page]()}
	manager.setState(managerStateRunning)
	page := NewPageWithContext(context.Background())
	page.ID = "page-1"
	prepareRuntimeReadyTestPage(page, 17)
	page.bumpTargetEpoch("")
	manager.sessions.Store(page.ID, page)
	manager.publishPageRuntimeReady(page)
	script := InitScript{Name: "test-runtime.js", Namespace: NamespaceIsolatedCore}

	manager.emitPageRuntimeReady(page, script, 1, 17)
	manager.emitPageRuntimeReady(page, script, 1, 17)

	if got := manager.lifecycleSeq.Load(); got != 1 {
		t.Fatalf("same runtime context lifecycle count = %d, want 1", got)
	}
}

func TestInitScriptRuntimeReadyIdentity(t *testing.T) {
	manager := &BrowserManager{
		sessions:          syncutil.NewSyncMap[string, *Page](),
		namespaces:        defaultNamespaceRegistrations(),
		initScriptsByName: map[string]initScriptRegistration{"test-runtime.js": {script: InitScript{Name: "test-runtime.js", Namespace: NamespaceIsolatedCore, Exec: "void 0"}}},
	}
	manager.setState(managerStateRunning)
	page := NewPageWithContext(context.Background())
	page.ID = "page-1"
	page.manager = manager
	page.setMainFrame("top-frame")
	page.executionContexts = map[targetContextKey]executionContextInfo{
		{ContextID: 7}:                      {ID: 7, FrameID: "top-frame", Name: worldNameIsolatedCore},
		{ContextID: 8}:                      {ID: 8, FrameID: "child-frame", Name: worldNameIsolatedCore},
		{ContextID: 9}:                      {ID: 9, FrameID: "top-frame", IsDefault: true},
		{SessionID: "oopif", ContextID: 10}: {ID: 10, FrameID: "top-frame", Name: worldNameIsolatedCore},
	}
	page.targetEpochs = map[string]int64{targetEpochKey(""): 1}
	manager.sessions.Store(page.ID, page)
	manager.publishPageRuntimeReady(page)

	if manager.ConfirmInitScriptRuntimeReady(page, "missing.js", 7) ||
		manager.ConfirmInitScriptRuntimeReady(page, "test-runtime.js", 8) ||
		manager.ConfirmInitScriptRuntimeReady(page, "test-runtime.js", 9) ||
		manager.ConfirmInitScriptRuntimeReady(page, "test-runtime.js", 10) {
		t.Fatal("runtime-ready accepted a missing script, child frame, wrong namespace, or target-session context")
	}
	if !manager.ConfirmInitScriptRuntimeReady(page, "test-runtime.js", 7) {
		t.Fatal("current top-frame core runtime was rejected")
	}
	handle, generation, ready := page.ReadyInitScriptRuntime("test-runtime.js")
	if !ready || generation != 1 || handle.Info.ContextID != 7 || handle.Info.FrameID != "top-frame" {
		t.Fatalf("ready runtime = handle:%+v generation:%d ready:%v", handle, generation, ready)
	}
	manager.setState(managerStateStopping)
	if _, _, ready := page.ReadyInitScriptRuntime("test-runtime.js"); ready {
		t.Fatal("stopping manager exposed a ready runtime")
	}
	manager.setState(managerStateRunning)

	page.topNavigationPending.Store(true)
	if _, _, ready := page.ReadyInitScriptRuntime("test-runtime.js"); ready {
		t.Fatal("navigation-pending page exposed its old ready runtime")
	}
	page.topNavigationPending.Store(false)
	if _, _, ready := page.ReadyInitScriptRuntime("test-runtime.js"); !ready {
		t.Fatal("canceled navigation did not restore the unchanged ready runtime")
	}
	page.clearExecutionContexts()
	if _, _, ready := page.ReadyInitScriptRuntime("test-runtime.js"); ready {
		t.Fatal("replacement generation retained the previous ready runtime")
	}

	replacement := NewPageWithContext(context.Background())
	replacement.ID = page.ID
	replacement.manager = manager
	manager.sessions.Store(page.ID, replacement)
	if manager.ConfirmInitScriptRuntimeReady(page, "test-runtime.js", 7) {
		t.Fatal("replaced Page instance confirmed runtime readiness")
	}
}

func TestInvalidateInitScriptRuntimeOnlyDeletesMatchingIdentity(t *testing.T) {
	page := NewPageWithContext(context.Background())
	page.runtimeReadyContexts = map[string]runtimeReadyIdentity{
		"test-runtime.js": {generation: 4, contextID: 17, frameID: "top-frame"},
	}
	if page.invalidateInitScriptRuntime("test-runtime.js", 3, 17) ||
		page.invalidateInitScriptRuntime("test-runtime.js", 4, 16) {
		t.Fatal("stale runtime identity invalidated the current runtime")
	}
	if got := page.runtimeReadyContexts["test-runtime.js"]; got.generation != 4 || got.contextID != 17 {
		t.Fatalf("current runtime identity changed: %+v", got)
	}
	if !page.invalidateInitScriptRuntime("test-runtime.js", 4, 17) {
		t.Fatal("matching runtime identity was not invalidated")
	}
	if _, ok := page.runtimeReadyContexts["test-runtime.js"]; ok {
		t.Fatal("matching runtime identity remains cached")
	}
}

func TestPageStoppedPendingNavigationEmitsCanceledLifecycle(t *testing.T) {
	manager := &BrowserManager{sessions: syncutil.NewSyncMap[string, *Page]()}
	manager.setState(managerStateRunning)
	handler := &orderedLifecycleTestHandler{
		sequence: make(map[string]uint64),
		done:     make(chan string, 1),
	}
	if err := manager.RegisterLifecycle(handler); err != nil {
		t.Fatal(err)
	}
	page := NewPageWithContext(context.Background())
	page.ID = "page-1"
	page.manager = manager
	page.setMainFrame("top-frame")
	page.topNavigationPending.Store(true)
	manager.sessions.Store(page.ID, page)
	page.handleRootPageEvent(CDPResponse{Method: "Page.frameStoppedLoading", Params: map[string]any{"frameId": "top-frame"}})

	select {
	case reason := <-handler.done:
		if reason != "frame_stopped_loading" {
			t.Fatalf("navigation-canceled reason = %q", reason)
		}
	case <-time.After(time.Second):
		t.Fatal("navigation-canceled lifecycle was not delivered")
	}
	if page.topNavigationPending.Load() {
		t.Fatal("canceled navigation remained pending")
	}
}

func TestRuntimeReadyLifecycleRejectsPendingSupersededGeneration(t *testing.T) {
	manager := &BrowserManager{sessions: syncutil.NewSyncMap[string, *Page]()}
	manager.setState(managerStateRunning)
	page := NewPageWithContext(context.Background())
	page.ID = "page-1"
	prepareRuntimeReadyTestPage(page, 7)
	page.bumpTargetEpoch("")
	manager.emitPageRuntimeReady(page, InitScript{Name: "test-runtime.js", Namespace: NamespaceIsolatedCore}, 1, 7)
	page.bumpTargetEpoch("")

	manager.sessions.Store(page.ID, page)
	manager.publishPageRuntimeReady(page)
	if got := manager.lifecycleSeq.Load(); got != 0 {
		t.Fatalf("superseded pending runtime-ready lifecycle escaped publication: sequence=%d", got)
	}
}

func TestRuntimeReadyLifecycleRejectsReplacedPage(t *testing.T) {
	manager := &BrowserManager{sessions: syncutil.NewSyncMap[string, *Page]()}
	manager.setState(managerStateRunning)
	stale := NewPageWithContext(context.Background())
	stale.ID = "page-1"
	prepareRuntimeReadyTestPage(stale, 7)
	replacement := NewPageWithContext(context.Background())
	replacement.ID = stale.ID

	manager.emitPageRuntimeReady(stale, InitScript{Name: "test-runtime.js", Namespace: NamespaceIsolatedCore}, 1, 7)
	manager.sessions.Store(replacement.ID, replacement)
	manager.publishPageRuntimeReady(stale)
	manager.emitPageRuntimeReady(stale, InitScript{Name: "test-runtime.js", Namespace: NamespaceIsolatedCore}, 2, 8)

	if got := manager.lifecycleSeq.Load(); got != 0 {
		t.Fatalf("replaced page emitted runtime-ready lifecycle: sequence=%d", got)
	}
}

func TestRuntimeReadyLifecycleRejectsStoppedManager(t *testing.T) {
	manager := &BrowserManager{sessions: syncutil.NewSyncMap[string, *Page]()}
	manager.setState(managerStateRunning)
	page := NewPageWithContext(context.Background())
	page.ID = "page-1"
	prepareRuntimeReadyTestPage(page, 7)
	manager.sessions.Store(page.ID, page)
	manager.publishPageRuntimeReady(page)
	manager.setState(managerStateStopping)

	manager.emitPageRuntimeReady(page, InitScript{Name: "test-runtime.js", Namespace: NamespaceIsolatedCore}, 1, 7)
	if got := manager.lifecycleSeq.Load(); got != 0 {
		t.Fatalf("stopped manager emitted runtime-ready lifecycle: sequence=%d", got)
	}
}

func TestPageUnbindClosesRuntimeReadyLifecycle(t *testing.T) {
	manager := &BrowserManager{sessions: syncutil.NewSyncMap[string, *Page]()}
	manager.setState(managerStateRunning)
	page := NewPageWithContext(context.Background())
	page.ID = "page-1"
	prepareRuntimeReadyTestPage(page, 7)
	manager.emitPageRuntimeReady(page, InitScript{Name: "test-runtime.js", Namespace: NamespaceIsolatedCore}, 1, 7)

	page.unbind()
	manager.sessions.Store(page.ID, page)
	manager.publishPageRuntimeReady(page)
	manager.emitPageRuntimeReady(page, InitScript{Name: "test-runtime.js", Namespace: NamespaceIsolatedCore}, 2, 8)

	page.runtimeReadyMu.Lock()
	closed := page.runtimeReadyClosed
	published := page.runtimeReadyPublished
	pending := len(page.pendingRuntimeReady)
	page.runtimeReadyMu.Unlock()
	if !closed || published || pending != 0 {
		t.Fatalf("closed runtime-ready state = closed:%v published:%v pending:%d", closed, published, pending)
	}
	if got := manager.lifecycleSeq.Load(); got != 0 {
		t.Fatalf("unbound page emitted runtime-ready lifecycle: sequence=%d", got)
	}
}

func TestLifecycleDeliveryIsFIFOForEachPageAndIndependentAcrossPages(t *testing.T) {
	manager := &BrowserManager{}
	handler := &orderedLifecycleTestHandler{
		sequence: make(map[string]uint64), entered: make(chan struct{}), release: make(chan struct{}), done: make(chan string, 3),
	}
	if err := manager.RegisterLifecycle(handler); err != nil {
		t.Fatal(err)
	}
	emit := func(pageID, reason string) {
		manager.emitLifecycle(LifecycleContext{Context: context.Background(), Manager: manager}, LifecycleEvent{
			Type: LifecyclePageNavigated, PageID: pageID, Reason: reason,
		})
	}
	emit("page-1", "page-1-first")
	select {
	case <-handler.entered:
	case <-time.After(time.Second):
		t.Fatal("first page handler did not start")
	}
	emit("page-1", "page-1-second")
	emit("page-2", "page-2-first")
	select {
	case got := <-handler.done:
		if got != "page-2-first" {
			t.Fatalf("blocked page delivered %q before independent page", got)
		}
	case <-time.After(time.Second):
		t.Fatal("independent page was blocked by page-1 lifecycle")
	}
	close(handler.release)
	for range 2 {
		select {
		case <-handler.done:
		case <-time.After(time.Second):
			t.Fatal("page-1 lifecycle queue did not drain")
		}
	}
	handler.mu.Lock()
	defer handler.mu.Unlock()
	var pageOne []string
	for _, reason := range handler.seen {
		if reason == "page-1-first" || reason == "page-1-second" {
			pageOne = append(pageOne, reason)
		}
	}
	if len(pageOne) != 2 || pageOne[0] != "page-1-first" || pageOne[1] != "page-1-second" {
		t.Fatalf("page-1 lifecycle order = %v; all=%v", pageOne, handler.seen)
	}
	if handler.sequence["page-1-first"] == 0 || handler.sequence["page-1-first"] >= handler.sequence["page-1-second"] {
		t.Fatalf("page-1 lifecycle sequence = %v", handler.sequence)
	}
}
