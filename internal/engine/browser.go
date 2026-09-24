package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gopkg.d7z.net/cdp/internal/syncutil"
	"gopkg.d7z.net/cdp/internal/webassets"
)

type BrowserManager struct {
	runtimeDiagnostics bool
	sessions           *syncutil.SyncMap[string, *Page]
	client             *http.Client
	baseURL            string

	ctx    context.Context
	cancel context.CancelFunc
	conn   *CdpConn

	idGroup *atomic.Int64
	state   atomic.Int32

	targetSessionMu        sync.RWMutex
	targetSessions         map[string]*TargetSession
	targetSessionsByTarget map[string]string
	pageBindMu             sync.Mutex
	pageBinds              map[string]*pageBindState
	pageBindChanged        chan struct{}
	pageBindWG             sync.WaitGroup
	pageWatchWG            sync.WaitGroup

	shutdownDone     chan struct{}
	shutdownDoneOnce sync.Once
	stopping         chan struct{}
	stoppingOnce     sync.Once

	lastActivePageID atomic.Value // string
	activePageMu     sync.Mutex

	serviceState *browserServiceState

	firstPageOnce   sync.Once
	firstPageNotify chan struct{}

	lifecycleMu    sync.RWMutex
	shutdownReason ShutdownReason
	shutdownErr    error

	bindingMu         sync.RWMutex
	bindingHandlers   map[string]BindingHandler
	bindingNamespaces map[string]RuntimeNamespace

	scriptMu          sync.RWMutex
	initScriptsByName map[string]initScriptRegistration
	initScripts       []InitScript
	namespaces        map[RuntimeNamespace]NamespaceRegistration
	runtimeFields     RuntimeFieldSet

	initScriptRuntimeMu       sync.Mutex
	initScriptInstalls        map[initScriptInstallKey]initScriptInstallState
	initScriptInstallInflight map[initScriptInstallKey]*initScriptInstallCall
	initScriptEnsureInflight  map[initScriptEnsureKey]*initScriptEnsureCall

	lifecycleHandlerMu  sync.RWMutex
	lifecycleHandlers   map[string]lifecycleRegistration
	lifecycleSeq        atomic.Uint64
	lifecycleDispatchMu sync.Mutex
	lifecycleDispatch   map[string]*lifecycleDispatchQueue

	dialogPolicyMu sync.RWMutex
	dialogPolicy   JavaScriptDialogPolicy
	printPolicyMu  sync.RWMutex
	printPolicy    PrintPolicy
	actionModeMu   sync.RWMutex
	actionMode     ActionMode

	pageLifecycleMu          sync.Mutex
	pageLifecycle            map[string]*pageLifecycleState
	pendingNavigationReasons map[navigationReasonKey]pendingNavigationReason
}

// pageBindState tracks one target across binding, teardown, and at most one
// event-driven retry. All fields are protected by BrowserManager.pageBindMu.
type pageBindState struct {
	targetVersion      uint64
	boundTargetVersion uint64
	inFlight           bool
	page               *Page
	runtimeCarrier     *Page
	lastBoundPage      *Page
	lastErr            error
	cancelPage         context.CancelFunc
}

// pageBindAttempt owns one candidate Page and its lifetime. pageBindMu protects
// installation in pageBindState; the attempt context protects CDP work.
type pageBindAttempt struct {
	page   *Page
	cancel context.CancelFunc
}

func (a pageBindAttempt) discard(err error) {
	if a.page != nil {
		a.page.finishInit(err)
		a.page.closeRuntimeReady()
	}
	if a.cancel != nil {
		a.cancel()
	}
}

// BrowserManagerConfig configures BrowserManager behavior for pages bound
// through a CDP connection.
type BrowserManagerConfig struct {
	RuntimeDiagnostics bool
	// DefaultActionMode is inherited by pages when they are first bound.
	// Empty values default to ActionModeStrict.
	DefaultActionMode ActionMode
	// Initialize registers bindings, scripts and lifecycle handlers before target discovery.
	// It must not issue browser commands.
	Initialize func(*BrowserManager) error
	// ConnectTimeout bounds setup only, not the lifetime of a connected manager.
	// Zero uses 20 seconds.
	ConnectTimeout time.Duration
}

type initScriptRegistration struct {
	script InitScript
}

type ManagerState int32

const (
	managerStateStarting ManagerState = iota
	managerStateRunning
	managerStateStopping
	managerStateStopped
)

type ShutdownReason string

const (
	shutdownReasonNone            ShutdownReason = ""
	shutdownReasonDisconnect      ShutdownReason = "disconnect"
	shutdownReasonParentContext   ShutdownReason = "parent_context"
	shutdownReasonBrowserWSClosed ShutdownReason = "browser_ws_closed"
	shutdownReasonSetupFailed     ShutdownReason = "setup_failed"
)

const (
	browserManagerShutdownTimeout = 8 * time.Second
	pageBindTimeout               = 15 * time.Second
	pageBindCleanupTimeout        = 2 * time.Second
	pageLoadTimeout               = 2*(pageBindTimeout+pageBindCleanupTimeout) + time.Second
)

type browserServiceState struct {
	foregroundGateMu sync.Mutex
	foregroundGate   chan struct{}
}

func (s *browserServiceState) acquireForeground(ctx context.Context, done <-chan struct{}) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.foregroundGateMu.Lock()
	if s.foregroundGate == nil {
		s.foregroundGate = make(chan struct{}, 1)
		s.foregroundGate <- struct{}{}
	}
	gate := s.foregroundGate
	s.foregroundGateMu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-done:
		return nil, ErrBrowserClosed
	case <-gate:
		return func() { gate <- struct{}{} }, nil
	}
}

var browserServiceStates sync.Map

func NewBrowserManager(ctx context.Context, u string) (*BrowserManager, error) {
	return NewBrowserManagerWithConfig(ctx, u, BrowserManagerConfig{})
}

// NewBrowserManagerWithConfig connects to a browser CDP endpoint and applies
// the supplied defaults to pages managed by the connection.
func NewBrowserManagerWithConfig(ctx context.Context, u string, config BrowserManagerConfig) (*BrowserManager, error) {
	return ConnectManager(ctx, ctx, u, config)
}

func ConnectManager(ctx, lifetime context.Context, u string, config BrowserManagerConfig) (*BrowserManager, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if config.ConnectTimeout <= 0 {
		config.ConnectTimeout = 20 * time.Second
	}
	setupCtx, setupCancel := context.WithTimeout(ctx, config.ConnectTimeout)
	defer setupCancel()
	managerCtx, cancel := context.WithCancel(context.Background())
	stopSetupCancellation := context.AfterFunc(setupCtx, cancel)
	defer stopSetupCancellation()
	baseURL, browserWS, err := normalizeBrowserEndpoints(u)
	if err != nil {
		cancel()
		return nil, err
	}
	defaultActionMode, err := normalizeActionMode(config.DefaultActionMode)
	if err != nil {
		cancel()
		return nil, err
	}
	runtimeFields, err := newRuntimeFieldSet()
	if err != nil {
		cancel()
		return nil, err
	}
	r := &BrowserManager{
		runtimeDiagnostics:        config.RuntimeDiagnostics,
		sessions:                  syncutil.NewSyncMap[string, *Page](),
		client:                    http.DefaultClient,
		baseURL:                   baseURL,
		ctx:                       managerCtx,
		cancel:                    cancel,
		idGroup:                   new(atomic.Int64),
		shutdownDone:              make(chan struct{}),
		targetSessions:            map[string]*TargetSession{},
		targetSessionsByTarget:    map[string]string{},
		pageBinds:                 map[string]*pageBindState{},
		pageBindChanged:           make(chan struct{}),
		serviceState:              loadBrowserServiceState(baseURL),
		firstPageNotify:           make(chan struct{}),
		bindingHandlers:           map[string]BindingHandler{},
		bindingNamespaces:         map[string]RuntimeNamespace{},
		initScriptsByName:         map[string]initScriptRegistration{},
		initScripts:               make([]InitScript, 0),
		namespaces:                defaultNamespaceRegistrations(),
		runtimeFields:             runtimeFields,
		stopping:                  make(chan struct{}),
		initScriptInstalls:        map[initScriptInstallKey]initScriptInstallState{},
		initScriptInstallInflight: map[initScriptInstallKey]*initScriptInstallCall{},
		initScriptEnsureInflight:  map[initScriptEnsureKey]*initScriptEnsureCall{},
		lifecycleHandlers:         map[string]lifecycleRegistration{},
		lifecycleDispatch:         map[string]*lifecycleDispatchQueue{},
		dialogPolicy:              JavaScriptDialogPolicyAutoHandle,
		printPolicy:               PrintPolicyIntercept,
		actionMode:                defaultActionMode,
		pageLifecycle:             map[string]*pageLifecycleState{},
		pendingNavigationReasons:  map[navigationReasonKey]pendingNavigationReason{},
	}
	r.setState(managerStateStarting)
	failSetup := func(err error) (*BrowserManager, error) {
		if setupCtx.Err() != nil {
			err = errors.Join(err, setupCtx.Err())
		}
		_ = r.shutdown(context.Background(), shutdownReasonSetupFailed)
		return nil, err
	}

	if err := r.RegisterBindingInNamespace(NamespaceIsolatedCore, NewCallCoreBinding()); err != nil {
		return failSetup(err)
	}
	if err := r.RegisterInitScript(InitScript{
		Name:      "core.js",
		Namespace: NamespaceIsolatedCore,
		Exec:      renderCoreScript(webassets.CoreJs, r.runtimeFields),
		Probe:     renderCoreScript(webassets.CoreJsProbe, r.runtimeFields),
		Cleanup:   webassets.CoreJsCleanup,
		Label:     r.runtimeFields.ScriptLabel,
	}); err != nil {
		return failSetup(err)
	}
	if err := r.RegisterInitScript(InitScript{Name: "main_runtime.js", Namespace: NamespaceMainRuntime, OnDemand: true, Exec: mainWorldInstallSource(webassets.MainRuntimeJs, r.runtimeFields.MainRuntime), Probe: renderMainWorldScript(webassets.MainRuntimeJsProbe, r.runtimeFields), Cleanup: renderMainWorldScript(webassets.MainRuntimeJsCleanup, r.runtimeFields), Label: r.runtimeFields.ScriptLabel}); err != nil {
		return failSetup(err)
	}
	if config.Initialize != nil {
		if err := config.Initialize(r); err != nil {
			return failSetup(fmt.Errorf("configure browser runtime: %w", err))
		}
	}
	var result map[string]any
	if browserWS == "" {
		if err := r.HTTPGet(setupCtx, "/json/version", &result); err != nil {
			return failSetup(fmt.Errorf("discover browser endpoint: %w", err))
		}
		var ok bool
		browserWS, ok = result["webSocketDebuggerUrl"].(string)
		if !ok {
			return failSetup(errors.New("未找到 webSocketDebuggerUrl"))
		}
	}
	ws, closed, err := r.wsWithContexts(setupCtx, r.ctx, browserWS)
	if err != nil {
		return failSetup(fmt.Errorf("connect browser websocket: %w", err))
	}
	r.conn = ws
	syncutil.Go(func() {
		r.watchLifecycle(lifetime, closed)
	})

	// Subscribe before discovery so existing and newly created targets share one stream.
	event, f, err := r.TargetSetDiscoverTargets(true)
	if err != nil {
		return failSetup(fmt.Errorf("discover browser targets: %w", err))
	}
	if err := r.TargetSetAutoAttach(true, false, true); err != nil {
		f()
		return failSetup(fmt.Errorf("attach browser targets: %w", err))
	}
	if err := setupCtx.Err(); err != nil {
		f()
		return failSetup(err)
	}

	syncutil.Go(func() {
		defer f()
		defer func() {
			slog.Debug("browser disconnected")
		}()

		// Discovery reports existing targets as well as newly created targets.
		for cdpEvent := range event {
			if cdpEvent.SessionID != "" {
				r.handleTargetSessionEvent(cdpEvent)
				continue
			}
			info := MustTargetInfo(&cdpEvent)
			switch cdpEvent.Method {
			case "Target.targetCreated", "Target.targetInfoChanged":
				if info.Type != "page" {
					continue
				}
				if cdpEvent.Method == "Target.targetInfoChanged" && strings.TrimSpace(info.URL) == "" {
					continue
				}
				if IsInternalPageURL(info.URL) {
					r.updatePageTargetInfo(info)
					if r.currentActivePageID() == info.TargetID {
						r.clearActivePage(info.TargetID, LifecycleSourceTarget, "target_internal")
					}
					continue
				}
				r.markPageDiscovered(info, nil)
				if IsPlaceholderPageURL(info.URL) && r.currentActivePageID() == info.TargetID {
					r.clearActivePage(info.TargetID, LifecycleSourceTarget, "target_placeholder")
				}
				r.requestPageBind(info.TargetID, false)
				if IsExecutablePageURL(info.URL) && r.getLastActivePageID(false) == "" {
					r.setActivePage(info.TargetID, LifecycleSourceTarget, "target_executable")
				}
			case "Target.attachedToTarget":
				r.handleAttachedTargetEvent(cdpEvent, nil)
			case "Target.detachedFromTarget":
				sessionID, _ := cdpEvent.Params["sessionId"].(string)
				if sessionID != "" {
					r.removeTargetSession(sessionID)
				}
			case "Target.targetDestroyed":
				targetID, _ := cdpEvent.Params["targetId"].(string)
				r.destroyPageTarget(targetID, "target_destroyed")
			}
		}
	})

	r.setState(managerStateRunning)
	return r, nil
}
