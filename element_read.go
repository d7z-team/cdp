package cdp

import "context"

func (e *Element) Attribute(ctx context.Context, name string) (string, bool, error) {
	var value *string
	err := e.Eval(ctx, "return this.getAttribute("+quote(name)+")", &value)
	if value == nil {
		return "", false, err
	}
	return *value, true, err
}

func (e *Element) TextContent(ctx context.Context) (string, error) {
	c, cancel, err := e.operation(ctx, e.timeouts().Read)
	if err != nil {
		return "", err
	}
	defer cancel()
	v, err := e.page.engine.SelectorTargetText(c, e.ref)
	return v, operationError("element.textcontent", err)
}
func (e *Element) HTML(ctx context.Context) (string, error) {
	c, cancel, err := e.operation(ctx, e.timeouts().Read)
	if err != nil {
		return "", err
	}
	defer cancel()
	v, err := e.page.engine.SelectorTargetHTML(c, e.ref)
	return v, operationError("element.html", err)
}
func (e *Element) Value(ctx context.Context) (string, error) {
	c, cancel, err := e.operation(ctx, e.timeouts().Read)
	if err != nil {
		return "", err
	}
	defer cancel()
	v, err := e.page.engine.SelectorTargetValue(c, e.ref)
	return v, operationError("element.value", err)
}
func (e *Element) Style(ctx context.Context, name string) (string, error) {
	c, cancel, err := e.operation(ctx, e.timeouts().Read)
	if err != nil {
		return "", err
	}
	defer cancel()
	v, err := e.page.engine.SelectorTargetStyle(c, e.ref, name)
	return v, operationError("element.style", err)
}
func (e *Element) HasAttribute(ctx context.Context, name string) (bool, error) {
	c, cancel, err := e.operation(ctx, e.timeouts().Read)
	if err != nil {
		return false, err
	}
	defer cancel()
	v, err := e.page.engine.SelectorTargetHasAttr(c, e.ref, name)
	return v, operationError("element.hasattribute", err)
}
func (e *Element) HasClass(ctx context.Context, name string) (bool, error) {
	c, cancel, err := e.operation(ctx, e.timeouts().Read)
	if err != nil {
		return false, err
	}
	defer cancel()
	v, err := e.page.engine.SelectorTargetHasClass(c, e.ref, name)
	return v, operationError("element.hasclass", err)
}
func (e *Element) IsVisible(ctx context.Context) (bool, error) {
	c, cancel, err := e.operation(ctx, e.timeouts().Read)
	if err != nil {
		return false, err
	}
	defer cancel()
	v, err := e.page.engine.SelectorTargetVisible(c, e.ref)
	return v, operationError("element.isvisible", err)
}
func (e *Element) IsChecked(ctx context.Context) (bool, error) {
	c, cancel, err := e.operation(ctx, e.timeouts().Read)
	if err != nil {
		return false, err
	}
	defer cancel()
	v, err := e.page.engine.SelectorTargetChecked(c, e.ref)
	return v, operationError("element.ischecked", err)
}
func (e *Element) IsEnabled(ctx context.Context) (bool, error) {
	c, cancel, err := e.operation(ctx, e.timeouts().Read)
	if err != nil {
		return false, err
	}
	defer cancel()
	v, err := e.page.engine.SelectorTargetEnabled(c, e.ref)
	return v, operationError("element.isenabled", err)
}
func (e *Element) IsEditable(ctx context.Context) (bool, error) {
	c, cancel, err := e.operation(ctx, e.timeouts().Read)
	if err != nil {
		return false, err
	}
	defer cancel()
	v, err := e.page.engine.SelectorTargetEditable(c, e.ref)
	return v, operationError("element.iseditable", err)
}
func (e *Element) IsEmpty(ctx context.Context) (bool, error) {
	c, cancel, err := e.operation(ctx, e.timeouts().Read)
	if err != nil {
		return false, err
	}
	defer cancel()
	v, err := e.page.engine.SelectorTargetEmpty(c, e.ref)
	return v, operationError("element.isempty", err)
}
