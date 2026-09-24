package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// RuntimeNamespace identifies a browser execution namespace. Public page
// evaluation defaults to NamespacePageMain; CDP internal helpers run in
// isolated namespaces.
type RuntimeNamespace string

const (
	NamespacePageMain     RuntimeNamespace = "page.main"
	NamespaceMainRuntime  RuntimeNamespace = "cdp.main.runtime"
	NamespaceIsolatedCore RuntimeNamespace = "cdp.isolated.core"
	NamespaceOverlay      RuntimeNamespace = "cdp.overlay"
)

const (
	worldNameIsolatedCore = "__cdp_isolated_core"
)

// RuntimeContext describes a resolved execution context.
type RuntimeContext struct {
	Namespace RuntimeNamespace
	PageID    string
	TargetID  string
	SessionID string
	FrameID   string
	ContextID int
	WorldName string
	IsDefault bool
}

// RuntimeContextOptions selects a runtime context for public or internal eval.
type RuntimeContextOptions struct {
	Namespace RuntimeNamespace
	FrameID   string
	Timeout   time.Duration
}

// RuntimeContextHandle is a public handle for evaluating JavaScript in a
// specific runtime namespace.
type RuntimeContextHandle struct {
	epoch      int64
	frameEpoch uint64
	Page       *Page
	Info       RuntimeContext
}

// ConfirmTopFrameContext records the root FrameId reported by a runtime that
// has established window.parent === window. It is used by init-script
// bindings, avoiding frame-tree queries when attaching to an existing page.
func (p *Page) ConfirmTopFrameContext(executionContextID int) bool {
	if p == nil || executionContextID == 0 {
		return false
	}
	info, ok := p.executionContextInfo(executionContextID)
	if !ok || strings.TrimSpace(info.FrameID) == "" {
		return false
	}
	p.setMainFrame(info.FrameID)
	// A late readiness callback can belong to the document being navigated
	// away from. Only navigation lifecycle events may end the pending state.
	return true
}

func (h *RuntimeContextHandle) Namespace() RuntimeNamespace {
	if h == nil {
		return ""
	}
	return h.Info.Namespace
}

func (h *RuntimeContextHandle) FrameID() string {
	if h == nil {
		return ""
	}
	return h.Info.FrameID
}

func (h *RuntimeContextHandle) Eval(expr string) error {
	_, err := h.eval(context.Background(), expr)
	return err
}

func (h *RuntimeContextHandle) EvalValue(expr string) (any, error) {
	return h.EvalValueContext(context.Background(), expr)
}

func (h *RuntimeContextHandle) EvalValueContext(ctx context.Context, expr string) (any, error) {
	result, err := h.eval(ctx, wrapUserEvalExpression(expr))
	if err != nil {
		return nil, err
	}
	value, _ := SafeGet[any](result, "result", "value")
	return value, nil
}

// EvalValueExactContext evaluates without resolving a replacement execution
// context when the handle's document has been superseded.
func (h *RuntimeContextHandle) EvalValueExactContext(ctx context.Context, expr string) (any, error) {
	result, err := h.evalResolved(ctx, wrapUserEvalExpression(expr))
	if err != nil {
		return nil, err
	}
	if runtimeErr := runtimeResultError(result); runtimeErr != nil {
		return nil, runtimeErr
	}
	value, _ := SafeGet[any](result, "result", "value")
	return value, nil
}

func (h *RuntimeContextHandle) EvalJSON(expr string) (string, error) {
	return h.EvalJSONContext(context.Background(), expr)
}

func (h *RuntimeContextHandle) EvalJSONContext(ctx context.Context, expr string) (string, error) {
	value, err := h.EvalValueContext(ctx, expr)
	if err != nil {
		return "", err
	}
	if value == nil {
		return "null", nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (h *RuntimeContextHandle) Call(function string, args ...any) (any, error) {
	if strings.TrimSpace(function) == "" {
		return nil, errors.New("runtime function is empty")
	}
	rawArgs, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	expr := fmt.Sprintf(`return await (%s)(...%s)`, function, rawArgs)
	return h.EvalValue(expr)
}

func (h *RuntimeContextHandle) eval(ctx context.Context, expression string) (map[string]any, error) {
	if h == nil || h.Page == nil {
		return nil, errors.New("runtime context handle is nil")
	}
	result, err := h.evalResolved(ctx, expression)

	if err != nil {
		return nil, err
	}
	if runtimeErr := runtimeResultError(result); runtimeErr != nil {
		return nil, runtimeErr
	}
	return result, nil
}

func (h *RuntimeContextHandle) evalResolved(ctx context.Context, expression string) (map[string]any, error) {
	if h.epoch != h.Page.RuntimeGeneration() || h.frameEpoch != h.Page.frameGeneration(h.Info.FrameID) {
		return nil, BrowserErrorFromNode(BrowserErrorNode{Op: "runtime.eval", Kind: "stale_target", Message: "execution context document changed"}, nil)
	}
	target := ExecutionTarget{
		PageID:    h.Info.PageID,
		TargetID:  h.Info.TargetID,
		SessionID: h.Info.SessionID,
		ContextID: h.Info.ContextID,
		FrameID:   h.Info.FrameID,
	}
	return h.Page.RuntimeEvaluateOnTarget(ctx, target, expression, true)
}

func (p *Page) EvalAllRuntimeContextsContext(ctx context.Context, namespace RuntimeNamespace, expression string) error {
	if p == nil {
		return errors.New("page is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	namespace = normalizeRuntimeNamespace(namespace)
	_ = p.ensureRuntimeContext(ctx, RuntimeContextOptions{Namespace: namespace})
	contexts := p.runtimeContexts(namespace)
	if len(contexts) == 0 {
		info, err := p.resolveRuntimeContext(ctx, RuntimeContextOptions{Namespace: namespace})
		if err != nil {
			return err
		}
		handle := &RuntimeContextHandle{Page: p, Info: info, epoch: p.RuntimeGeneration(), frameEpoch: p.frameGeneration(info.FrameID)}
		_, err = handle.eval(ctx, expression)
		return err
	}
	var errs []error
	for _, info := range contexts {
		handle := &RuntimeContextHandle{Page: p, Info: info, epoch: p.RuntimeGeneration(), frameEpoch: p.frameGeneration(info.FrameID)}
		_, err := handle.eval(ctx, expression)
		if err != nil {
			if isMissingExecutionContextError(err) {
				continue
			}
			errs = append(errs, fmt.Errorf("context %d frame %s: %w", info.ContextID, info.FrameID, err))
		}
	}
	return errors.Join(errs...)
}

func wrapUserEvalExpression(expr string) string {
	return fmt.Sprintf(`(async function() {
	const AsyncFunction = Object.getPrototypeOf(async function(){}).constructor;
	const fn = new AsyncFunction(%s);
	return await fn.call(globalThis);
})()`, jsStringLiteral(expr))
}

func (p *Page) Context(opts RuntimeContextOptions) (*RuntimeContextHandle, error) {
	return p.ContextWithContext(context.Background(), opts)
}

func (p *Page) ContextWithContext(ctx context.Context, opts RuntimeContextOptions) (*RuntimeContextHandle, error) {
	if p == nil {
		return nil, errors.New("page is nil")
	}
	if ctx == nil {
		return nil, errors.New("runtime context is nil")
	}
	if opts.Namespace == "" {
		opts.Namespace = NamespacePageMain
	}
	info, err := p.resolveRuntimeContext(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &RuntimeContextHandle{Page: p, Info: info, epoch: p.RuntimeGeneration(), frameEpoch: p.frameGeneration(info.FrameID)}, nil
}
