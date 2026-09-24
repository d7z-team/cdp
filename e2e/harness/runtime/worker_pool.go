package runtime

import (
	"context"
	"fmt"
	"sync"

	"gopkg.d7z.net/cdp"
)

// PageFactory creates pages for a worker pool.
type PageFactory interface {
	NewPage() (*cdp.Page, error)
}

// Worker owns one reusable page.
type Worker struct {
	ID   string
	Page *cdp.Page
}

// PageLease grants temporary exclusive access to a worker.
type PageLease struct {
	Worker  *Worker
	release func()
}

// Release returns the worker to its pool.
func (l *PageLease) Release() {
	if l == nil || l.release == nil {
		return
	}
	l.release()
	l.release = nil
}

// WorkerPool manages a fixed set of reusable browser pages.
type WorkerPool struct {
	mu      sync.Mutex
	workers chan *Worker
	all     []*Worker
}

// NewWorkerPool creates size workers with pages from factory.
func NewWorkerPool(factory PageFactory, size int) (*WorkerPool, error) {
	if factory == nil {
		return nil, fmt.Errorf("page factory is nil")
	}
	if size <= 0 {
		return nil, fmt.Errorf("worker pool size must be positive")
	}
	pool := &WorkerPool{
		workers: make(chan *Worker, size),
		all:     make([]*Worker, 0, size),
	}
	for i := 0; i < size; i++ {
		page, err := factory.NewPage()
		if err != nil {
			pool.Close()
			return nil, err
		}
		worker := &Worker{
			ID:   fmt.Sprintf("worker-%d", i),
			Page: page,
		}
		pool.all = append(pool.all, worker)
		pool.workers <- worker
	}
	return pool, nil
}

// Acquire waits for an available page lease.
func (p *WorkerPool) Acquire(ctx context.Context) (*PageLease, error) {
	if p == nil {
		return nil, fmt.Errorf("worker pool is nil")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case worker, ok := <-p.workers:
		if !ok {
			return nil, fmt.Errorf("worker pool closed")
		}
		return &PageLease{
			Worker: worker,
			release: func() {
				p.mu.Lock()
				defer p.mu.Unlock()
				if p.workers != nil {
					p.workers <- worker
				}
			},
		}, nil
	}
}

// Close prevents new leases and closes every worker page.
func (p *WorkerPool) Close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	ch := p.workers
	p.workers = nil
	p.mu.Unlock()
	if ch != nil {
		close(ch)
	}
	for _, worker := range p.all {
		if worker != nil && worker.Page != nil {
			_ = worker.Page.Close(context.Background())
		}
	}
}

// APIBrowserFactory creates pages through the high-level browser API.
type APIBrowserFactory struct {
	Browser *cdp.Browser
}

// NewPage creates a high-level browser page.
func (f APIBrowserFactory) NewPage() (*cdp.Page, error) {
	if f.Browser == nil {
		return nil, fmt.Errorf("api browser is nil")
	}
	return f.Browser.NewPage(context.Background())
}
