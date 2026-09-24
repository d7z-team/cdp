package syncutil

import (
	"context"
	"sync"
	"sync/atomic"
)

// Subscriber receives one published value.
type Subscriber[T any] func(msg T)

// PubSub delivers values to dynamic and optional static subscribers.
type PubSub[T any] struct {
	rwmux       sync.RWMutex
	subscribers map[uint64]Subscriber[T]
	changed     func(before, after int)
	nextID      atomic.Uint64
	staticSub   Subscriber[T]
}

// NewPubSub creates a publisher with an optional subscriber-count callback and static subscriber.
func NewPubSub[T any](onCountChanged func(before, after int), staticSub ...Subscriber[T]) *PubSub[T] {
	ps := &PubSub[T]{
		subscribers: make(map[uint64]Subscriber[T]),
		changed:     onCountChanged,
	}
	if len(staticSub) > 0 && staticSub[0] != nil {
		ps.staticSub = staticSub[0]
	}
	return ps
}

// Watch subscribes result until ctx is done.
func (ps *PubSub[T]) Watch(ctx context.Context, result chan T) <-chan T {
	return ps.WatchWhere(ctx, nil, result)
}

// WatchWhere subscribes matching values until ctx is done.
func (ps *PubSub[T]) WatchWhere(ctx context.Context, predicate func(T) bool, result chan T) <-chan T {
	if result == nil {
		result = make(chan T, 64)
	}

	var mu sync.Mutex
	var closed bool

	ps.rwmux.Lock()
	closer := ps.sub(func(msg T) {
		if predicate != nil && !predicate(msg) {
			return
		}
		mu.Lock()
		if closed {
			mu.Unlock()
			return
		}
		mu.Unlock()

		defer func() { _ = recover() }()
		select {
		case result <- msg:
			return
		case <-ctx.Done():
			return
		default:
		}
		// A feature subscriber must never stall the CDP reader. Coalesce an
		// overflowing buffered stream to its newest observable event.
		select {
		case <-result:
		default:
		}
		select {
		case result <- msg:
		case <-ctx.Done():
		default:
		}
	})
	ps.rwmux.Unlock()

	Go(func() {
		<-ctx.Done()
		closer()
		mu.Lock()
		if !closed {
			closed = true
			close(result)
		}
		mu.Unlock()
	})
	return result
}

// WatchWhereReliable preserves every matching value until ctx is canceled in
// an internal queue without blocking Publish. Protocol state consumers should
// use this instead of the coalescing WatchWhere stream.
func (ps *PubSub[T]) WatchWhereReliable(ctx context.Context, predicate func(T) bool, result chan T) <-chan T {
	if result == nil {
		result = make(chan T)
	}

	var (
		mu     sync.Mutex
		closed bool
		queue  []T
	)
	notify := make(chan struct{}, 1)

	ps.rwmux.Lock()
	closer := ps.sub(func(msg T) {
		if predicate != nil && !predicate(msg) {
			return
		}
		mu.Lock()
		if closed {
			mu.Unlock()
			return
		}
		queue = append(queue, msg)
		mu.Unlock()
		select {
		case notify <- struct{}{}:
		default:
		}
	})
	ps.rwmux.Unlock()

	Go(func() {
		defer func() {
			mu.Lock()
			closed = true
			queue = nil
			mu.Unlock()
			closer()
			close(result)
		}()
		for {
			select {
			case <-notify:
			case <-ctx.Done():
				return
			}
			for {
				mu.Lock()
				if len(queue) == 0 {
					queue = nil
					mu.Unlock()
					break
				}
				value := queue[0]
				var zero T
				queue[0] = zero
				queue = queue[1:]
				mu.Unlock()
				select {
				case result <- value:
				case <-ctx.Done():
					return
				}
			}
		}
	})
	return result
}

// Publish synchronously delivers msg to a snapshot of current subscribers.
func (ps *PubSub[T]) Publish(msg T) {
	ps.rwmux.RLock()
	subscribers := make([]Subscriber[T], 0, len(ps.subscribers))
	for _, sub := range ps.subscribers {
		subscribers = append(subscribers, sub)
	}
	staticSub := ps.staticSub
	ps.rwmux.RUnlock()

	for _, sub := range subscribers {
		sub(msg)
	}
	if staticSub != nil {
		staticSub(msg)
	}
}

// WatchOnce waits for one published value or context cancellation.
func (ps *PubSub[T]) WatchOnce(ctx context.Context) (*T, error) {
	var once sync.Once
	var closer func()
	ps.rwmux.Lock()
	resultChan := make(chan T, 1)
	closer = ps.sub(func(msg T) {
		once.Do(func() {
			select {
			case resultChan <- msg:
			case <-ctx.Done():
			}
		})
	})
	ps.rwmux.Unlock()
	defer func() {
		closer()
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-resultChan:
		return &r, nil
	}
}

// SubscriberCount returns the number of dynamic subscribers.
func (ps *PubSub[T]) SubscriberCount() int {
	ps.rwmux.RLock()
	defer ps.rwmux.RUnlock()
	return len(ps.subscribers)
}

// Subscribe registers sub and returns an idempotent unsubscribe function.
func (ps *PubSub[T]) Subscribe(sub Subscriber[T]) func() {
	ps.rwmux.Lock()
	defer ps.rwmux.Unlock()
	return ps.sub(sub)
}

func (ps *PubSub[T]) sub(sub Subscriber[T]) func() {
	subID := ps.nextID.Add(1)
	before := len(ps.subscribers)
	ps.subscribers[subID] = sub
	after := len(ps.subscribers)
	if ps.changed != nil {
		ps.changed(before, after)
	}
	var unsubOnce sync.Once
	return func() {
		unsubOnce.Do(func() {
			ps.rwmux.Lock()
			defer ps.rwmux.Unlock()

			if _, exists := ps.subscribers[subID]; !exists {
				return
			}
			before := len(ps.subscribers)
			delete(ps.subscribers, subID)
			after := len(ps.subscribers)
			if ps.changed != nil {
				ps.changed(before, after)
			}
		})
	}
}
