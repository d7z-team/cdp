package cdp

import (
	"context"
	"encoding/json"
	"fmt"

	engine "gopkg.d7z.net/cdp/internal/engine"
)

type World string

const (
	WorldMain        World = "page.main"
	WorldCore        World = "cdp.isolated.core"
	WorldMainRuntime World = "cdp.main.runtime"
)

func (w World) validate() error {
	switch w {
	case "", WorldMain, WorldCore, WorldMainRuntime:
		return nil
	default:
		return fmt.Errorf("unknown execution world %q", w)
	}
}

type ExecutionContextOptions struct {
	World   World
	FrameID string
}
type ExecutionContext struct {
	page   *Page
	handle *engine.RuntimeContextHandle
}

func (p *Page) ExecutionContext(ctx context.Context, opts ExecutionContextOptions) (*ExecutionContext, error) {
	if err := opts.World.validate(); err != nil {
		return nil, err
	}
	c, cancel, err := p.operation(ctx, p.timeouts().Read)
	if err != nil {
		return nil, err
	}
	defer cancel()
	if opts.World == "" {
		opts.World = WorldMain
	}
	if opts.World == WorldMainRuntime {
		if err := p.engine.EnsureMainRuntime(c); err != nil {
			return nil, operationError("execution_context", err)
		}
	}
	h, err := p.engine.ContextWithContext(c, engine.RuntimeContextOptions{Namespace: engine.RuntimeNamespace(opts.World), FrameID: opts.FrameID})
	if err != nil {
		return nil, operationError("execution_context", err)
	}
	return &ExecutionContext{page: p, handle: h}, nil
}
func (c *ExecutionContext) Eval(ctx context.Context, script string, result any) error {
	raw, err := c.EvalJSON(ctx, script)
	if err != nil {
		return err
	}
	if result == nil {
		return nil
	}
	return json.Unmarshal(raw, result)
}
func (c *ExecutionContext) EvalJSON(ctx context.Context, script string) (json.RawMessage, error) {
	if c == nil || c.page == nil || c.handle == nil {
		return nil, ErrClosed
	}
	ctx, cancel, err := c.page.operation(ctx, c.page.timeouts().Read)
	if err != nil {
		return nil, err
	}
	defer cancel()
	raw, err := c.handle.EvalJSONContext(ctx, script)
	if raw == "" || raw == "undefined" {
		raw = "null"
	}
	return json.RawMessage(raw), operationError("execution_context.eval", err)
}
