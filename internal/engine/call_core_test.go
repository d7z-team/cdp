package engine

import (
	"context"
	"testing"

	"gopkg.d7z.net/cdp/internal/syncutil"
)

func TestCoreRuntimeReadyAcceptsOnlyCurrentTopRootContext(t *testing.T) {
	manager := &BrowserManager{
		sessions:          syncutil.NewSyncMap[string, *Page](),
		namespaces:        defaultNamespaceRegistrations(),
		initScriptsByName: map[string]initScriptRegistration{"core.js": {script: InitScript{Name: "core.js", Namespace: NamespaceIsolatedCore, Exec: "void 0"}}},
	}
	manager.setState(managerStateRunning)
	page := NewPageWithContext(context.Background())
	page.ID = "page-1"
	page.manager = manager
	page.executionContexts = map[targetContextKey]executionContextInfo{
		{ContextID: 7}: {ID: 7, FrameID: "top-frame", Name: worldNameIsolatedCore},
	}
	page.bumpTargetEpoch("")
	manager.sessions.Store(page.ID, page)
	manager.publishPageRuntimeReady(page)

	call := CallCore{}
	call.CoreRuntimeReady(GoCallContext{Page: page, Manager: manager, ExecutionContextID: 7}, false)
	if frameID := page.mainFrame(); frameID != "" {
		t.Fatalf("non-top runtime set main frame %q", frameID)
	}
	call.CoreRuntimeReady(GoCallContext{Page: page, Manager: manager, SessionID: "oopif", ExecutionContextID: 7}, true)
	if frameID := page.mainFrame(); frameID != "" {
		t.Fatalf("target-session runtime set main frame %q", frameID)
	}
	call.CoreRuntimeReady(GoCallContext{Page: page, Manager: manager, ExecutionContextID: 99}, true)
	if frameID := page.mainFrame(); frameID != "" {
		t.Fatalf("unknown runtime context set main frame %q", frameID)
	}

	replacement := NewPageWithContext(context.Background())
	replacement.ID = page.ID
	replacement.manager = manager
	manager.sessions.Store(page.ID, replacement)
	call.CoreRuntimeReady(GoCallContext{Page: page, Manager: manager, ExecutionContextID: 7}, true)
	if frameID := page.mainFrame(); frameID != "" {
		t.Fatalf("stale page runtime set main frame %q", frameID)
	}

	manager.sessions.Store(page.ID, page)
	call.CoreRuntimeReady(GoCallContext{Page: page, Manager: manager, ExecutionContextID: 7}, true)
	if frameID := page.mainFrame(); frameID != "top-frame" {
		t.Fatalf("top runtime main frame = %q, want top-frame", frameID)
	}
	page.runtimeReadyMu.Lock()
	ready := page.runtimeReadyContexts["core.js"]
	page.runtimeReadyMu.Unlock()
	if ready.contextID != 7 || ready.frameID != "top-frame" || ready.generation != page.RuntimeGeneration() {
		t.Fatalf("core runtime-ready identity = %+v", ready)
	}
}

func TestWaitMainFrameUsesEventAndContext(t *testing.T) {
	pageCtx, cancelPage := context.WithCancel(context.Background())
	defer cancelPage()
	page := NewPageWithContext(pageCtx)

	result := make(chan string, 1)
	go func() {
		frameID, _ := page.waitMainFrame(context.Background())
		result <- frameID
	}()
	page.setMainFrame("root-frame")
	if frameID := <-result; frameID != "root-frame" {
		t.Fatalf("waited frame = %q, want root-frame", frameID)
	}

	empty := NewPageWithContext(pageCtx)
	waitCtx, cancelWait := context.WithCancel(context.Background())
	cancelWait()
	if _, err := empty.waitMainFrame(waitCtx); err == nil {
		t.Fatal("canceled main-frame wait returned no error")
	}
}
