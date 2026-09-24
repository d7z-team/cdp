package syncutil

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestWatch(t *testing.T) {
	sub := NewPubSub[string](nil)
	waitgroup := &sync.WaitGroup{}
	waitgroup.Add(1)
	go func() {
		defer waitgroup.Done()
		once, err := sub.WatchOnce(t.Context())
		if err != nil {
			t.Error(err)
		}
		assert.Equal(t, "100", *once)
	}()
	time.Sleep(time.Millisecond * 100)
	sub.Publish("100")
	waitgroup.Wait()
}

func TestWatchWhereFiltersAndPreservesOrder(t *testing.T) {
	type event struct {
		method string
		seq    int
	}
	pubsub := NewPubSub[event](nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	events := pubsub.WatchWhere(ctx, func(value event) bool {
		return value.method == "wanted"
	}, nil)

	for _, value := range []event{
		{method: "ignored", seq: 1},
		{method: "wanted", seq: 2},
		{method: "ignored", seq: 3},
		{method: "wanted", seq: 4},
	} {
		pubsub.Publish(value)
	}
	assert.Equal(t, 2, (<-events).seq)
	assert.Equal(t, 4, (<-events).seq)
}

func TestWatchCoalescesOverflowWithoutBlockingPublisher(t *testing.T) {
	pubsub := NewPubSub[int](nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	events := pubsub.Watch(ctx, make(chan int, 1))
	pubsub.Publish(1)
	pubsub.Publish(2)
	select {
	case value := <-events:
		assert.Equal(t, 2, value)
	case <-time.After(time.Second):
		t.Fatal("overflowing subscriber blocked publication")
	}
}

func TestWatchWhereReliableDoesNotBlockPublishAndPreservesOrder(t *testing.T) {
	pubsub := NewPubSub[int](nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	events := pubsub.WatchWhereReliable(ctx, nil, make(chan int))

	const total = 1_000
	published := make(chan struct{})
	go func() {
		defer close(published)
		for value := range total {
			pubsub.Publish(value)
		}
	}()

	// Do not read from the unbuffered result until every Publish has returned.
	// The delivery goroutine is therefore blocked on its first result while the
	// subscription callback must continue appending the remaining values.
	select {
	case <-published:
	case <-time.After(time.Second):
		t.Fatal("reliable subscription blocked Publish while its consumer was paused")
	}

	for want := range total {
		select {
		case got, ok := <-events:
			if !ok {
				t.Fatalf("reliable subscription closed before event %d", want)
			}
			assert.Equal(t, want, got)
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for reliable event %d", want)
		}
	}
}

func TestWatchWhereReliableCancelUnblocksDeliveryAndUnsubscribes(t *testing.T) {
	pubsub := NewPubSub[int](nil)
	ctx, cancel := context.WithCancel(t.Context())
	events := pubsub.WatchWhereReliable(ctx, nil, make(chan int))
	assert.Equal(t, 1, pubsub.SubscriberCount())

	// With no receiver, delivery may be waiting on this value. Cancellation
	// must still stop that goroutine, unregister the callback, and close result.
	pubsub.Publish(1)
	cancel()

	deadline := time.After(time.Second)
	for {
		select {
		case _, ok := <-events:
			if !ok {
				assert.Equal(t, 0, pubsub.SubscriberCount())
				pubsub.Publish(2)
				return
			}
		case <-deadline:
			t.Fatal("reliable subscription did not close after cancellation")
		}
	}
}
