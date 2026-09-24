package cdp

import (
	"context"
	"encoding/base64"
	"errors"
)

type PrintArtifact struct {
	MIMEType string
	Data     []byte
}
type PrintWatcher struct {
	result PrintArtifact
	err    error
	done   chan struct{}
	cancel context.CancelFunc
}

func (p *Page) WatchPrint(ctx context.Context) (*PrintWatcher, error) {
	_, stop, err := p.operation(ctx, p.timeouts().Read)
	if err != nil {
		return nil, err
	}
	stop()

	if ctx == nil {
		return nil, errors.New("nil context")
	}
	life, cancel := context.WithTimeout(ctx, p.timeouts().Print)
	ch, err := p.engine.ExpectPrint(life)
	if err != nil {
		cancel()
		return nil, operationError("print.watch", err)
	}
	w := &PrintWatcher{done: make(chan struct{}), cancel: cancel}
	go func() {
		defer close(w.done)
		defer cancel()
		defer p.engine.CancelPrintWait(ch)
		select {
		case <-life.Done():
			w.err = life.Err()
		case <-p.Done():
			w.err = ErrClosed
		case result, ok := <-ch:
			if !ok {
				w.err = ErrClosed
				return
			}
			w.result = PrintArtifact{MIMEType: result.Artifact.MimeType, Data: append([]byte(nil), result.Artifact.Data...)}
			w.err = result.Err
		}
	}()
	return w, nil
}
func (w *PrintWatcher) Wait(ctx context.Context) (PrintArtifact, error) {
	if w == nil || w.done == nil {
		return PrintArtifact{}, ErrClosed
	}
	if ctx == nil {
		return PrintArtifact{}, errors.New("nil context")
	}
	select {
	case <-ctx.Done():
		return PrintArtifact{}, ctx.Err()
	case <-w.done:
	}
	v := w.result
	v.Data = append([]byte(nil), v.Data...)
	return v, operationError("print.wait", w.err)
}
func (w *PrintWatcher) Close() error {
	if w == nil || w.cancel == nil {
		return nil
	}
	w.cancel()
	<-w.done
	return nil
}
func (p *Page) PDF(ctx context.Context) (PrintArtifact, error) {
	var r struct {
		Data string `json:"data"`
	}
	if err := p.Session().Call(ctx, "Page.printToPDF", map[string]any{"printBackground": true}, &r); err != nil {
		return PrintArtifact{}, err
	}
	data, err := base64.StdEncoding.DecodeString(r.Data)
	return PrintArtifact{MIMEType: "application/pdf", Data: data}, operationError("pdf", err)
}
