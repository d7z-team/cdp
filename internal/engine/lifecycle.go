package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.d7z.net/cdp/internal/syncutil"
)

type lifecycleDispatchItem struct {
	ctx           LifecycleContext
	event         LifecycleEvent
	registrations []lifecycleRegistration
}

type lifecycleDispatchQueue struct {
	mu      sync.Mutex
	pending []lifecycleDispatchItem
	running bool
}

// LifecycleHandler receives BrowserManager lifecycle events.
type LifecycleHandler interface {
	Name() string
	HandleLifecycle(ctx LifecycleContext, event LifecycleEvent) error
}

// LifecycleContext provides the manager/page context for a lifecycle callback.
type LifecycleContext struct {
	Context context.Context
	Manager *BrowserManager
	Page    *Page
}

// LifecycleEventType describes the semantic lifecycle event emitted by BrowserManager.
type LifecycleEventType string

const (
	// LifecyclePageDiscovered is emitted when a page target is first observed by BrowserManager.
	LifecyclePageDiscovered LifecycleEventType = "page_discovered"
	// LifecyclePageBound is emitted after a page websocket is connected and base runtime setup finishes.
	LifecyclePageBound LifecycleEventType = "page_bound"
	// LifecyclePageActivated is emitted when a page becomes the current active page in BrowserManager.
	LifecyclePageActivated LifecycleEventType = "page_activated"
	// LifecyclePageDeactivated is emitted when a page loses active-page status.
	LifecyclePageDeactivated LifecycleEventType = "page_deactivated"
	// LifecyclePageNavigating is emitted when the main frame starts navigating.
	LifecyclePageNavigating LifecycleEventType = "page_navigating"
	// LifecyclePageNavigated is emitted when the main frame finishes a navigation or same-document navigation.
	LifecyclePageNavigated LifecycleEventType = "page_navigated"
	// LifecyclePageNavigationCanceled is emitted when a started main-frame navigation stops without committing.
	LifecyclePageNavigationCanceled LifecycleEventType = "page_navigation_canceled"
	// LifecyclePageURLChanged is emitted when the main-frame URL changes.
	LifecyclePageURLChanged LifecycleEventType = "page_url_changed"
	// LifecyclePageClosed is emitted when a page target is destroyed or BrowserManager shuts down.
	// A transient page websocket disconnect only unbinds that connection.
	LifecyclePageClosed LifecycleEventType = "page_closed"
	// LifecyclePageRuntimeReady is emitted after an init-script runtime is ready in the current main-frame generation.
	LifecyclePageRuntimeReady LifecycleEventType = "page_runtime_ready"
	// LifecycleBrowserStopping is emitted when BrowserManager begins shutdown.
	LifecycleBrowserStopping LifecycleEventType = "browser_stopping"
)

// LifecycleEventSource describes which subsystem produced a lifecycle event.
type LifecycleEventSource string

const (
	LifecycleSourceTarget  LifecycleEventSource = "target"
	LifecycleSourcePage    LifecycleEventSource = "page"
	LifecycleSourceHelper  LifecycleEventSource = "helper"
	LifecycleSourceManager LifecycleEventSource = "manager"
)

// PageOpenKind describes how a page target was opened from BrowserManager's perspective.
type PageOpenKind string

const (
	// PageOpenKindUnknown means the open source cannot be inferred.
	PageOpenKindUnknown PageOpenKind = "unknown"
	// PageOpenKindOpener means the page was opened by another page target such as target=_blank or window.open.
	PageOpenKindOpener PageOpenKind = "opener"
	// PageOpenKindManual means the page has no opener relationship and looks like a manually or browser-created tab.
	PageOpenKindManual PageOpenKind = "manual_or_browser"
)

// IsKnown reports whether the open kind was inferred successfully.
func (k PageOpenKind) IsKnown() bool {
	return k != "" && k != PageOpenKindUnknown
}

// OpenedByPage reports whether the page was opened by another page target.
func (k PageOpenKind) OpenedByPage() bool {
	return k == PageOpenKindOpener
}

// OpenedWithoutOpener reports whether the page has no opener relationship.
func (k PageOpenKind) OpenedWithoutOpener() bool {
	return k == PageOpenKindManual
}

// TargetInfoSnapshot is the lifecycle-facing snapshot of CDP TargetInfo.
type TargetInfoSnapshot struct {
	Attached         bool
	BrowserContextID string
	CanAccessOpener  bool
	OpenerFrameID    string
	OpenerID         string
	ParentFrameID    string
	TargetID         string
	Title            string
	Type             string
	URL              string
}

// HasOpener reports whether CDP exposed an opener target for this page.
func (t TargetInfoSnapshot) HasOpener() bool {
	return strings.TrimSpace(t.OpenerID) != ""
}

// OpenerPageID returns the opener target ID when present.
func (t TargetInfoSnapshot) OpenerPageID() string {
	return strings.TrimSpace(t.OpenerID)
}

// IsPageTarget reports whether this target snapshot represents a page target.
func (t TargetInfoSnapshot) IsPageTarget() bool {
	return t.Type == "page"
}

// LifecycleEvent is the normalized event payload delivered to lifecycle handlers.
type LifecycleEvent struct {
	Type      LifecycleEventType
	Source    LifecycleEventSource
	Timestamp time.Time
	Sequence  uint64

	PageID       string
	OpenerPageID string
	OpenKind     PageOpenKind
	Title        string
	URL          string

	PreviousPageID string
	PreviousURL    string
	Reason         string
	ScriptName     string
	Namespace      RuntimeNamespace
	Generation     int64

	TargetInfo *TargetInfoSnapshot
}

type pendingPageRuntimeReady struct {
	script     InitScript
	generation int64
	contextID  int
}

type runtimeReadyIdentity struct {
	generation int64
	contextID  int
	frameID    string
}

// ConfirmInitScriptRuntimeReady records a page-side readiness acknowledgment
// for the exact current top-frame execution context.
func (r *BrowserManager) ConfirmInitScriptRuntimeReady(page *Page, scriptName string, contextID int) bool {
	if r == nil || page == nil || contextID == 0 || !r.isCurrentManagedPage(page) {
		return false
	}
	scriptName = strings.TrimSpace(scriptName)
	r.scriptMu.RLock()
	registration, ok := r.initScriptsByName[scriptName]
	r.scriptMu.RUnlock()
	if !ok {
		return false
	}
	info, ok := page.executionContextInfo(contextID)
	if !ok || strings.TrimSpace(info.FrameID) == "" || !namespaceMatchesContext(r.namespaceRegistration(registration.script.Namespace), info) {
		return false
	}
	mainFrameID := page.mainFrame()
	if mainFrameID == "" || info.FrameID != mainFrameID {
		return false
	}
	generation := page.RuntimeGeneration()
	if generation == 0 {
		return false
	}
	r.emitPageRuntimeReady(page, registration.script, generation, contextID)
	return true
}

// ReadyInitScriptRuntime returns the exact acknowledged top-frame runtime for
// an init script. It never creates or searches for a replacement context.
func (p *Page) ReadyInitScriptRuntime(scriptName string) (*RuntimeContextHandle, int64, bool) {
	if p == nil || p.manager == nil || !p.manager.IsAlive() || p.topNavigationPending.Load() || !p.manager.isCurrentManagedPage(p) {
		return nil, 0, false
	}
	scriptName = strings.TrimSpace(scriptName)
	p.manager.scriptMu.RLock()
	registration, registered := p.manager.initScriptsByName[scriptName]
	p.manager.scriptMu.RUnlock()
	if !registered {
		return nil, 0, false
	}
	p.runtimeReadyMu.Lock()
	identity, ready := p.runtimeReadyContexts[scriptName]
	p.runtimeReadyMu.Unlock()
	if !ready || identity.contextID == 0 || identity.generation == 0 || p.RuntimeGeneration() != identity.generation {
		return nil, 0, false
	}
	info, ok := p.executionContextInfo(identity.contextID)
	if !ok || info.FrameID != identity.frameID || info.FrameID != p.mainFrame() ||
		!namespaceMatchesContext(p.manager.namespaceRegistration(registration.script.Namespace), info) {
		return nil, 0, false
	}
	target, _ := p.executionContextTarget("", identity.contextID)
	if target.SessionID != "" {
		return nil, 0, false
	}
	handle := &RuntimeContextHandle{Page: p, epoch: p.RuntimeGeneration(), frameEpoch: p.frameGeneration(info.FrameID), Info: RuntimeContext{
		Namespace: registration.script.Namespace,
		PageID:    p.ID,
		TargetID:  target.TargetID,
		FrameID:   info.FrameID,
		ContextID: identity.contextID,
		WorldName: p.manager.namespaceWorldName(registration.script.Namespace),
		IsDefault: info.IsDefault,
	}}
	if p.topNavigationPending.Load() || p.RuntimeGeneration() != identity.generation || !p.manager.isCurrentManagedPage(p) {
		return nil, 0, false
	}
	return handle, identity.generation, true
}

func (p *Page) invalidateInitScriptRuntime(scriptName string, generation int64, contextID int) bool {
	if p == nil || generation == 0 || contextID == 0 {
		return false
	}
	p.runtimeReadyMu.Lock()
	defer p.runtimeReadyMu.Unlock()
	identity, ok := p.runtimeReadyContexts[strings.TrimSpace(scriptName)]
	if !ok || identity.generation != generation || identity.contextID != contextID {
		return false
	}
	delete(p.runtimeReadyContexts, strings.TrimSpace(scriptName))
	return true
}

// EnsureReadyInitScriptRuntime verifies an acknowledged top-frame runtime and,
// when its facade is missing, injects the registered script once into that same
// execution context. Child frames remain owned by the page-side frame bridge.
func (r *BrowserManager) EnsureReadyInitScriptRuntime(
	ctx context.Context,
	pageID, scriptName string,
) (*RuntimeContextHandle, int64, bool, error) {
	if r == nil || !r.IsAlive() {
		return nil, 0, false, ErrBrowserClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	pageID = strings.TrimSpace(pageID)
	scriptName = strings.TrimSpace(scriptName)
	if pageID == "" || scriptName == "" {
		return nil, 0, false, errors.New("page id and init script name are required")
	}
	r.scriptMu.RLock()
	registration, registered := r.initScriptsByName[scriptName]
	r.scriptMu.RUnlock()
	if !registered {
		return nil, 0, false, fmt.Errorf("init script not registered: %s", scriptName)
	}
	page, ok := r.GetPage(pageID)
	if !ok || page == nil {
		return nil, 0, false, nil
	}
	handle, generation, ready := page.ReadyInitScriptRuntime(scriptName)
	if !ready {
		return nil, 0, false, nil
	}
	contextID := handle.Info.ContextID
	probeSource := r.initScriptSource(registration.script, "ensure_ready", "verify")
	probe, err := handle.EvalValueExactContext(ctx, "return await "+probeSource)
	if err != nil {
		if isSupersededDocumentError(err) {
			page.invalidateInitScriptRuntime(scriptName, generation, contextID)
			return nil, generation, false, nil
		}
		return nil, generation, false, fmt.Errorf("probe init script %s on page %s: %w", scriptName, pageID, err)
	}
	if isReady, ok := probe.(bool); !ok {
		return nil, generation, false, fmt.Errorf("probe init script %s on page %s returned %T, want boolean", scriptName, pageID, probe)
	} else if !isReady {
		page.invalidateInitScriptRuntime(scriptName, generation, contextID)
		if !r.IsAlive() {
			return nil, generation, false, ErrBrowserClosed
		}
		if !r.isCurrentManagedPage(page) || page.RuntimeGeneration() != generation {
			return nil, generation, false, nil
		}
		result, evalErr := page.evaluateInitScriptInContext(
			ctx,
			contextID,
			r.initScriptSource(registration.script, "ensure_ready", "exec"),
		)
		if evalErr != nil {
			err = evalErr
		} else {
			err = runtimeResultError(result)
		}
		if err != nil {
			return nil, generation, false, fmt.Errorf("inject init script %s on page %s: %w", scriptName, pageID, err)
		}
		// The script bootstrap may itself need Go binding. The caller's
		// operation waits for bootstrap and confirms readiness after it returns.
	}

	currentPage, current := r.GetPage(pageID)
	info, contextExists := page.executionContextInfo(contextID)
	if !r.IsAlive() || !current || currentPage != page || page.RuntimeGeneration() != generation ||
		!contextExists || info.FrameID != handle.Info.FrameID || info.FrameID != page.mainFrame() {
		return nil, generation, false, nil
	}
	return handle, generation, true, nil
}

func (r *BrowserManager) emitPageRuntimeReady(page *Page, script InitScript, generation int64, contextID int) {
	if r == nil || page == nil {
		return
	}
	page.runtimeReadyMu.Lock()
	defer page.runtimeReadyMu.Unlock()
	if page.runtimeReadyClosed || !r.IsAlive() || page.ctx == nil || page.ctx.Err() != nil {
		return
	}
	info, ok := page.executionContextInfo(contextID)
	if !ok {
		return
	}
	identity := runtimeReadyIdentity{generation: generation, contextID: contextID, frameID: info.FrameID}
	if contextID != 0 && page.runtimeReadyContexts != nil && page.runtimeReadyContexts[script.Name] == identity {
		return
	}
	if !page.runtimeReadyPublished {
		if page.pendingRuntimeReady == nil {
			page.pendingRuntimeReady = make(map[string]pendingPageRuntimeReady)
		}
		if pending, ok := page.pendingRuntimeReady[script.Name]; ok {
			if pending.generation > generation || (pending.generation == generation && pending.contextID == contextID) {
				return
			}
		}
		page.pendingRuntimeReady[script.Name] = pendingPageRuntimeReady{script: script, generation: generation, contextID: contextID}
		return
	}
	if r.dispatchPageRuntimeReadyLocked(page, script, identity) {
		if page.runtimeReadyContexts == nil {
			page.runtimeReadyContexts = make(map[string]runtimeReadyIdentity)
		}
		page.runtimeReadyContexts[script.Name] = identity
	}
}

func (r *BrowserManager) publishPageRuntimeReady(page *Page) {
	if r == nil || page == nil {
		return
	}
	page.runtimeReadyMu.Lock()
	defer page.runtimeReadyMu.Unlock()
	if page.runtimeReadyClosed || !r.IsAlive() || page.ctx == nil || page.ctx.Err() != nil {
		return
	}
	page.runtimeReadyPublished = true
	pending := page.pendingRuntimeReady
	page.pendingRuntimeReady = nil
	for _, ready := range pending {
		info, ok := page.executionContextInfo(ready.contextID)
		identity := runtimeReadyIdentity{generation: ready.generation, contextID: ready.contextID}
		if ok {
			identity.frameID = info.FrameID
		}
		if r.dispatchPageRuntimeReadyLocked(page, ready.script, identity) {
			if page.runtimeReadyContexts == nil {
				page.runtimeReadyContexts = make(map[string]runtimeReadyIdentity)
			}
			page.runtimeReadyContexts[ready.script.Name] = identity
		}
	}
}

// dispatchPageRuntimeReadyLocked validates and queues runtime-ready while the
// Page's runtimeReadyMu is held, so unbind cannot overtake the lifecycle event.
func (r *BrowserManager) dispatchPageRuntimeReadyLocked(page *Page, script InitScript, identity runtimeReadyIdentity) bool {
	if r.sessions == nil {
		return false
	}
	current, ok := r.GetPage(page.ID)
	if !ok || current != page || !r.IsAlive() || page.ctx == nil || page.ctx.Err() != nil ||
		identity.generation == 0 || identity.contextID == 0 || page.RuntimeGeneration() != identity.generation {
		return false
	}
	info, ok := page.executionContextInfo(identity.contextID)
	if !ok || info.FrameID != identity.frameID || info.FrameID != page.mainFrame() ||
		!namespaceMatchesContext(r.namespaceRegistration(script.Namespace), info) {
		return false
	}
	r.emitLifecycle(LifecycleContext{
		Context: page.ctx,
		Manager: r,
		Page:    page,
	}, LifecycleEvent{
		Type:       LifecyclePageRuntimeReady,
		Source:     LifecycleSourceHelper,
		PageID:     page.ID,
		ScriptName: script.Name,
		Namespace:  script.Namespace,
		Generation: identity.generation,
	})
	return true
}

// HasPage reports whether the event is scoped to a concrete page.
func (e LifecycleEvent) HasPage() bool {
	return strings.TrimSpace(e.PageID) != ""
}

// HasTargetInfo reports whether the event includes a target snapshot.
func (e LifecycleEvent) HasTargetInfo() bool {
	return e.TargetInfo != nil
}

// HasOpener reports whether the event page has an opener page relationship.
func (e LifecycleEvent) HasOpener() bool {
	if strings.TrimSpace(e.OpenerPageID) != "" {
		return true
	}
	return e.TargetInfo != nil && e.TargetInfo.HasOpener()
}

// OpenedByPage reports whether the event page was opened by another page.
func (e LifecycleEvent) OpenedByPage() bool {
	return e.OpenKind.OpenedByPage() || e.HasOpener()
}

// OpenedWithoutOpener reports whether the event page has no opener relationship.
func (e LifecycleEvent) OpenedWithoutOpener() bool {
	return e.OpenKind.OpenedWithoutOpener()
}

// IsPageLifecycle reports whether the event is scoped to a page rather than the manager itself.
func (e LifecycleEvent) IsPageLifecycle() bool {
	return e.Type != LifecycleBrowserStopping
}

// IsDiscovery reports whether this event belongs to the page-creation lifecycle.
func (e LifecycleEvent) IsDiscovery() bool {
	return e.Type == LifecyclePageDiscovered || e.Type == LifecyclePageBound
}

// IsNavigation reports whether this event belongs to page navigation lifecycle.
func (e LifecycleEvent) IsNavigation() bool {
	return e.Type == LifecyclePageNavigating || e.Type == LifecyclePageNavigated ||
		e.Type == LifecyclePageNavigationCanceled || e.Type == LifecyclePageURLChanged
}

// IsActivation reports whether this event belongs to active-page switching lifecycle.
func (e LifecycleEvent) IsActivation() bool {
	return e.Type == LifecyclePageActivated || e.Type == LifecyclePageDeactivated
}

// IsTerminal reports whether this event represents a terminal lifecycle transition.
func (e LifecycleEvent) IsTerminal() bool {
	return e.Type == LifecyclePageClosed || e.Type == LifecycleBrowserStopping
}

// IsInitial reports whether the event was produced from BrowserManager's initial page snapshot.
func (e LifecycleEvent) IsInitial() bool {
	return e.Reason == "initial_page"
}

// HasPreviousPage reports whether the event references a previous active page.
func (e LifecycleEvent) HasPreviousPage() bool {
	return strings.TrimSpace(e.PreviousPageID) != ""
}

// HasPreviousURL reports whether the event references a previous URL.
func (e LifecycleEvent) HasPreviousURL() bool {
	return strings.TrimSpace(e.PreviousURL) != ""
}

// IsSamePageSwitch reports whether the previous-page pointer matches the current page.
func (e LifecycleEvent) IsSamePageSwitch() bool {
	return e.HasPage() && e.PageID == e.PreviousPageID
}

type lifecycleRegistration struct {
	order   int
	handler LifecycleHandler
}

type pageLifecycleState struct {
	discovered bool
	bound      bool
	closed     bool
	active     bool
	openKind   PageOpenKind
	title      string
	url        string
	targetInfo TargetInfoSnapshot
	activeAt   time.Time
}

type navigationReasonKey struct {
	pageID  string
	frameID string
}

type pendingNavigationReason struct {
	reason    string
	url       string
	timestamp time.Time
}

const pendingNavigationReasonTTL = 10 * time.Second

func newTargetInfoSnapshot(info TargetInfo) TargetInfoSnapshot {
	return TargetInfoSnapshot{
		Attached:         info.Attached,
		BrowserContextID: info.BrowserContextID,
		CanAccessOpener:  info.CanAccessOpener,
		OpenerFrameID:    info.OpenerFrameID,
		OpenerID:         info.OpenerID,
		ParentFrameID:    info.ParentFrameID,
		TargetID:         info.TargetID,
		Title:            info.Title,
		Type:             info.Type,
		URL:              info.URL,
	}
}

func derivePageOpenKind(info TargetInfoSnapshot) PageOpenKind {
	if strings.TrimSpace(info.OpenerID) != "" {
		return PageOpenKindOpener
	}
	if strings.TrimSpace(info.TargetID) == "" {
		return PageOpenKindUnknown
	}
	return PageOpenKindManual
}

// RegisterLifecycle registers a named lifecycle handler on BrowserManager.
func (r *BrowserManager) RegisterLifecycle(h LifecycleHandler) error {
	if r == nil {
		return errors.New("browser manager is nil")
	}
	if h == nil {
		return errors.New("lifecycle handler is nil")
	}
	name := strings.TrimSpace(h.Name())
	if name == "" {
		return errors.New("lifecycle handler name is empty")
	}

	r.lifecycleHandlerMu.Lock()
	defer r.lifecycleHandlerMu.Unlock()
	if r.lifecycleHandlers == nil {
		r.lifecycleHandlers = map[string]lifecycleRegistration{}
	}
	if _, exists := r.lifecycleHandlers[name]; exists {
		return errors.New("lifecycle handler already registered: " + name)
	}
	r.lifecycleHandlers[name] = lifecycleRegistration{
		order:   len(r.lifecycleHandlers),
		handler: h,
	}
	return nil
}

func (r *BrowserManager) sortedLifecycleRegistrations() []lifecycleRegistration {
	r.lifecycleHandlerMu.RLock()
	registrations := make([]lifecycleRegistration, 0, len(r.lifecycleHandlers))
	for _, registration := range r.lifecycleHandlers {
		registrations = append(registrations, registration)
	}
	r.lifecycleHandlerMu.RUnlock()

	sort.Slice(registrations, func(i, j int) bool {
		return registrations[i].order < registrations[j].order
	})
	return registrations
}

func (r *BrowserManager) withPageLifecycleState(pageID string, fn func(state *pageLifecycleState)) {
	r.pageLifecycleMu.Lock()
	defer r.pageLifecycleMu.Unlock()
	if r.pageLifecycle == nil {
		r.pageLifecycle = map[string]*pageLifecycleState{}
	}
	state, ok := r.pageLifecycle[pageID]
	if !ok {
		state = &pageLifecycleState{openKind: PageOpenKindUnknown}
		r.pageLifecycle[pageID] = state
	}
	fn(state)
}

func (r *BrowserManager) snapshotPageState(pageID string) pageLifecycleState {
	r.pageLifecycleMu.Lock()
	defer r.pageLifecycleMu.Unlock()
	if r.pageLifecycle == nil {
		return pageLifecycleState{openKind: PageOpenKindUnknown}
	}
	state := r.pageLifecycle[pageID]
	if state == nil {
		return pageLifecycleState{openKind: PageOpenKindUnknown}
	}
	copyState := *state
	if copyState.openKind == "" {
		copyState.openKind = derivePageOpenKind(copyState.targetInfo)
	}
	return copyState
}

// TopPageURL returns the latest browser-managed URL for a top-level page.
// The lifecycle snapshot is authoritative for iframe and OOPIF binding calls.
func (r *BrowserManager) TopPageURL(pageID string) string {
	if r == nil || strings.TrimSpace(pageID) == "" {
		return ""
	}
	return strings.TrimSpace(r.snapshotPageState(pageID).url)
}

func (r *BrowserManager) deletePageLifecycleState(pageID string) {
	r.pageLifecycleMu.Lock()
	defer r.pageLifecycleMu.Unlock()
	delete(r.pageLifecycle, pageID)
	for key := range r.pendingNavigationReasons {
		if key.pageID == pageID {
			delete(r.pendingNavigationReasons, key)
		}
	}
}

func (r *BrowserManager) rememberPendingNavigationReason(pageID, frameID, reason, pageURL string) {
	if r == nil {
		return
	}
	pageID = strings.TrimSpace(pageID)
	frameID = strings.TrimSpace(frameID)
	reason = strings.TrimSpace(reason)
	if pageID == "" || frameID == "" || reason == "" {
		return
	}

	now := time.Now()
	r.pageLifecycleMu.Lock()
	defer r.pageLifecycleMu.Unlock()
	if r.pendingNavigationReasons == nil {
		r.pendingNavigationReasons = map[navigationReasonKey]pendingNavigationReason{}
	}
	for key, pending := range r.pendingNavigationReasons {
		if now.Sub(pending.timestamp) > pendingNavigationReasonTTL {
			delete(r.pendingNavigationReasons, key)
		}
	}
	r.pendingNavigationReasons[navigationReasonKey{
		pageID:  pageID,
		frameID: frameID,
	}] = pendingNavigationReason{
		reason:    reason,
		url:       strings.TrimSpace(pageURL),
		timestamp: now,
	}
}

func (r *BrowserManager) consumePendingNavigationReason(pageID, frameID, pageURL string) string {
	if r == nil {
		return ""
	}
	pageID = strings.TrimSpace(pageID)
	frameID = strings.TrimSpace(frameID)
	pageURL = strings.TrimSpace(pageURL)
	if pageID == "" || frameID == "" {
		return ""
	}

	now := time.Now()
	r.pageLifecycleMu.Lock()
	defer r.pageLifecycleMu.Unlock()
	if r.pendingNavigationReasons == nil {
		return ""
	}

	var fallbackKey navigationReasonKey
	var fallback pendingNavigationReason
	var hasFallback bool

	for key, pending := range r.pendingNavigationReasons {
		if now.Sub(pending.timestamp) > pendingNavigationReasonTTL {
			delete(r.pendingNavigationReasons, key)
			continue
		}
		if key.pageID != pageID || key.frameID != frameID {
			continue
		}
		if pageURL != "" && pending.url != "" && pending.url == pageURL {
			delete(r.pendingNavigationReasons, key)
			return pending.reason
		}
		if !hasFallback || pending.timestamp.After(fallback.timestamp) {
			fallbackKey = key
			fallback = pending
			hasFallback = true
		}
	}

	if hasFallback {
		delete(r.pendingNavigationReasons, fallbackKey)
		return fallback.reason
	}
	return ""
}

func (r *BrowserManager) emitLifecycle(ctx LifecycleContext, event LifecycleEvent) {
	if r == nil {
		return
	}
	if event.PageID == "" && ctx.Page != nil {
		event.PageID = ctx.Page.ID
	}
	if event.PageID != "" {
		snapshot := r.snapshotPageState(event.PageID)
		if event.Title == "" {
			event.Title = snapshot.title
		}
		if event.URL == "" {
			event.URL = snapshot.url
		}
		if event.OpenerPageID == "" {
			event.OpenerPageID = snapshot.targetInfo.OpenerID
		}
		if event.OpenKind == "" {
			event.OpenKind = snapshot.openKind
		}
		if event.TargetInfo == nil && snapshot.targetInfo.TargetID != "" {
			info := snapshot.targetInfo
			event.TargetInfo = &info
		}
	}
	if event.OpenKind == "" {
		event.OpenKind = PageOpenKindUnknown
	}

	r.enqueueLifecycle(lifecycleDispatchItem{
		ctx:           ctx,
		event:         event,
		registrations: r.sortedLifecycleRegistrations(),
	})
}

func (r *BrowserManager) enqueueLifecycle(item lifecycleDispatchItem) {
	key := strings.TrimSpace(item.event.PageID)
	if key == "" {
		key = "__browser__"
	}
	r.lifecycleDispatchMu.Lock()
	if r.lifecycleDispatch == nil {
		r.lifecycleDispatch = make(map[string]*lifecycleDispatchQueue)
	}
	queue := r.lifecycleDispatch[key]
	if queue == nil {
		queue = &lifecycleDispatchQueue{}
		r.lifecycleDispatch[key] = queue
	}
	r.lifecycleDispatchMu.Unlock()

	queue.mu.Lock()
	item.event.Timestamp = time.Now()
	item.event.Sequence = r.lifecycleSeq.Add(1)
	queue.pending = append(queue.pending, item)
	if queue.running {
		queue.mu.Unlock()
		return
	}
	queue.running = true
	queue.mu.Unlock()

	syncutil.Go(r.Logger(), func() {
		for {
			queue.mu.Lock()
			if len(queue.pending) == 0 {
				queue.running = false
				queue.mu.Unlock()
				return
			}
			current := queue.pending[0]
			queue.pending[0] = lifecycleDispatchItem{}
			queue.pending = queue.pending[1:]
			queue.mu.Unlock()

			for _, registration := range current.registrations {
				if err := registration.handler.HandleLifecycle(current.ctx, current.event); err != nil {
					r.log(slog.LevelWarn, "lifecycle handler failed", "handler", registration.handler.Name(), "event", current.event.Type, "page_id", current.event.PageID, "sequence", current.event.Sequence, "error", err)
				}
			}
		}
	})
}

func (r *BrowserManager) updatePageTargetInfo(info TargetInfo) {
	if r == nil || strings.TrimSpace(info.TargetID) == "" {
		return
	}
	r.withPageLifecycleState(info.TargetID, func(state *pageLifecycleState) {
		state.title = info.Title
		state.url = info.URL
		state.targetInfo = newTargetInfoSnapshot(info)
		state.openKind = derivePageOpenKind(state.targetInfo)
	})
}

func (r *BrowserManager) markPageDiscovered(info TargetInfo, page *Page) {
	if r == nil || strings.TrimSpace(info.TargetID) == "" {
		return
	}
	r.updatePageTargetInfo(info)

	var shouldEmit bool
	r.withPageLifecycleState(info.TargetID, func(state *pageLifecycleState) {
		state.closed = false
		if state.discovered {
			return
		}
		state.discovered = true
		shouldEmit = true
	})
	if !shouldEmit {
		return
	}

	targetInfo := newTargetInfoSnapshot(info)
	r.emitLifecycle(LifecycleContext{
		Context: r.ctx,
		Manager: r,
		Page:    page,
	}, LifecycleEvent{
		Type:         LifecyclePageDiscovered,
		Source:       LifecycleSourceTarget,
		PageID:       info.TargetID,
		Title:        info.Title,
		URL:          info.URL,
		OpenerPageID: info.OpenerID,
		OpenKind:     derivePageOpenKind(targetInfo),
		TargetInfo:   &targetInfo,
	})
}

func (r *BrowserManager) markPageBound(page *Page) {
	if r == nil || page == nil {
		return
	}
	var shouldEmit bool
	r.withPageLifecycleState(page.ID, func(state *pageLifecycleState) {
		if state.bound {
			return
		}
		state.closed = false
		state.bound = true
		shouldEmit = true
	})
	if !shouldEmit {
		return
	}

	snapshot := r.snapshotPageState(page.ID)
	r.emitLifecycle(LifecycleContext{
		Context: page.ctx,
		Manager: r,
		Page:    page,
	}, LifecycleEvent{
		Type:         LifecyclePageBound,
		Source:       LifecycleSourceManager,
		PageID:       page.ID,
		Title:        snapshot.title,
		URL:          snapshot.url,
		OpenerPageID: snapshot.targetInfo.OpenerID,
		OpenKind:     snapshot.openKind,
		TargetInfo:   &snapshot.targetInfo,
	})
}

func (r *BrowserManager) markPageUnbound(pageID string) {
	if r == nil || strings.TrimSpace(pageID) == "" {
		return
	}
	r.withPageLifecycleState(pageID, func(state *pageLifecycleState) {
		state.bound = false
		state.active = false
	})
}

func (r *BrowserManager) updatePageLocation(page *Page, title, pageURL string) {
	if r == nil || page == nil {
		return
	}
	r.withPageLifecycleState(page.ID, func(state *pageLifecycleState) {
		if title != "" {
			state.title = title
			state.targetInfo.Title = title
		}
		if pageURL != "" {
			state.url = pageURL
			state.targetInfo.URL = pageURL
		}
	})
}

func (r *BrowserManager) emitPageNavigating(page *Page, url, reason string) {
	if r == nil || page == nil {
		return
	}
	if url != "" {
		r.updatePageLocation(page, "", url)
	}
	r.emitLifecycle(LifecycleContext{
		Context: page.ctx,
		Manager: r,
		Page:    page,
	}, LifecycleEvent{
		Type:   LifecyclePageNavigating,
		Source: LifecycleSourcePage,
		PageID: page.ID,
		URL:    url,
		Reason: reason,
	})
}

func (r *BrowserManager) emitPageNavigated(page *Page, title, pageURL, reason string) {
	if r == nil || page == nil {
		return
	}
	r.updatePageLocation(page, title, pageURL)
	r.emitLifecycle(LifecycleContext{
		Context: page.ctx,
		Manager: r,
		Page:    page,
	}, LifecycleEvent{
		Type:   LifecyclePageNavigated,
		Source: LifecycleSourcePage,
		PageID: page.ID,
		Title:  title,
		URL:    pageURL,
		Reason: reason,
	})
}

func (r *BrowserManager) emitPageNavigationCanceled(page *Page, pageURL, reason string) {
	if r == nil || page == nil {
		return
	}
	r.emitLifecycle(LifecycleContext{
		Context: page.ctx,
		Manager: r,
		Page:    page,
	}, LifecycleEvent{
		Type:   LifecyclePageNavigationCanceled,
		Source: LifecycleSourcePage,
		PageID: page.ID,
		URL:    pageURL,
		Reason: reason,
	})
}

func (r *BrowserManager) emitPageURLChanged(page *Page, previousURL, pageURL, reason string) {
	if r == nil || page == nil {
		return
	}
	r.updatePageLocation(page, "", pageURL)
	r.emitLifecycle(LifecycleContext{
		Context: page.ctx,
		Manager: r,
		Page:    page,
	}, LifecycleEvent{
		Type:        LifecyclePageURLChanged,
		Source:      LifecycleSourcePage,
		PageID:      page.ID,
		URL:         pageURL,
		PreviousURL: previousURL,
		Reason:      reason,
	})
}

func (r *BrowserManager) currentActivePageID() string {
	if r == nil {
		return ""
	}
	val := r.lastActivePageID.Load()
	if val == nil {
		return ""
	}
	id, _ := val.(string)
	return id
}

func (r *BrowserManager) setActivePage(id string, source LifecycleEventSource, reason string) {
	if r == nil {
		return
	}
	r.activePageMu.Lock()
	defer r.activePageMu.Unlock()
	id = strings.TrimSpace(id)
	current := r.currentActivePageID()
	if current == id {
		if id != "" {
			r.lastActivePageID.Store(id)
		}
		return
	}

	if current != "" {
		r.withPageLifecycleState(current, func(state *pageLifecycleState) {
			state.active = false
		})
		page, _ := r.sessions.Load(current)
		r.emitLifecycle(LifecycleContext{
			Context: r.ctx,
			Manager: r,
			Page:    page,
		}, LifecycleEvent{
			Type:           LifecyclePageDeactivated,
			Source:         source,
			PageID:         current,
			PreviousPageID: id,
			Reason:         reason,
		})
	}

	r.lastActivePageID.Store(id)
	if id == "" {
		return
	}

	r.firstPageOnce.Do(func() {
		close(r.firstPageNotify)
	})
	r.withPageLifecycleState(id, func(state *pageLifecycleState) {
		state.active = true
		state.activeAt = time.Now()
	})

	page, _ := r.sessions.Load(id)
	r.emitLifecycle(LifecycleContext{
		Context: r.ctx,
		Manager: r,
		Page:    page,
	}, LifecycleEvent{
		Type:           LifecyclePageActivated,
		Source:         source,
		PageID:         id,
		PreviousPageID: current,
		Reason:         reason,
	})
}

func (r *BrowserManager) clearActivePage(id string, source LifecycleEventSource, reason string) {
	if r == nil || strings.TrimSpace(id) == "" {
		return
	}
	r.activePageMu.Lock()
	defer r.activePageMu.Unlock()
	if r.currentActivePageID() != id {
		return
	}

	r.withPageLifecycleState(id, func(state *pageLifecycleState) {
		state.active = false
	})
	r.lastActivePageID.Store("")

	page, _ := r.sessions.Load(id)
	r.emitLifecycle(LifecycleContext{
		Context: r.ctx,
		Manager: r,
		Page:    page,
	}, LifecycleEvent{
		Type:   LifecyclePageDeactivated,
		Source: source,
		PageID: id,
		Reason: reason,
	})
}

func (r *BrowserManager) emitPageClosed(page *Page, pageID, reason string) {
	if r == nil || strings.TrimSpace(pageID) == "" {
		return
	}
	var shouldEmit bool
	r.withPageLifecycleState(pageID, func(state *pageLifecycleState) {
		if state.closed || (!state.discovered && !state.bound) {
			return
		}
		state.closed = true
		shouldEmit = true
	})
	if !shouldEmit {
		return
	}
	r.emitLifecycle(LifecycleContext{
		Context: r.ctx,
		Manager: r,
		Page:    page,
	}, LifecycleEvent{
		Type:   LifecyclePageClosed,
		Source: LifecycleSourceManager,
		PageID: pageID,
		Reason: reason,
	})
	r.deletePageLifecycleState(pageID)
}
