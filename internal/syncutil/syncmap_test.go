package syncutil

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSyncMapWaitLoadWakesAllWaiters(t *testing.T) {
	values := NewSyncMap[string, int]()
	results := make(chan int, 2)
	for range 2 {
		go func() {
			value, err := values.WaitLoad(t.Context(), "ready")
			if err != nil {
				t.Errorf("WaitLoad failed: %v", err)
				return
			}
			results <- value
		}()
	}

	values.Store("ready", 42)
	for range 2 {
		select {
		case value := <-results:
			if value != 42 {
				t.Fatalf("WaitLoad value = %d, want 42", value)
			}
		case <-time.After(time.Second):
			t.Fatal("WaitLoad did not wake after Store")
		}
	}
}

func TestSyncMapWaitLoadCancellationDoesNotRetainWaiter(t *testing.T) {
	values := NewSyncMap[string, int]()
	ctx, cancel := context.WithCancel(t.Context())
	waitDone := make(chan error, 1)
	go func() {
		_, err := values.WaitLoad(ctx, "canceled")
		waitDone <- err
	}()

	cancel()
	if err := <-waitDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitLoad error = %v, want context cancellation", err)
	}

	values.lock.Lock()
	notify := values.notify
	values.lock.Unlock()
	values.Store("other", 1)
	select {
	case <-notify:
	case <-time.After(time.Second):
		t.Fatal("Store did not rotate the shared notification")
	}
}
