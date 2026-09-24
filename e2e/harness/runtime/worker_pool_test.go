package runtime

import (
	"context"
	"testing"
	"time"

	"gopkg.d7z.net/cdp"
)

type fakePageFactory struct {
	count int
}

func (f *fakePageFactory) NewPage() (*cdp.Page, error) {
	f.count++
	return &cdp.Page{}, nil
}

func TestWorkerPoolAcquireRelease(t *testing.T) {
	factory := &fakePageFactory{}
	pool, err := NewWorkerPool(factory, 2)
	if err != nil {
		t.Fatalf("NewWorkerPool() error = %v", err)
	}
	defer pool.Close()

	lease1, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	lease2, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if lease1.Worker.ID == lease2.Worker.ID {
		t.Fatalf("expected different workers, got %s", lease1.Worker.ID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := pool.Acquire(ctx); err == nil {
		t.Fatal("expected acquire timeout while all workers are leased")
	}

	lease1.Release()
	lease3, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire() after release error = %v", err)
	}
	if lease3.Worker == nil {
		t.Fatal("expected leased worker")
	}
	lease2.Release()
	lease3.Release()
}
