package cdp

import (
	"context"
	"encoding/json"

	engine "gopkg.d7z.net/cdp/internal/engine"
)

type Modifier string

const (
	KeyAlt     Modifier = "Alt"
	KeyControl Modifier = "Control"
	KeyMeta    Modifier = "Meta"
	KeyShift   Modifier = "Shift"
)

type KeyOptions struct{ Modifiers []Modifier }

func modifiers(opts KeyOptions) []string {
	out := make([]string, len(opts.Modifiers))
	for i, v := range opts.Modifiers {
		out[i] = string(v)
	}
	return out
}
func (p *Page) Press(ctx context.Context, key string, opts KeyOptions) error {
	c, cancel, err := p.operation(ctx, p.timeouts().Action)
	if err != nil {
		return err
	}
	defer cancel()
	return operationError("press", p.engine.PressKeyContext(c, key, modifiers(opts)...))
}
func (e *Element) Press(ctx context.Context, key string, opts KeyOptions) error {
	c, cancel, err := e.operation(ctx, e.timeouts().Action)
	if err != nil {
		return err
	}
	defer cancel()
	return operationError("element.press", e.page.engine.SelectorTargetPressKeyContext(c, e.ref, key, modifiers(opts)...))
}
func (l *Locator) Press(ctx context.Context, key string, opts KeyOptions) error {
	c, cancel, err := l.operation(ctx, l.timeouts().Action)
	if err != nil {
		return l.wrapError("press", err)
	}
	defer cancel()
	e, err := l.resolve(c, "click")
	if err != nil {
		return l.wrapError("press", err)
	}
	return l.wrapError("press", e.Press(c, key, opts))
}
func (p *Page) Type(ctx context.Context, text string) error {
	c, cancel, err := p.operation(ctx, p.timeouts().Action)
	if err != nil {
		return err
	}
	defer cancel()
	return operationError("type", p.engine.InputInsertTextContext(c, text))
}
func (p *Page) ScrollTo(ctx context.Context, x, y float64) error {
	return operationError("scroll_to", p.engine.ScrollToPositionContext(ctx, x, y))
}
func (p *Page) ScrollBy(ctx context.Context, x, y float64) error {
	return operationError("scroll_by", p.engine.ScrollByContext(ctx, x, y))
}
func (p *Page) Highlight(ctx context.Context, selectors ...string) error {
	c, cancel, err := p.operation(ctx, p.timeouts().Read)
	if err != nil {
		return err
	}
	defer cancel()
	return operationError("highlight", p.engine.HighlightWithOptionsContext(c, selectors, engine.HighlightOptions{}))
}
func (p *Page) ShowAlert(ctx context.Context, text string) error {
	c, err := p.ExecutionContext(ctx, ExecutionContextOptions{World: WorldCore})
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(text)
	return c.Eval(ctx, "return window.__cdp_ffi.run.alertAdd("+string(raw)+")", nil)
}
func (p *Page) HideAlert(ctx context.Context) error {
	c, err := p.ExecutionContext(ctx, ExecutionContextOptions{World: WorldCore})
	if err != nil {
		return err
	}
	return c.Eval(ctx, "return window.__cdp_ffi.run.alertRemove()", nil)
}
func (p *Page) IsFullscreen(ctx context.Context) (bool, error) {
	c, cancel, err := p.operation(ctx, p.timeouts().Read)
	if err != nil {
		return false, err
	}
	defer cancel()
	if err = p.engine.EnsureMainRuntime(c); err != nil {
		return false, operationError("fullscreen", err)
	}
	var v bool
	err = p.Eval(c, "return !!document.fullscreenElement", &v)
	return v, err
}
func (e *Element) DragTo(ctx context.Context, target *Element) error {
	c, cancel, err := e.operation(ctx, e.timeouts().Action)
	if err != nil {
		return err
	}
	defer cancel()
	if err = target.validate(c); err != nil {
		return err
	}
	if target.page != e.page {
		return ErrStaleElement
	}
	return operationError("element.drag", e.page.engine.SelectorTargetDragContext(c, e.ref, target.ref))
}
func (l *Locator) DragTo(ctx context.Context, target *Locator) error {
	c, cancel, err := l.operation(ctx, l.timeouts().Action)
	if err != nil {
		return l.wrapError("drag_to", err)
	}
	defer cancel()
	from, err := l.resolve(c, "drag")
	if err != nil {
		return l.wrapError("drag_to", err)
	}
	to, err := target.resolve(c, "drag")
	if err != nil {
		return l.wrapError("drag_to", err)
	}
	return from.DragTo(c, to)
}

func (p *Page) MouseMove(ctx context.Context, x, y float64) error {
	c, cancel, err := p.operation(ctx, p.timeouts().Action)
	if err != nil {
		return err
	}
	defer cancel()
	return operationError("MouseMove", p.engine.MouseMoveContext(c, x, y))
}

func (p *Page) MouseClick(ctx context.Context, x, y float64) error {
	c, cancel, err := p.operation(ctx, p.timeouts().Action)
	if err != nil {
		return err
	}
	defer cancel()
	return operationError("MouseClick", p.engine.MouseClickContext(c, x, y))
}

func (p *Page) MouseDoubleClick(ctx context.Context, x, y float64) error {
	c, cancel, err := p.operation(ctx, p.timeouts().Action)
	if err != nil {
		return err
	}
	defer cancel()
	return operationError("MouseDoubleClick", p.engine.MouseDoubleClickContext(c, x, y))
}

func (p *Page) MouseRightClick(ctx context.Context, x, y float64) error {
	c, cancel, err := p.operation(ctx, p.timeouts().Action)
	if err != nil {
		return err
	}
	defer cancel()
	return operationError("MouseRightClick", p.engine.MouseRightClickContext(c, x, y))
}

func (p *Page) MouseDrag(ctx context.Context, fromX, fromY, toX, toY float64) error {
	c, cancel, err := p.operation(ctx, p.timeouts().Action)
	if err != nil {
		return err
	}
	defer cancel()
	return operationError("MouseDrag", p.engine.MouseDragContext(c, fromX, fromY, toX, toY))
}

func (p *Page) MouseWheel(ctx context.Context, x, y, dx, dy float64) error {
	c, cancel, err := p.operation(ctx, p.timeouts().Action)
	if err != nil {
		return err
	}
	defer cancel()
	return operationError("MouseWheel", p.engine.MouseWheelContext(c, x, y, dx, dy))
}
