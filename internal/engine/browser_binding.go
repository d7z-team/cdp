package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"gopkg.d7z.net/cdp/internal/syncutil"
)

func (r *BrowserManager) pageBindChangedLocked() <-chan struct{} {
	if r.pageBindChanged == nil {
		r.pageBindChanged = make(chan struct{})
	}
	return r.pageBindChanged
}

func (r *BrowserManager) notifyPageBindChangeLocked() {
	if r.pageBindChanged != nil {
		close(r.pageBindChanged)
	}
	r.pageBindChanged = make(chan struct{})
}

func (r *BrowserManager) newPageBindAttempt(id string) pageBindAttempt {
	pageCtx, pageCancel := context.WithCancel(r.ctx)
	return pageBindAttempt{
		page: &Page{
			ID:             id,
			ctx:            pageCtx,
			manager:        r,
			timeout:        10 * time.Second,
			mainFrameReady: make(chan struct{}),
			initDone:       make(chan struct{}),
			dialogPolicy:   r.JavaScriptDialogPolicy(),
			printPolicy:    r.PrintPolicy(),
			actionMode:     r.DefaultActionMode(),
			CreatedAt:      time.Now(),
		},
		cancel: pageCancel,
	}
}

func (r *BrowserManager) requestPageBind(id string, failedOnly bool) {
	id = strings.TrimSpace(id)
	if r == nil || id == "" || !r.IsAlive() {
		return
	}
	r.pageBindMu.Lock()
	if !r.IsAlive() {
		r.pageBindMu.Unlock()
		return
	}
	if r.pageBinds == nil {
		r.pageBinds = make(map[string]*pageBindState)
	}
	state := r.pageBinds[id]
	if failedOnly && (state == nil || state.inFlight || state.lastErr == nil) {
		r.pageBindMu.Unlock()
		return
	}
	if failedOnly {
		var networkError net.Error
		if !errors.As(state.lastErr, &networkError) && !errors.Is(state.lastErr, context.DeadlineExceeded) && !errors.Is(state.lastErr, errPageConnectionClosed) {
			r.pageBindMu.Unlock()
			return
		}
	}
	if state == nil {
		state = &pageBindState{}
		r.pageBinds[id] = state
	}
	state.targetVersion++
	if state.page != nil && !state.inFlight {
		// A healthy bound page already observes target changes through its live
		// CDP connection. Advance the reconnect baseline so an old navigation
		// cannot be mistaken for a target update that arrived during teardown.
		state.boundTargetVersion = state.targetVersion
		r.pageBindMu.Unlock()
		return
	}
	if state.inFlight {
		r.pageBindMu.Unlock()
		return
	}
	state.inFlight = true
	state.lastErr = nil
	version := state.targetVersion
	attempt := r.newPageBindAttempt(id)
	if state.runtimeCarrier != nil {
		state.runtimeCarrier.copyTargetSessionRuntimeTo(attempt.page)
		state.runtimeCarrier = nil
	}
	state.page = attempt.page
	state.cancelPage = attempt.cancel
	r.pageBindWG.Add(1)
	r.notifyPageBindChangeLocked()
	r.pageBindMu.Unlock()
	syncutil.Go(r.Logger(), func() {
		defer r.pageBindWG.Done()
		r.runPageBind(id, version, attempt)
	})
}

func (r *BrowserManager) runPageBind(id string, version uint64, attempt pageBindAttempt) {
	retried := false
	for {
		r.pageBindMu.Lock()
		state := r.pageBinds[id]
		if state == nil || !state.inFlight || state.page != attempt.page {
			r.pageBindMu.Unlock()
			attempt.discard(context.Canceled)
			return
		}
		if !r.IsAlive() {
			state.inFlight = false
			state.page = nil
			state.lastErr = ErrBrowserClosed
			state.cancelPage = nil
			r.notifyPageBindChangeLocked()
			r.pageBindMu.Unlock()
			attempt.discard(ErrBrowserClosed)
			return
		}
		owner := state
		r.pageBindMu.Unlock()

		initCtx, initCancel := context.WithTimeout(attempt.page.ctx, pageBindTimeout)
		closed, err := r.bindPageContext(initCtx, attempt.page.ctx, attempt.page)
		initCancel()
		if err == nil {
			select {
			case <-closed:
				err = errPageConnectionClosed
			default:
			}
		} else if errors.Is(err, ErrBrowserClosed) && r.IsAlive() {
			err = fmt.Errorf("%w during initialization: %w", errPageConnectionClosed, err)
		}

		r.pageBindMu.Lock()
		state = r.pageBinds[id]
		if state != owner || state.page != attempt.page {
			r.pageBindMu.Unlock()
			r.cleanupFailedPageBind(attempt.page, attempt.cancel)
			return
		}
		if err == nil && !r.IsAlive() {
			err = ErrBrowserClosed
		}
		if err == nil {
			state.inFlight = false
			state.boundTargetVersion = state.targetVersion
			state.lastBoundPage = attempt.page
			state.lastErr = nil
			r.sessions.Store(id, attempt.page)
			activeID := r.currentActivePageID()
			if IsExecutablePageURL(r.TopPageURL(id)) && (activeID == "" || !r.isUsableActivePage(activeID)) {
				r.setActivePage(id, LifecycleSourceManager, "page_bound")
			}
			r.markPageBound(attempt.page)
			r.publishPageRuntimeReady(attempt.page)
			r.notifyPageBindChangeLocked()
			r.pageBindMu.Unlock()
			r.log(slog.LevelDebug, "page init done", "id", attempt.page.ID)
			r.pageWatchWG.Add(1)
			syncutil.Go(r.Logger(), func() {
				defer r.pageWatchWG.Done()
				select {
				case <-r.Done():
				case <-closed:
				}
				r.handlePageConnectionClosed(id, attempt.page, attempt.cancel)
			})
			return
		}

		// Keep a pending successor visible while the failed root connection is
		// cleaned. Target-session contexts and binding calls must never be routed
		// back to the doomed Page during this window.
		var successor pageBindAttempt
		hasSuccessor := r.IsAlive()
		if hasSuccessor {
			successor = r.newPageBindAttempt(id)
			attempt.page.copyTargetSessionRuntimeTo(successor.page)
			state.page = successor.page
			state.cancelPage = successor.cancel
		}
		r.pageBindMu.Unlock()
		r.cleanupFailedPageBind(attempt.page, attempt.cancel)

		r.pageBindMu.Lock()
		state = r.pageBinds[id]
		expectedPage := attempt.page
		if hasSuccessor {
			expectedPage = successor.page
		}
		if state != owner || state.page != expectedPage {
			r.pageBindMu.Unlock()
			if hasSuccessor {
				successor.discard(context.Canceled)
			}
			return
		}
		if hasSuccessor && !retried && r.IsAlive() && state.targetVersion > version {
			version = state.targetVersion
			retried = true
			r.pageBindMu.Unlock()
			r.log(slog.LevelDebug, "retry page bind for newer target event", "page_id", id, "error", err)
			attempt = successor
			continue
		}
		state.page = nil
		state.cancelPage = nil
		state.inFlight = false
		terminalErr := err
		if !r.IsAlive() {
			terminalErr = ErrBrowserClosed
		}
		state.lastErr = terminalErr
		if hasSuccessor {
			state.runtimeCarrier = successor.page
		}
		r.notifyPageBindChangeLocked()
		r.pageBindMu.Unlock()
		if hasSuccessor {
			successor.discard(terminalErr)
		}

		if !r.IsAlive() || errors.Is(err, context.Canceled) || errors.Is(err, ErrBrowserClosed) {
			r.log(slog.LevelDebug, "page bind stopped", "page_id", id, "error", err)
		} else {
			r.log(slog.LevelWarn, "page bind failed", "page_id", id, "error", err)
		}
		return
	}
}

func (r *BrowserManager) cleanupFailedPageBind(page *Page, pageCancel context.CancelFunc) {
	if page == nil {
		if pageCancel != nil {
			pageCancel()
		}
		return
	}
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), pageBindCleanupTimeout)
	var cleanupErrors []error
	if err := r.removeRegisteredInitScripts(cleanupCtx, page.ID, page, true); err != nil && !errors.Is(err, ErrBrowserClosed) {
		cleanupErrors = append(cleanupErrors, err)
	}
	scripts := r.cleanupScriptsSnapshot()
	for scriptIndex := len(scripts) - 1; scriptIndex >= 0 && cleanupCtx.Err() == nil; scriptIndex-- {
		script := scripts[scriptIndex]
		if strings.TrimSpace(script.Cleanup) == "" {
			continue
		}
		if r.namespaceRegistration(script.Namespace).Namespace == NamespaceOverlay {
			continue
		}
		contexts := page.runtimeContexts(script.Namespace)
		for contextIndex := len(contexts) - 1; contextIndex >= 0 && cleanupCtx.Err() == nil; contextIndex-- {
			runtimeContext := contexts[contextIndex]
			if runtimeContext.SessionID != "" {
				continue
			}
			result, err := page.evaluateInitScriptInContext(cleanupCtx, runtimeContext.ContextID, r.initScriptSource(script, "", "cleanup"))
			if err == nil {
				err = runtimeResultError(result)
			}
			if err == nil || errors.Is(err, ErrBrowserClosed) || isSupersededDocumentError(err) {
				continue
			}
			cleanupErrors = append(cleanupErrors, fmt.Errorf("cleanup init script %s context %d session %s: %w", script.Name, runtimeContext.ContextID, runtimeContext.SessionID, err))
		}
	}
	if cleanupCtx.Err() != nil && !errors.Is(cleanupCtx.Err(), context.Canceled) {
		cleanupErrors = append(cleanupErrors, cleanupCtx.Err())
	}
	cleanupCancel()
	if len(cleanupErrors) > 0 {
		r.log(slog.LevelDebug, "partial page bind cleanup incomplete", "page_id", page.ID, "error", errors.Join(cleanupErrors...))
	}
	if pageCancel != nil {
		pageCancel()
	}
	page.unbind()
	r.forgetRootInitScriptRuntime(page.ID)
}

func (r *BrowserManager) bindPageContext(initCtx, pageCtx context.Context, page *Page) (closed <-chan struct{}, bindErr error) {
	if page == nil {
		return nil, errors.New("page is nil")
	}
	page.SetJavaScriptDialogPolicy(r.JavaScriptDialogPolicy())
	page.SetPrintPolicy(r.PrintPolicy())
	defer func() {
		page.finishInit(bindErr)
	}()
	ws, closed, err := r.wsWithContexts(initCtx, pageCtx, fmt.Sprintf("%sdevtools/page/%s", r.baseURL, page.ID))
	if err != nil {
		return nil, err
	}
	if err := page.bind(initCtx, pageCtx, ws, r); err != nil {
		return closed, err
	}
	return closed, nil
}

func (r *BrowserManager) handlePageConnectionClosed(id string, page *Page, pageCancel context.CancelFunc) {
	if pageCancel != nil {
		pageCancel()
	}
	r.pageBindMu.Lock()
	state := r.pageBinds[id]
	if state == nil || state.page != page {
		r.pageBindMu.Unlock()
		return
	}
	r.sessions.CompareAndDelete(id, page)
	// Retain ownership without holding pageBindMu across Page teardown. Target
	// updates only advance version until this exact Page finishes cleanup.
	state.inFlight = true
	state.lastErr = nil
	state.lastBoundPage = page
	owner := state
	var successor pageBindAttempt
	hasSuccessor := r.IsAlive()
	if hasSuccessor {
		successor = r.newPageBindAttempt(id)
		page.copyTargetSessionRuntimeTo(successor.page)
		state.page = successor.page
		state.cancelPage = successor.cancel
	}
	r.notifyPageBindChangeLocked()
	r.pageBindMu.Unlock()

	if r.currentActivePageID() == id {
		r.clearActivePage(id, LifecycleSourceManager, "page_ws_closed")
	}
	page.unbind()

	r.pageBindMu.Lock()
	state = r.pageBinds[id]
	expectedPage := page
	if hasSuccessor {
		expectedPage = successor.page
	}
	if state != owner || state.page != expectedPage {
		r.pageBindMu.Unlock()
		if hasSuccessor {
			successor.discard(context.Canceled)
		}
		return
	}
	r.forgetRootInitScriptRuntime(id)
	r.markPageUnbound(id)
	retry := hasSuccessor && r.IsAlive() && state.targetVersion > state.boundTargetVersion
	version := state.targetVersion
	state.inFlight = retry
	if retry {
		r.pageBindWG.Add(1)
	} else {
		state.page = nil
		state.cancelPage = nil
		if hasSuccessor {
			state.runtimeCarrier = successor.page
		}
		if r.IsAlive() {
			state.lastErr = errPageConnectionClosed
		} else {
			state.lastErr = ErrBrowserClosed
		}
	}
	r.notifyPageBindChangeLocked()
	r.pageBindMu.Unlock()

	if retry {
		syncutil.Go(r.Logger(), func() {
			defer r.pageBindWG.Done()
			r.runPageBind(id, version, successor)
		})
		return
	}
	if hasSuccessor {
		terminalErr := error(errPageConnectionClosed)
		if !r.IsAlive() {
			terminalErr = ErrBrowserClosed
		}
		successor.discard(terminalErr)
	}
	if !r.IsAlive() {
		r.emitPageClosed(page, id, string(r.ShutdownReason()))
	}
}

func (r *BrowserManager) destroyPageTarget(id, reason string) {
	id = strings.TrimSpace(id)
	if r == nil || id == "" {
		return
	}
	r.pageBindMu.Lock()
	state := r.pageBinds[id]
	delete(r.pageBinds, id)
	var page, runtimeCarrier, eventPage *Page
	var pageCancel context.CancelFunc
	var inFlight bool
	if state != nil {
		page = state.page
		runtimeCarrier = state.runtimeCarrier
		eventPage = state.lastBoundPage
		inFlight = state.inFlight
		pageCancel = state.cancelPage
	}
	if page != nil {
		r.sessions.CompareAndDelete(id, page)
	} else if current, ok := r.sessions.LoadAndDelete(id); ok {
		page = current
	}
	if eventPage == nil {
		eventPage = page
	}
	r.notifyPageBindChangeLocked()
	r.pageBindMu.Unlock()
	if pageCancel != nil {
		pageCancel()
	}
	if runtimeCarrier != nil {
		runtimeCarrier.closeRuntimeReady()
	}
	if page != nil && inFlight {
		page.closeRuntimeReady()
	}
	if r.currentActivePageID() == id {
		r.clearActivePage(id, LifecycleSourceTarget, reason)
	}
	if page != nil && !inFlight {
		page.unbind()
	}
	r.forgetInitScriptRuntime(id, "")
	r.emitPageClosed(eventPage, id, reason)
	r.deletePageLifecycleState(id)
}
