package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"gopkg.d7z.net/cdp/internal/syncutil"
)

func TestBrowserManagerDisconnectContextWaitsForActiveShutdown(t *testing.T) {
	shutdownErr := errors.New("cleanup failed")
	r := &BrowserManager{
		shutdownDone: make(chan struct{}),
		shutdownErr:  shutdownErr,
	}
	r.setState(managerStateStopping)

	errCh := make(chan error, 1)
	go func() {
		errCh <- r.DisconnectContext(context.Background())
	}()

	select {
	case err := <-errCh:
		t.Fatalf("DisconnectContext returned before shutdown completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	r.setState(managerStateStopped)
	r.closeShutdownDone()

	select {
	case err := <-errCh:
		if !errors.Is(err, shutdownErr) {
			t.Fatalf("DisconnectContext returned error %v, want stored shutdown error", err)
		}
	case <-time.After(time.Second):
		t.Fatal("DisconnectContext did not return after shutdown completed")
	}
}

func TestBrowserManagerDisconnectContextReturnsStoredErrorAfterShutdown(t *testing.T) {
	shutdownErr := errors.New("runtime cleanup failed")
	r := &BrowserManager{shutdownErr: shutdownErr}
	r.setState(managerStateStopped)

	if err := r.DisconnectContext(context.Background()); !errors.Is(err, shutdownErr) {
		t.Fatalf("DisconnectContext error = %v, want stored shutdown error", err)
	}
}

func TestBrowserManagerDisconnectContextHonorsWaitContext(t *testing.T) {
	r := &BrowserManager{
		shutdownDone: make(chan struct{}),
	}
	r.setState(managerStateStopping)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	if err := r.DisconnectContext(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("DisconnectContext error = %v, want deadline exceeded", err)
	}
}

func TestBrowserManagerDisconnectContextCompletesOwnedShutdown(t *testing.T) {
	managerCtx, cancel := context.WithCancel(context.Background())
	r := &BrowserManager{
		sessions:     syncutil.NewSyncMap[string, *Page](),
		ctx:          managerCtx,
		cancel:       cancel,
		shutdownDone: make(chan struct{}),
	}
	r.setState(managerStateRunning)

	if err := r.DisconnectContext(context.Background()); err != nil {
		t.Fatalf("DisconnectContext returned error: %v", err)
	}
	if r.State() != managerStateStopped {
		t.Fatalf("state = %v, want stopped", r.State())
	}

	select {
	case <-r.Done():
	default:
		t.Fatal("manager context was not canceled")
	}
	select {
	case <-r.ShutdownDone():
	default:
		t.Fatal("shutdown done was not closed")
	}
}

func TestBrowserManagerDisconnectContextStopsAndStoresCleanupError(t *testing.T) {
	managerCtx, managerCancel := context.WithCancel(context.Background())
	r := &BrowserManager{
		sessions:     syncutil.NewSyncMap[string, *Page](),
		ctx:          managerCtx,
		cancel:       managerCancel,
		shutdownDone: make(chan struct{}),
	}
	r.setState(managerStateRunning)

	shutdownCtx, cancelShutdown := context.WithCancel(context.Background())
	cancelShutdown()
	if err := r.DisconnectContext(shutdownCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("DisconnectContext error = %v, want cleanup context cancellation", err)
	}
	if r.State() != managerStateStopped {
		t.Fatalf("state = %v, want stopped after cleanup error", r.State())
	}
	if err := r.DisconnectContext(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("subsequent DisconnectContext error = %v, want stored cleanup error", err)
	}
	select {
	case <-r.Done():
	default:
		t.Fatal("manager context was not canceled after cleanup error")
	}
}

func TestBrowserManagerStoppingPrecedesDone(t *testing.T) {
	managerCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := &BrowserManager{ctx: managerCtx, stopping: make(chan struct{})}
	manager.setState(managerStateStopping)
	manager.signalStopping()

	select {
	case <-manager.Stopping():
	default:
		t.Fatal("stopping signal is still open")
	}
	select {
	case <-manager.Done():
		t.Fatal("manager done closed before runtime cleanup completed")
	default:
	}
}
