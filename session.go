package cdp

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"

	"gopkg.d7z.net/cdp/internal/engine"
)

type Session struct{ page *Page }

func (p *Page) Session() *Session { return &Session{page: p} }
func (s *Session) Call(ctx context.Context, method string, params, result any) error {
	if s == nil || s.page == nil {
		return ErrClosed
	}
	p := s.page
	c, cancel, err := p.operation(ctx, p.timeouts().Read)
	if err != nil {
		return err
	}
	defer cancel()
	var args map[string]any
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(raw, &args); err != nil {
			return err
		}
	}
	value, err := p.engine.CdpConn.SendMessageContext(c, method, args)
	if err != nil {
		return operationError(method, err)
	}
	if result == nil {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return operationError(method, json.Unmarshal(raw, result))
}

type Event struct {
	Method string
	Params map[string]any
}
type Subscription struct {
	events  chan Event
	done    chan struct{}
	cancel  context.CancelFunc
	once    sync.Once
	dropped atomic.Uint64
	mu      sync.Mutex
	err     error
}

func (s *Session) Subscribe(ctx context.Context, methods ...string) (*Subscription, error) {
	if ctx == nil {
		return nil, errors.New("nil context")
	}
	if s == nil || s.page == nil || s.page.engine == nil {
		return nil, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.page.browser.Diagnostics() != DiagnosticsRuntime {
		for _, method := range methods {
			if engine.IsRuntimeDiagnosticEvent(method) {
				return nil, operationError("subscribe", ErrDiagnosticsDisabled)
			}
		}
	}
	life, cancel := context.WithCancel(ctx)
	sub := &Subscription{events: make(chan Event, 256), done: make(chan struct{}), cancel: cancel}
	events, closeEvents := s.page.engine.CdpConn.SubscribeReliable(methods...)
	go func() {
		defer close(sub.done)
		defer close(sub.events)
		defer closeEvents()
		for {
			select {
			case <-life.Done():
				sub.mu.Lock()
				sub.err = life.Err()
				sub.mu.Unlock()
				return
			case <-s.page.Done():
				sub.mu.Lock()
				sub.err = ErrClosed
				sub.mu.Unlock()
				return
			case e, ok := <-events:
				if !ok {
					return
				}
				v := Event{Method: e.Method, Params: e.Params}
				select {
				case sub.events <- v:
				default:
					sub.dropped.Add(1)
					sub.mu.Lock()
					sub.err = errors.New("event subscription overflow")
					sub.mu.Unlock()
					return
				}
			}
		}
	}()
	return sub, nil
}
func (s *Subscription) Events() <-chan Event { return s.events }
func (s *Subscription) Close() error {
	if s == nil || s.cancel == nil {
		return nil
	}
	s.once.Do(s.cancel)
	<-s.done
	err := s.Err()
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
func (s *Subscription) Err() error      { s.mu.Lock(); defer s.mu.Unlock(); return s.err }
func (s *Subscription) Dropped() uint64 { return s.dropped.Load() }
