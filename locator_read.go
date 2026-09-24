package cdp

import "context"

func (l *Locator) TextContent(ctx context.Context) (string, error) {
	return readLocator(ctx, l, "text_content", func(c context.Context, e *Element) (string, error) { return e.TextContent(c) })
}
func (l *Locator) HTML(ctx context.Context) (string, error) {
	return readLocator(ctx, l, "html", func(c context.Context, e *Element) (string, error) { return e.HTML(c) })
}
func (l *Locator) Value(ctx context.Context) (string, error) {
	return readLocator(ctx, l, "value", func(c context.Context, e *Element) (string, error) { return e.Value(c) })
}
func (l *Locator) Style(ctx context.Context, name string) (string, error) {
	return readLocator(ctx, l, "style", func(c context.Context, e *Element) (string, error) { return e.Style(c, name) })
}
func (l *Locator) HasAttribute(ctx context.Context, name string) (bool, error) {
	return readLocator(ctx, l, "has_attribute", func(c context.Context, e *Element) (bool, error) { return e.HasAttribute(c, name) })
}
func (l *Locator) HasClass(ctx context.Context, name string) (bool, error) {
	return readLocator(ctx, l, "has_class", func(c context.Context, e *Element) (bool, error) { return e.HasClass(c, name) })
}
func (l *Locator) IsVisible(ctx context.Context) (bool, error) {
	return readLocator(ctx, l, "is_visible", func(c context.Context, e *Element) (bool, error) { return e.IsVisible(c) })
}
func (l *Locator) IsChecked(ctx context.Context) (bool, error) {
	return readLocator(ctx, l, "is_checked", func(c context.Context, e *Element) (bool, error) { return e.IsChecked(c) })
}
func (l *Locator) IsEnabled(ctx context.Context) (bool, error) {
	return readLocator(ctx, l, "is_enabled", func(c context.Context, e *Element) (bool, error) { return e.IsEnabled(c) })
}
func (l *Locator) IsEditable(ctx context.Context) (bool, error) {
	return readLocator(ctx, l, "is_editable", func(c context.Context, e *Element) (bool, error) { return e.IsEditable(c) })
}
func (l *Locator) IsEmpty(ctx context.Context) (bool, error) {
	return readLocator(ctx, l, "is_empty", func(c context.Context, e *Element) (bool, error) { return e.IsEmpty(c) })
}

func readLocator[T any](ctx context.Context, l *Locator, op string, read func(context.Context, *Element) (T, error)) (T, error) {
	var zero T
	c, cancel, err := l.operation(ctx, l.timeouts().Read)
	if err != nil {
		return zero, l.wrapError(op, err)
	}
	defer cancel()
	e, err := l.resolve(c, "")
	if err != nil {
		return zero, l.wrapError(op, err)
	}
	value, err := read(c, e)
	return value, l.wrapError(op, err)
}
