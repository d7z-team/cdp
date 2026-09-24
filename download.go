package cdp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

type Download struct {
	ID, Name string
	Data     []byte
}
type DownloadWatcher struct {
	mu      sync.Mutex
	results []Download
	err     error
	done    chan struct{}
	cancel  context.CancelFunc
}

func (p *Page) WatchDownloads(ctx context.Context, count int) (*DownloadWatcher, error) {
	_, stop, err := p.operation(ctx, p.timeouts().Read)
	if err != nil {
		return nil, err
	}
	stop()

	if ctx == nil {
		return nil, errors.New("nil context")
	}
	if count < 1 {
		return nil, errors.New("download count must be positive")
	}
	p.browser.downloadMu.Lock()
	if p.browser.downloadActive {
		p.browser.downloadMu.Unlock()
		return nil, errors.New("a download watcher is already active")
	}
	p.browser.downloadActive = true
	p.browser.downloadMu.Unlock()
	release := func() { p.browser.downloadMu.Lock(); p.browser.downloadActive = false; p.browser.downloadMu.Unlock() }
	dir, err := os.MkdirTemp("", "cdp-download-*")
	if err != nil {
		release()
		return nil, err
	}
	life, cancel := context.WithTimeout(ctx, p.timeouts().Download)
	sub, err := p.Session().Subscribe(life, "Browser.downloadWillBegin", "Page.downloadWillBegin", "Browser.downloadProgress", "Page.downloadProgress")
	if err != nil {
		cancel()
		os.RemoveAll(dir)
		release()
		return nil, err
	}
	if err = p.Session().Call(life, "Browser.setDownloadBehavior", map[string]any{"behavior": "allowAndName", "downloadPath": dir, "eventsEnabled": true}, nil); err != nil {
		sub.Close()
		cancel()
		os.RemoveAll(dir)
		release()
		return nil, err
	}
	w := &DownloadWatcher{done: make(chan struct{}), cancel: cancel}
	go func() {
		names := map[string]string{}
		completed := map[string]bool{}
		var resultErr error
		defer func() {
			cancel()
			_ = sub.Close()
			cleanup, stop := context.WithTimeout(context.Background(), p.timeouts().Shutdown)
			defer stop()
			for id := range names {
				if !completed[id] {
					_ = p.Session().Call(cleanup, "Browser.cancelDownload", map[string]any{"guid": id}, nil)
				}
			}
			_ = p.Session().Call(cleanup, "Browser.setDownloadBehavior", map[string]any{"behavior": "default"}, nil)
			_ = os.RemoveAll(dir)
			release()
			w.mu.Lock()
			w.err = resultErr
			w.mu.Unlock()
			close(w.done)
		}()
		for {
			select {
			case <-life.Done():
				resultErr = life.Err()
				return
			case event, ok := <-sub.Events():
				if !ok {
					resultErr = errors.Join(ErrClosed, sub.Err())
					return
				}
				id, _ := event.Params["guid"].(string)
				if id == "" {
					continue
				}
				if name, ok := event.Params["suggestedFilename"].(string); ok {
					names[id] = name
				}
				state, _ := event.Params["state"].(string)
				if state == "canceled" {
					resultErr = errors.New("download canceled")
					return
				}
				if state != "completed" || completed[id] {
					continue
				}
				completed[id] = true
				data, err := os.ReadFile(filepath.Join(dir, filepath.Base(id)))
				if err != nil {
					resultErr = err
					return
				}
				name := names[id]
				if name == "" {
					name = id
				}
				w.mu.Lock()
				w.results = append(w.results, Download{ID: id, Name: name, Data: data})
				done := len(w.results) >= count
				w.mu.Unlock()
				if done {
					return
				}
			}
		}
	}()
	return w, nil
}
func (w *DownloadWatcher) Wait(ctx context.Context) ([]Download, error) {
	if w == nil || w.done == nil {
		return nil, ErrClosed
	}
	if ctx == nil {
		return nil, errors.New("nil context")
	}
	var err error
	select {
	case <-ctx.Done():
		err = ctx.Err()
	case <-w.done:
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]Download, len(w.results))
	for i, v := range w.results {
		out[i] = v
		out[i].Data = append([]byte(nil), v.Data...)
	}
	if err == nil {
		err = w.err
	}
	return out, operationError("download.wait", err)
}
func (w *DownloadWatcher) Close() error {
	if w == nil || w.cancel == nil {
		return nil
	}
	w.cancel()
	<-w.done
	return nil
}
