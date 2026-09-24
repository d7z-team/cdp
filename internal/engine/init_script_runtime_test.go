package engine

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func waitForInstallInflight(t *testing.T, manager *BrowserManager, key initScriptInstallKey) *initScriptInstallCall {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		manager.initScriptRuntimeMu.Lock()
		call := manager.initScriptInstallInflight[key]
		manager.initScriptRuntimeMu.Unlock()
		if call != nil {
			return call
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for install in-flight call")
	return nil
}

func waitForEnsureInflight(t *testing.T, manager *BrowserManager, key initScriptEnsureKey) *initScriptEnsureCall {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		manager.initScriptRuntimeMu.Lock()
		call := manager.initScriptEnsureInflight[key]
		manager.initScriptRuntimeMu.Unlock()
		if call != nil {
			return call
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for ensure in-flight call")
	return nil
}

func TestInstallInitScriptOnceCoalescesConcurrentCalls(t *testing.T) {
	manager := &BrowserManager{
		initScriptInstalls:        map[initScriptInstallKey]initScriptInstallState{},
		initScriptInstallInflight: map[initScriptInstallKey]*initScriptInstallCall{},
	}
	key := initScriptInstallKey{PageID: "page-1", ScriptName: "core.js", WorldName: "__cdp_isolated_core"}
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	install := func(context.Context) (string, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return "script-id", nil
	}

	const goroutines = 8
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	wg.Add(1)
	go func() {
		defer wg.Done()
		errs <- manager.installInitScriptOnce(context.Background(), key, install)
	}()
	<-started
	for range goroutines - 1 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- manager.installInitScriptOnce(context.Background(), key, install)
		}()
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("installInitScriptOnce returned error: %v", err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("install called %d times, want 1", got)
	}
	state, ok := manager.registeredInitScriptsSnapshot()[key]
	if !ok || state.Identifier != "script-id" {
		t.Fatalf("registered state = %#v, %v; want script-id", state, ok)
	}
}

func TestInstallInitScriptOnceRetriesAfterFailure(t *testing.T) {
	manager := &BrowserManager{}
	key := initScriptInstallKey{PageID: "page-1", ScriptName: "test-runtime.js", WorldName: "__cdp_isolated_core"}
	fail := errors.New("boom")
	var calls atomic.Int32

	err := manager.installInitScriptOnce(context.Background(), key, func(context.Context) (string, error) {
		calls.Add(1)
		return "", fail
	})
	if !errors.Is(err, fail) {
		t.Fatalf("first error = %v, want %v", err, fail)
	}
	err = manager.installInitScriptOnce(context.Background(), key, func(context.Context) (string, error) {
		calls.Add(1)
		return "script-id", nil
	})
	if err != nil {
		t.Fatalf("second install error: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("install called %d times, want 2", got)
	}
}

func TestEnsureInitScriptOnceCoalescesConcurrentCalls(t *testing.T) {
	manager := &BrowserManager{
		initScriptEnsureInflight: map[initScriptEnsureKey]*initScriptEnsureCall{},
	}
	key := initScriptEnsureKey{PageID: "page-1", ContextID: 7, ScriptName: "test-webassets.js"}
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	ensure := func(context.Context) error {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return nil
	}

	const goroutines = 8
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	wg.Add(1)
	go func() {
		defer wg.Done()
		errs <- manager.ensureInitScriptOnce(context.Background(), key, ensure)
	}()
	<-started
	for range goroutines - 1 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- manager.ensureInitScriptOnce(context.Background(), key, ensure)
		}()
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("ensureInitScriptOnce returned error: %v", err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("ensure called %d times, want 1", got)
	}
}

func TestForgetInitScriptRuntimeReleasesWaiters(t *testing.T) {
	manager := &BrowserManager{
		initScriptInstalls: map[initScriptInstallKey]initScriptInstallState{
			{PageID: "page-1", ScriptName: "core.js"}:                         {Identifier: "top-id"},
			{PageID: "page-1", SessionID: "session-1", ScriptName: "core.js"}: {Identifier: "frame-id"},
			{PageID: "page-2", ScriptName: "core.js"}:                         {Identifier: "other-id"},
		},
		initScriptEnsureInflight: map[initScriptEnsureKey]*initScriptEnsureCall{
			{PageID: "page-1", SessionID: "session-1", ContextID: 2, ScriptName: "core.js"}: {done: make(chan struct{})},
		},
	}
	call := manager.initScriptEnsureInflight[initScriptEnsureKey{PageID: "page-1", SessionID: "session-1", ContextID: 2, ScriptName: "core.js"}]

	manager.forgetInitScriptRuntime("page-1", "")

	select {
	case <-call.done:
	case <-time.After(time.Second):
		t.Fatal("waiting ensure call was not released")
	}
	if err := call.result(); !errors.Is(err, ErrBrowserClosed) {
		t.Fatalf("released error = %v, want ErrBrowserClosed", err)
	}
	snapshot := manager.registeredInitScriptsSnapshot()
	if _, ok := snapshot[initScriptInstallKey{PageID: "page-1", ScriptName: "core.js"}]; ok {
		t.Fatal("top page init script state was not removed")
	}
	if _, ok := snapshot[initScriptInstallKey{PageID: "page-1", SessionID: "session-1", ScriptName: "core.js"}]; ok {
		t.Fatal("session init script state was not removed")
	}
	if _, ok := snapshot[initScriptInstallKey{PageID: "page-2", ScriptName: "core.js"}]; !ok {
		t.Fatal("unrelated page init script state was removed")
	}
}

func TestForgetRootInitScriptRuntimePreservesTargetSessions(t *testing.T) {
	rootInstall := initScriptInstallKey{PageID: "page-1", ScriptName: "core.js"}
	sessionInstall := initScriptInstallKey{PageID: "page-1", SessionID: "session-1", ScriptName: "core.js"}
	rootEnsure := initScriptEnsureKey{PageID: "page-1", ContextID: 1, ScriptName: "core.js"}
	sessionEnsure := initScriptEnsureKey{PageID: "page-1", SessionID: "session-1", ContextID: 2, ScriptName: "core.js"}
	manager := &BrowserManager{
		initScriptInstalls: map[initScriptInstallKey]initScriptInstallState{
			rootInstall:    {Identifier: "root-id"},
			sessionInstall: {Identifier: "session-id"},
		},
		initScriptEnsureInflight: map[initScriptEnsureKey]*initScriptEnsureCall{
			rootEnsure:    {done: make(chan struct{})},
			sessionEnsure: {done: make(chan struct{})},
		},
	}

	manager.forgetRootInitScriptRuntime("page-1")
	if _, ok := manager.initScriptInstalls[rootInstall]; ok {
		t.Fatal("root init-script install was retained")
	}
	if _, ok := manager.initScriptInstalls[sessionInstall]; !ok {
		t.Fatal("target-session init-script install was removed with root state")
	}
	select {
	case <-manager.initScriptEnsureInflight[sessionEnsure].done:
		t.Fatal("target-session ensure call was abandoned with root state")
	default:
	}
	if _, ok := manager.initScriptEnsureInflight[rootEnsure]; ok {
		t.Fatal("root ensure call was retained")
	}
}

func TestRegisteredInitScriptsSnapshotForPageIncludesTargetSessions(t *testing.T) {
	manager := &BrowserManager{
		initScriptInstalls: map[initScriptInstallKey]initScriptInstallState{
			{PageID: "page-1", ScriptName: "core.js"}:                                     {Identifier: "top-1"},
			{PageID: "page-1", SessionID: "session-1", ScriptName: "test-runtime.js"}:     {Identifier: "frame-1"},
			{SessionID: "unassociated-session", ScriptName: "core.js"}:                    {Identifier: "unassociated-frame"},
			{PageID: "page-2", ScriptName: "core.js"}:                                     {Identifier: "top-2"},
			{PageID: "page-2", SessionID: "other-session", ScriptName: "test-runtime.js"}: {Identifier: "frame-2"},
		},
		targetSessions: map[string]*TargetSession{
			"session-1":            {SessionID: "session-1", PageID: "page-1"},
			"unassociated-session": {SessionID: "unassociated-session", PageID: "page-1"},
			"other-session":        {SessionID: "other-session", PageID: "page-2"},
		},
	}

	snapshot := manager.registeredInitScriptsSnapshotForPage("page-1")
	if len(snapshot) != 3 {
		t.Fatalf("page-1 install count = %d, want 3: %+v", len(snapshot), snapshot)
	}
	for key := range snapshot {
		if key.PageID == "page-2" || key.SessionID == "other-session" {
			t.Fatalf("page-1 snapshot contains unrelated install: %+v", key)
		}
	}
	if _, ok := snapshot[initScriptInstallKey{SessionID: "unassociated-session", ScriptName: "core.js"}]; !ok {
		t.Fatal("page-1 snapshot omitted target-session install recorded before page association")
	}
}

func TestInstallInitScriptAbandonedCallDoesNotDeleteNewInflight(t *testing.T) {
	manager := &BrowserManager{}
	key := initScriptInstallKey{PageID: "page-1", ScriptName: "core.js", WorldName: "__cdp_isolated_core"}
	oldRelease := make(chan struct{})
	oldDone := make(chan error, 1)

	go func() {
		oldDone <- manager.installInitScriptOnce(context.Background(), key, func(context.Context) (string, error) {
			<-oldRelease
			return "old-id", nil
		})
	}()
	oldCall := waitForInstallInflight(t, manager, key)
	manager.forgetInitScriptRuntime("page-1", "")

	newRelease := make(chan struct{})
	newDone := make(chan error, 1)
	go func() {
		newDone <- manager.installInitScriptOnce(context.Background(), key, func(context.Context) (string, error) {
			<-newRelease
			return "new-id", nil
		})
	}()
	newCall := waitForInstallInflight(t, manager, key)
	if newCall == oldCall {
		t.Fatal("expected a new in-flight call after forget")
	}

	close(oldRelease)
	if err := <-oldDone; !errors.Is(err, ErrBrowserClosed) {
		t.Fatalf("old install error = %v, want ErrBrowserClosed", err)
	}
	if got := waitForInstallInflight(t, manager, key); got != newCall {
		t.Fatal("old install completion removed the new in-flight call")
	}

	close(newRelease)
	if err := <-newDone; err != nil {
		t.Fatalf("new install error: %v", err)
	}
	state, ok := manager.registeredInitScriptsSnapshot()[key]
	if !ok || state.Identifier != "new-id" {
		t.Fatalf("registered state = %#v, %v; want new-id", state, ok)
	}
}

func TestEnsureInitScriptAbandonedCallDoesNotDeleteNewInflight(t *testing.T) {
	manager := &BrowserManager{}
	key := initScriptEnsureKey{PageID: "page-1", ContextID: 7, ScriptName: "core.js"}
	oldRelease := make(chan struct{})
	oldDone := make(chan error, 1)

	go func() {
		oldDone <- manager.ensureInitScriptOnce(context.Background(), key, func(context.Context) error {
			<-oldRelease
			return nil
		})
	}()
	oldCall := waitForEnsureInflight(t, manager, key)
	manager.forgetInitScriptRuntime("page-1", "")

	newRelease := make(chan struct{})
	newDone := make(chan error, 1)
	go func() {
		newDone <- manager.ensureInitScriptOnce(context.Background(), key, func(context.Context) error {
			<-newRelease
			return nil
		})
	}()
	newCall := waitForEnsureInflight(t, manager, key)
	if newCall == oldCall {
		t.Fatal("expected a new in-flight call after forget")
	}

	close(oldRelease)
	if err := <-oldDone; !errors.Is(err, ErrBrowserClosed) {
		t.Fatalf("old ensure error = %v, want ErrBrowserClosed", err)
	}
	if got := waitForEnsureInflight(t, manager, key); got != newCall {
		t.Fatal("old ensure completion removed the new in-flight call")
	}

	close(newRelease)
	if err := <-newDone; err != nil {
		t.Fatalf("new ensure error: %v", err)
	}
}
