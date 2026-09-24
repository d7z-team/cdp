package engine

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gopkg.d7z.net/cdp/internal/syncutil"
)

type controlledPageBindTransport struct {
	calls      atomic.Int32
	started    chan int
	release    chan struct{}
	canceled   chan struct{}
	blockEvery bool
}

type pageClosedCapture struct {
	closed chan *Page
}

func (c *pageClosedCapture) Name() string { return "page-closed-capture" }

func (c *pageClosedCapture) HandleLifecycle(ctx LifecycleContext, event LifecycleEvent) error {
	if event.Type == LifecyclePageClosed {
		c.closed <- ctx.Page
	}
	return nil
}

func (t *controlledPageBindTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	call := int(t.calls.Add(1))
	t.started <- call
	if call == 1 || t.blockEvery {
		select {
		case <-t.release:
		case <-request.Context().Done():
			if t.canceled != nil {
				close(t.canceled)
			}
			return nil, request.Context().Err()
		}
	}
	return nil, errors.New("controlled websocket handshake failure")
}

func TestBrowserManagerBindFailurePreservesTargetUntilDestroyed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := &BrowserManager{
		ctx:           ctx,
		client:        http.DefaultClient,
		baseURL:       "http://%/",
		sessions:      syncutil.NewSyncMap[string, *Page](),
		pageBinds:     map[string]*pageBindState{},
		pageLifecycle: map[string]*pageLifecycleState{},
	}
	manager.updatePageTargetInfo(TargetInfo{TargetID: "page-1", Type: "page", URL: "https://example.test"})

	pageCtx, pageCancel := context.WithCancel(ctx)
	defer pageCancel()
	page := &Page{
		ID:       "page-1",
		ctx:      pageCtx,
		initDone: make(chan struct{}),
	}
	if _, err := manager.bindPageContext(ctx, pageCtx, page); err == nil {
		t.Fatal("bind with invalid websocket endpoint unexpectedly succeeded")
	}
	if err := page.waitInitContext(context.Background()); err == nil {
		t.Fatal("failed bind initialization result was lost")
	}
	if page, exists := manager.GetPage("page-1"); exists || page != nil {
		t.Fatalf("failed bind retained unusable page: %+v", page)
	}
	if got := manager.TopPageURL("page-1"); got != "https://example.test" {
		t.Fatalf("failed bind lost latest target URL %q", got)
	}
	manager.destroyPageTarget("page-1", "test")
	if got := manager.TopPageURL("page-1"); got != "" {
		t.Fatalf("destroyed target retained lifecycle URL %q", got)
	}
}

func TestFailedRootBindCleanupPreservesTargetSessionRuntime(t *testing.T) {
	rootInstall := initScriptInstallKey{PageID: "page-1", ScriptName: "core.js"}
	sessionInstall := initScriptInstallKey{PageID: "page-1", SessionID: "session-1", ScriptName: "core.js"}
	manager := &BrowserManager{
		initScriptInstalls: map[initScriptInstallKey]initScriptInstallState{
			rootInstall:    {Identifier: "root-id"},
			sessionInstall: {Identifier: "session-id"},
		},
	}
	page := NewPageWithContext(context.Background())
	page.ID = "page-1"

	manager.cleanupFailedPageBind(page, nil)
	snapshot := manager.registeredInitScriptsSnapshot()
	if _, ok := snapshot[rootInstall]; ok {
		t.Fatal("failed root bind retained root init-script runtime")
	}
	if _, ok := snapshot[sessionInstall]; !ok {
		t.Fatal("failed root bind removed target-session init-script runtime")
	}
}

func TestPageLoadTimeoutCoversTwoBindAttemptsAndCleanup(t *testing.T) {
	minimum := 2 * (pageBindTimeout + pageBindCleanupTimeout)
	if pageLoadTimeout <= minimum {
		t.Fatalf("page load timeout %s must exceed two complete bind attempts %s", pageLoadTimeout, minimum)
	}
}

func TestStandbyPageDoesNotConsumeBindTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := &BrowserManager{ctx: ctx}
	attempt := manager.newPageBindAttempt("page-1")
	defer attempt.cancel()
	if deadline, ok := attempt.page.Deadline(); ok {
		t.Fatalf("standby page has premature bind deadline %s", deadline)
	}
}

func TestPageInitializationResultUsesCallerContext(t *testing.T) {
	page := &Page{ID: "page-1", initDone: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := page.waitInitContext(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait init error = %v, want context deadline", err)
	}

	want := errors.New("page bind failed")
	page.finishInit(want)
	if err := page.waitInitContext(context.Background()); !errors.Is(err, want) {
		t.Fatalf("completed init error = %v, want %v", err, want)
	}
}

func TestOldPageCleanupDoesNotDeleteReplacement(t *testing.T) {
	manager := &BrowserManager{
		sessions:      syncutil.NewSyncMap[string, *Page](),
		pageBinds:     map[string]*pageBindState{},
		pageLifecycle: map[string]*pageLifecycleState{},
	}
	oldPage := NewPageWithContext(context.Background())
	oldPage.ID = "page-1"
	replacement := NewPageWithContext(context.Background())
	replacement.ID = "page-1"
	manager.sessions.Store("page-1", replacement)
	manager.pageBinds["page-1"] = &pageBindState{page: replacement}

	manager.handlePageConnectionClosed("page-1", oldPage, nil)
	if got, ok := manager.GetPage("page-1"); !ok || got != replacement {
		t.Fatalf("old cleanup removed replacement page: got=%p ok=%v", got, ok)
	}
	if got := manager.pageBinds["page-1"].page; got != replacement {
		t.Fatalf("old cleanup cleared replacement bind state: got=%p", got)
	}
}

func TestReplacedPageCannotDispatchProtocolEvents(t *testing.T) {
	manager := &BrowserManager{
		sessions:  syncutil.NewSyncMap[string, *Page](),
		pageBinds: map[string]*pageBindState{},
	}
	stale := NewPageWithContext(context.Background())
	stale.ID = "page-1"
	replacement := NewPageWithContext(context.Background())
	replacement.ID = stale.ID
	manager.sessions.Store(replacement.ID, replacement)
	manager.pageBinds[replacement.ID] = &pageBindState{page: replacement}
	if manager.isCurrentManagedPage(stale) {
		t.Fatal("replaced page remained eligible to dispatch protocol events")
	}
	if !manager.isCurrentManagedPage(replacement) {
		t.Fatal("replacement page was not eligible to dispatch protocol events")
	}
}

func TestPageConnectionClosePreservesTargetUntilDestroyed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := &BrowserManager{
		ctx:           ctx,
		sessions:      syncutil.NewSyncMap[string, *Page](),
		pageBinds:     map[string]*pageBindState{},
		pageLifecycle: map[string]*pageLifecycleState{},
	}
	manager.setState(managerStateRunning)
	page := NewPageWithContext(ctx)
	page.ID = "page-1"
	manager.sessions.Store(page.ID, page)
	manager.pageBinds[page.ID] = &pageBindState{targetVersion: 1, boundTargetVersion: 1, page: page}
	manager.markPageDiscovered(TargetInfo{TargetID: page.ID, Type: "page", URL: "https://example.test/current"}, page)
	manager.markPageBound(page)

	manager.handlePageConnectionClosed(page.ID, page, nil)
	if _, ok := manager.GetPage(page.ID); ok {
		t.Fatal("closed page connection remained ready")
	}
	if got := manager.TopPageURL(page.ID); got != "https://example.test/current" {
		t.Fatalf("page connection close lost target URL %q", got)
	}
	state := manager.snapshotPageState(page.ID)
	if state.bound {
		t.Fatal("page connection close retained bound lifecycle state")
	}

	manager.destroyPageTarget(page.ID, "test_destroyed")
	if got := manager.TopPageURL(page.ID); got != "" {
		t.Fatalf("destroyed target retained URL %q", got)
	}
}

func TestBoundTargetUpdateDoesNotArmStaleReconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	transport := &controlledPageBindTransport{
		started: make(chan int, 1),
		release: make(chan struct{}),
	}
	manager := &BrowserManager{
		ctx:           ctx,
		client:        &http.Client{Transport: transport},
		baseURL:       "http://cdp.test/",
		sessions:      syncutil.NewSyncMap[string, *Page](),
		pageBinds:     map[string]*pageBindState{},
		pageLifecycle: map[string]*pageLifecycleState{},
	}
	manager.setState(managerStateRunning)
	page := NewPageWithContext(ctx)
	page.ID = "page-1"
	manager.sessions.Store(page.ID, page)
	manager.pageBinds[page.ID] = &pageBindState{targetVersion: 1, boundTargetVersion: 1, page: page}

	manager.requestPageBind(page.ID, false)
	state := manager.pageBinds[page.ID]
	if state.targetVersion != 2 || state.boundTargetVersion != 2 || state.inFlight {
		t.Fatalf("bound target update state = %+v", state)
	}

	manager.handlePageConnectionClosed(page.ID, page, nil)
	manager.pageBindWG.Wait()
	state = manager.pageBinds[page.ID]
	if state == nil || state.inFlight || state.page != nil || !errors.Is(state.lastErr, errPageConnectionClosed) {
		t.Fatalf("closed page state = %+v", state)
	}
	if calls := transport.calls.Load(); calls != 0 {
		t.Fatalf("historical target update started %d reconnect attempts", calls)
	}
}

func TestPageConnectionCloseCannotRecreateDestroyedLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := &BrowserManager{
		ctx:           ctx,
		sessions:      syncutil.NewSyncMap[string, *Page](),
		pageBinds:     map[string]*pageBindState{},
		pageLifecycle: map[string]*pageLifecycleState{},
	}
	manager.setState(managerStateRunning)
	closedCapture := &pageClosedCapture{closed: make(chan *Page, 1)}
	if err := manager.RegisterLifecycle(closedCapture); err != nil {
		t.Fatal(err)
	}
	page := NewPageWithContext(ctx)
	page.ID = "page-1"
	manager.sessions.Store(page.ID, page)
	manager.pageBinds[page.ID] = &pageBindState{targetVersion: 1, boundTargetVersion: 1, page: page}
	manager.markPageDiscovered(TargetInfo{TargetID: page.ID, Type: "page", URL: "https://example.test/current"}, page)
	manager.markPageBound(page)

	// Hold the Page lock after connection cleanup has claimed this generation.
	// Target destruction must remain independent from the blocked Page teardown.
	page.lock.Lock()
	connectionClosed := make(chan struct{})
	go func() {
		manager.handlePageConnectionClosed(page.ID, page, nil)
		close(connectionClosed)
	}()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, ok := manager.GetPage(page.ID); !ok {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if _, ok := manager.GetPage(page.ID); ok {
		page.lock.Unlock()
		t.Fatal("connection cleanup did not reach unbind")
	}

	targetDestroyed := make(chan struct{})
	go func() {
		manager.destroyPageTarget(page.ID, "test_destroyed")
		close(targetDestroyed)
	}()
	select {
	case <-targetDestroyed:
	case <-time.After(time.Second):
		page.lock.Unlock()
		t.Fatal("target destruction blocked behind Page teardown")
	}
	page.lock.Unlock()

	for name, done := range map[string]<-chan struct{}{
		"connection cleanup": connectionClosed,
		"target destruction": targetDestroyed,
	} {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("%s did not finish", name)
		}
	}
	manager.pageLifecycleMu.Lock()
	_, lifecycleExists := manager.pageLifecycle[page.ID]
	manager.pageLifecycleMu.Unlock()
	if lifecycleExists {
		t.Fatal("connection cleanup recreated lifecycle state after target destruction")
	}
	manager.pageBindMu.Lock()
	_, bindExists := manager.pageBinds[page.ID]
	manager.pageBindMu.Unlock()
	if bindExists {
		t.Fatal("target destruction retained page bind state")
	}
	select {
	case closedPage := <-closedCapture.closed:
		if closedPage != page {
			t.Fatalf("page-closed lifecycle Page = %p, want %p", closedPage, page)
		}
	case <-time.After(time.Second):
		t.Fatal("target destruction did not publish page-closed lifecycle")
	}
}

func TestDestroyPageTargetCancelsPendingBindAndCleansLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pageCtx, pageCancel := context.WithCancel(ctx)
	manager := &BrowserManager{
		ctx:           ctx,
		sessions:      syncutil.NewSyncMap[string, *Page](),
		pageBinds:     map[string]*pageBindState{},
		pageLifecycle: map[string]*pageLifecycleState{},
	}
	manager.setState(managerStateRunning)
	page := &Page{ID: "page-1", ctx: pageCtx, initDone: make(chan struct{})}
	manager.pageBinds[page.ID] = &pageBindState{
		inFlight:   true,
		page:       page,
		cancelPage: pageCancel,
	}
	manager.markPageDiscovered(TargetInfo{TargetID: page.ID, Type: "page", URL: "https://example.test"}, page)

	manager.destroyPageTarget(page.ID, "test_destroyed")
	if !errors.Is(pageCtx.Err(), context.Canceled) {
		t.Fatalf("destroy did not cancel pending bind: page=%v", pageCtx.Err())
	}
	if _, ok := manager.pageBinds[page.ID]; ok {
		t.Fatal("destroy retained pending bind state")
	}
	if got := manager.TopPageURL(page.ID); got != "" {
		t.Fatalf("destroy retained lifecycle URL %q", got)
	}
}

func TestDestroyPageTargetCancelsInFlightBind(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	transport := &controlledPageBindTransport{
		started:  make(chan int, 1),
		release:  make(chan struct{}),
		canceled: make(chan struct{}),
	}
	manager := &BrowserManager{
		ctx:           ctx,
		client:        &http.Client{Transport: transport},
		baseURL:       "http://cdp.test/",
		sessions:      syncutil.NewSyncMap[string, *Page](),
		pageBinds:     map[string]*pageBindState{},
		pageLifecycle: map[string]*pageLifecycleState{},
	}
	manager.setState(managerStateRunning)
	manager.markPageDiscovered(TargetInfo{TargetID: "page-1", Type: "page", URL: "https://example.test"}, nil)
	manager.requestPageBind("page-1", false)
	select {
	case <-transport.started:
	case <-time.After(time.Second):
		t.Fatal("bind attempt did not reach websocket handshake")
	}

	manager.destroyPageTarget("page-1", "test_destroyed")
	select {
	case <-transport.canceled:
	case <-time.After(time.Second):
		t.Fatal("target destroy did not cancel websocket handshake")
	}
	if _, ok := manager.pageBinds["page-1"]; ok {
		t.Fatal("target destroy retained in-flight bind state")
	}
	if got := manager.TopPageURL("page-1"); got != "" {
		t.Fatalf("target destroy retained lifecycle URL %q", got)
	}
	time.Sleep(30 * time.Millisecond)
	if calls := transport.calls.Load(); calls != 1 {
		t.Fatalf("destroyed target started %d bind attempts", calls)
	}
}

func TestEnsurePageBoundCoalescesTargetEventsDuringBind(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := &BrowserManager{
		ctx:      ctx,
		sessions: syncutil.NewSyncMap[string, *Page](),
		pageBinds: map[string]*pageBindState{
			"page-1": {targetVersion: 1, inFlight: true},
		},
	}
	manager.setState(managerStateRunning)

	manager.requestPageBind("page-1", false)
	manager.requestPageBind("page-1", false)
	state := manager.pageBinds["page-1"]
	if state == nil || !state.inFlight || state.targetVersion != 3 {
		t.Fatalf("coalesced bind state = %+v", state)
	}
}

func TestEnsurePageBoundPublishesPendingPageBeforeReturn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	transport := &controlledPageBindTransport{
		started: make(chan int, 1),
		release: make(chan struct{}),
	}
	manager := &BrowserManager{
		ctx:                      ctx,
		client:                   &http.Client{Transport: transport},
		baseURL:                  "http://cdp.test/",
		sessions:                 syncutil.NewSyncMap[string, *Page](),
		pageBinds:                map[string]*pageBindState{},
		pageLifecycle:            map[string]*pageLifecycleState{},
		targetSessions:           map[string]*TargetSession{},
		targetSessionsByTarget:   map[string]string{},
		initScriptsByName:        map[string]initScriptRegistration{},
		pendingNavigationReasons: map[navigationReasonKey]pendingNavigationReason{},
	}
	manager.setState(managerStateRunning)

	manager.requestPageBind("page-1", false)
	manager.pageBindMu.Lock()
	state := manager.pageBinds["page-1"]
	binding := state != nil && state.inFlight
	var pending *Page
	if state != nil {
		pending = state.page
	}
	manager.pageBindMu.Unlock()
	if state == nil || !binding || pending == nil {
		t.Fatalf("pending bind state: exists=%v binding=%v page=%p", state != nil, binding, pending)
	}
	if pending.manager != manager || pending.ctx == nil || pending.ctx.Err() != nil {
		t.Fatalf("pending page was not initialized for event routing: manager=%p context=%v", pending.manager, pending.ctx)
	}
	if ready, ok := manager.GetPage(pending.ID); ok || ready != nil {
		t.Fatalf("pending page was published as ready: %p", ready)
	}

	session := manager.rememberTargetSession(TargetSession{
		TargetID:  "iframe-target",
		SessionID: "iframe-session",
		Type:      "iframe",
		ParentID:  pending.ID,
	})
	manager.handleTargetSessionEvent(CDPResponse{
		Method:    "Runtime.executionContextCreated",
		SessionID: session.SessionID,
		Params: map[string]any{"context": map[string]any{
			"id": 41, "auxData": map[string]any{"frameId": "frame-1", "isDefault": true},
		}},
	})
	if target, ok := pending.executionContextTarget(session.SessionID, 41); !ok || target.PageID != pending.ID {
		t.Fatalf("early iframe context target = %+v/%v", target, ok)
	}

	manager.destroyPageTarget(pending.ID, "test_destroyed")
	manager.pageBindWG.Wait()
}

func TestEnsurePageBoundRestoresTargetSessionRuntimeCarrier(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	transport := &controlledPageBindTransport{
		started:    make(chan int, 1),
		release:    make(chan struct{}),
		blockEvery: true,
	}
	carrier := NewPageWithContext(context.Background())
	carrier.ID = "page-1"
	carrier.storeExecutionContextForSession("iframe-session", executionContextInfo{
		ID: 41, FrameID: "iframe-frame", IsDefault: true,
	})
	manager := &BrowserManager{
		ctx:           ctx,
		client:        &http.Client{Transport: transport},
		baseURL:       "http://cdp.test/",
		sessions:      syncutil.NewSyncMap[string, *Page](),
		pageBinds:     map[string]*pageBindState{"page-1": {runtimeCarrier: carrier}},
		pageLifecycle: map[string]*pageLifecycleState{},
	}
	manager.setState(managerStateRunning)
	manager.requestPageBind(carrier.ID, false)
	select {
	case <-transport.started:
	case <-time.After(time.Second):
		t.Fatal("restored bind attempt did not start")
	}
	manager.pageBindMu.Lock()
	pending := manager.pageBinds[carrier.ID].page
	manager.pageBindMu.Unlock()
	if pending == nil || pending == carrier {
		t.Fatalf("restored pending page = %p, carrier = %p", pending, carrier)
	}
	if target, ok := pending.executionContextTarget("iframe-session", 41); !ok || target.FrameID != "iframe-frame" {
		t.Fatalf("restored iframe runtime = %+v/%v", target, ok)
	}
	manager.destroyPageTarget(carrier.ID, "test_destroyed")
	manager.pageBindWG.Wait()
}

func TestPageBindPreservesPreexistingTargetSessionContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := &BrowserManager{
		ctx:           ctx,
		sessions:      syncutil.NewSyncMap[string, *Page](),
		pageBinds:     map[string]*pageBindState{},
		pageLifecycle: map[string]*pageLifecycleState{},
	}
	manager.setState(managerStateRunning)
	manager.lastActivePageID.Store("existing-page")
	attempt := manager.newPageBindAttempt("page-1")
	defer attempt.cancel()

	attempt.page.storeExecutionContext(executionContextInfo{ID: 11, FrameID: "root-frame", IsDefault: true})
	attempt.page.storeExecutionContextForSession("iframe-session", executionContextInfo{ID: 41, FrameID: "iframe-frame", IsDefault: true})
	wsCtx, wsCancel := context.WithCancel(ctx)
	defer wsCancel()
	ws := &CdpConn{
		Context: wsCtx,
		event:   syncutil.NewPubSub[CDPResponse](nil),
	}
	if err := attempt.page.bind(attempt.page.ctx, attempt.page.ctx, ws, manager); err == nil {
		t.Fatal("bind with unavailable connection unexpectedly succeeded")
	}
	if _, ok := attempt.page.executionContextTarget("", 11); ok {
		t.Fatal("root context was not reset at bind start")
	}
	if target, ok := attempt.page.executionContextTarget("iframe-session", 41); !ok || target.FrameID != "iframe-frame" {
		t.Fatalf("preexisting iframe context target = %+v/%v", target, ok)
	}
}

func TestPageBindConsumesNewTargetVersionOnceAfterFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	transport := &controlledPageBindTransport{
		started: make(chan int, 2),
		release: make(chan struct{}),
	}
	manager := &BrowserManager{
		ctx:           ctx,
		client:        &http.Client{Transport: transport},
		baseURL:       "http://cdp.test/",
		sessions:      syncutil.NewSyncMap[string, *Page](),
		pageBinds:     map[string]*pageBindState{},
		pageLifecycle: map[string]*pageLifecycleState{},
	}
	manager.setState(managerStateRunning)

	manager.requestPageBind("page-1", false)
	select {
	case call := <-transport.started:
		if call != 1 {
			t.Fatalf("first bind transport call = %d", call)
		}
	case <-time.After(time.Second):
		t.Fatal("first bind attempt did not start")
	}
	if page, ok := manager.GetPage("page-1"); ok || page != nil {
		t.Fatalf("in-flight bind exposed a ready page: %+v", page)
	}
	for range 5 {
		manager.requestPageBind("page-1", false)
	}
	installKey := initScriptInstallKey{PageID: "page-1", ScriptName: "test-runtime.js", WorldName: "__cdp_isolated_core"}
	manager.initScriptRuntimeMu.Lock()
	manager.initScriptInstalls = map[initScriptInstallKey]initScriptInstallState{
		installKey: {Identifier: "old-install"},
	}
	manager.initScriptRuntimeMu.Unlock()
	if calls := transport.calls.Load(); calls != 1 {
		t.Fatalf("target updates started %d concurrent bind attempts", calls)
	}
	close(transport.release)
	select {
	case call := <-transport.started:
		if call != 2 {
			t.Fatalf("coalesced retry transport call = %d", call)
		}
	case <-time.After(time.Second):
		t.Fatal("new target version was not consumed after bind failure")
	}
	if _, ok := manager.registeredInitScriptsSnapshot()[installKey]; ok {
		t.Fatal("replacement bind started before the old page init-script cache was cleared")
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		manager.pageBindMu.Lock()
		state := manager.pageBinds["page-1"]
		stopped := state != nil && !state.inFlight && state.page == nil
		manager.pageBindMu.Unlock()
		if stopped {
			break
		}
		time.Sleep(time.Millisecond)
	}
	manager.pageBindMu.Lock()
	state := manager.pageBinds["page-1"]
	var final pageBindState
	if state != nil {
		final = *state
	}
	manager.pageBindMu.Unlock()
	if state == nil || final.inFlight || final.page != nil || final.targetVersion != 6 {
		t.Fatalf("final bind state = %+v", final)
	}
	loadCtx, cancelLoad := context.WithTimeout(context.Background(), time.Second)
	defer cancelLoad()
	if page, err := manager.LoadPageContext(loadCtx, "page-1"); err == nil || page != nil || !strings.Contains(err.Error(), "controlled websocket handshake failure") {
		t.Fatalf("terminal bind result = page:%p error:%v", page, err)
	}
	time.Sleep(30 * time.Millisecond)
	if calls := transport.calls.Load(); calls != 3 {
		t.Fatalf("explicit page acquisition should perform one recovery attempt: %d attempts", calls)
	}
}

func TestPageBindRetriesTargetEventReceivedDuringCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	transport := &controlledPageBindTransport{
		started:    make(chan int, 2),
		release:    make(chan struct{}, 2),
		blockEvery: true,
	}
	handler := &pendingPageBindingCapture{called: make(chan *Page, 1)}
	manager := &BrowserManager{
		ctx:                      ctx,
		client:                   &http.Client{Transport: transport},
		baseURL:                  "http://cdp.test/",
		sessions:                 syncutil.NewSyncMap[string, *Page](),
		pageBinds:                map[string]*pageBindState{},
		pageLifecycle:            map[string]*pageLifecycleState{},
		targetSessions:           map[string]*TargetSession{},
		targetSessionsByTarget:   map[string]string{},
		bindingHandlers:          map[string]BindingHandler{handler.Name(): handler},
		bindingNamespaces:        map[string]RuntimeNamespace{handler.Name(): NamespaceIsolatedCore},
		initScriptsByName:        map[string]initScriptRegistration{},
		pendingNavigationReasons: map[navigationReasonKey]pendingNavigationReason{},
	}
	manager.setState(managerStateRunning)
	manager.requestPageBind("page-1", false)
	select {
	case <-transport.started:
	case <-time.After(time.Second):
		t.Fatal("first bind attempt did not start")
	}
	manager.pageBindMu.Lock()
	first := manager.pageBinds["page-1"].page
	manager.pageBindMu.Unlock()
	first.lock.Lock()
	locked := true
	defer func() {
		if locked {
			first.lock.Unlock()
		}
	}()
	transport.release <- struct{}{}
	select {
	case <-first.Done():
	case <-time.After(time.Second):
		t.Fatal("failed bind did not reach cleanup")
	}
	manager.pageBindMu.Lock()
	standby := manager.pageBinds[first.ID].page
	manager.pageBindMu.Unlock()
	if standby == nil || standby == first {
		t.Fatalf("cleanup standby page = %p, first = %p", standby, first)
	}

	session := manager.rememberTargetSession(TargetSession{
		TargetID:  "iframe-target",
		SessionID: "iframe-session",
		Type:      "iframe",
		ParentID:  first.ID,
	})
	manager.handleTargetSessionEvent(CDPResponse{
		Method:    "Runtime.executionContextCreated",
		SessionID: session.SessionID,
		Params: map[string]any{"context": map[string]any{
			"id": 41, "auxData": map[string]any{"frameId": "iframe-frame", "isDefault": true},
		}},
	})
	manager.handleTargetSessionEvent(CDPResponse{
		Method:    "Runtime.bindingCalled",
		SessionID: session.SessionID,
		Params: map[string]any{
			"name": handler.Name(), "payload": "{}", "executionContextId": 41,
		},
	})
	select {
	case page := <-handler.called:
		if page != standby {
			t.Fatalf("cleanup-window binding page = %p, want standby %p", page, standby)
		}
	case <-time.After(time.Second):
		t.Fatal("cleanup-window binding was not delivered")
	}
	manager.requestPageBind(first.ID, false)
	first.lock.Unlock()
	locked = false

	select {
	case call := <-transport.started:
		if call != 2 {
			t.Fatalf("cleanup-window retry transport call = %d", call)
		}
	case <-time.After(time.Second):
		t.Fatal("target event received during cleanup did not trigger retry")
	}
	manager.pageBindMu.Lock()
	state := manager.pageBinds[first.ID]
	var replacement *Page
	if state != nil {
		replacement = state.page
	}
	manager.pageBindMu.Unlock()
	if replacement != standby {
		t.Fatalf("cleanup-window replacement page = %p, want standby %p", replacement, standby)
	}
	if target, ok := replacement.executionContextTarget(session.SessionID, 41); !ok || target.FrameID != "iframe-frame" {
		t.Fatalf("cleanup-window iframe context target = %+v/%v", target, ok)
	}

	transport.release <- struct{}{}
	manager.pageBindWG.Wait()
	if calls := transport.calls.Load(); calls != 2 {
		t.Fatalf("cleanup-window bind attempts = %d, want 2", calls)
	}
}

func TestPageBindRetryBatchStopsAfterOneNewerVersion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	transport := &controlledPageBindTransport{
		started:    make(chan int, 3),
		release:    make(chan struct{}, 2),
		blockEvery: true,
	}
	manager := &BrowserManager{
		ctx:           ctx,
		client:        &http.Client{Transport: transport},
		baseURL:       "http://cdp.test/",
		sessions:      syncutil.NewSyncMap[string, *Page](),
		pageBinds:     map[string]*pageBindState{},
		pageLifecycle: map[string]*pageLifecycleState{},
	}
	manager.setState(managerStateRunning)

	manager.requestPageBind("page-1", false)
	select {
	case call := <-transport.started:
		if call != 1 {
			t.Fatalf("first bind transport call = %d", call)
		}
	case <-time.After(time.Second):
		t.Fatal("first bind attempt did not start")
	}
	manager.requestPageBind("page-1", false)
	transport.release <- struct{}{}
	select {
	case call := <-transport.started:
		if call != 2 {
			t.Fatalf("retry bind transport call = %d", call)
		}
	case <-time.After(time.Second):
		t.Fatal("retry bind attempt did not start")
	}
	manager.requestPageBind("page-1", false)
	transport.release <- struct{}{}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		manager.pageBindMu.Lock()
		state := manager.pageBinds["page-1"]
		stopped := state != nil && !state.inFlight && state.page == nil
		manager.pageBindMu.Unlock()
		if stopped {
			break
		}
		time.Sleep(time.Millisecond)
	}
	manager.pageBindWG.Wait()
	if calls := transport.calls.Load(); calls != 2 {
		t.Fatalf("one retry batch started %d attempts", calls)
	}
	select {
	case call := <-transport.started:
		t.Fatalf("unexpected third bind attempt %d", call)
	default:
	}
}

func TestBrowserManagerShutdownCancelsBindWithoutRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	transport := &controlledPageBindTransport{
		started:  make(chan int, 2),
		release:  make(chan struct{}),
		canceled: make(chan struct{}),
	}
	manager := &BrowserManager{
		ctx:           ctx,
		cancel:        cancel,
		client:        &http.Client{Transport: transport},
		baseURL:       "http://cdp.test/",
		sessions:      syncutil.NewSyncMap[string, *Page](),
		pageBinds:     map[string]*pageBindState{},
		pageLifecycle: map[string]*pageLifecycleState{},
		shutdownDone:  make(chan struct{}),
	}
	manager.setState(managerStateRunning)
	manager.requestPageBind("page-1", false)
	select {
	case <-transport.started:
	case <-time.After(time.Second):
		t.Fatal("bind attempt did not start")
	}
	manager.requestPageBind("page-1", false)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
	defer shutdownCancel()
	if err := manager.DisconnectContext(shutdownCtx); err != nil {
		t.Fatalf("shutdown failed: %v", err)
	}
	select {
	case <-transport.canceled:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel the active bind attempt")
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		manager.pageBindMu.Lock()
		state := manager.pageBinds["page-1"]
		cleaned := state != nil && !state.inFlight && state.page == nil
		manager.pageBindMu.Unlock()
		if cleaned {
			break
		}
		time.Sleep(time.Millisecond)
	}
	manager.pageBindMu.Lock()
	state := manager.pageBinds["page-1"]
	var final pageBindState
	if state != nil {
		final = *state
	}
	manager.pageBindMu.Unlock()
	if state == nil || final.inFlight || final.page != nil {
		t.Fatalf("shutdown bind state = %+v", final)
	}
	time.Sleep(20 * time.Millisecond)
	if calls := transport.calls.Load(); calls != 1 {
		t.Fatalf("shutdown started %d bind attempts, want 1", calls)
	}
}
