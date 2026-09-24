package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gopkg.d7z.net/cdp/internal/binding"
	"gopkg.d7z.net/cdp/internal/syncutil"
)

type pendingPageBindingCapture struct {
	called chan *Page
}

func (h *pendingPageBindingCapture) Name() string { return "pending-page-binding" }

func (h *pendingPageBindingCapture) Handle(ctx BindingContext, _ *binding.BindingCalledEvent) error {
	h.called <- ctx.Page
	return nil
}

func TestCDPRequestSessionIDJSON(t *testing.T) {
	raw, err := json.Marshal(CDPRequest{
		ID:        7,
		Method:    "Runtime.evaluate",
		SessionID: "session-1",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	if !strings.Contains(string(raw), `"sessionId":"session-1"`) {
		t.Fatalf("sessionId missing from request json: %s", raw)
	}
}

func TestTargetSessionResolveNestedIframeToPage(t *testing.T) {
	manager := &BrowserManager{
		sessions:                 syncutil.NewSyncMap[string, *Page](),
		targetSessions:           map[string]*TargetSession{},
		targetSessionsByTarget:   map[string]string{},
		firstPageNotify:          make(chan struct{}),
		pendingNavigationReasons: map[navigationReasonKey]pendingNavigationReason{},
	}
	manager.sessions.Store("page-1", &Page{ID: "page-1"})

	iframe := manager.rememberTargetSession(TargetSession{
		TargetID:  "iframe-target",
		SessionID: "iframe-session",
		Type:      "iframe",
		ParentID:  "page-1",
	})
	if iframe.PageID != "page-1" {
		t.Fatalf("iframe page id = %q, want page-1", iframe.PageID)
	}

	nested := manager.rememberTargetSession(TargetSession{
		TargetID:  "nested-target",
		SessionID: "nested-session",
		Type:      "iframe",
		ParentID:  "iframe-target",
	})
	if nested.PageID != "page-1" {
		t.Fatalf("nested iframe page id = %q, want page-1", nested.PageID)
	}
}

func TestTargetSessionResolvePageBeforeBindPageIsAssigned(t *testing.T) {
	manager := &BrowserManager{
		sessions:               syncutil.NewSyncMap[string, *Page](),
		pageBinds:              map[string]*pageBindState{"new-page": {inFlight: true}},
		targetSessions:         map[string]*TargetSession{},
		targetSessionsByTarget: map[string]string{},
	}
	manager.sessions.Store("old-page", &Page{ID: "old-page"})
	manager.lastActivePageID.Store("old-page")

	session := manager.rememberTargetSession(TargetSession{
		TargetID:  "iframe-target",
		SessionID: "iframe-session",
		Type:      "iframe",
		ParentID:  "new-page",
	})
	if session.PageID != "new-page" {
		t.Fatalf("pending target iframe page id = %q, want new-page", session.PageID)
	}
}

func TestTargetSessionAttachedIframeEventInheritsParentPage(t *testing.T) {
	manager := &BrowserManager{
		sessions:               syncutil.NewSyncMap[string, *Page](),
		targetSessions:         map[string]*TargetSession{},
		targetSessionsByTarget: map[string]string{},
	}
	manager.sessions.Store("page-1", &Page{ID: "page-1"})
	parent := manager.rememberTargetSession(TargetSession{
		TargetID:  "parent-frame-target",
		SessionID: "parent-frame-session",
		Type:      "iframe",
		ParentID:  "page-1",
	})

	manager.handleTargetSessionEvent(CDPResponse{
		Method:    "Target.attachedToTarget",
		SessionID: parent.SessionID,
		Params: map[string]any{
			"sessionId": "child-frame-session",
			"targetInfo": map[string]any{
				"targetId":      "child-frame-target",
				"type":          "iframe",
				"url":           "https://child.example.test/",
				"parentFrameId": "child-frame-id",
			},
		},
	})

	child, ok := manager.targetSession("child-frame-session")
	if !ok {
		t.Fatal("child iframe session was not stored")
	}
	if child.PageID != "page-1" {
		t.Fatalf("child page id = %q, want page-1", child.PageID)
	}
	if child.ParentID != "parent-frame-target" {
		t.Fatalf("child parent id = %q, want parent-frame-target", child.ParentID)
	}
	if child.FrameID != "child-frame-id" {
		t.Fatalf("child frame id = %q, want child-frame-id", child.FrameID)
	}
}

func TestTargetSessionEventsUsePendingPageWithoutBlocking(t *testing.T) {
	page := NewPageWithContext(context.Background())
	page.ID = "page-1"
	handler := &pendingPageBindingCapture{called: make(chan *Page, 1)}
	manager := &BrowserManager{
		sessions:                 syncutil.NewSyncMap[string, *Page](),
		pageBinds:                map[string]*pageBindState{page.ID: {inFlight: true, page: page}},
		targetSessions:           map[string]*TargetSession{},
		targetSessionsByTarget:   map[string]string{},
		bindingHandlers:          map[string]BindingHandler{handler.Name(): handler},
		bindingNamespaces:        map[string]RuntimeNamespace{handler.Name(): NamespaceIsolatedCore},
		initScriptsByName:        map[string]initScriptRegistration{},
		pendingNavigationReasons: map[navigationReasonKey]pendingNavigationReason{},
	}
	session := manager.rememberTargetSession(TargetSession{
		TargetID:  "iframe-target",
		SessionID: "iframe-session",
		Type:      "iframe",
		ParentID:  page.ID,
	})
	if session.PageID != page.ID {
		t.Fatalf("pending page association = %q, want %q", session.PageID, page.ID)
	}

	manager.handleTargetSessionEvent(CDPResponse{
		Method:    "Runtime.executionContextCreated",
		SessionID: session.SessionID,
		Params: map[string]any{"context": map[string]any{
			"id": 41, "auxData": map[string]any{"frameId": "frame-1", "isDefault": true},
		}},
	})
	if target, ok := page.executionContextTarget(session.SessionID, 41); !ok || target.PageID != page.ID {
		t.Fatalf("pending page target = %+v/%v", target, ok)
	}

	manager.handleTargetSessionEvent(CDPResponse{
		Method:    "Runtime.bindingCalled",
		SessionID: session.SessionID,
		Params: map[string]any{
			"name": handler.Name(), "payload": "{}", "executionContextId": 41,
		},
	})
	select {
	case got := <-handler.called:
		if got != page {
			t.Fatalf("binding page = %p, want %p", got, page)
		}
	case <-time.After(time.Second):
		t.Fatal("pending page binding was not delivered")
	}

	manager.handleTargetSessionEvent(CDPResponse{
		Method:    "Runtime.executionContextDestroyed",
		SessionID: session.SessionID,
		Params:    map[string]any{"executionContextId": 41},
	})
	if _, ok := page.executionContextTarget(session.SessionID, 41); ok {
		t.Fatal("destroyed pending-page context remained")
	}
}

func TestRemoveTargetSessionClearsPendingPageContext(t *testing.T) {
	page := NewPageWithContext(context.Background())
	page.ID = "page-1"
	manager := &BrowserManager{
		sessions:               syncutil.NewSyncMap[string, *Page](),
		pageBinds:              map[string]*pageBindState{page.ID: {inFlight: true, page: page}},
		targetSessions:         map[string]*TargetSession{},
		targetSessionsByTarget: map[string]string{},
	}
	session := manager.rememberTargetSession(TargetSession{
		TargetID:  "iframe-target",
		SessionID: "iframe-session",
		Type:      "iframe",
		ParentID:  page.ID,
	})
	page.storeExecutionContextForSession(session.SessionID, executionContextInfo{ID: 41, FrameID: "frame-1", IsDefault: true})
	if _, ok := page.executionContextTarget(session.SessionID, 41); !ok {
		t.Fatal("pending page context was not stored")
	}

	manager.removeTargetSession(session.SessionID)
	if _, ok := page.executionContextTarget(session.SessionID, 41); ok {
		t.Fatal("detached target session retained pending page context")
	}
}

func TestPageUnbindClearsAllRuntimeAndBindingNotifications(t *testing.T) {
	page := NewPageWithContext(context.Background())
	page.ID = "page-1"
	page.storeExecutionContext(executionContextInfo{ID: 11, FrameID: "root-frame", IsDefault: true})
	page.storeExecutionContextForSession("iframe-session", executionContextInfo{ID: 41, FrameID: "iframe-frame", IsDefault: true})
	page.bindingNotifyPending = []func(){func() {}}
	page.mainFrameID = "root-frame"

	page.unbind()

	page.contextMu.RLock()
	contexts := len(page.executionContexts)
	targets := len(page.executionTargetsByContext)
	page.contextMu.RUnlock()
	page.bindingNotifyMu.Lock()
	notifications := len(page.bindingNotifyPending)
	closed := page.bindingNotifyClosed
	page.bindingNotifyMu.Unlock()
	if contexts != 0 || targets != 0 || notifications != 0 || !closed || page.mainFrame() != "" {
		t.Fatalf("unbind state: contexts=%d targets=%d notifications=%d closed=%v mainFrame=%q", contexts, targets, notifications, closed, page.mainFrame())
	}
}

func TestRemoveTargetSessionDeletesTargetIndex(t *testing.T) {
	manager := &BrowserManager{
		sessions:               syncutil.NewSyncMap[string, *Page](),
		targetSessions:         map[string]*TargetSession{},
		targetSessionsByTarget: map[string]string{},
	}
	manager.rememberTargetSession(TargetSession{
		TargetID:  "iframe-target",
		SessionID: "iframe-session",
		Type:      "iframe",
	})

	manager.removeTargetSession("iframe-session")

	if _, ok := manager.targetSession("iframe-session"); ok {
		t.Fatal("target session was not removed")
	}
	if sessionID := manager.targetSessionsByTarget["iframe-target"]; sessionID != "" {
		t.Fatalf("target index still points to %q", sessionID)
	}
}

func TestPageDefaultContextForFrameTracksLifecycle(t *testing.T) {
	page := &Page{}
	defaultContext := func() (int, bool) {
		page.contextMu.RLock()
		defer page.contextMu.RUnlock()
		key := page.frameContexts["frame-1"]
		return key.ContextID, key.ContextID != 0
	}

	page.storeExecutionContext(executionContextInfo{
		ID:        10,
		FrameID:   "frame-1",
		IsDefault: false,
	})
	if _, ok := defaultContext(); ok {
		t.Fatal("non-default execution context should not be indexed by frame")
	}

	page.storeExecutionContext(executionContextInfo{
		ID:        11,
		FrameID:   "frame-1",
		IsDefault: true,
	})
	if id, ok := defaultContext(); !ok || id != 11 {
		t.Fatalf("default context = %d/%v, want 11/true", id, ok)
	}

	page.removeExecutionContext(10)
	if id, ok := defaultContext(); !ok || id != 11 {
		t.Fatalf("removing non-default context changed default index: %d/%v", id, ok)
	}

	page.removeExecutionContext(11)
	if _, ok := defaultContext(); ok {
		t.Fatal("default context index was not removed")
	}

	page.storeExecutionContext(executionContextInfo{
		ID:        12,
		FrameID:   "frame-1",
		IsDefault: true,
	})
	page.clearExecutionContexts()
	if _, ok := defaultContext(); ok {
		t.Fatal("default context index survived clear")
	}
}

func TestPageExecutionTargetLifecycle(t *testing.T) {
	page := &Page{ID: "page-1"}

	page.storeExecutionContext(executionContextInfo{
		ID:        21,
		FrameID:   "frame-1",
		IsDefault: true,
	})
	page.storeExecutionTarget(ExecutionTarget{
		RuntimeID: "runtime-top",
		PageID:    "page-1",
		ContextID: 21,
		FrameID:   "frame-1",
	})
	target, ok := page.executionTargetByRuntime("runtime-top")
	if !ok {
		t.Fatal("runtime target was not indexed")
	}
	if target.ContextID != 21 || target.SessionID != "" {
		t.Fatalf("runtime target = %+v, want top context 21", target)
	}

	page.removeExecutionContext(21)
	if _, ok := page.executionTargetByRuntime("runtime-top"); ok {
		t.Fatal("runtime target survived execution context removal")
	}

	page.storeExecutionTarget(ExecutionTarget{
		RuntimeID: "runtime-stable",
		PageID:    "page-1",
		ContextID: 22,
		FrameID:   "frame-stable",
	})
	page.storeExecutionTarget(ExecutionTarget{
		PageID:    "page-1",
		ContextID: 22,
		FrameID:   "frame-stable",
	})
	if target, ok := page.executionTargetByRuntime("runtime-stable"); !ok || target.ContextID != 22 {
		t.Fatalf("empty candidate overwrote runtime target: %+v/%v", target, ok)
	}

	page.storeExecutionTarget(ExecutionTarget{
		RuntimeID: "runtime-frame",
		PageID:    "page-1",
		SessionID: "session-1",
		ContextID: 31,
		TargetID:  "target-1",
		FrameID:   "frame-2",
	})
	if _, ok := page.executionTargetByRuntime("runtime-frame"); !ok {
		t.Fatal("session runtime target was not indexed")
	}
	page.clearExecutionTargets("session-1")
	if _, ok := page.executionTargetByRuntime("runtime-frame"); ok {
		t.Fatal("session runtime target survived session clear")
	}
}

func TestRemoveTargetSessionClearsPageExecutionTargets(t *testing.T) {
	page := &Page{ID: "page-1"}
	page.storeExecutionContextForSession("iframe-session", executionContextInfo{
		ID: 41, FrameID: "iframe-frame", IsDefault: true,
	})
	page.storeExecutionTarget(ExecutionTarget{
		RuntimeID: "runtime-frame",
		PageID:    "page-1",
		SessionID: "iframe-session",
		ContextID: 41,
		TargetID:  "iframe-target",
	})
	manager := &BrowserManager{
		sessions:               syncutil.NewSyncMap[string, *Page](),
		targetSessions:         map[string]*TargetSession{},
		targetSessionsByTarget: map[string]string{},
	}
	manager.sessions.Store("page-1", page)
	manager.rememberTargetSession(TargetSession{
		TargetID:  "iframe-target",
		SessionID: "iframe-session",
		Type:      "iframe",
		PageID:    "page-1",
	})

	manager.removeTargetSession("iframe-session")

	if _, ok := page.executionTargetByRuntime("runtime-frame"); ok {
		t.Fatal("runtime target survived target session removal")
	}
	page.contextMu.RLock()
	_, contextExists := page.executionContexts[targetContextKey{SessionID: "iframe-session", ContextID: 41}]
	page.contextMu.RUnlock()
	if contextExists {
		t.Fatal("execution context survived target session removal")
	}
}

func TestExecutionContextIdentityIncludesSession(t *testing.T) {
	page := &Page{ID: "page-1"}
	page.storeExecutionContext(executionContextInfo{ID: 17, FrameID: "top-frame", IsDefault: true})
	page.storeExecutionContextForSession("iframe-session", executionContextInfo{ID: 17, FrameID: "child-frame", IsDefault: true})

	top, topOK := page.executionContextTarget("", 17)
	child, childOK := page.executionContextTarget("iframe-session", 17)
	if !topOK || !childOK || top.SessionID != "" || child.SessionID != "iframe-session" || top.FrameID == child.FrameID {
		t.Fatalf("context collision: top=%+v/%v child=%+v/%v", top, topOK, child, childOK)
	}
	page.removeExecutionContextForSession("iframe-session", 17)
	if _, ok := page.executionContextTarget("iframe-session", 17); ok {
		t.Fatal("session context survived exact removal")
	}
	if _, ok := page.executionContextTarget("", 17); !ok {
		t.Fatal("session context removal deleted the top context with the same id")
	}
}

func TestRuntimeContextResolutionIsDeterministicAndPrefersTopTarget(t *testing.T) {
	page := &Page{ID: "page-1"}
	page.setMainFrame("frame-1")
	page.storeExecutionContextForSession("session-b", executionContextInfo{ID: 91, FrameID: "frame-1", IsDefault: true})
	page.storeExecutionContext(executionContextInfo{ID: 12, FrameID: "frame-1", IsDefault: true})
	page.storeExecutionContext(executionContextInfo{ID: 18, FrameID: "frame-1", IsDefault: true})
	page.storeExecutionContextForSession("session-a", executionContextInfo{ID: 99, FrameID: "frame-1", IsDefault: true})

	for range 20 {
		resolved, ok := page.findRuntimeContext(RuntimeContextOptions{Namespace: NamespacePageMain})
		if !ok || resolved.SessionID != "" || resolved.ContextID != 18 {
			t.Fatalf("resolved context = %+v/%v", resolved, ok)
		}
		contexts := page.runtimeContexts(NamespacePageMain)
		if len(contexts) != 4 || contexts[0].SessionID != "" || contexts[0].ContextID != 18 || contexts[2].SessionID != "session-a" {
			t.Fatalf("runtime context order = %+v", contexts)
		}
	}
}

func TestRuntimeContextResolutionWaitsForAuthoritativePageRoot(t *testing.T) {
	page := &Page{ID: "target-id"}
	page.storeExecutionContext(executionContextInfo{ID: 91, FrameID: "child-frame", IsDefault: true})
	page.storeExecutionContext(executionContextInfo{ID: 12, FrameID: "root-frame", IsDefault: true})

	resolved, ok := page.findRuntimeContext(RuntimeContextOptions{Namespace: NamespacePageMain})
	if !ok || resolved.FrameID != "" || resolved.ContextID != 0 {
		t.Fatalf("default context before root frame = %+v/%v, want unresolved synthetic page context", resolved, ok)
	}
	if got := page.currentMainFrameID(context.Background()); got != "" {
		t.Fatalf("main frame before Page.frameNavigated = %q, want empty", got)
	}

	page.setMainFrame("root-frame")
	resolved, ok = page.findRuntimeContext(RuntimeContextOptions{Namespace: NamespacePageMain})
	if !ok || resolved.FrameID != "root-frame" || resolved.ContextID != 12 {
		t.Fatalf("default context after root frame = %+v/%v, want root page context", resolved, ok)
	}
}

func TestConfirmTopFrameContextUsesRuntimeFrameNotTargetID(t *testing.T) {
	page := &Page{ID: "target-id"}
	page.storeExecutionContext(executionContextInfo{ID: 12, FrameID: "root-frame", IsDefault: false})
	page.topNavigationPending.Store(true)

	if !page.ConfirmTopFrameContext(12) {
		t.Fatal("confirm top frame context returned false")
	}
	if got := page.mainFrame(); got != "root-frame" {
		t.Fatalf("main frame = %q, want runtime frame root-frame", got)
	}
	if !page.topNavigationPending.Load() {
		t.Fatal("runtime confirmation completed an in-flight navigation")
	}
	if page.ConfirmTopFrameContext(99) {
		t.Fatal("unknown execution context was accepted as top frame")
	}
}

func TestStaleProbeEpochCannotReinsertExecutionTarget(t *testing.T) {
	page := &Page{ID: "page-1"}
	target := ExecutionTarget{PageID: "page-1", SessionID: "iframe-session", ContextID: 44, RuntimeID: "stale-runtime"}
	epoch := page.currentTargetEpoch(target)
	page.bumpTargetEpoch(target.SessionID)
	if page.storeExecutionTargetAtEpoch(target, epoch) {
		t.Fatal("stale target was stored after its session epoch advanced")
	}
	if _, ok := page.executionTargetByRuntime(target.RuntimeID); ok {
		t.Fatal("stale runtime target was reinserted")
	}
}

func TestEvaluateInRuntimeContextRequiresRuntimeID(t *testing.T) {
	page := &Page{}
	if _, err := page.evaluateInRuntimeContext(context.Background(),

		"", "1", true); err == nil || !strings.Contains(err.Error(), "runtime id is required") {
		t.Fatalf("expected missing runtime id error, got %v", err)
	}
}

func TestExecutionTargetMissingRuntimeIncludesDiagnostics(t *testing.T) {
	previousTimeout := executionTargetLookupTimeout
	previousInterval := executionTargetLookupInterval
	executionTargetLookupTimeout = 5 * time.Millisecond
	executionTargetLookupInterval = time.Millisecond
	t.Cleanup(func() {
		executionTargetLookupTimeout = previousTimeout
		executionTargetLookupInterval = previousInterval
	})

	page := &Page{ID: "page-1"}
	page.storeExecutionTarget(ExecutionTarget{
		PageID:    "page-1",
		ContextID: 11,
		FrameID:   "top-frame",
	})
	page.storeExecutionTarget(ExecutionTarget{
		PageID:    "page-1",
		SessionID: "session-1",
		ContextID: 21,
		TargetID:  "target-1",
		FrameID:   "child-frame",
	})

	_, err := page.executionTargetForRuntime("runtime-missing")
	if err == nil {
		t.Fatal("expected missing runtime error")
	}
	message := err.Error()
	if !strings.Contains(message, "runtime-missing") ||
		!strings.Contains(message, "top_contexts=1") ||
		!strings.Contains(message, "session_contexts=1") {
		t.Fatalf("missing runtime error lacks diagnostics: %v", err)
	}
}

func TestSelectorTargetRefBecomesStaleAcrossEpochBump(t *testing.T) {
	page := &Page{ID: "page-1"}
	target := ExecutionTarget{
		RuntimeID: "runtime-frame",
		PageID:    "page-1",
		SessionID: "session-1",
		ContextID: 31,
		TargetID:  "target-1",
		FrameID:   "frame-1",
	}
	page.bumpTargetEpoch(target.SessionID)
	page.storeExecutionTarget(target)
	epoch := page.currentTargetEpoch(target)

	gotTarget, backendNodeID, err := page.selectorTargetTargetAndBackendNodeID(context.Background(),

		SelectorTargetRef{
			RuntimeID:     target.RuntimeID,
			BackendNodeID: 77,
			Target:        target,
			Epoch:         epoch,
		})
	if err != nil {
		t.Fatalf("resolve selector target: %v", err)
	}
	if gotTarget.SessionID != target.SessionID || gotTarget.ContextID != target.ContextID || backendNodeID != 77 {
		t.Fatalf("selector target = %+v backend=%d, want %+v backend=77", gotTarget, backendNodeID, target)
	}

	page.bumpTargetEpoch(target.SessionID)
	_, _, err = page.selectorTargetTargetAndBackendNodeID(context.Background(),

		SelectorTargetRef{
			RuntimeID:     target.RuntimeID,
			BackendNodeID: 77,
			Target:        target,
			Epoch:         epoch,
		})
	if err == nil || !strings.Contains(err.Error(), "stale_target") {
		t.Fatalf("expected stale target after epoch bump, got %v", err)
	}
}

func TestNodeBackendCacheIsSessionScoped(t *testing.T) {
	page := &Page{}
	page.rememberNodeBackendInTarget(ExecutionTarget{SessionID: "session-a"}, 10, 101)
	page.rememberNodeBackendInTarget(ExecutionTarget{SessionID: "session-b"}, 10, 202)

	if got, ok := page.backendIDForNodeInTarget(ExecutionTarget{SessionID: "session-a"}, 10); !ok || got != 101 {
		t.Fatalf("session-a backend = %d/%v, want 101/true", got, ok)
	}
	if got, ok := page.backendIDForNodeInTarget(ExecutionTarget{SessionID: "session-b"}, 10); !ok || got != 202 {
		t.Fatalf("session-b backend = %d/%v, want 202/true", got, ok)
	}
	if _, ok := page.backendIDForNodeInTarget(topPageExecutionTarget(), 10); ok {
		t.Fatal("top target unexpectedly reused session scoped node cache")
	}
}
