package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gopkg.d7z.net/cdp/internal/syncutil"
)

func (r *BrowserManager) SetLastActivePageID(id string) {
	r.setActivePage(id, LifecycleSourceManager, "set_last_active_page")
}

func (r *BrowserManager) IsAlive() bool {
	if r == nil {
		return false
	}
	state := r.State()
	return state == managerStateStarting || state == managerStateRunning
}

func (r *BrowserManager) Done() <-chan struct{} {
	if r == nil || r.ctx == nil {
		done := make(chan struct{})
		close(done)
		return done
	}
	return r.ctx.Done()
}

// Stopping is closed as soon as shutdown begins, before page runtimes are
// cleaned. Done is closed later, after cleanup has finished.
func (r *BrowserManager) Stopping() <-chan struct{} {
	if r == nil {
		done := make(chan struct{})
		close(done)
		return done
	}
	r.lifecycleMu.Lock()
	if r.stopping == nil {
		r.stopping = make(chan struct{})
	}
	stopping := r.stopping
	if !r.IsAlive() {
		r.stoppingOnce.Do(func() { close(stopping) })
	}
	r.lifecycleMu.Unlock()
	return stopping
}

func (r *BrowserManager) signalStopping() {
	if r == nil {
		return
	}
	r.lifecycleMu.Lock()
	if r.stopping == nil {
		r.stopping = make(chan struct{})
	}
	stopping := r.stopping
	r.stoppingOnce.Do(func() { close(stopping) })
	r.lifecycleMu.Unlock()
}

func (r *BrowserManager) ShutdownDone() <-chan struct{} {
	if r == nil || r.shutdownDone == nil {
		done := make(chan struct{})
		close(done)
		return done
	}
	return r.shutdownDone
}

func (r *BrowserManager) DisconnectContext(ctx context.Context) error {
	return r.shutdown(ctx, shutdownReasonDisconnect)
}

func (r *BrowserManager) State() ManagerState {
	if r == nil {
		return managerStateStopped
	}
	return ManagerState(r.state.Load())
}

func (r *BrowserManager) ShutdownReason() ShutdownReason {
	if r == nil {
		return shutdownReasonNone
	}
	r.lifecycleMu.RLock()
	defer r.lifecycleMu.RUnlock()
	return r.shutdownReason
}

func (r *BrowserManager) cleanupAllPages(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if r.sessions == nil {
		return nil
	}
	scripts := r.cleanupScriptsSnapshot()
	var errs []error
	r.sessions.Range(func(_ string, page *Page) bool {
		if ctx.Err() != nil {
			return false
		}
		if page == nil {
			return true
		}
		for i := len(scripts) - 1; i >= 0; i-- {
			script := scripts[i]
			if strings.TrimSpace(script.Cleanup) == "" {
				continue
			}
			if err := page.cleanupInitScript(ctx, script); err != nil && !errors.Is(err, ErrBrowserClosed) {
				errs = append(errs, fmt.Errorf("cleanup page %s init script %s: %w", page.ID, script.Name, err))
			}
			if ctx.Err() != nil {
				return false
			}
		}
		return true
	})
	return errors.Join(append(errs, ctx.Err())...)
}

func (r *BrowserManager) shutdown(ctx context.Context, reason ShutdownReason) error {
	if r == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		state := r.State()
		if state == managerStateStopped {
			return r.shutdownError()
		}
		if state == managerStateStopping {
			return r.waitShutdown(ctx)
		}
		r.pageBindMu.Lock()
		if r.state.CompareAndSwap(int32(state), int32(managerStateStopping)) {
			r.signalStopping()
			// Stop incomplete candidates first. Published pages must remain live
			// until their registered scripts and page facades are cleaned below.
			pageCancels := make([]context.CancelFunc, 0, len(r.pageBinds))
			for _, bind := range r.pageBinds {
				if bind != nil && bind.inFlight && bind.cancelPage != nil {
					pageCancels = append(pageCancels, bind.cancelPage)
				}
			}
			r.pageBindMu.Unlock()
			for _, cancel := range pageCancels {
				cancel()
			}
			bindsDone := make(chan struct{})
			syncutil.Go(r.Logger(), func() {
				r.pageBindWG.Wait()
				close(bindsDone)
			})
			var waitBindsErr error
			select {
			case <-bindsDone:
			case <-ctx.Done():
				waitBindsErr = ctx.Err()
			}
			defer r.closeShutdownDone()
			r.setShutdownReason(reason)
			r.emitLifecycle(LifecycleContext{
				Context: r.ctx,
				Manager: r,
			}, LifecycleEvent{
				Type:   LifecycleBrowserStopping,
				Source: LifecycleSourceManager,
				Reason: string(reason),
			})
			removeScriptsErr := r.cleanupRegisteredInitScripts(ctx)
			cleanupRuntimeErr := r.cleanupAllPages(ctx)
			if r.cancel != nil {
				r.cancel()
			}
			pageWatchesDone := make(chan struct{})
			syncutil.Go(r.Logger(), func() {
				r.pageWatchWG.Wait()
				close(pageWatchesDone)
			})
			var waitPageWatchesErr error
			select {
			case <-pageWatchesDone:
			case <-ctx.Done():
				waitPageWatchesErr = ctx.Err()
			}
			shutdownErr := errors.Join(waitBindsErr, removeScriptsErr, cleanupRuntimeErr, waitPageWatchesErr)
			r.lifecycleMu.Lock()
			r.shutdownErr = shutdownErr
			r.lifecycleMu.Unlock()
			r.setState(managerStateStopped)
			return shutdownErr
		}
		r.pageBindMu.Unlock()
	}
}

func (r *BrowserManager) waitShutdown(ctx context.Context) error {
	if r == nil || r.shutdownDone == nil {
		return nil
	}
	select {
	case <-r.shutdownDone:
		return r.shutdownError()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *BrowserManager) shutdownError() error {
	if r == nil {
		return nil
	}
	r.lifecycleMu.RLock()
	defer r.lifecycleMu.RUnlock()
	return r.shutdownErr
}

func (r *BrowserManager) closeShutdownDone() {
	if r == nil || r.shutdownDone == nil {
		return
	}
	r.shutdownDoneOnce.Do(func() {
		close(r.shutdownDone)
	})
}

func (r *BrowserManager) setState(state ManagerState) {
	r.state.Store(int32(state))
}

func (r *BrowserManager) setShutdownReason(reason ShutdownReason) {
	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()
	if r.shutdownReason == shutdownReasonNone {
		r.shutdownReason = reason
	}
}

func (r *BrowserManager) watchLifecycle(parent context.Context, browserClosed <-chan struct{}) {
	select {
	case <-parent.Done():
		ctx, cancel := context.WithTimeout(context.Background(), browserManagerShutdownTimeout)
		defer cancel()
		_ = r.shutdown(ctx, shutdownReasonParentContext)
	case <-browserClosed:
		ctx, cancel := context.WithTimeout(context.Background(), browserManagerShutdownTimeout)
		defer cancel()
		_ = r.shutdown(ctx, shutdownReasonBrowserWSClosed)
	case <-r.Done():
	}
}
