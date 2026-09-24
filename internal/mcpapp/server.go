package mcpapp

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"gopkg.d7z.net/cdp"
	"gopkg.d7z.net/cdp/snapshot"
)

const instructions = `Use browser_navigate or browser_tabs(new), then browser_snapshot. Snapshot refs such as s17/e5 are exact references to the current DOM snapshot. Every new snapshot invalidates all previous refs. Element actions never retry or fall back to a similar element. Actions return a compact semantic delta; navigation returns a full snapshot.`

type Service struct {
	ctx     context.Context
	browser *BrowserService
	mu      sync.RWMutex
	tabs    map[string]*tabRuntime
	active  string
}

func NewService(browser *BrowserService) *Service {
	return &Service{ctx: context.Background(), browser: browser, tabs: map[string]*tabRuntime{}}
}

func New(browser *cdp.Browser, config Config) (*App, error) {
	options := HTTPOptions{AllowedOrigins: config.AllowedOrigins}
	options, err := NormalizeHTTPOptions(options)
	if err != nil {
		return nil, err
	}
	service := NewService(nil)
	if browser != nil {
		service.browser = BorrowBrowser(browser, config.MaxTabs)
	}
	life, cancel := context.WithCancel(context.Background())
	service.ctx = life
	server := mcp.NewServer(&mcp.Implementation{Name: "cdp-browser-tools", Version: "2.0.0"}, &mcp.ServerOptions{Instructions: instructions})
	catalog := newToolCatalog()

	registerTypedTool(catalog, server, &mcp.Tool{Name: "browser_navigate", Description: "Navigate a new or existing tab and return a full AI snapshot."}, service.navigate)
	registerTypedTool(catalog, server, &mcp.Tool{Name: "browser_history", Description: "Go back, forward, or reload in an existing tab and return a full snapshot."}, service.history)
	registerTypedTool(catalog, server, &mcp.Tool{Name: "browser_snapshot", Description: "Capture one composed-DOM AI snapshot. A cursor pages the current snapshot without recapturing."}, service.capture)
	registerTypedTool(catalog, server, &mcp.Tool{Name: "browser_find", Description: "Search the current AI snapshot by text, role, accessible name, and state."}, service.find)
	registerTypedTool(catalog, server, &mcp.Tool{Name: "browser_click", Description: "Click one exact current snapshot ref."}, service.click)
	registerTypedTool(catalog, server, &mcp.Tool{Name: "browser_type", Description: "Replace or append text in one exact current snapshot ref."}, service.typeText)
	registerTypedTool(catalog, server, &mcp.Tool{Name: "browser_select", Description: "Select option values on one exact current snapshot ref."}, service.selectOptions)
	registerTypedTool(catalog, server, &mcp.Tool{Name: "browser_check", Description: "Set the checked state of one exact current snapshot ref."}, service.check)
	registerTypedTool(catalog, server, &mcp.Tool{Name: "browser_hover", Description: "Hover one exact current snapshot ref."}, service.hover)
	registerTypedTool(catalog, server, &mcp.Tool{Name: "browser_drag", Description: "Drag from one exact current snapshot ref to another."}, service.drag)
	registerTypedTool(catalog, server, &mcp.Tool{Name: "browser_press_key", Description: "Press a key on the page or after focusing an exact ref."}, service.pressKey)
	registerTypedTool(catalog, server, &mcp.Tool{Name: "browser_scroll", Description: "Explicitly scroll the page or an exact scroll-container ref."}, service.scroll)
	registerTypedTool(catalog, server, &mcp.Tool{Name: "browser_upload", Description: "Set local files on an exact file-input ref."}, service.upload)
	registerTypedTool(catalog, server, &mcp.Tool{Name: "browser_wait", Description: "Wait for an explicit time, text, URL, or network-idle condition."}, service.wait)
	registerTypedTool(catalog, server, &mcp.Tool{Name: "browser_dialog", Description: "Inspect, accept, or dismiss the current JavaScript dialog."}, service.dialog)
	registerTypedTool(catalog, server, &mcp.Tool{Name: "browser_screenshot", Description: "Capture the page or exact element as MCP image content without pre-actions."}, service.screenshot)
	registerTypedTool(catalog, server, &mcp.Tool{Name: "browser_evaluate", Description: "Await JavaScript in page main world, optionally with an exact ref as this."}, service.evaluate)
	registerTypedTool(catalog, server, &mcp.Tool{Name: "browser_tabs", Description: "List, create, select, or close browser tabs."}, service.tabAction)
	registerTypedTool(catalog, server, &mcp.Tool{Name: "browser_console", Description: "Read structured console events after a monotonic cursor."}, service.consoleEvents)
	registerTypedTool(catalog, server, &mcp.Tool{Name: "browser_requests", Description: "Read structured request, response, failure, and download events after a cursor."}, service.requestEvents)

	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{JSONResponse: true})
	protectedMCPHandler, err := newMCPHTTPHandler(mcpHandler, options)
	if err != nil {
		cancel()
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp", protectedMCPHandler)
	if config.EnableDebug {
		debugMux := http.NewServeMux()
		registerDebugRoutes(debugMux, service, catalog)
		mux.Handle("/", newDebugHTTPHandler(debugMux))
	}
	return &App{handler: mux, service: service, ctx: life, cancel: cancel}, nil
}

func (s *Service) runtime(ctx context.Context, tabID string) (*tabRuntime, error) {
	s.mu.RLock()
	runtime := s.tabs[tabID]
	s.mu.RUnlock()
	if runtime != nil && runtime.currentState() != TabClosed {
		return runtime, nil
	}
	page, err := s.browser.Page(ctx, tabID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if runtime = s.tabs[tabID]; runtime == nil || runtime.currentState() == TabClosed {
		runtime = newTabRuntime(s.ctx, page, s.browser.browser.Diagnostics())
		s.tabs[tabID] = runtime
	}
	return runtime, nil
}

func (s *Service) attach(page *cdp.Page) *tabRuntime {
	s.mu.Lock()
	defer s.mu.Unlock()
	runtime := s.tabs[page.ID()]
	if runtime == nil || runtime.currentState() == TabClosed {
		runtime = newTabRuntime(s.ctx, page, s.browser.browser.Diagnostics())
		s.tabs[page.ID()] = runtime
	}
	s.active = page.ID()
	return runtime
}

func (s *Service) setActive(tabID string) {
	s.mu.Lock()
	s.active = tabID
	s.mu.Unlock()
}

func (s *Service) activeID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.active
}

func renderSnapshot(document snapshot.Document, cursor string, maxRunes int) (*SnapshotView, error) {
	rendered, err := snapshot.Render(document, snapshot.RenderOptions{Cursor: cursor, MaxRunes: maxRunes})
	if err != nil {
		return nil, err
	}
	return &SnapshotView{ID: document.ID, URL: document.URL, Title: document.Title, Markdown: rendered.Markdown, Cursor: rendered.Cursor, HasMore: rendered.HasMore}, nil
}

func failedBase(tabID string, state TabState, op, ref string, err error) BaseOutput {
	return BaseOutput{OK: false, TabID: tabID, State: state, Error: normalizeToolError(op, ref, err)}
}

func (s *Service) navigate(ctx context.Context, _ *mcp.CallToolRequest, input NavigateInput) (*mcp.CallToolResult, NavigateOutput, error) {
	var runtime *tabRuntime
	if input.TabID != "" {
		var err error
		runtime, err = s.runtime(ctx, input.TabID)
		if err != nil {
			out := NavigateOutput{BaseOutput: failedBase(input.TabID, "", "browser_navigate", "", err)}
			return &mcp.CallToolResult{IsError: true}, out, nil
		}
		if err := runtime.acquireModalSensitive(ctx, "browser_navigate", false); err != nil {
			out := NavigateOutput{BaseOutput: failedBase(input.TabID, runtime.currentState(), "browser_navigate", "", err)}
			return &mcp.CallToolResult{IsError: true}, out, nil
		}
		defer runtime.release()
		runtime.setState(TabNavigating)
	}
	page, document, warnings, err := s.browser.Navigate(ctx, input.TabID, input.URL)
	if err != nil {
		state := TabState("")
		if runtime != nil {
			runtime.setState(TabReady)
			runtime.setPhase(phaseFailed)
			state = runtime.currentState()
		}
		out := NavigateOutput{BaseOutput: failedBase(input.TabID, state, "browser_navigate", "", err)}
		out.Warnings = warnings
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	runtime = s.attach(page)
	runtime.setState(TabReady)
	runtime.setPhase(phaseCompleted)
	view, err := renderSnapshot(document, "", 0)
	if err != nil {
		out := NavigateOutput{BaseOutput: failedBase(page.ID(), runtime.currentState(), "browser_navigate.render", "", err)}
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	return nil, NavigateOutput{BaseOutput: BaseOutput{OK: true, TabID: page.ID(), State: runtime.currentState(), Warnings: append(warnings, document.Warnings...)}, Snapshot: view}, nil
}

func (s *Service) history(ctx context.Context, _ *mcp.CallToolRequest, input HistoryInput) (*mcp.CallToolResult, HistoryOutput, error) {
	runtime, err := s.runtime(ctx, input.TabID)
	if err != nil {
		out := HistoryOutput{BaseOutput: failedBase(input.TabID, "", "browser_history", "", err)}
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	if err := runtime.acquireModalSensitive(ctx, "browser_history", false); err != nil {
		out := HistoryOutput{BaseOutput: failedBase(input.TabID, runtime.currentState(), "browser_history", "", err)}
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	defer runtime.release()
	runtime.setState(TabNavigating)
	runtime.setPhase(phaseExecuting)
	sub, subErr := runtime.page.Session().Subscribe(ctx, "Page.domContentEventFired", "Page.loadEventFired")
	if subErr != nil {
		return nil, NavigateOutput{}, subErr
	}
	defer sub.Close()
	events := sub.Events()
	switch input.Action {
	case "back":
		err = runtime.page.Back(ctx)
	case "forward":
		err = runtime.page.Forward(ctx)
	case "reload":
		err = runtime.page.Reload(ctx)
	default:
		err = NewToolError("invalid_argument", "browser_history", "action must be back, forward, or reload", nil)
	}
	warnings := []string(nil)
	if err == nil {
		warnings, err = waitForDocument(ctx, runtime.page, events, 30*time.Second)
	}
	var document snapshot.Document
	if err == nil {
		runtime.setPhase(phaseCapturing)
		document, err = runtime.page.Snapshot(ctx)
	}
	if err != nil {
		runtime.setState(TabReady)
		runtime.setPhase(phaseFailed)
		out := HistoryOutput{BaseOutput: failedBase(input.TabID, runtime.currentState(), "browser_history", "", err)}
		out.Warnings = warnings
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	runtime.setState(TabReady)
	view, err := renderSnapshot(document, "", 0)
	if err != nil {
		runtime.setPhase(phaseFailed)
		out := HistoryOutput{BaseOutput: failedBase(input.TabID, runtime.currentState(), "browser_history.render", "", err)}
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	runtime.setPhase(phaseCompleted)
	return nil, HistoryOutput{BaseOutput: BaseOutput{OK: true, TabID: input.TabID, State: runtime.currentState(), Warnings: append(warnings, document.Warnings...)}, Snapshot: view}, nil
}

func (s *Service) capture(ctx context.Context, _ *mcp.CallToolRequest, input SnapshotInput) (*mcp.CallToolResult, SnapshotOutput, error) {
	runtime, err := s.runtime(ctx, input.TabID)
	if err != nil {
		out := SnapshotOutput{BaseOutput: failedBase(input.TabID, "", "browser_snapshot", "", err)}
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	if err := runtime.acquireModalSensitive(ctx, "browser_snapshot", false); err != nil {
		out := SnapshotOutput{BaseOutput: failedBase(input.TabID, runtime.currentState(), "browser_snapshot", "", err)}
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	defer runtime.release()
	var document snapshot.Document
	if input.Cursor == "" {
		runtime.setPhase(phaseCapturing)
		document, err = runtime.page.Snapshot(ctx)
	} else {
		var ok bool
		document, ok = runtime.page.CurrentSnapshot()
		if !ok {
			err = NewToolError("stale_target", "browser_snapshot", "there is no current snapshot for this cursor", nil)
		}
	}
	if err != nil {
		runtime.setPhase(phaseFailed)
		out := SnapshotOutput{BaseOutput: failedBase(input.TabID, runtime.currentState(), "browser_snapshot", "", err)}
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	view, err := renderSnapshot(document, input.Cursor, input.MaxRunes)
	if err != nil {
		runtime.setPhase(phaseFailed)
		out := SnapshotOutput{BaseOutput: failedBase(input.TabID, runtime.currentState(), "browser_snapshot.render", "", err)}
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	runtime.setPhase(phaseCompleted)
	return nil, SnapshotOutput{BaseOutput: BaseOutput{OK: true, TabID: input.TabID, State: runtime.currentState(), Warnings: document.Warnings}, Snapshot: view}, nil
}

func (s *Service) find(ctx context.Context, _ *mcp.CallToolRequest, input FindInput) (*mcp.CallToolResult, FindOutput, error) {
	runtime, err := s.runtime(ctx, input.TabID)
	if err != nil {
		out := FindOutput{BaseOutput: failedBase(input.TabID, "", "browser_find", "", err), Matches: []snapshot.Match{}}
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	document, ok := runtime.page.CurrentSnapshot()
	if !ok {
		out := FindOutput{BaseOutput: failedBase(input.TabID, runtime.currentState(), "browser_find", "", NewToolError("stale_target", "browser_find", "capture a snapshot before searching", nil)), Matches: []snapshot.Match{}}
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	matches := snapshot.Search(document, snapshot.SearchQuery{Text: input.Text, Role: input.Role, Name: input.Name, States: input.States, Limit: input.Limit})
	return nil, FindOutput{BaseOutput: BaseOutput{OK: true, TabID: input.TabID, State: runtime.currentState()}, Matches: matches}, nil
}

func parseTimeWait(value string) (time.Duration, error) {
	milliseconds, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || milliseconds < 0 {
		return 0, NewToolError("invalid_argument", "browser_wait", "time value must be non-negative milliseconds", err)
	}
	return time.Duration(milliseconds) * time.Millisecond, nil
}

func decodeJSONValue(raw string) (any, error) {
	if raw == "undefined" {
		return nil, nil
	}
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, err
	}
	return value, nil
}

type Config struct {
	AllowedOrigins []string
	MaxTabs        int
	EnableDebug    bool
}
type App struct {
	handler http.Handler
	service *Service
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	closed  bool
	wg      sync.WaitGroup
	once    sync.Once
}

func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		http.Error(w, "server closed", http.StatusServiceUnavailable)
		return
	}
	a.wg.Add(1)
	a.mu.Unlock()
	defer a.wg.Done()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stop := context.AfterFunc(a.ctx, cancel)
	defer stop()
	a.handler.ServeHTTP(w, r.WithContext(ctx))
}
func (a *App) Close() error {
	a.once.Do(func() {
		a.mu.Lock()
		a.closed = true
		a.cancel()
		a.mu.Unlock()
		a.service.mu.RLock()
		tabs := make([]*tabRuntime, 0, len(a.service.tabs))
		for _, t := range a.service.tabs {
			tabs = append(tabs, t)
		}
		a.service.mu.RUnlock()
		for _, t := range tabs {
			if t.observer != nil && t.observer.subscription != nil {
				_ = t.observer.subscription.Close()
			}
		}
		a.wg.Wait()
	})
	return nil
}
