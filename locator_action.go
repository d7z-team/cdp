package cdp

import (
	"context"
	"time"
)

func (l *Locator) Click(ctx context.Context) error {
	return l.withElement(ctx, "click", "click", l.timeouts().Action, func(c context.Context, e *Element) error { return e.Click(c) })
}
func (l *Locator) DoubleClick(ctx context.Context) error {
	return l.withElement(ctx, "double_click", "click", l.timeouts().Action, func(c context.Context, e *Element) error { return e.DoubleClick(c) })
}
func (l *Locator) RightClick(ctx context.Context) error {
	return l.withElement(ctx, "right_click", "click", l.timeouts().Action, func(c context.Context, e *Element) error { return e.RightClick(c) })
}
func (l *Locator) Hover(ctx context.Context) error {
	return l.withElement(ctx, "hover", "hover", l.timeouts().Action, func(c context.Context, e *Element) error { return e.Hover(c) })
}
func (l *Locator) Fill(ctx context.Context, value string) error {
	return l.withElement(ctx, "fill", "input", l.timeouts().Action, func(c context.Context, e *Element) error { return e.Fill(c, value) })
}
func (l *Locator) Type(ctx context.Context, value string) error {
	return l.withElement(ctx, "type", "input", l.timeouts().Action, func(c context.Context, e *Element) error { return e.Type(c, value) })
}
func (l *Locator) SetChecked(ctx context.Context, checked bool) error {
	return l.withElement(ctx, "set_checked", "click", l.timeouts().Action, func(c context.Context, e *Element) error { return e.SetChecked(c, checked) })
}
func (l *Locator) Select(ctx context.Context, values []string) error {
	return l.withElement(ctx, "select", "click", l.timeouts().Action, func(c context.Context, e *Element) error { return e.Select(c, values) })
}
func (l *Locator) SetFiles(ctx context.Context, files []string) error {
	return l.withElement(ctx, "set_files", "", l.timeouts().Action, func(c context.Context, e *Element) error { return e.SetFiles(c, files) })
}
func (l *Locator) ScrollIntoView(ctx context.Context) error {
	return l.withElement(ctx, "scroll_into_view", "", l.timeouts().Action, func(c context.Context, e *Element) error { return e.ScrollIntoView(c) })
}
func (l *Locator) ScrollTo(ctx context.Context, x, y float64) error {
	return l.withElement(ctx, "scroll_to", "", l.timeouts().Action, func(c context.Context, e *Element) error { return e.ScrollTo(c, x, y) })
}
func (l *Locator) ScrollBy(ctx context.Context, x, y float64) error {
	return l.withElement(ctx, "scroll_by", "", l.timeouts().Action, func(c context.Context, e *Element) error { return e.ScrollBy(c, x, y) })
}
func (l *Locator) SetStyle(ctx context.Context, name, value string) error {
	return l.withElement(ctx, "set_style", "", l.timeouts().Action, func(c context.Context, e *Element) error { return e.SetStyle(c, name, value) })
}
func (l *Locator) RemoveStyle(ctx context.Context, name string) error {
	return l.withElement(ctx, "remove_style", "", l.timeouts().Action, func(c context.Context, e *Element) error { return e.RemoveStyle(c, name) })
}
func (l *Locator) SetTextContent(ctx context.Context, text string) error {
	return l.withElement(ctx, "set_text_content", "", l.timeouts().Action, func(c context.Context, e *Element) error { return e.SetTextContent(c, text) })
}
func (l *Locator) Highlight(ctx context.Context, label string) error {
	return l.withElement(ctx, "highlight", "", l.timeouts().Read, func(c context.Context, e *Element) error { return e.Highlight(c, label) })
}

func (l *Locator) withElement(ctx context.Context, op, purpose string, timeout time.Duration, action func(context.Context, *Element) error) error {
	c, cancel, err := l.operation(ctx, timeout)
	if err != nil {
		return l.wrapError(op, err)
	}
	defer cancel()
	e, err := l.resolve(c, purpose)
	if err != nil {
		return l.wrapError(op, err)
	}
	return l.wrapError(op, action(c, e))
}
