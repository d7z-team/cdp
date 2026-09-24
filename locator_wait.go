package cdp

import (
	"context"
	"fmt"
	"strings"
)

type ElementState string

const (
	StateVisible   ElementState = "visible"
	StateHidden    ElementState = "hidden"
	StateAttached  ElementState = "attached"
	StateDetached  ElementState = "detached"
	StateEnabled   ElementState = "enabled"
	StateDisabled  ElementState = "disabled"
	StateChecked   ElementState = "checked"
	StateUnchecked ElementState = "unchecked"
	StateEditable  ElementState = "editable"
	StateEmpty     ElementState = "empty"
)

func (l *Locator) Wait(ctx context.Context, state ElementState) error {
	c, cancel, err := l.operation(ctx, l.timeouts().Action)
	if err != nil {
		return l.wrapError("wait", err)
	}
	defer cancel()
	return operationError("locator.wait", poll(c, func() (bool, error) {
		query, err := l.query(c, "", 0)
		refs := query.ids
		if err != nil {
			return false, l.wrapError("wait", err)
		}
		if len(refs) == 0 {
			return state == StateHidden || state == StateDetached, nil
		}
		if state == StateDetached {
			return false, nil
		}
		if state == StateAttached {
			return true, nil
		}
		for _, ref := range refs {
			e := l.page.element(ref)
			var ok bool
			var err error
			switch state {
			case StateVisible, StateHidden:
				ok, err = e.IsVisible(c)
				if state == StateHidden {
					ok = !ok
				}
			case StateEnabled, StateDisabled:
				ok, err = e.IsEnabled(c)
				if state == StateDisabled {
					ok = !ok
				}
			case StateChecked, StateUnchecked:
				ok, err = e.IsChecked(c)
				if state == StateUnchecked {
					ok = !ok
				}
			case StateEditable:
				ok, err = e.IsEditable(c)
			case StateEmpty:
				ok, err = e.IsEmpty(c)
			default:
				return false, fmt.Errorf("invalid element state %q", state)
			}
			if err != nil {
				return false, l.wrapError("wait", err)
			}
			if !ok {
				return false, nil
			}
		}
		return true, nil
	}))
}
func (l *Locator) WaitForText(ctx context.Context, text string, opts TextOptions) error {
	return l.waitValue(ctx, "text", func(ctx context.Context) (bool, error) {
		v, err := l.TextContent(ctx)
		if opts.Exact {
			return v == text, err
		}
		return strings.Contains(v, text), err
	})
}
func (l *Locator) waitValue(ctx context.Context, name string, check func(context.Context) (bool, error)) error {
	c, cancel, err := l.operation(ctx, l.timeouts().Action)
	if err != nil {
		return l.wrapError("wait_value", err)
	}
	defer cancel()
	return operationError("locator.wait_"+name, poll(c, func() (bool, error) { return check(c) }))
}
func (l *Locator) WaitForCount(ctx context.Context, count int) error {
	return l.waitValue(ctx, "count", func(ctx context.Context) (bool, error) { v, err := l.Count(ctx); return v == count, err })
}
func (l *Locator) WaitForValue(ctx context.Context, value string) error {
	return l.waitValue(ctx, "value", func(ctx context.Context) (bool, error) { v, err := l.Value(ctx); return v == value, err })
}
func (l *Locator) WaitForAttribute(ctx context.Context, name, value string) error {
	return l.waitValue(ctx, "attribute", func(ctx context.Context) (bool, error) {
		v, ok, err := l.Attribute(ctx, name)
		return ok && v == value, err
	})
}
func (l *Locator) WaitForClass(ctx context.Context, name string) error {
	return l.waitValue(ctx, "class", func(ctx context.Context) (bool, error) { return l.HasClass(ctx, name) })
}
func (l *Locator) WaitForStyle(ctx context.Context, name, value string) error {
	return l.waitValue(ctx, "style", func(ctx context.Context) (bool, error) { v, err := l.Style(ctx, name); return v == value, err })
}
