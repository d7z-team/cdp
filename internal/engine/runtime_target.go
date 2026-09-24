package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gopkg.d7z.net/cdp/internal/webassets"
)

// ExecutionTarget identifies the CDP session and execution context that owns a
// page-side runtime. A zero SessionID means the command is routed to the top
// page target through Page.CdpConn.
type ExecutionTarget struct {
	RuntimeID string
	PageID    string
	SessionID string
	ContextID int
	TargetID  string
	FrameID   string
}

func topPageExecutionTarget() ExecutionTarget {
	return ExecutionTarget{}
}

type targetContextKey struct {
	SessionID string
	ContextID int
}

type targetNodeKey struct {
	SessionID string
	NodeID    int
}

var runtimeInfoExpression = webassets.RuntimeOptionalMethodCall(webassets.RuntimeFFI, "runtimeInfo")

var (
	executionTargetLookupTimeout  = 600 * time.Millisecond
	executionTargetLookupInterval = 50 * time.Millisecond
)

func (p *Page) storeExecutionTarget(target ExecutionTarget) {
	if p == nil || target.ContextID == 0 {
		return
	}
	target.RuntimeID = strings.TrimSpace(target.RuntimeID)
	target.PageID = firstNonEmpty(target.PageID, p.ID)
	target.SessionID = strings.TrimSpace(target.SessionID)
	target.TargetID = strings.TrimSpace(target.TargetID)
	target.FrameID = strings.TrimSpace(target.FrameID)

	p.contextMu.Lock()
	defer p.contextMu.Unlock()
	p.ensureExecutionTargetMapsLocked()
	p.storeExecutionTargetLocked(target)
}

func (p *Page) storeExecutionTargetLocked(target ExecutionTarget) {
	if target.ContextID == 0 {
		return
	}
	p.ensureExecutionTargetMapsLocked()
	key := targetContextKey{SessionID: target.SessionID, ContextID: target.ContextID}
	if previous, ok := p.executionTargetsByContext[key]; ok {
		if target.RuntimeID == "" {
			target.RuntimeID = previous.RuntimeID
		}
		target.PageID = firstNonEmpty(target.PageID, previous.PageID)
		target.TargetID = firstNonEmpty(target.TargetID, previous.TargetID)
		target.FrameID = firstNonEmpty(target.FrameID, previous.FrameID)
		if previous.RuntimeID != "" && previous.RuntimeID != target.RuntimeID {
			delete(p.executionTargetsByRuntime, previous.RuntimeID)
		}
	}
	p.executionTargetsByContext[key] = target
	if target.RuntimeID != "" {
		p.executionTargetsByRuntime[target.RuntimeID] = target
	}
}

func (p *Page) ensureExecutionTargetMapsLocked() {
	if p.executionTargetsByRuntime == nil {
		p.executionTargetsByRuntime = map[string]ExecutionTarget{}
	}
	if p.executionTargetsByContext == nil {
		p.executionTargetsByContext = map[targetContextKey]ExecutionTarget{}
	}
	if p.targetEpochs == nil {
		p.targetEpochs = map[string]int64{}
	}
}

func targetEpochKey(sessionID string) string {
	return "session:" + strings.TrimSpace(sessionID)
}

func targetContextEpochKey(sessionID string, contextID int) string {
	return fmt.Sprintf("context:%s:%d", strings.TrimSpace(sessionID), contextID)
}

func (p *Page) currentTargetEpoch(target ExecutionTarget) int64 {
	if p == nil {
		return 0
	}
	p.contextMu.RLock()
	defer p.contextMu.RUnlock()
	if p.targetEpochs == nil {
		return 0
	}
	return p.currentTargetEpochLocked(target)
}

func (p *Page) currentTargetEpochLocked(target ExecutionTarget) int64 {
	return p.targetEpochs[targetEpochKey(target.SessionID)] + p.targetEpochs[targetContextEpochKey(target.SessionID, target.ContextID)]
}

func (p *Page) storeExecutionTargetAtEpoch(target ExecutionTarget, epoch int64) bool {
	if p == nil || target.ContextID == 0 {
		return false
	}
	p.contextMu.Lock()
	defer p.contextMu.Unlock()
	p.ensureExecutionTargetMapsLocked()
	if p.currentTargetEpochLocked(target) != epoch {
		return false
	}
	p.storeExecutionTargetLocked(target)
	return true
}

// RuntimeGeneration identifies the current top-page execution-context generation.
func (p *Page) RuntimeGeneration() int64 {
	return p.currentTargetEpoch(topPageExecutionTarget())
}

func (p *Page) bumpTargetEpoch(sessionID string) {
	if p == nil {
		return
	}
	p.contextMu.Lock()
	defer p.contextMu.Unlock()
	p.bumpTargetEpochLocked(sessionID)
}

func (p *Page) bumpTargetEpochLocked(sessionID string) {
	p.ensureExecutionTargetMapsLocked()
	key := targetEpochKey(sessionID)
	p.targetEpochs[key]++
}

func (p *Page) bumpTargetContextEpochLocked(sessionID string, contextID int) {
	p.ensureExecutionTargetMapsLocked()
	key := targetContextEpochKey(sessionID, contextID)
	p.targetEpochs[key]++
}

func (p *Page) executionTargetByRuntime(runtimeID string) (ExecutionTarget, bool) {
	runtimeID = strings.TrimSpace(runtimeID)
	if p == nil || runtimeID == "" {
		return ExecutionTarget{}, false
	}
	p.contextMu.RLock()
	defer p.contextMu.RUnlock()
	target, ok := p.executionTargetsByRuntime[runtimeID]
	return target, ok
}

func (p *Page) executionContextTarget(sessionID string, contextID int) (ExecutionTarget, bool) {
	if p == nil || contextID == 0 {
		return ExecutionTarget{}, false
	}
	p.contextMu.RLock()
	defer p.contextMu.RUnlock()
	target, ok := p.executionTargetsByContext[targetContextKey{SessionID: strings.TrimSpace(sessionID), ContextID: contextID}]
	return target, ok
}

func (p *Page) executionContextInfo(contextID int) (executionContextInfo, bool) {
	if p == nil || contextID == 0 {
		return executionContextInfo{}, false
	}
	p.contextMu.RLock()
	defer p.contextMu.RUnlock()
	info, ok := p.executionContexts[targetContextKey{ContextID: contextID}]
	return info, ok
}

func (p *Page) removeExecutionTarget(sessionID string, contextID int) {
	if p == nil || contextID == 0 {
		return
	}
	p.contextMu.Lock()
	defer p.contextMu.Unlock()
	p.removeExecutionTargetLocked(strings.TrimSpace(sessionID), contextID)
}

func (p *Page) removeExecutionTargetLocked(sessionID string, contextID int) {
	if p.executionTargetsByContext == nil {
		return
	}
	key := targetContextKey{SessionID: strings.TrimSpace(sessionID), ContextID: contextID}
	if target, ok := p.executionTargetsByContext[key]; ok {
		p.bumpTargetContextEpochLocked(target.SessionID, target.ContextID)
		if target.RuntimeID != "" {
			delete(p.executionTargetsByRuntime, target.RuntimeID)
		}
		delete(p.executionTargetsByContext, key)
	}
}

func (p *Page) clearExecutionTargets(sessionID string) {
	if p == nil {
		return
	}
	p.contextMu.Lock()
	defer p.contextMu.Unlock()
	p.clearExecutionTargetsLocked(strings.TrimSpace(sessionID))
}

func (p *Page) clearExecutionTargetsLocked(sessionID string) {
	if p.executionTargetsByContext == nil {
		return
	}
	for key, target := range p.executionTargetsByContext {
		if key.SessionID != sessionID {
			continue
		}
		if target.RuntimeID != "" {
			delete(p.executionTargetsByRuntime, target.RuntimeID)
		}
		p.bumpTargetContextEpochLocked(target.SessionID, target.ContextID)
		delete(p.executionTargetsByContext, key)
	}
}

func (p *Page) executionTargetCandidates() []ExecutionTarget {
	if p == nil {
		return nil
	}
	p.contextMu.RLock()
	defer p.contextMu.RUnlock()
	candidates := make([]ExecutionTarget, 0, len(p.executionTargetsByContext))
	for _, target := range p.executionTargetsByContext {
		if target.ContextID != 0 {
			candidates = append(candidates, target)
		}
	}
	return candidates
}

func (p *Page) executionTargetCounts() (int, int) {
	if p == nil {
		return 0, 0
	}
	p.contextMu.RLock()
	defer p.contextMu.RUnlock()
	var top, session int
	for key := range p.executionTargetsByContext {
		if key.SessionID == "" {
			top++
		} else {
			session++
		}
	}
	return top, session
}

func (p *Page) probeExecutionTarget(ctx context.Context, target ExecutionTarget) (ExecutionTarget, error) {
	epoch := p.currentTargetEpoch(target)
	result, err := p.RuntimeEvaluateOnTarget(ctx, target, runtimeInfoExpression, true)
	if err != nil {
		return target, err
	}
	if runtimeErr := runtimeResultError(result); runtimeErr != nil {
		return target, runtimeErr
	}
	value, ok := SafeGet[map[string]any](result, "result", "value")
	if !ok {
		return target, fmt.Errorf("runtime info result is empty")
	}
	target.RuntimeID = readString(value["runtimeId"])
	if target.RuntimeID == "" {
		return target, fmt.Errorf("runtime info missing runtimeId")
	}
	if !p.storeExecutionTargetAtEpoch(target, epoch) {
		return target, fmt.Errorf("runtime target changed while probing session=%s context=%d", target.SessionID, target.ContextID)
	}
	return target, nil
}

func (p *Page) RuntimeEvaluateOnTarget(ctx context.Context, target ExecutionTarget, expression string, returnByValue bool) (map[string]any, error) {
	if err := p.checkConnContext(ctx); err != nil {
		return nil, err
	}
	if target.ContextID == 0 && target.FrameID != "" && target.FrameID != p.mainFrame() {
		for _, session := range p.manager.targetSessionsSnapshot() {
			if session.Type == "iframe" && session.PageID == p.ID && session.TargetID == target.FrameID {
				target.SessionID = session.SessionID
				return p.RuntimeEvaluateOnTarget(ctx, ExecutionTarget{SessionID: target.SessionID}, expression, returnByValue)
			}
		}
		owner, err := p.sendTargetMessage(ctx, target, "DOM.getFrameOwner", map[string]any{"frameId": target.FrameID})
		if err != nil {
			return nil, err
		}
		described, err := p.sendTargetMessage(ctx, target, "DOM.describeNode", map[string]any{"backendNodeId": owner["backendNodeId"], "depth": 1})
		if err != nil {
			return nil, err
		}
		backend, ok := SafeGet[float64](described, "node", "contentDocument", "backendNodeId")
		if !ok {
			return nil, fmt.Errorf("main document unavailable for frame %s", target.FrameID)
		}
		resolved, err := p.sendTargetMessage(ctx, target, "DOM.resolveNode", map[string]any{"backendNodeId": backend})
		if err != nil {
			return nil, err
		}
		objectID, _ := SafeGet[string](resolved, "object", "objectId")
		if objectID == "" {
			return nil, fmt.Errorf("missing document handle for frame %s", target.FrameID)
		}
		defer p.sendTargetPacket(ctx, target, "Runtime.releaseObject", map[string]any{"objectId": objectID})
		return p.sendTargetMessage(ctx, target, "Runtime.callFunctionOn", map[string]any{"objectId": objectID, "functionDeclaration": "function(){return (" + expression + "\n)}", "awaitPromise": true, "returnByValue": returnByValue})
	}
	params := map[string]any{
		"expression":      expression,
		"awaitPromise":    true,
		"generatePreview": false,
	}
	if target.ContextID != 0 {
		params["contextId"] = target.ContextID
	}
	if returnByValue {
		params["returnByValue"] = true
	} else {
		params["serializationOptions"] = map[string]any{
			"serialization": "idOnly",
		}
	}
	return p.sendTargetMessage(ctx, target, "Runtime.evaluate", params)
}

func (p *Page) sendTargetPacket(ctx context.Context, target ExecutionTarget, method string, params map[string]any) error {
	_, err := p.sendTargetMessage(ctx, target, method, params)
	return err
}

func (p *Page) sendTargetMessage(ctx context.Context, target ExecutionTarget, method string, params map[string]any) (map[string]any, error) {
	if err := p.checkConnContext(ctx); err != nil {
		return nil, err
	}
	if target.SessionID == "" {
		res, err := p.CdpConn.SendMessageContext(ctx, method, params)
		return res, BrowserErrorFromCDP(method, topPageExecutionTarget(), err)
	}
	if p.manager == nil {
		return nil, fmt.Errorf("browser manager is nil")
	}
	conn, err := p.manager.activeConn()
	if err != nil {
		return nil, err
	}
	res, err := conn.SendSessionMessage(ctx, target.SessionID, method, params)
	return res, BrowserErrorFromCDP(method, target, err)
}

func (p *Page) executionTargetForRuntime(runtimeID string) (ExecutionTarget, error) {
	return p.executionTargetForRuntimeContext(context.Background(), runtimeID)
}

func (p *Page) executionTargetForRuntimeContext(ctx context.Context, runtimeID string) (ExecutionTarget, error) {
	runtimeID = strings.TrimSpace(runtimeID)
	if runtimeID == "" {
		return ExecutionTarget{}, fmt.Errorf("runtime id is required")
	}
	if ctx == nil {
		return ExecutionTarget{}, fmt.Errorf("runtime lookup context is nil")
	}
	if err := ctx.Err(); err != nil {
		return ExecutionTarget{}, err
	}
	deadline := time.Now().Add(executionTargetLookupTimeout)
	for {
		if target, ok := p.executionTargetByRuntime(runtimeID); ok {
			return target, nil
		}
		candidates := p.executionTargetCandidates()
		if len(candidates) > 0 {
			for _, candidate := range candidates {
				if candidate.RuntimeID == runtimeID {
					return candidate, nil
				}
				probed, err := p.probeExecutionTarget(ctx, candidate)
				if err != nil {
					if isMissingExecutionContextError(err) {
						p.removeExecutionTarget(candidate.SessionID, candidate.ContextID)
						continue
					}
					continue
				}
				if probed.RuntimeID == runtimeID {
					return probed, nil
				}
			}
		}
		if time.Now().After(deadline) {
			top, session := p.executionTargetCounts()
			if top+session == 0 {
				return ExecutionTarget{}, fmt.Errorf("未找到可用 execution context")
			}
			return ExecutionTarget{}, fmt.Errorf("未找到 runtime 对应的 execution context: %s (top_contexts=%d session_contexts=%d)", runtimeID, top, session)
		}
		timer := time.NewTimer(executionTargetLookupInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ExecutionTarget{}, ctx.Err()
		case <-timer.C:
		}
	}
}
