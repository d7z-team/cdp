package engine

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gopkg.d7z.net/cdp/internal/pageurl"
)

type PageState int

type PageStat struct {
	ID   string
	Type PageState

	Title string
	URL   string
}

type Page struct {
	networkMu      sync.Mutex
	networkPending map[string]networkRequest
	networkChanged time.Time
	ID             string
	CdpConn        *CdpConn
	ctx            context.Context
	manager        *BrowserManager

	lock sync.RWMutex

	nodeRefMu      sync.RWMutex
	nodeBackendIDs map[targetNodeKey]int

	screencastOpMu    sync.Mutex
	screencastMu      sync.Mutex
	screencast        *pageScreencastState
	screencastChanged chan struct{}
	screencastSeq     uint64
	screencastGen     uint64
	screencastOwners  map[ScreencastOwner]ScreencastOptions

	frameGenerations          map[string]uint64
	contextMu                 sync.RWMutex
	executionContexts         map[targetContextKey]executionContextInfo
	frameContexts             map[string]targetContextKey
	executionTargetsByRuntime map[string]ExecutionTarget
	executionTargetsByContext map[targetContextKey]ExecutionTarget
	targetEpochs              map[string]int64

	bindingNotifyMu       sync.Mutex
	bindingNotifyPending  []func()
	bindingNotifyRunning  bool
	bindingNotifyClosed   bool
	bindingInstallMu      sync.Mutex
	installedBindings     map[string]struct{}
	runtimeReadyMu        sync.Mutex
	runtimeReadyPublished bool
	runtimeReadyClosed    bool
	pendingRuntimeReady   map[string]pendingPageRuntimeReady
	runtimeReadyContexts  map[string]runtimeReadyIdentity
	topNavigationPending  atomic.Bool

	snapshotMu      sync.RWMutex
	currentSnapshot *AISnapshotSession

	dialogLock        sync.Mutex
	dialogPolicyMu    sync.RWMutex
	nextDialogHandler *dialogHandler
	dialogPolicy      JavaScriptDialogPolicy
	currentDialog     *JavaScriptDialog
	dialogSequence    uint64
	dialogChanged     chan struct{}
	printPolicyMu     sync.RWMutex
	printPolicy       PrintPolicy
	printMu           sync.Mutex
	nextPrintWaiter   chan printResult
	fullscreenMu      sync.RWMutex
	fullscreenActive  bool
	fullscreenElement string
	actionModeMu      sync.RWMutex
	actionMode        ActionMode

	initMu         sync.Mutex
	initErr        error
	initDone       chan struct{}
	timeout        time.Duration
	frameMu        sync.RWMutex
	mainFrameID    string
	mainFrameReady chan struct{}

	// 记录当前鼠标位置
	mouseX float64
	mouseY float64

	CreatedAt time.Time
	LastNavAt time.Time
}

func NewPageWithContext(ctx context.Context) *Page {
	if ctx == nil {
		ctx = context.Background()
	}
	return &Page{
		ctx:            ctx,
		mainFrameReady: make(chan struct{}),
		initDone: func() chan struct{} {
			ch := make(chan struct{})
			close(ch)
			return ch
		}(),
		timeout: 10 * time.Second,
	}
}

func (p *Page) Deadline() (time.Time, bool) {
	if p == nil || p.ctx == nil {
		return time.Time{}, false
	}
	return p.ctx.Deadline()
}

func (p *Page) Done() <-chan struct{} {
	if p == nil || p.ctx == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return p.ctx.Done()
}

func (p *Page) Err() error {
	if p == nil || p.ctx == nil {
		return context.Canceled
	}
	return p.ctx.Err()
}

func (p *Page) Value(key any) any {
	if p == nil || p.ctx == nil {
		return nil
	}
	return p.ctx.Value(key)
}

type dialogHandler struct {
	accept     bool
	promptText string
}

type printResult struct {
	Artifact PrintArtifact
	Err      error
}

type JavaScriptDialogPolicy string

type JavaScriptDialog struct {
	ID         uint64 `json:"id"`
	Type       string `json:"type"`
	Message    string `json:"message"`
	Default    string `json:"default_prompt,omitempty"`
	HasHandler bool   `json:"has_browser_handler"`
}

var (
	ErrJavaScriptDialogNotOpen = errors.New("JavaScript dialog is not open")
	ErrJavaScriptDialogChanged = errors.New("JavaScript dialog has changed")
)

const (
	JavaScriptDialogPolicyAutoHandle  JavaScriptDialogPolicy = "auto_handle"
	JavaScriptDialogPolicyPassthrough JavaScriptDialogPolicy = "passthrough"
)

type PrintPolicy string

const (
	PrintPolicyIntercept   PrintPolicy = "intercept"
	PrintPolicyPassthrough PrintPolicy = "passthrough"
)

type PrintArtifact struct {
	Data      []byte
	MimeType  string
	URL       string
	Title     string
	Timestamp time.Time
}

type executionContextInfo struct {
	ID        int
	FrameID   string
	Name      string
	IsDefault bool
}

type navigationHistoryEntry struct {
	URL            string
	UserTypedURL   string
	TransitionType string
}

func navigationReasonFromParams(params map[string]any) string {
	reason, _ := params["reason"].(string)
	return strings.TrimSpace(reason)
}

func navigationURLFromParams(params map[string]any) string {
	pageURL, _ := params["url"].(string)
	return strings.TrimSpace(pageURL)
}

func navigationReasonFromTransitionType(transitionType string) string {
	switch strings.TrimSpace(transitionType) {
	case "typed", "address_bar", "auto_bookmark", "generated", "keyword", "keyword_generated":
		return strings.TrimSpace(transitionType)
	default:
		return ""
	}
}

// PageURLKind classifies top-level browser page URLs for CDP page selection.
type PageURLKind = pageurl.PageURLKind

const (
	PageURLExecutable  = pageurl.PageURLExecutable
	PageURLPlaceholder = pageurl.PageURLPlaceholder
	PageURLInternal    = pageurl.PageURLInternal
)

// ClassifyPageURL returns whether pageURL is executable, a placeholder, or an
// internal browser URL.
func ClassifyPageURL(pageURL string) PageURLKind {
	return pageurl.ClassifyPageURL(pageURL)
}

// IsExecutablePageURL reports whether pageURL can be used as an active page.
func IsExecutablePageURL(pageURL string) bool {
	return pageurl.IsExecutablePageURL(pageURL)
}

// IsPlaceholderPageURL reports whether pageURL is a blank or new-tab carrier.
func IsPlaceholderPageURL(pageURL string) bool {
	return pageurl.IsPlaceholderPageURL(pageURL)
}

// IsInternalPageURL reports whether pageURL is an internal browser URL.
func IsInternalPageURL(pageURL string) bool {
	return pageurl.IsInternalPageURL(pageURL)
}

func (p *Page) Timestamps() (time.Time, time.Time) {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return p.CreatedAt, p.LastNavAt
}
