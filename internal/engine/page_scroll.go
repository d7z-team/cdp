package engine

import (
	"context"

	"gopkg.d7z.net/cdp/internal/webassets"
)

func (p *Page) SelectorTargetScrollNext(ctx context.Context, ref SelectorTargetRef, axis string, direction int) (bool, error) {
	var moved bool
	err := p.runForegroundInteractionContext(ctx, func() error {
		target, backendNodeID, err := p.selectorTargetTargetAndBackendNodeID(ctx, ref)
		if err != nil {
			return err
		}
		result, err := p.callTargetBackendNodeFunctionContext(ctx, target, backendNodeID, true, ffiElementRequiredFunction("", "elementScrollNext", "core scroll helper is not available", webassets.JSLit(axis), webassets.JSLit(direction)))
		if err != nil {
			return err
		}
		if err = runtimeResultError(result); err != nil {
			return err
		}
		moved, _ = SafeGet[bool](result, "result", "value")
		return nil
	})
	return moved, err
}

func (p *Page) SelectorTargetScrollTo(ctx context.Context, ref SelectorTargetRef, x, y float64) error {
	return p.runForegroundInteractionContext(ctx, func() error {
		target, backendNodeID, err := p.selectorTargetTargetAndBackendNodeID(ctx, ref)
		if err != nil {
			return err
		}
		result, err := p.callTargetBackendNodeFunctionContext(ctx, target, backendNodeID, true, ffiElementRequiredFunction("", "elementScrollTo", "core scroll helper is not available", webassets.JSLit(x), webassets.JSLit(y)))
		if err != nil {
			return err
		}
		return runtimeResultError(result)
	})
}
