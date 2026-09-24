package cdp

import (
	"context"
	"errors"

	engine "gopkg.d7z.net/cdp/internal/engine"
)

type Dialog struct {
	ID         uint64 `json:"id"`
	Type       string `json:"type"`
	Message    string `json:"message"`
	Default    string `json:"default_prompt,omitempty"`
	HasHandler bool   `json:"has_browser_handler"`
}
type DialogPolicy string

const (
	DialogAutoHandle  DialogPolicy = "auto_handle"
	DialogPassthrough DialogPolicy = "passthrough"
)

var (
	ErrDialogNotOpen = errors.New("JavaScript dialog is not open")
	ErrDialogChanged = errors.New("JavaScript dialog has changed")
)

func (p *Page) SetDialogPolicy(policy DialogPolicy) error {
	if policy != DialogAutoHandle && policy != DialogPassthrough {
		return errors.New("invalid dialog policy")
	}
	p.engine.SetJavaScriptDialogPolicy(engine.JavaScriptDialogPolicy(policy))
	return nil
}
func (p *Page) DialogState() (Dialog, bool, <-chan struct{}) {
	if p == nil || p.engine == nil {
		return Dialog{}, false, nil
	}
	d, open, changed := p.engine.JavaScriptDialogState()
	return Dialog(d), open, changed
}
func (p *Page) CurrentDialog() (Dialog, bool) { d, open, _ := p.DialogState(); return d, open }
func (p *Page) HandleDialog(ctx context.Context, id uint64, accept bool, prompt string) (Dialog, bool, error) {
	d, open, err := p.engine.HandleJavaScriptDialog(ctx, id, accept, prompt)
	if errors.Is(err, engine.ErrJavaScriptDialogNotOpen) {
		err = errors.Join(ErrDialogNotOpen, err)
	}
	if errors.Is(err, engine.ErrJavaScriptDialogChanged) {
		err = errors.Join(ErrDialogChanged, err)
	}
	return Dialog(d), open, operationError("dialog", err)
}
func (p *Page) ExpectDialog(accept bool, prompt string) { p.engine.ExpectDialog(accept, prompt) }
