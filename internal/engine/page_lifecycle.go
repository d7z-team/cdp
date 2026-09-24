package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"gopkg.d7z.net/cdp/internal/binding"
	"gopkg.d7z.net/cdp/internal/syncutil"
)

func (p *Page) waitInitContext(ctx context.Context) error {
	if p == nil || p.initDone == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-p.initDone:
		p.initMu.Lock()
		err := p.initErr
		p.initMu.Unlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *Page) finishInit(err error) {
	p.initMu.Lock()
	p.initErr = err
	p.initMu.Unlock()
	close(p.initDone)
}

func (p *Page) mainFrame() string {
	if p == nil {
		return ""
	}
	p.frameMu.RLock()
	defer p.frameMu.RUnlock()
	return strings.TrimSpace(p.mainFrameID)
}

func (p *Page) setMainFrame(frameID string) {
	if p == nil {
		return
	}
	frameID = strings.TrimSpace(frameID)
	p.frameMu.Lock()
	p.mainFrameID = frameID
	if frameID != "" && p.mainFrameReady != nil {
		close(p.mainFrameReady)
		p.mainFrameReady = nil
	}
	p.frameMu.Unlock()
}

func (p *Page) waitMainFrame(ctx context.Context) (string, error) {
	if p == nil {
		return "", errors.New("page is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		p.frameMu.Lock()
		if frameID := strings.TrimSpace(p.mainFrameID); frameID != "" {
			p.frameMu.Unlock()
			return frameID, nil
		}
		if p.mainFrameReady == nil {
			p.mainFrameReady = make(chan struct{})
		}
		ready := p.mainFrameReady
		p.frameMu.Unlock()

		select {
		case <-ready:
		case <-ctx.Done():
			return "", ctx.Err()
		case <-p.Done():
			return "", ErrBrowserClosed
		}
	}
}

func (p *Page) bind(initCtx, pageCtx context.Context, ws *CdpConn, manager *BrowserManager) (bindErr error) {
	if initCtx == nil {
		initCtx = context.Background()
	}
	p.ctx = pageCtx
	p.CdpConn = ws
	p.manager = manager
	// OOPIF target-session events can arrive through the browser connection
	// before the root page websocket is ready. Reset only the root-session state
	// so those already-routed iframe contexts survive page initialization.
	p.clearExecutionContexts()
	p.nodeRefMu.Lock()
	p.nodeBackendIDs = map[targetNodeKey]int{}
	p.nodeRefMu.Unlock()
	p.snapshotMu.Lock()
	p.currentSnapshot = nil
	p.snapshotMu.Unlock()
	if manager.getLastActivePageID(false) == "" && manager.isUsableActivePage(p.ID) {
		manager.SetLastActivePageID(p.ID)
	}

	// 订阅所有事件
	events, unsubscribe := ws.SubscribeReliable(
		"Network.requestWillBeSent", "Network.loadingFinished", "Network.loadingFailed",
		"Page.frameScheduledNavigation",
		"Page.frameStartedNavigating",
		"Page.frameNavigated",
		"Page.frameDetached", "Page.frameAttached",
		"Page.navigatedWithinDocument",
		"Page.frameStoppedLoading",
		"Page.javascriptDialogOpening",
		"Page.javascriptDialogClosed",
		"Runtime.executionContextCreated",
		"Runtime.executionContextDestroyed",
		"Runtime.executionContextsCleared",
		"Runtime.bindingCalled",
	)
	syncutil.Go(func() {
		defer unsubscribe()
		for event := range events {
			if !manager.isCurrentManagedPage(p) {
				return
			}
			p.handleRootPageEvent(event)
		}
	})

	// 顺序初始化
	for _, method := range []string{"Page.enable", "DOM.enable", "Network.enable"} {
		if _, err := ws.SendMessageContext(initCtx, method, nil); err != nil {
			return fmt.Errorf("initialize page %s: %w", method, BrowserErrorFromCDP(method, topPageExecutionTarget(), err))
		}
	}
	if err := manager.enableRuntimeDiagnostics(initCtx, ws, ""); err != nil {
		return err
	}
	// Register each binding and script once. runImmediately covers existing
	// contexts and future documents execute the registered source automatically.
	if err := manager.registerPageBindingsAndScripts(initCtx, p, "new_doc"); err != nil {
		return fmt.Errorf("initialize page registrations: %w", err)
	}
	// A synchronous window.open keeps its opener blocked until this target is
	// resumed. Register future-document sources first, then evaluate contexts.
	if _, err := ws.SendMessageContext(initCtx, "Runtime.runIfWaitingForDebugger", nil); err != nil {
		return fmt.Errorf("initialize page Runtime.runIfWaitingForDebugger: %w", BrowserErrorFromCDP("Runtime.runIfWaitingForDebugger", topPageExecutionTarget(), err))
	}
	if err := manager.reconcileDocumentRuntime(initCtx, p, ""); err != nil {
		return err
	}
	if err := ws.flushEvents(initCtx); err != nil {
		return fmt.Errorf("flush initial page events: %w", err)
	}
	for _, script := range manager.initScriptsSnapshot() {
		if err := p.ensureInitScriptInjectedInRuntimeContexts(initCtx, script, "bind_ctx"); err != nil && !isSupersededDocumentError(err) {
			return fmt.Errorf("initialize script %s in current runtime: %w", script.Name, err)
		}
	}

	if IsExecutablePageURL(manager.TopPageURL(p.ID)) {
		if _, err := p.waitMainFrame(initCtx); err != nil {
			return fmt.Errorf("wait for executable page main frame: %w", err)
		}
	}
	slog.Debug("page init", "page_id", p.ID)

	return nil
}

func (p *Page) handleRootPageEvent(event CDPResponse) {
	p.observeNetworkEvent(event)
	manager := p.manager
	switch event.Method {
	case "Page.frameScheduledNavigation":
		frameID, _ := event.Params["frameId"].(string)
		manager.rememberPendingNavigationReason(p.ID, frameID, navigationReasonFromParams(event.Params), navigationURLFromParams(event.Params))
	case "Page.frameStartedNavigating":
		frameID, _ := event.Params["frameId"].(string)
		pageURL := navigationURLFromParams(event.Params)
		reason := navigationReasonFromParams(event.Params)
		manager.rememberPendingNavigationReason(p.ID, frameID, reason, pageURL)
		if mainFrameID := p.mainFrame(); mainFrameID != "" && frameID == mainFrameID {
			p.topNavigationPending.Store(true)
			manager.emitPageNavigating(p, pageURL, reason)
		}
	case "Page.frameNavigated":
		frame, ok := event.Params["frame"].(map[string]any)
		if !ok {
			return
		}
		navigatedID, _ := frame["id"].(string)
		p.forgetFrameContexts("", navigatedID)
		if parent, _ := frame["parentId"].(string); parent == "" {
			p.clearExecutionContexts()
		}
		parentID, _ := frame["parentId"].(string)
		if parentID != "" {
			p.reconcilePageRegistrationsAsync("child navigation")
			return
		}
		frameID, _ := frame["id"].(string)
		p.setMainFrame(frameID)
		p.lock.Lock()
		p.LastNavAt = time.Now()
		p.lock.Unlock()
		p.topNavigationPending.Store(false)
		previousURL := manager.snapshotPageState(p.ID).url
		pageURL, _ := frame["url"].(string)
		reason := manager.consumePendingNavigationReason(p.ID, frameID, pageURL)
		if reason == "" {
			reason = p.resolveNavigationReasonFromHistory(pageURL)
		}
		manager.emitPageNavigated(p, "", pageURL, reason)
		p.reconcilePageRegistrationsAsync("navigation")
		if previousURL != "" && pageURL != "" && previousURL != pageURL {
			manager.emitPageURLChanged(p, previousURL, pageURL, reason)
		}
	case "Page.navigatedWithinDocument":
		frameID, _ := event.Params["frameId"].(string)
		mainFrameID := p.mainFrame()
		if mainFrameID == "" || frameID != mainFrameID {
			return
		}
		p.topNavigationPending.Store(false)
		previousURL := manager.snapshotPageState(p.ID).url
		pageURL, _ := event.Params["url"].(string)
		reason := manager.consumePendingNavigationReason(p.ID, frameID, pageURL)
		if reason == "" {
			reason = p.resolveNavigationReasonFromHistory(pageURL)
		}
		manager.emitPageNavigated(p, "", pageURL, reason)
		p.reconcilePageRegistrationsAsync("same-document navigation")
		if previousURL != "" && pageURL != "" && previousURL != pageURL {
			manager.emitPageURLChanged(p, previousURL, pageURL, reason)
		}
	case "Page.frameStoppedLoading":
		frameID, _ := event.Params["frameId"].(string)
		mainFrameID := p.mainFrame()
		if frameID != "" && mainFrameID != "" && frameID != mainFrameID {
			return
		}
		if p.topNavigationPending.Swap(false) {
			manager.emitPageNavigationCanceled(p, manager.TopPageURL(p.ID), "frame_stopped_loading")
			p.reconcilePageRegistrationsAsync("canceled navigation")
		}
	case "Page.javascriptDialogOpening":
		if err := p.handleJavaScriptDialogOpening(event.Params); err != nil && !errors.Is(err, ErrBrowserClosed) {
			slog.Error("handle javascript dialog error", "error", err, "page_id", p.ID)
		}
	case "Page.javascriptDialogClosed":
		p.handleJavaScriptDialogClosed()
	case "Page.frameAttached", "Page.frameDetached":
		if event.Method == "Page.frameDetached" {
			id, _ := event.Params["frameId"].(string)
			p.forgetFrameContexts("", id)
		}
		p.reconcilePageRegistrationsAsync("frame topology")
	case "Runtime.bindingCalled":
		var bindingData binding.BindingCalledEvent
		if err := event.ParamsUnmarshal(&bindingData); err != nil {
			slog.Error("parse binding call", "error", err, "page_id", p.ID)
			return
		}
		handle := func() {
			if !manager.isCurrentManagedPage(p) {
				return
			}
			if err := manager.handleBindingCalled(p, &bindingData); err != nil && !errors.Is(err, ErrBrowserClosed) {
				slog.Error("handle binding call", "error", err, "page_id", p.ID)
			}
		}
		if bindingCallKind(bindingData.Payload) == "notify" {
			p.enqueueBindingNotification(handle)
		} else {
			syncutil.Go(handle)
		}
	}
}

func (p *Page) reconcilePageRegistrationsAsync(reason string) {
	syncutil.Go(func() {
		if err := p.manager.reconcilePageRegistrations(p.ctx, p); err != nil && !errors.Is(err, ErrBrowserClosed) {
			slog.Debug("reconcile page registrations failed", "page_id", p.ID, "reason", reason, "error", err)
		}
	})
}

func bindingCallKind(payload string) string {
	var header struct {
		Kind string `json:"kind"`
	}
	_ = json.Unmarshal([]byte(payload), &header)
	return header.Kind
}

func (p *Page) enqueueBindingNotification(handle func()) {
	if p == nil || handle == nil {
		return
	}
	p.bindingNotifyMu.Lock()
	if p.bindingNotifyClosed {
		p.bindingNotifyMu.Unlock()
		return
	}
	p.bindingNotifyPending = append(p.bindingNotifyPending, handle)
	if p.bindingNotifyRunning {
		p.bindingNotifyMu.Unlock()
		return
	}
	p.bindingNotifyRunning = true
	p.bindingNotifyMu.Unlock()
	syncutil.Go(func() {
		for {
			p.bindingNotifyMu.Lock()
			if p.bindingNotifyClosed || len(p.bindingNotifyPending) == 0 {
				p.bindingNotifyPending = nil
				p.bindingNotifyRunning = false
				p.bindingNotifyMu.Unlock()
				return
			}
			next := p.bindingNotifyPending[0]
			p.bindingNotifyPending[0] = nil
			p.bindingNotifyPending = p.bindingNotifyPending[1:]
			p.bindingNotifyMu.Unlock()
			next()
		}
	})
}

func (p *Page) unbind() {
	p.screencastOpMu.Lock()
	defer p.screencastOpMu.Unlock()
	p.lock.Lock()
	defer p.lock.Unlock()
	p.closeRuntimeReady()
	p.bindingNotifyMu.Lock()
	p.bindingNotifyClosed = true
	p.bindingNotifyPending = nil
	p.bindingNotifyMu.Unlock()
	p.screencastMu.Lock()
	state := p.takeScreencastState()
	p.screencastOwners = nil
	p.screencastMu.Unlock()
	if state != nil {
		if state.cancelSub != nil {
			state.cancelSub()
		}
		for _, waiter := range state.waiters {
			close(waiter)
		}
	}
	p.CdpConn = nil
	p.bindingInstallMu.Lock()
	p.installedBindings = nil
	p.bindingInstallMu.Unlock()
	p.topNavigationPending.Store(false)
	p.contextMu.Lock()
	p.executionContexts = nil
	p.frameContexts = nil
	p.executionTargetsByRuntime = nil
	p.executionTargetsByContext = nil
	p.targetEpochs = nil
	p.contextMu.Unlock()
	p.setMainFrame("")
	p.nodeRefMu.Lock()
	p.nodeBackendIDs = nil
	p.nodeRefMu.Unlock()
	p.dialogLock.Lock()
	p.currentDialog = nil
	p.signalJavaScriptDialogChangeLocked()
	p.dialogLock.Unlock()
	slog.Info("page destroy", "page_id", p.ID)
}

func (p *Page) closeRuntimeReady() {
	if p == nil {
		return
	}
	p.runtimeReadyMu.Lock()
	p.runtimeReadyClosed = true
	p.runtimeReadyPublished = false
	p.pendingRuntimeReady = nil
	p.runtimeReadyContexts = nil
	p.runtimeReadyMu.Unlock()
}

func (p *Page) checkConn() error {
	return p.checkConnContext(context.Background())
}

func (p *Page) checkConnContext(ctx context.Context) error {
	if p == nil {
		return errors.New("页面未连接")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := p.waitInitContext(ctx); err != nil {
		return err
	}
	if p.CdpConn == nil {
		if p.ctx != nil {
			select {
			case <-p.Done():
				return ErrBrowserClosed
			default:
			}
		}
		return errors.New("页面未连接")
	}
	return p.CdpConn.unavailableErr()
}
