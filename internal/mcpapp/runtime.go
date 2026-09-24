package mcpapp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"gopkg.d7z.net/cdp"
	"gopkg.d7z.net/cdp/snapshot"
)

type TabState string

const (
	TabReady       TabState = "ready"
	TabNavigating  TabState = "navigating"
	TabDialog      TabState = "dialog"
	TabFileChooser TabState = "file_chooser"
	TabClosed      TabState = "closed"
)

type operationPhase string

const (
	phaseQueued     operationPhase = "queued"
	phaseResolving  operationPhase = "resolving"
	phaseActionable operationPhase = "actionable"
	phaseExecuting  operationPhase = "executing"
	phaseSettling   operationPhase = "settling"
	phaseCapturing  operationPhase = "capturing"
	phaseCompleted  operationPhase = "completed"
	phaseFailed     operationPhase = "failed"
	phaseCanceled   operationPhase = "canceled"
)

type tabRuntime struct {
	lifetime   context.Context
	done       <-chan struct{}
	id         string
	page       *cdp.Page
	gate       chan struct{}
	dialogGate chan struct{}
	mu         sync.RWMutex
	state      TabState
	phase      operationPhase
	observer   *tabObserver
	pendingMu  sync.Mutex
	pending    *pendingAction
}

type pendingAction struct {
	done chan struct{}
	err  error
}

func newTabRuntime(ctx context.Context, page *cdp.Page, diagnostics cdp.DiagnosticsMode) *tabRuntime {
	runtime := &tabRuntime{
		lifetime: ctx, id: page.ID(), page: page, done: page.Done(), gate: make(chan struct{}, 1), dialogGate: make(chan struct{}, 1),
		state: TabReady, phase: phaseCompleted,
	}
	runtime.gate <- struct{}{}
	runtime.dialogGate <- struct{}{}
	// An already-open JavaScript dialog can block Page commands. Runtime attach
	// must remain available so browser_dialog can close that dialog.
	go func() {
		_ = page.Session().Call(ctx, "Page.setInterceptFileChooserDialog", map[string]any{"enabled": true}, nil)
	}()
	runtime.observer = newTabObserver(ctx, page, runtime, diagnostics)
	return runtime
}

func (t *tabRuntime) acquire(ctx context.Context) error {
	if err := t.lock(ctx); err != nil {
		t.setPhase(phaseCanceled)
		return err
	}
	t.setPhase(phaseQueued)
	return nil
}

func (t *tabRuntime) lock(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.done:
		t.setState(TabClosed)
		return NewToolError("tab_not_found", "operation.acquire", "tab is closed", cdp.ErrClosed)
	case <-t.gate:
		return nil
	}
}

func (t *tabRuntime) release() {
	select {
	case t.gate <- struct{}{}:
	default:
	}
}

func (t *tabRuntime) acquireModalSensitive(ctx context.Context, op string, allowFileChooser bool) error {
	if err := t.currentModalError(op, allowFileChooser); err != nil {
		return err
	}
	if err := t.acquire(ctx); err != nil {
		return err
	}
	if err := t.currentModalError(op, allowFileChooser); err != nil {
		t.release()
		return err
	}
	return nil
}

func (t *tabRuntime) currentModalError(op string, allowFileChooser bool) error {
	state := t.currentState()
	if allowFileChooser && state == TabFileChooser {
		return nil
	}
	return modalError(op, state)
}

func (t *tabRuntime) acquireDialog(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.done:
		return NewToolError("tab_not_found", "browser_dialog", "tab is closed", cdp.ErrClosed)
	case <-t.dialogGate:
		return nil
	}
}

func (t *tabRuntime) releaseDialog() {
	select {
	case t.dialogGate <- struct{}{}:
	default:
	}
}

func (t *tabRuntime) transferGateToPending(actionDone <-chan error) {
	pending := &pendingAction{done: make(chan struct{})}
	t.pendingMu.Lock()
	t.pending = pending
	t.pendingMu.Unlock()
	go func() {
		pending.err = <-actionDone
		close(pending.done)
		t.release()
	}()
}

func (t *tabRuntime) waitPendingActionOrDialog(ctx context.Context) (cdp.Dialog, bool, error) {
	for {
		dialog, open, dialogChanged := t.page.DialogState()
		if open {
			return dialog, true, nil
		}
		t.pendingMu.Lock()
		pending := t.pending
		t.pendingMu.Unlock()
		if pending == nil {
			return cdp.Dialog{}, false, nil
		}
		select {
		case <-ctx.Done():
			return cdp.Dialog{}, false, ctx.Err()
		case <-t.done:
			return cdp.Dialog{}, false, cdp.ErrClosed
		case <-dialogChanged:
			continue
		case <-pending.done:
			t.pendingMu.Lock()
			if t.pending == pending {
				t.pending = nil
			}
			t.pendingMu.Unlock()
			dialog, open, _ = t.page.DialogState()
			return dialog, open, pending.err
		}
	}
}

func (t *tabRuntime) setState(state TabState) {
	t.mu.Lock()
	t.state = state
	t.mu.Unlock()
	if t.observer != nil {
		t.observer.signal()
	}
}

func (t *tabRuntime) currentState() TabState {
	_, open, _ := t.page.DialogState()
	return t.stateWithDialog(open)
}

func (t *tabRuntime) currentDialogState() (cdp.Dialog, bool, TabState) {
	dialog, open, _ := t.page.DialogState()
	return dialog, open, t.stateWithDialog(open)
}

func (t *tabRuntime) stateWithDialog(dialogOpen bool) TabState {
	t.mu.RLock()
	state := t.state
	t.mu.RUnlock()
	if state == TabClosed {
		return TabClosed
	}
	if dialogOpen {
		return TabDialog
	}
	return state
}

func (t *tabRuntime) setPhase(phase operationPhase) {
	t.mu.Lock()
	t.phase = phase
	t.mu.Unlock()
	if t.observer != nil {
		t.observer.signal()
	}
}

func (t *tabRuntime) currentPhase() operationPhase {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.phase
}

type Event struct {
	Cursor uint64         `json:"cursor"`
	Type   string         `json:"type"`
	Time   time.Time      `json:"time"`
	Data   map[string]any `json:"data,omitempty"`
}

type eventRing struct {
	mu     sync.RWMutex
	next   uint64
	limit  int
	events []Event
}

func newEventRing(limit int) *eventRing {
	return &eventRing{limit: limit}
}

func (r *eventRing) add(kind string, data map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.next++
	event := Event{Cursor: r.next, Type: kind, Time: time.Now(), Data: data}
	r.events = append(r.events, event)
	if len(r.events) > r.limit {
		copy(r.events, r.events[len(r.events)-r.limit:])
		r.events = r.events[:r.limit]
	}
}

func (r *eventRing) after(cursor uint64, limit int, include func(Event) bool) ([]Event, uint64) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if limit <= 0 || limit > r.limit {
		limit = r.limit
	}
	result := make([]Event, 0, min(limit, len(r.events)))
	last := cursor
	for _, event := range r.events {
		if event.Cursor <= cursor || !include(event) {
			continue
		}
		result = append(result, event)
		last = event.Cursor
		if len(result) == limit {
			break
		}
	}
	return result, last
}

type tabObserver struct {
	collectionStartedAt time.Time
	subscription        *cdp.Subscription
	page                *cdp.Page
	runtime             *tabRuntime
	events              *eventRing
	mu                  sync.RWMutex
	inflight            map[string]observedRequest
	seq                 uint64
	wake                chan struct{}
	watchers            map[chan struct{}]struct{}
}

type observedRequest struct {
	started      uint64
	method       string
	url          string
	resourceType string
	tracked      bool
}

func newTabObserver(ctx context.Context, page *cdp.Page, runtime *tabRuntime, diagnostics cdp.DiagnosticsMode) *tabObserver {
	observer := &tabObserver{
		page: page, runtime: runtime, events: newEventRing(512), inflight: map[string]observedRequest{},
		wake: make(chan struct{}, 1), watchers: map[chan struct{}]struct{}{},
	}
	methods := []string{
		"Page.javascriptDialogOpening",
		"Page.javascriptDialogClosed",
		"Page.fileChooserOpened",
		"Page.downloadWillBegin",
		"Browser.downloadWillBegin",
		"Network.requestWillBeSent",
		"Network.responseReceived",
		"Network.loadingFinished",
		"Network.loadingFailed",
	}
	if diagnostics == cdp.DiagnosticsRuntime {
		methods = append(methods, "Runtime.consoleAPICalled", "Runtime.exceptionThrown")
		observer.collectionStartedAt = time.Now()
	}
	subscription, err := page.Session().Subscribe(ctx, methods...)
	if err != nil {
		runtime.setState(TabClosed)
		return observer
	}
	observer.subscription = subscription
	responses := subscription.Events()
	go func() {
		defer subscription.Close()
		for {
			select {
			case <-page.Done():
				runtime.setState(TabClosed)
				observer.signal()
				return
			case response, ok := <-responses:
				if !ok {
					return
				}
				observer.consume(response.Method, response.Params)
			}
		}
	}()
	return observer
}

func (o *tabObserver) signal() {
	select {
	case o.wake <- struct{}{}:
	default:
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	for watcher := range o.watchers {
		select {
		case watcher <- struct{}{}:
		default:
		}
	}
}

func (o *tabObserver) subscribe() (<-chan struct{}, func()) {
	watcher := make(chan struct{}, 1)
	o.mu.Lock()
	o.watchers[watcher] = struct{}{}
	o.mu.Unlock()
	watcher <- struct{}{}
	return watcher, func() {
		o.mu.Lock()
		delete(o.watchers, watcher)
		o.mu.Unlock()
	}
}

func (o *tabObserver) consume(method string, params map[string]any) {
	requestID := stringValue(params["requestId"])
	var requestInfo observedRequest
	o.mu.Lock()
	o.seq++
	seq := o.seq
	switch method {
	case "Network.requestWillBeSent":
		request, _ := params["request"].(map[string]any)
		resourceType := strings.ToLower(stringValue(params["type"]))
		requestInfo = observedRequest{
			started: seq, method: stringValue(request["method"]), url: stringValue(request["url"]),
			resourceType: resourceType, tracked: trackedResource(resourceType),
		}
		if requestID != "" {
			o.inflight[requestID] = requestInfo
		}
	case "Network.responseReceived":
		requestInfo = o.inflight[requestID]
	case "Network.loadingFinished", "Network.loadingFailed":
		requestInfo = o.inflight[requestID]
		delete(o.inflight, requestID)
	}
	o.mu.Unlock()

	switch method {
	case "Page.javascriptDialogOpening":
		o.events.add("dialog", map[string]any{"kind": params["type"], "message": params["message"]})
	case "Page.javascriptDialogClosed":
	case "Page.fileChooserOpened":
		o.runtime.setState(TabFileChooser)
		o.events.add("file_chooser", nil)
	case "Runtime.consoleAPICalled":
		parts := []string{}
		if args, ok := params["args"].([]any); ok {
			for _, raw := range args {
				argument, _ := raw.(map[string]any)
				if value, exists := argument["value"]; exists {
					parts = append(parts, fmt.Sprint(value))
				} else if description := stringValue(argument["description"]); description != "" {
					parts = append(parts, description)
				}
			}
		}
		data := map[string]any{"level": params["type"], "text": strings.Join(parts, " "), "context_id": params["executionContextId"]}
		if stack, ok := params["stackTrace"].(map[string]any); ok {
			if frames, ok := stack["callFrames"].([]any); ok && len(frames) > 0 {
				if frame, ok := frames[0].(map[string]any); ok {
					data["location"] = map[string]any{"url": frame["url"], "line": frame["lineNumber"], "column": frame["columnNumber"]}
				}
			}
		}
		o.events.add("console", data)
	case "Runtime.exceptionThrown":
		o.events.add("console", map[string]any{"level": "error", "exception": params["exceptionDetails"]})
	case "Network.requestWillBeSent":
		o.events.add("request", map[string]any{"request_id": requestID, "method": requestInfo.method, "url": requestInfo.url, "resource_type": requestInfo.resourceType})
	case "Network.responseReceived":
		response, _ := params["response"].(map[string]any)
		url := stringValue(response["url"])
		if url == "" {
			url = requestInfo.url
		}
		o.events.add("response", map[string]any{"request_id": requestID, "method": requestInfo.method, "url": url, "status": response["status"], "resource_type": firstNonEmpty(strings.ToLower(stringValue(params["type"])), requestInfo.resourceType)})
	case "Network.loadingFailed":
		o.events.add("request_failed", map[string]any{"request_id": requestID, "method": requestInfo.method, "url": requestInfo.url, "resource_type": requestInfo.resourceType, "error": params["errorText"]})
	case "Page.downloadWillBegin", "Browser.downloadWillBegin":
		o.events.add("download", params)
	}
	o.signal()
}

func (o *tabObserver) mark() uint64 {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.seq
}

func (o *tabObserver) waitRequests(ctx context.Context, after uint64) bool {
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		o.mu.RLock()
		pending := false
		for _, request := range o.inflight {
			if request.tracked && request.started > after {
				pending = true
				break
			}
		}
		o.mu.RUnlock()
		if !pending {
			return false
		}
		select {
		case <-ctx.Done():
			return true
		case <-deadline.C:
			return true
		case <-o.wake:
		}
	}
}

func trackedResource(value string) bool {
	switch value {
	case "document", "script", "xhr", "fetch":
		return true
	default:
		return false
	}
}

func stringValue(value any) string {
	result, _ := value.(string)
	return result
}

type ToolError struct {
	Code    string `json:"code"`
	Op      string `json:"op"`
	Message string `json:"message"`
	Ref     string `json:"ref,omitempty"`
	Detail  string `json:"detail,omitempty"`
	cause   error
}

func NewToolError(code, op, message string, cause error) *ToolError {
	return &ToolError{Code: code, Op: op, Message: message, cause: cause}
}

func (e *ToolError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%s: %s", e.Op, e.Message)
}

func (e *ToolError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func normalizeToolError(op, ref string, err error) *ToolError {
	if err == nil {
		return nil
	}
	var typed *ToolError
	if errors.As(err, &typed) {
		copy := *typed
		if copy.Ref == "" {
			copy.Ref = ref
		}
		return &copy
	}
	var browserError *cdp.BrowserError
	if errors.As(err, &browserError) {
		code := browserError.Kind
		if code == "" {
			code = "browser_error"
		}
		return &ToolError{Code: code, Op: firstNonEmpty(browserError.Op, op), Message: firstNonEmpty(browserError.Message, "browser operation failed"), Detail: browserError.Detail, Ref: ref, cause: err}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &ToolError{Code: "canceled", Op: op, Message: err.Error(), Ref: ref, cause: err}
	}
	return &ToolError{Code: "browser_error", Op: op, Message: err.Error(), Ref: ref, cause: err}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func modalError(op string, state TabState) error {
	switch state {
	case TabDialog:
		return NewToolError("dialog_open", op, "a JavaScript dialog is open", nil)
	case TabFileChooser:
		return NewToolError("file_chooser_open", op, "a file chooser is open", nil)
	default:
		return nil
	}
}

type operationResult struct {
	Document snapshot.Document
	Delta    snapshot.Delta
	Warnings []string
}
