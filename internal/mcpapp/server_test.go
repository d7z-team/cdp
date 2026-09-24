package mcpapp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"gopkg.d7z.net/cdp"
)

func TestTypedToolRegistration(t *testing.T) {
	handler, err := newTestApp(t, HTTPOptions{})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: httpServer.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(result.Tools))
	for _, tool := range result.Tools {
		if tool.InputSchema == nil || tool.OutputSchema == nil {
			t.Fatalf("tool %s lacks typed schema", tool.Name)
		}
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	want := []string{
		"browser_check", "browser_click", "browser_console", "browser_dialog", "browser_drag",
		"browser_evaluate", "browser_find", "browser_history", "browser_hover", "browser_navigate",
		"browser_press_key", "browser_requests", "browser_screenshot", "browser_scroll", "browser_select",
		"browser_snapshot", "browser_tabs", "browser_type", "browser_upload", "browser_wait",
	}
	if len(names) != len(want) {
		t.Fatalf("tools = %v", names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("tools = %v", names)
		}
	}
}

func TestTabGateCancellationAndIndependence(t *testing.T) {
	pageA := new(cdp.Page)
	pageB := new(cdp.Page)

	a := &tabRuntime{id: "a", page: pageA, gate: make(chan struct{}, 1), state: TabReady}
	b := &tabRuntime{id: "b", page: pageB, gate: make(chan struct{}, 1), state: TabReady}
	a.gate <- struct{}{}
	b.gate <- struct{}{}
	if err := a.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("queued acquire = %v", err)
	}
	if err := b.acquire(context.Background()); err != nil {
		t.Fatalf("other tab blocked: %v", err)
	}
	b.release()
	a.release()
}

func TestQueuedOperationDoesNotOverwriteActivePhase(t *testing.T) {
	page := new(cdp.Page)

	runtime := &tabRuntime{id: "tab", page: page, gate: make(chan struct{}, 1), state: TabReady, phase: phaseExecuting}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.acquire(ctx) }()
	time.Sleep(10 * time.Millisecond)
	if phase := runtime.currentPhase(); phase != phaseExecuting {
		t.Fatalf("queued request overwrote active phase: %s", phase)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("queued acquire=%v", err)
	}
	if phase := runtime.currentPhase(); phase != phaseCanceled {
		t.Fatalf("canceled phase=%s", phase)
	}
}

func TestEventRingCursorAndBound(t *testing.T) {
	ring := newEventRing(3)
	for i := 0; i < 5; i++ {
		ring.add("request", map[string]any{"index": i})
	}
	events, cursor := ring.after(0, 10, func(Event) bool { return true })
	if len(events) != 3 || events[0].Cursor != 3 || cursor != 5 {
		t.Fatalf("events=%+v cursor=%d", events, cursor)
	}
	if next, _ := ring.after(cursor, 10, func(Event) bool { return true }); len(next) != 0 {
		t.Fatalf("events after cursor = %+v", next)
	}
}

func TestEffectiveStateAndModalGateOwnership(t *testing.T) {
	page := new(cdp.Page)
	runtime := &tabRuntime{
		id: "tab", page: page, gate: make(chan struct{}, 1), dialogGate: make(chan struct{}, 1),
		state: TabFileChooser, phase: phaseCompleted,
	}
	runtime.gate <- struct{}{}
	runtime.dialogGate <- struct{}{}
	if got := runtime.stateWithDialog(true); got != TabDialog {
		t.Fatalf("dialog did not override file chooser: %s", got)
	}
	runtime.setState(TabClosed)
	if got := runtime.stateWithDialog(true); got != TabClosed {
		t.Fatalf("dialog overrode closed state: %s", got)
	}
	runtime.setState(TabReady)

	if err := runtime.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	actionDone := make(chan error, 1)
	runtime.transferGateToPending(actionDone)
	blockedCtx, cancelBlocked := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelBlocked()
	if err := runtime.acquire(blockedCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("pending action did not retain gate: %v", err)
	}
	pendingErr := errors.New("action failed after dialog")
	actionDone <- pendingErr
	if _, _, err := runtime.waitPendingActionOrDialog(context.Background()); !errors.Is(err, pendingErr) {
		t.Fatalf("pending action error = %v", err)
	}
	if err := runtime.acquire(context.Background()); err != nil {
		t.Fatalf("pending action did not release gate: %v", err)
	}
	runtime.release()

	if err := runtime.acquireDialog(context.Background()); err != nil {
		t.Fatal(err)
	}
	dialogCtx, cancelDialog := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelDialog()
	if err := runtime.acquireDialog(dialogCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("dialog gate was not serialized: %v", err)
	}
	runtime.releaseDialog()
	if err := runtime.acquireDialog(context.Background()); err != nil {
		t.Fatalf("dialog gate was not released: %v", err)
	}
	runtime.releaseDialog()
}

func TestEventBurstAndDebugFrameState(t *testing.T) {
	ring := newEventRing(512)
	for i := 0; i < 250; i++ {
		ring.add("console", map[string]any{"index": i})
	}
	cursor, count, batches := uint64(0), 0, 0
	for {
		events, next := ring.after(cursor, 100, func(Event) bool { return true })
		cursor = next
		count += len(events)
		if len(events) == 0 {
			break
		}
		batches++
		if len(events) < 100 {
			break
		}
	}
	if count != 250 || batches != 3 || cursor != 250 {
		t.Fatalf("burst count=%d batches=%d cursor=%d", count, batches, cursor)
	}

	frames := &debugFrameState{}
	input := &debugFrameInfo{Sequence: 7, CSSViewportWidth: 800, CSSViewportHeight: 600}
	frames.store(input)
	input.Sequence = 8
	first := frames.load()
	first.CSSViewportWidth = 1
	second := frames.load()
	if first.Sequence != 7 || second.Sequence != 7 || second.CSSViewportWidth != 800 {
		t.Fatalf("frame state did not copy values: first=%+v second=%+v", first, second)
	}
}

func TestNormalizeToolErrorPreservesBrowserCause(t *testing.T) {
	cause := errors.New("cdp failed")
	browserErr := &cdp.BrowserError{Op: "selector.actionability", Kind: "not_actionable", Message: "covered", Cause: cause}
	got := normalizeToolError("browser_click", "s1/e2", browserErr)
	if got.Code != "not_actionable" || got.Op != "selector.actionability" || got.Ref != "s1/e2" || !errors.Is(got, cause) {
		t.Fatalf("error = %+v unwrap=%v", got, errors.Unwrap(got))
	}
}

func TestWaitRequestsIgnoresEarlierRequests(t *testing.T) {
	observer := &tabObserver{inflight: map[string]observedRequest{"old": {started: 2, tracked: true}}, wake: make(chan struct{}, 1)}
	start := time.Now()
	if observer.waitRequests(context.Background(), 2) {
		t.Fatal("earlier request should not block settle")
	}
	if time.Since(start) > 50*time.Millisecond {
		t.Fatal("earlier request delayed settle")
	}
}

func TestDebugHTTPAndAssets(t *testing.T) {
	handler, err := newTestApp(t, HTTPOptions{})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	response, err := http.Get(httpServer.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	if response.Request.URL.Path != "/debug" || response.StatusCode != http.StatusOK {
		t.Fatalf("root redirect path=%q status=%d", response.Request.URL.Path, response.StatusCode)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if !strings.Contains(string(body), "CDP Remote Debug") || strings.Contains(string(body), `src="https://`) || strings.Contains(string(body), `href="https://`) {
		t.Fatalf("unexpected debug document: %s", body)
	}
	for _, asset := range []string{"/debug/assets/app.js", "/debug/assets/app.css"} {
		assetResponse, requestErr := http.Get(httpServer.URL + asset)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		assetBody, _ := io.ReadAll(assetResponse.Body)
		_ = assetResponse.Body.Close()
		if assetResponse.StatusCode != http.StatusOK || len(assetBody) < 100 {
			t.Fatalf("asset %s status=%d bytes=%d", asset, assetResponse.StatusCode, len(assetBody))
		}
		if asset == "/debug/assets/app.js" {
			for _, required := range []string{"selectionGeneration", "pendingFrameMeta", "frame_sequence: point.frameSequence", "resetPendingControls"} {
				if !strings.Contains(string(assetBody), required) {
					t.Fatalf("debug app is missing %q", required)
				}
			}
		}
	}
	request, _ := http.NewRequest(http.MethodGet, httpServer.URL+"/api/debug/tools/browser_tabs", nil)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET tool status=%d", response.StatusCode)
	}
	for _, test := range []struct {
		name   string
		path   string
		body   string
		status int
	}{
		{name: "unknown", path: "/api/debug/tools/not_a_tool", body: `{}`, status: http.StatusNotFound},
		{name: "invalid json", path: "/api/debug/tools/browser_tabs", body: `{`, status: http.StatusBadRequest},
	} {
		response, err = http.Post(httpServer.URL+test.path, "application/json", strings.NewReader(test.body))
		if err != nil {
			t.Fatal(err)
		}
		var output map[string]any
		decodeErr := json.NewDecoder(response.Body).Decode(&output)
		_ = response.Body.Close()
		if response.StatusCode != test.status || decodeErr != nil || output["ok"] != false {
			t.Fatalf("%s status=%d output=%v decode=%v", test.name, response.StatusCode, output, decodeErr)
		}
	}
}

func TestObserverSubscribersAndSettleWakeAreIndependent(t *testing.T) {
	observer := &tabObserver{wake: make(chan struct{}, 1), watchers: map[chan struct{}]struct{}{}}
	first, unsubscribeFirst := observer.subscribe()
	second, unsubscribeSecond := observer.subscribe()
	defer unsubscribeSecond()
	observer.signal()
	for name, channel := range map[string]<-chan struct{}{"first": first, "second": second, "settle": observer.wake} {
		select {
		case <-channel:
		case <-time.After(time.Second):
			t.Fatalf("%s subscriber was not notified", name)
		}
	}
	unsubscribeFirst()
	observer.signal()
	select {
	case <-first:
		t.Fatal("unsubscribed observer was notified")
	default:
	}
	select {
	case <-second:
	case <-time.After(time.Second):
		t.Fatal("remaining observer was not notified")
	}
}

func TestUploadWaitsForActionInsideExistingFileChooser(t *testing.T) {
	page := new(cdp.Page)
	runtime := &tabRuntime{page: page, state: TabFileChooser, observer: &tabObserver{wake: make(chan struct{}, 1)}}
	actionRelease := make(chan struct{})
	done := make(chan struct {
		completed bool
		modal     TabState
		err       error
	}, 1)
	go func() {
		completed, modal, _, err := waitForActionOrModal(context.Background(), runtime, true, func(context.Context) error {
			<-actionRelease
			return nil
		})
		done <- struct {
			completed bool
			modal     TabState
			err       error
		}{completed: completed, modal: modal, err: err}
	}()
	runtime.observer.signal()
	select {
	case result := <-done:
		t.Fatalf("file chooser interrupted upload action: %+v", result)
	case <-time.After(20 * time.Millisecond):
	}
	close(actionRelease)
	result := <-done
	if !result.completed || result.modal != TabFileChooser || result.err != nil {
		t.Fatalf("upload action result=%+v", result)
	}
}

func TestActionReturnsWhenFileChooserOpens(t *testing.T) {
	page := new(cdp.Page)
	runtime := &tabRuntime{page: page, state: TabReady, observer: &tabObserver{wake: make(chan struct{}, 1)}}
	actionRelease := make(chan struct{})
	done := make(chan struct {
		completed  bool
		modal      TabState
		actionDone <-chan error
		err        error
	}, 1)
	go func() {
		completed, modal, actionDone, err := waitForActionOrModal(context.Background(), runtime, false, func(context.Context) error {
			<-actionRelease
			return nil
		})
		done <- struct {
			completed  bool
			modal      TabState
			actionDone <-chan error
			err        error
		}{completed: completed, modal: modal, actionDone: actionDone, err: err}
	}()
	runtime.setState(TabFileChooser)
	result := <-done
	if result.completed || result.modal != TabFileChooser || result.actionDone == nil || result.err != nil {
		t.Fatalf("file chooser action result=%+v", result)
	}
	close(actionRelease)
	if err := <-result.actionDone; err != nil {
		t.Fatal(err)
	}
}

func TestDebugViewerReferencesAndLatestFrameQueue(t *testing.T) {
	page := new(cdp.Page)

	runtime := &tabRuntime{id: "tab", page: page, gate: make(chan struct{}, 1), state: TabReady, phase: phaseCompleted}
	runtime.gate <- struct{}{}
	viewer := &debugViewer{count: 2}
	hub := &debugHub{viewers: map[string]*debugViewer{"tab": viewer}}
	if count := hub.viewerCount(); count != 2 {
		t.Fatalf("viewer count=%d", count)
	}
	hub.detach(runtime)
	if count := hub.viewerCount(); count != 1 {
		t.Fatalf("viewer count after first detach=%d", count)
	}
	hub.detach(runtime)
	if count := hub.viewerCount(); count != 0 {
		t.Fatalf("viewer count after last detach=%d", count)
	}

	frames := make(chan debugOutbound, 1)
	ctx := context.Background()
	if !queueLatestFrame(ctx, frames, debugOutbound{frame: []byte("old")}) || !queueLatestFrame(ctx, frames, debugOutbound{frame: []byte("new")}) {
		t.Fatal("queue rejected a frame")
	}
	if got := string((<-frames).frame); got != "new" {
		t.Fatalf("queued frame=%q", got)
	}
}

func newTestApp(t *testing.T, options HTTPOptions) (*App, error) {
	t.Helper()
	app, err := New(nil, Config{AllowedOrigins: options.AllowedOrigins, EnableDebug: true})
	if err == nil {
		t.Cleanup(func() { _ = app.Close() })
	}
	return app, err
}
