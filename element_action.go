package cdp

import "context"

func (e *Element) Click(ctx context.Context) error {
	c, cancel, err := e.operation(ctx, e.timeouts().Action)
	if err != nil {
		return err
	}
	defer cancel()
	return operationError("element.click", e.page.engine.SelectorTargetClickContext(c, e.ref))
}
func (e *Element) DoubleClick(ctx context.Context) error {
	c, cancel, err := e.operation(ctx, e.timeouts().Action)
	if err != nil {
		return err
	}
	defer cancel()
	return operationError("element.doubleclick", e.page.engine.SelectorTargetDoubleClick(c, e.ref))
}
func (e *Element) RightClick(ctx context.Context) error {
	c, cancel, err := e.operation(ctx, e.timeouts().Action)
	if err != nil {
		return err
	}
	defer cancel()
	return operationError("element.rightclick", e.page.engine.SelectorTargetRightClick(c, e.ref))
}
func (e *Element) Hover(ctx context.Context) error {
	c, cancel, err := e.operation(ctx, e.timeouts().Action)
	if err != nil {
		return err
	}
	defer cancel()
	return operationError("element.hover", e.page.engine.SelectorTargetHoverContext(c, e.ref))
}
func (e *Element) Fill(ctx context.Context, value string) error {
	c, cancel, err := e.operation(ctx, e.timeouts().Action)
	if err != nil {
		return err
	}
	defer cancel()
	return operationError("element.fill", e.page.engine.SelectorTargetInputContext(c, e.ref, value))
}
func (e *Element) Type(ctx context.Context, value string) error {
	c, cancel, err := e.operation(ctx, e.timeouts().Action)
	if err != nil {
		return err
	}
	defer cancel()
	return operationError("element.type", e.page.engine.SelectorTargetInsertTextContext(c, e.ref, value))
}
func (e *Element) SetChecked(ctx context.Context, checked bool) error {
	c, cancel, err := e.operation(ctx, e.timeouts().Action)
	if err != nil {
		return err
	}
	defer cancel()
	return operationError("element.setchecked", e.page.engine.SelectorTargetSetCheckedContext(c, e.ref, checked))
}
func (e *Element) Select(ctx context.Context, values []string) error {
	c, cancel, err := e.operation(ctx, e.timeouts().Action)
	if err != nil {
		return err
	}
	defer cancel()
	return operationError("element.select", e.page.engine.SelectorTargetSelectContext(c, e.ref, values))
}
func (e *Element) SetFiles(ctx context.Context, files []string) error {
	c, cancel, err := e.operation(ctx, e.timeouts().Action)
	if err != nil {
		return err
	}
	defer cancel()
	return operationError("element.setfiles", e.page.engine.SelectorTargetSetFileInputFilesContext(c, e.ref, files))
}
func (e *Element) ScrollIntoView(ctx context.Context) error {
	c, cancel, err := e.operation(ctx, e.timeouts().Action)
	if err != nil {
		return err
	}
	defer cancel()
	return operationError("element.scrollintoview", e.page.engine.SelectorTargetScrollIntoView(c, e.ref))
}
func (e *Element) ScrollTo(ctx context.Context, x, y float64) error {
	c, cancel, err := e.operation(ctx, e.timeouts().Action)
	if err != nil {
		return err
	}
	defer cancel()
	return operationError("element.scrollto", e.page.engine.SelectorTargetScrollPositionContext(c, e.ref, x, y))
}
func (e *Element) ScrollBy(ctx context.Context, x, y float64) error {
	c, cancel, err := e.operation(ctx, e.timeouts().Action)
	if err != nil {
		return err
	}
	defer cancel()
	return operationError("element.scrollby", e.page.engine.SelectorTargetScrollByContext(c, e.ref, x, y))
}
func (e *Element) SetStyle(ctx context.Context, name, value string) error {
	c, cancel, err := e.operation(ctx, e.timeouts().Action)
	if err != nil {
		return err
	}
	defer cancel()
	return operationError("element.setstyle", e.page.engine.SelectorTargetSetStyle(c, e.ref, name, value))
}
func (e *Element) RemoveStyle(ctx context.Context, name string) error {
	c, cancel, err := e.operation(ctx, e.timeouts().Action)
	if err != nil {
		return err
	}
	defer cancel()
	return operationError("element.removestyle", e.page.engine.SelectorTargetRemoveStyle(c, e.ref, name))
}
func (e *Element) SetTextContent(ctx context.Context, text string) error {
	c, cancel, err := e.operation(ctx, e.timeouts().Action)
	if err != nil {
		return err
	}
	defer cancel()
	return operationError("element.settextcontent", e.page.engine.SelectorTargetSetText(c, e.ref, text))
}

// Highlight draws an overlay around this exact element without changing its DOM.
func (e *Element) Highlight(ctx context.Context, label string) error {
	c, cancel, err := e.operation(ctx, e.timeouts().Read)
	if err != nil {
		return err
	}
	defer cancel()
	return operationError("element.highlight", e.page.engine.SelectorTargetHighlight(c, e.ref, label))
}
