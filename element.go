package cdp

import (
	"context"
	"encoding/json"
	"errors"
	"image"
	"time"

	engine "gopkg.d7z.net/cdp/internal/engine"
)

type Element struct {
	page       *Page
	ref        engine.SelectorTargetRef
	generation int64
	snapshotID uint64
}

func (p *Page) element(ref engine.SelectorTargetRef) *Element {
	return &Element{page: p, ref: ref, generation: p.engine.RuntimeGeneration()}
}
func (p *Page) Element(ctx context.Context, ref string) (*Element, error) {
	c, cancel, err := p.operation(ctx, p.timeouts().Read)
	if err != nil {
		return nil, err
	}
	defer cancel()
	r, err := p.engine.ResolveAISnapshotRef(c, ref)
	if err != nil {
		return nil, operationError("element", errors.Join(ErrStaleElement, err))
	}
	e := p.element(r)
	doc, _ := p.engine.CurrentAISnapshot()
	e.snapshotID = doc.ID
	return e, nil
}
func (e *Element) validate(ctx context.Context) error {
	if e == nil || e.page == nil {
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if e.generation != e.page.engine.RuntimeGeneration() {
		return ErrStaleElement
	}
	if e.snapshotID != 0 {
		doc, ok := e.page.engine.CurrentAISnapshot()
		if !ok || doc.ID != e.snapshotID {
			return ErrStaleElement
		}
	}
	return nil
}
func (e *Element) Eval(ctx context.Context, script string, result any) error {
	raw, err := e.EvalJSON(ctx, script)
	if err != nil {
		return err
	}
	if result == nil {
		return nil
	}
	return operationError("element.eval.decode", json.Unmarshal(raw, result))
}
func (e *Element) EvalJSON(ctx context.Context, script string) (json.RawMessage, error) {
	c, cancel, err := e.operation(ctx, e.timeouts().Read)
	if err != nil {
		return nil, err
	}
	defer cancel()
	v, err := e.page.engine.SelectorTargetEvalJSON(c, e.ref, script)
	if v == "undefined" || v == "" {
		v = "null"
	}
	return json.RawMessage(v), operationError("element.eval", err)
}
func (e *Element) Screenshot(ctx context.Context) (*image.RGBA, error) {
	c, cancel, err := e.operation(ctx, e.timeouts().Screenshot)
	if err != nil {
		return nil, err
	}
	defer cancel()
	v, err := e.page.engine.CaptureTargetScreenshotContext(c, e.ref)
	return v, operationError("element.screenshot", err)
}

// Locator queries descendants of this exact element. A stale root is never re-resolved.
func (e *Element) Locator(selectors ...string) *Locator {
	if e == nil {
		return (&Locator{}).Locator(selectors...)
	}
	return (&Locator{page: e.page, root: e}).Locator(selectors...)
}

// ContentFrame enters the iframe represented by this exact element.
func (e *Element) ContentFrame() *Locator {
	if e == nil {
		return (&Locator{}).ContentFrame()
	}
	return (&Locator{page: e.page, root: e}).ContentFrame()
}

func (e *Element) timeouts() Timeouts {
	if e == nil {
		return (*Page)(nil).timeouts()
	}
	return e.page.timeouts()
}
func (e *Element) operation(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc, error) {
	if e == nil {
		return nil, nil, ErrClosed
	}
	c, cancel, err := e.page.operation(ctx, timeout)
	if err != nil {
		return nil, nil, err
	}
	if err := e.validate(c); err != nil {
		cancel()
		return nil, nil, err
	}
	return c, cancel, nil
}
