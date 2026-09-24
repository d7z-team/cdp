package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"gopkg.d7z.net/cdp/internal/webassets"
)

type BoxModelResult struct {
	Model BoxModel `json:"model"`
}

type BoxModel struct {
	Content      []float64 `json:"content"`
	Padding      []float64 `json:"padding"`
	Border       []float64 `json:"border"`
	Margin       []float64 `json:"margin"`
	Width        float64   `json:"width"`
	Height       float64   `json:"height"`
	ShapeOutside []float64 `json:"shapeOutside,omitempty"`
}

type LayoutMetricsResult struct {
	LayoutViewport    LayoutViewport `json:"layoutViewport"`
	VisualViewport    VisualViewport `json:"visualViewport"`
	ContentSize       ContentSize    `json:"contentSize"`
	CSSLayoutViewport LayoutViewport `json:"cssLayoutViewport"`
	CSSVisualViewport VisualViewport `json:"cssVisualViewport"`
	CSSContentSize    ContentSize    `json:"cssContentSize"`
}

func (p *Page) logReleaseObjectError(objectID string, err error) {
	if err == nil || errors.Is(err, ErrBrowserClosed) || errors.Is(err, context.Canceled) || isMissingObjectErr(err) {
		return
	}
	p.log(slog.LevelDebug, "release remote object failed", "object_id", objectID, "error", err)
}

func isMissingNodeErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "No node with given id found") ||
		strings.Contains(msg, "Could not find node with given id") ||
		strings.Contains(msg, "does not belong to the document")
}

func isMissingObjectErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "Could not find object with given id")
}

func (p *Page) rememberNodeBackendInTarget(target ExecutionTarget, nodeID int, backendID int) {
	if nodeID <= 0 || backendID <= 0 {
		return
	}
	p.nodeRefMu.Lock()
	if p.nodeBackendIDs == nil {
		p.nodeBackendIDs = map[targetNodeKey]int{}
	}
	p.nodeBackendIDs[targetNodeKey{SessionID: target.SessionID, NodeID: nodeID}] = backendID
	p.nodeRefMu.Unlock()
}

func (p *Page) backendIDForNodeInTarget(target ExecutionTarget, nodeID int) (int, bool) {
	p.nodeRefMu.RLock()
	defer p.nodeRefMu.RUnlock()
	if p.nodeBackendIDs == nil {
		return 0, false
	}
	backendID, ok := p.nodeBackendIDs[targetNodeKey{SessionID: target.SessionID, NodeID: nodeID}]
	return backendID, ok
}

func extractNodeMap(raw map[string]any) (map[string]any, bool) {
	if nodeRaw, ok := raw["node"].(map[string]any); ok {
		return nodeRaw, true
	}
	if nodeRaw, ok := raw["root"].(map[string]any); ok {
		return nodeRaw, true
	}
	return nil, false
}

func parseNodeIdentity(raw map[string]any) (int, int, bool) {
	nodeRaw, ok := extractNodeMap(raw)
	if !ok {
		return 0, 0, false
	}
	nodeIDValue, nodeIDOK := nodeRaw["nodeId"].(float64)
	backendIDValue, backendIDOK := nodeRaw["backendNodeId"].(float64)
	if !nodeIDOK || !backendIDOK {
		return 0, 0, false
	}
	return int(nodeIDValue), int(backendIDValue), true
}

func (p *Page) rememberNodeIdentity(raw map[string]any) {
	p.rememberNodeIdentityInTarget(topPageExecutionTarget(), raw)
}

func (p *Page) rememberNodeIdentityInTarget(target ExecutionTarget, raw map[string]any) {
	nodeID, backendID, ok := parseNodeIdentity(raw)
	if ok {
		p.rememberNodeBackendInTarget(target, nodeID, backendID)
	}
	nodeRaw, ok := extractNodeMap(raw)
	if !ok {
		return
	}
	contentDoc, ok := nodeRaw["contentDocument"].(map[string]any)
	if !ok {
		return
	}
	contentNodeID, nodeIDOK := contentDoc["nodeId"].(float64)
	contentBackendID, backendIDOK := contentDoc["backendNodeId"].(float64)
	if !nodeIDOK || !backendIDOK {
		return
	}
	p.rememberNodeBackendInTarget(target, int(contentNodeID), int(contentBackendID))
}

type LayoutViewport struct {
	PageX        float64 `json:"pageX"`
	PageY        float64 `json:"pageY"`
	ClientWidth  float64 `json:"clientWidth"`
	ClientHeight float64 `json:"clientHeight"`
}

type VisualViewport struct {
	OffsetX      float64 `json:"offsetX"`
	OffsetY      float64 `json:"offsetY"`
	PageX        float64 `json:"pageX"`
	PageY        float64 `json:"pageY"`
	ClientWidth  float64 `json:"clientWidth"`
	ClientHeight float64 `json:"clientHeight"`
	Scale        float64 `json:"scale"`
	Zoom         float64 `json:"zoom"`
}

type ContentSize struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

func (p *Page) GetAttribute(ctx context.Context, nodeID int, name string) (string, error) {
	nameLiteral := jsStringLiteral(name)
	result, err := p.EvalNodef(ctx, nodeID, "function() { return this.getAttribute(%s); }", nameLiteral)
	if err != nil {
		return "", err
	}

	if value, ok := SafeGet[string](result, "result", "value"); ok {
		return value, nil
	}
	return "", nil
}

func (p *Page) GetValue(ctx context.Context, nodeID int) (string, error) {
	result, err := p.EvalNodef(ctx, nodeID, "function() { return this.value; }")
	if err != nil {
		return "", err
	}

	if value, ok := SafeGet[string](result, "result", "value"); ok {
		return value, nil
	}
	if value, ok := SafeGet[any](result, "result", "value"); ok {
		return fmt.Sprintf("%v", value), nil
	}
	return "", nil
}

func (p *Page) GetHTML(ctx context.Context, nodeID int) (string, error) {
	code := `function() {
		const elem = this.cloneNode(true);
		function clean(el) {
			if (el.attributes) {
				while(el.attributes.length > 0) {
					el.removeAttribute(el.attributes[0].name);
				}
			}
			const children = Array.from(el.childNodes);
			for (const child of children) {
				if (child.nodeType === 3) {
					const text = child.textContent.trim();
					if (text === "") {
						el.removeChild(child);
					} else {
						child.textContent = text;
					}
				} else if (child.nodeType === 1) {
					clean(child);
				}
			}
		}
		clean(elem);
		return elem.outerHTML.replace(/>\s+</g, "><");
	}`
	result, err := p.EvalNodef(ctx, nodeID, "%s", code)
	if err != nil {
		return "", err
	}

	if value, ok := SafeGet[string](result, "result", "value"); ok {
		return value, nil
	}
	return "", nil
}

var elementPureVisibleBody = `
	if (!this || !this.isConnected || !(this instanceof Element)) {
		return false;
	}
	const cdp = ` + webassets.RuntimeSlotExpr(webassets.RuntimeFFI) + `;
	if (cdp?.locator?.isVisible) {
		return cdp.locator.isVisible(this);
	}
	const composedParent = (node) => {
		if (node.parentElement) {
			return node.parentElement;
		}
		const root = node.getRootNode?.();
		return root instanceof ShadowRoot ? root.host : null;
	};
	let current = this;
	while (current) {
		const style = current.ownerDocument?.defaultView?.getComputedStyle?.(current);
		if (!style) {
			return false;
		}
		if (style.display === 'none' || style.visibility === 'hidden' || style.visibility === 'collapse') {
			return false;
		}
		if (parseFloat(style.opacity || '1') === 0) {
			return false;
		}
		current = composedParent(current);
	}
	for (const rect of Array.from(this.getClientRects())) {
		if (rect.width > 0 && rect.height > 0) {
			return true;
		}
	}
	const rect = this.getBoundingClientRect();
	return rect.width > 0 && rect.height > 0;
`

var elementPureVisibleScript = `function() {` + elementPureVisibleBody + `}`

var elementInViewportScript = `function() {
	const isVisible = () => {` + elementPureVisibleBody + `};
	if (!isVisible()) {
		return false;
	}
	const view = this.ownerDocument?.defaultView || window;
	const intersectsViewport = (rect) => rect.width > 0
		&& rect.height > 0
		&& rect.bottom > 0
		&& rect.right > 0
		&& rect.top < view.innerHeight
		&& rect.left < view.innerWidth;
	for (const rect of Array.from(this.getClientRects())) {
		if (intersectsViewport(rect)) {
			return true;
		}
	}
	return intersectsViewport(this.getBoundingClientRect());
}`

func (p *Page) ScrollIntoView(ctx context.Context, nodeID int) error {
	return p.runForegroundInteractionContext(ctx, func() error {
		return p.scrollNodeIntoView(ctx, nodeID)
	})
}

func (p *Page) scrollNodeIntoView(ctx context.Context, nodeID int) error {
	p.lock.RLock()
	defer p.lock.RUnlock()
	if err := p.checkConnContext(ctx); err != nil {
		return err
	}
	return p.scrollIntoViewIfNeededInTarget(ctx, topPageExecutionTarget(), nodeID)
}

func (p *Page) scrollIntoViewIfNeededByBackendNodeIDInTarget(ctx context.Context, target ExecutionTarget, backendNodeID int) error {
	if backendNodeID <= 0 {
		return errors.New("invalid backendNodeId")
	}
	return p.sendTargetPacket(ctx, target, "DOM.scrollIntoViewIfNeeded", map[string]any{"backendNodeId": backendNodeID})
}

func (p *Page) scrollIntoViewIfNeededByTargetRef(ctx context.Context, ref SelectorTargetRef) error {
	p.lock.RLock()
	defer p.lock.RUnlock()
	if err := p.checkConnContext(ctx); err != nil {
		return err
	}
	target, backendNodeID, err := p.selectorTargetTargetAndBackendNodeID(ctx, ref)
	if err != nil {
		return err
	}
	return p.scrollIntoViewIfNeededByBackendNodeIDInTarget(ctx, target, backendNodeID)
}

func (p *Page) resolveNodeObjectInTarget(ctx context.Context, target ExecutionTarget, nodeID int) (string, error) {
	msg, err := p.ResolveNodeInTarget(ctx, target, nodeID)
	if err != nil {
		if !isMissingNodeErr(err) {
			return "", err
		}
		backendID, ok := p.backendIDForNodeInTarget(target, nodeID)
		if !ok {
			return "", err
		}
		if desc, descErr := p.DescribeBackendNodeInTargetContext(ctx, target, backendID); descErr == nil {
			p.rememberNodeIdentityInTarget(target, desc)
		}
		msg, err = p.ResolveBackendNodeInTargetContext(ctx, target, backendID)
		if err != nil {
			return "", err
		}
	}
	objectID, ok := SafeGet[string](msg, "object", "objectId")
	if !ok {
		return "", errors.New("获取对象 ID 失败")
	}
	return objectID, nil
}

func (p *Page) callTargetNodeFunction(ctx context.Context, target ExecutionTarget, nodeID int, code string, args ...any) (map[string]any, error) {
	objectID, err := p.resolveNodeObjectInTarget(ctx, target, nodeID)
	if err != nil {
		return nil, err
	}
	callRes, err := p.CallFunctionOnInTargetContext(ctx, target, objectID, code, true, args...)
	if err != nil && isMissingObjectErr(err) {
		p.logReleaseObjectError(objectID, p.ReleaseObjectInTargetContext(ctx, target, objectID))
		objectID, err = p.resolveNodeObjectInTarget(ctx, target, nodeID)
		if err != nil {
			return nil, err
		}
		callRes, err = p.CallFunctionOnInTargetContext(ctx, target, objectID, code, true, args...)
	}
	p.logReleaseObjectError(objectID, p.ReleaseObjectInTargetContext(ctx, target, objectID))
	return callRes, BrowserErrorFromCDP("Runtime.callFunctionOn", target, err)
}

func (p *Page) resolveBackendNodeObjectInTargetContext(ctx context.Context, target ExecutionTarget, backendNodeID int) (string, error) {
	if desc, descErr := p.DescribeBackendNodeInTargetContext(ctx, target, backendNodeID); descErr == nil {
		p.rememberNodeIdentityInTarget(target, desc)
	}
	msg, err := p.ResolveBackendNodeInTargetContext(ctx, target, backendNodeID)
	if err != nil {
		return "", err
	}
	objectID, ok := SafeGet[string](msg, "object", "objectId")
	if !ok {
		return "", errors.New("获取对象 ID 失败")
	}
	return objectID, nil
}

func (p *Page) callTargetBackendNodeFunctionContext(ctx context.Context, target ExecutionTarget, backendNodeID int, returnByValue bool, code string, args ...any) (map[string]any, error) {
	objectID, err := p.resolveBackendNodeObjectInTargetContext(ctx, target, backendNodeID)
	if err != nil {
		return nil, err
	}
	callRes, err := p.CallFunctionOnInTargetContext(ctx, target, objectID, code, returnByValue, args...)
	if err != nil && isMissingObjectErr(err) {
		p.logReleaseObjectError(objectID, p.ReleaseObjectInTargetContext(ctx, target, objectID))
		objectID, err = p.resolveBackendNodeObjectInTargetContext(ctx, target, backendNodeID)
		if err != nil {
			return nil, err
		}
		callRes, err = p.CallFunctionOnInTargetContext(ctx, target, objectID, code, returnByValue, args...)
	}
	p.logReleaseObjectError(objectID, p.ReleaseObjectInTargetContext(ctx, target, objectID))
	return callRes, BrowserErrorFromCDP("Runtime.callFunctionOn", target, err)
}
