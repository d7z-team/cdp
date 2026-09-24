package syncutil

import (
	"context"
	"sync"
)

// SyncMap is a typed concurrent map with wait-for-key notification.
type SyncMap[K comparable, V any] struct {
	sm     sync.Map
	lock   sync.Mutex
	notify chan struct{}
}

// NewSyncMap creates an empty SyncMap.
func NewSyncMap[K comparable, V any]() *SyncMap[K, V] {
	return &SyncMap[K, V]{
		notify: make(chan struct{}),
	}
}

func (m *SyncMap[K, V]) broadcast() {
	m.lock.Lock()
	if m.notify != nil {
		close(m.notify)
	}
	m.notify = make(chan struct{})
	m.lock.Unlock()
}

// Store associates value with key and wakes current waiters.
func (m *SyncMap[K, V]) Store(key K, value V) {
	m.sm.Store(key, value)
	m.broadcast()
}

// Load returns the value stored for key.
func (m *SyncMap[K, V]) Load(key K) (V, bool) {
	val, ok := m.sm.Load(key)
	if !ok {
		var zero V
		return zero, false
	}
	return val.(V), ok
}

// WaitLoad waits until key is stored or ctx is canceled.
func (m *SyncMap[K, V]) WaitLoad(ctx context.Context, key K) (V, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		val, ok := m.Load(key)
		if ok {
			return val, nil
		}

		m.lock.Lock()
		// Double check after locking
		val, ok = m.Load(key)
		if ok {
			m.lock.Unlock()
			return val, nil
		}

		if m.notify == nil {
			m.notify = make(chan struct{})
		}
		notify := m.notify
		m.lock.Unlock()

		select {
		case <-ctx.Done():
			var zero V
			return zero, ctx.Err()
		case <-notify:
		}
	}
}

// LoadOrStore returns the existing value or stores and returns value.
func (m *SyncMap[K, V]) LoadOrStore(key K, value V) (V, bool) {
	val, ok := m.sm.LoadOrStore(key, value)
	if !ok {
		m.broadcast()
	}
	return val.(V), ok
}

// Delete removes key.
func (m *SyncMap[K, V]) Delete(key K) {
	m.sm.Delete(key)
}

// Range calls f for each stored key and value until f returns false.
func (m *SyncMap[K, V]) Range(f func(key K, value V) bool) {
	m.sm.Range(func(key, value any) bool {
		return f(key.(K), value.(V))
	})
}

// LoadAndDelete removes key and returns its previous value.
func (m *SyncMap[K, V]) LoadAndDelete(key K) (V, bool) {
	val, ok := m.sm.LoadAndDelete(key)
	if !ok {
		var zero V
		return zero, false
	}
	return val.(V), ok
}

// CompareAndDelete deletes key only when its current value equals old.
func (m *SyncMap[K, V]) CompareAndDelete(key K, old V) bool {
	return m.sm.CompareAndDelete(key, old)
}

// Swap stores value and returns the previous value, if any.
func (m *SyncMap[K, V]) Swap(key K, value V) (V, bool) {
	val, ok := m.sm.Swap(key, value)
	if !ok {
		var zero V
		return zero, false
	}
	return val.(V), ok
}

// Clear removes all entries.
func (m *SyncMap[K, V]) Clear() {
	m.sm.Clear()
}
