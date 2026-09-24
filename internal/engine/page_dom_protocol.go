package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gopkg.d7z.net/cdp/internal/webassets"
)

func (p *Page) EvalNodef(ctx context.Context, nodeID int, call string, args ...any) (map[string]interface{}, error) {
	p.lock.RLock()
	defer p.lock.RUnlock()
	if err := p.checkConnContext(ctx); err != nil {
		return nil, err
	}

	functionDeclaration := call
	if len(args) > 0 {
		functionDeclaration = fmt.Sprintf(call, args...)
	}

	return p.callTargetNodeFunction(ctx, topPageExecutionTarget(), nodeID, functionDeclaration)
}

func (p *Page) DoubleClick(ctx context.Context, nodeID int) error {
	return p.runForegroundInteractionContext(ctx, func() error {
		return p.performValidatedMouseAction(ctx, func(scrollMode actionTargetScrollMode) (ActionabilityDiagnostic, error) {
			return p.nodeActionabilityDiagnostic(ctx, nodeID, scrollMode, "click")
		},
			actionVisualDoubleClick,
			func(x, y float64) error {
				return p.dispatchMouseDoubleClick(ctx,
					x, y)
			},
		)
	})
}

func (p *Page) RightClick(ctx context.Context, nodeID int) error {
	return p.runForegroundInteractionContext(ctx, func() error {
		return p.performValidatedMouseAction(ctx, func(scrollMode actionTargetScrollMode) (ActionabilityDiagnostic, error) {
			return p.nodeActionabilityDiagnostic(ctx, nodeID, scrollMode, "click")
		},
			actionVisualRightClick,
			func(x, y float64) error {
				return p.dispatchMouseClick(ctx,
					x, y, "right", 50*time.Millisecond)
			},
		)
	})
}

func (p *Page) Hover(ctx context.Context, nodeID int) error {
	return p.runForegroundInteractionContext(ctx, func() error {
		return p.performValidatedMouseAction(ctx, func(scrollMode actionTargetScrollMode) (ActionabilityDiagnostic, error) {
			return p.nodeActionabilityDiagnostic(ctx, nodeID, scrollMode, "hover")
		},
			actionVisualMove,
			func(x, y float64) error {
				return p.mouseMove(ctx,
					x, y)
			},
		)
	})
}

func (p *Page) Drag(ctx context.Context, fromID, toID int) error {
	return p.runForegroundInteractionContext(ctx, func() error {
		return p.dragNodes(ctx, fromID, toID)
	})
}

func (p *Page) dragNodes(ctx context.Context, fromID, toID int) error {
	fromDiagnostic, err := prepareActionabilityDiagnostic(func(scrollMode actionTargetScrollMode) (ActionabilityDiagnostic, error) {
		return p.nodeActionabilityDiagnostic(ctx, fromID, scrollMode)
	})
	if err != nil {
		return err
	}
	fromX, fromY, err := actionabilityPoint(fromDiagnostic)
	if err != nil {
		return err
	}
	toDiagnostic, err := prepareActionabilityDiagnostic(func(scrollMode actionTargetScrollMode) (ActionabilityDiagnostic, error) {
		return p.nodeActionabilityDiagnostic(ctx, toID, scrollMode)
	})
	if err != nil {
		return err
	}
	toX, toY, err := actionabilityPoint(toDiagnostic)
	if err != nil {
		return err
	}
	return p.mouseDrag(ctx,
		fromX, fromY, toX, toY)
}

// GetBackendID 将不稳定的 nodeId 转换为跨进程持久的 backendNodeId
func (p *Page) GetBackendID(ctx context.Context, nodeID int) (int, error) {
	p.lock.RLock()
	defer p.lock.RUnlock()
	if err := p.checkConnContext(ctx); err != nil {
		return -1, err
	}

	msg, err := p.DescribeNode(ctx, nodeID)
	if err != nil {
		return -1, err
	}
	p.rememberNodeIdentity(msg)

	backendID, ok := SafeGet[float64](msg, "node", "backendNodeId")
	if !ok {
		return -1, errors.New("获取后端节点 ID 失败")
	}

	return int(backendID), nil
}

// GetIframeRootID 通过 iframe 标签的 nodeId 获取其内部文档的根节点
func (p *Page) GetIframeRootID(ctx context.Context, iframeNodeID int) (int, error) {
	p.lock.RLock()
	defer p.lock.RUnlock()
	if err := p.checkConnContext(ctx); err != nil {
		return -1, err
	}

	msg, err := p.DescribeNode(ctx, iframeNodeID)
	if err != nil {
		return -1, err
	}
	p.rememberNodeIdentity(msg)

	// 跨域 Iframe 内容在 contentDocument 字段中
	nodeID, ok := SafeGet[float64](msg, "node", "contentDocument", "nodeId")
	if !ok {
		return -1, errors.New("无法访问 iframe 内容 (可能受安全限制)")
	}

	return int(nodeID), nil
}

func (p *Page) ResolveNodeInTarget(ctx context.Context, target ExecutionTarget, nodeID int) (map[string]any, error) {
	params := map[string]any{"nodeId": nodeID}
	if target.ContextID != 0 {
		params["executionContextId"] = target.ContextID
	}
	return p.sendTargetMessage(ctx, target, "DOM.resolveNode", params)
}

func (p *Page) ResolveBackendNodeInTargetContext(ctx context.Context, target ExecutionTarget, backendNodeID int) (map[string]any, error) {
	params := map[string]any{"backendNodeId": backendNodeID}
	if target.ContextID != 0 {
		params["executionContextId"] = target.ContextID
	}
	return p.sendTargetMessage(ctx, target, "DOM.resolveNode", params)
}

func (p *Page) CallFunctionOnInTargetContext(ctx context.Context, target ExecutionTarget, objectID string, code string, returnByValue bool, args ...any) (map[string]any, error) {
	params := map[string]any{
		"objectId":            objectID,
		"functionDeclaration": code,
		"returnByValue":       returnByValue,
		"awaitPromise":        true,
		"generatePreview":     false,
	}
	if !returnByValue {
		params["serializationOptions"] = map[string]any{
			"serialization": "idOnly",
		}
	}
	if len(args) > 0 {
		params["arguments"] = args
	}
	return p.sendTargetMessage(ctx, target, "Runtime.callFunctionOn", params)
}

func (p *Page) ReleaseObjectInTargetContext(ctx context.Context, target ExecutionTarget, objectID string) error {
	if objectID == "" {
		return nil
	}
	return p.sendTargetPacket(ctx, target, "Runtime.releaseObject", map[string]any{"objectId": objectID})
}

func (p *Page) scrollIntoViewIfNeededInTarget(ctx context.Context, target ExecutionTarget, nodeID int) error {
	err := p.sendTargetPacket(ctx, target, "DOM.scrollIntoViewIfNeeded", map[string]any{"nodeId": nodeID})
	if err != nil && isMissingNodeErr(err) {
		if backendID, ok := p.backendIDForNodeInTarget(target, nodeID); ok {
			return p.sendTargetPacket(ctx, target, "DOM.scrollIntoViewIfNeeded", map[string]any{"backendNodeId": backendID})
		}
	}
	return err
}

func (p *Page) DescribeBackendNodeInTargetContext(ctx context.Context, target ExecutionTarget, backendNodeID int) (map[string]any, error) {
	res, err := p.sendTargetMessage(ctx, target, "DOM.describeNode", map[string]any{
		"backendNodeId": backendNodeID,
		"depth":         0,
		"pierce":        false,
	})
	if err == nil {
		p.rememberNodeIdentityInTarget(target, res)
	}
	return res, err
}

func (p *Page) DescribeNode(ctx context.Context, nodeID int) (map[string]any, error) {
	return p.DescribeNodeInTarget(ctx, topPageExecutionTarget(), nodeID)
}

func (p *Page) DescribeNodeInTarget(ctx context.Context, target ExecutionTarget, nodeID int) (map[string]any, error) {
	res, err := p.sendTargetMessage(ctx, target, "DOM.describeNode", map[string]any{
		"nodeId": nodeID,
		"depth":  0,
		"pierce": false,
	})
	if err != nil && isMissingNodeErr(err) {
		if backendID, ok := p.backendIDForNodeInTarget(target, nodeID); ok {
			res, err = p.sendTargetMessage(ctx, target, "DOM.describeNode", map[string]any{
				"backendNodeId": backendID,
				"depth":         0,
				"pierce":        false,
			})
		}
	}
	if err == nil {
		p.rememberNodeIdentityInTarget(target, res)
	}
	return res, err
}

func (p *Page) DescribeNodeByObjectIDInTargetContext(ctx context.Context, target ExecutionTarget, objectID string) (map[string]any, error) {
	res, err := p.sendTargetMessage(ctx, target, "DOM.describeNode", map[string]any{
		"objectId": objectID,
		"depth":    0,
		"pierce":   false,
	})
	if err == nil {
		p.rememberNodeIdentityInTarget(target, res)
	}
	return res, err
}

type HighlightOptions struct {
	TimeoutMs      int    `json:"timeoutMs,omitempty"`
	TrackOnScroll  bool   `json:"trackOnScroll,omitempty"`
	EveryFrameRoot bool   `json:"everyFrameRoot,omitempty"`
	ModeName       string `json:"modeName,omitempty"`
	Color          string `json:"color,omitempty"`
}

func (p *Page) Highlight(ctx context.Context, selectors ...string) error {
	return p.HighlightWithOptionsContext(ctx, selectors, HighlightOptions{})
}

func (p *Page) HighlightWithOptionsContext(ctx context.Context, selectors []string, options HighlightOptions) error {
	if p == nil {
		return errors.New("页面未连接")
	}
	if len(selectors) == 0 {
		return nil
	}

	if err := p.checkConnContext(ctx); err != nil {
		return err
	}
	handle, err := p.Context(RuntimeContextOptions{Namespace: NamespaceIsolatedCore})
	if err != nil {
		return err
	}
	_, err = handle.eval(ctx,
		webassets.RuntimeOptionalMethodCall(webassets.RuntimeFFI, "highlight", webassets.JSLit(selectors), webassets.JSLit(options)),
	)
	return err
}
