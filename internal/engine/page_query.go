package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"gopkg.d7z.net/cdp/internal/webassets"
)

func (p *Page) StartSelectorQuery(ctx context.Context, parentID int, plan SelectorPlan, options SelectorQueryOptions) (SelectorQuerySession, error) {
	p.lock.RLock()
	defer p.lock.RUnlock()
	if err := p.checkConnContext(ctx); err != nil {
		return SelectorQuerySession{}, err
	}

	if err := p.RefreshShadowRoots(ctx); err != nil {
		return SelectorQuerySession{}, err
	}
	target, err := p.mainFrameRuntimeTarget(ctx, NamespaceIsolatedCore)
	if err != nil {
		return SelectorQuerySession{}, err
	}
	var documentQueryExpression string
	evalOnDocument := func(target ExecutionTarget) (map[string]any, error) {
		if documentQueryExpression == "" {
			planJSON, planErr := json.Marshal(plan)
			optionsJSON, optionsErr := json.Marshal(options)
			if planErr != nil {
				return nil, planErr
			}
			if optionsErr != nil {
				return nil, optionsErr
			}
			documentQueryExpression = `(async () => {
				const plan = ` + string(planJSON) + `;
				const options = ` + string(optionsJSON) + `;
				const session = await ` + webassets.RuntimeOptionalMethodCall(webassets.RuntimeFFI, "startSelectorQuery", webassets.JSRaw("document"), webassets.JSRaw("plan"), webassets.JSRaw("options")) + `;
				return session || "";
			})()`
		}
		return p.RuntimeEvaluateOnTarget(ctx, target, documentQueryExpression, true)
	}
	var callRes map[string]any
	if parentID > 0 {
		callRes, err = p.callTargetNodeFunction(ctx, target, parentID, `async function(plan, options) {
			const session = await `+webassets.RuntimeOptionalMethodCall(webassets.RuntimeFFI, "startSelectorQuery", webassets.JSRaw("this"), webassets.JSRaw("plan"), webassets.JSRaw("options"))+`;
			return session || "";
		}`,
			map[string]any{"value": plan},
			map[string]any{"value": options},
		)
	} else {
		callRes, err = evalOnDocument(target)
	}
	if err != nil && isMissingNodeErr(err) {
		callRes, err = evalOnDocument(target)
	}
	if err != nil && isMissingExecutionContextError(err) {
		if target.ContextID != 0 {
			p.removeExecutionContextForSession(target.SessionID, target.ContextID)
		}
		for attempt := 0; attempt < 3 && err != nil && isMissingExecutionContextError(err); attempt++ {
			refreshed, refreshErr := p.mainFrameRuntimeTarget(ctx, NamespaceIsolatedCore)
			if refreshErr != nil {
				break
			}
			target = refreshed
			callRes, err = evalOnDocument(target)
			if err != nil && isMissingExecutionContextError(err) && target.ContextID != 0 {
				p.removeExecutionContextForSession(target.SessionID, target.ContextID)
			}
		}
	}
	if err != nil {
		return SelectorQuerySession{}, err
	}
	session, err := requireSelectorQuerySession(callRes)
	if err != nil {
		return SelectorQuerySession{}, err
	}
	target.RuntimeID = firstNonEmpty(target.RuntimeID, session.RuntimeID)
	session.Target = target
	if target.ContextID != 0 {
		p.storeExecutionTarget(target)
	}
	return session, nil
}

func (p *Page) StartSelectorQueryFromRef(ctx context.Context, ref SelectorTargetRef, plan SelectorPlan, options SelectorQueryOptions) (SelectorQuerySession, error) {
	p.lock.RLock()
	defer p.lock.RUnlock()
	if err := p.checkConnContext(ctx); err != nil {
		return SelectorQuerySession{}, err
	}

	if err := p.RefreshShadowRoots(ctx); err != nil {
		return SelectorQuerySession{}, err
	}

	target, backendNodeID, err := p.selectorTargetTargetAndBackendNodeID(ctx, ref)
	if err != nil {
		return SelectorQuerySession{}, err
	}
	callRes, err := p.callTargetBackendNodeFunctionContext(ctx, target, backendNodeID, true, `async function(plan, options) {
		const session = await `+webassets.RuntimeOptionalMethodCall(webassets.RuntimeFFI, "startSelectorQuery", webassets.JSRaw("this"), webassets.JSRaw("plan"), webassets.JSRaw("options"))+`;
		return session || "";
	}`,
		map[string]any{"value": plan},
		map[string]any{"value": options},
	)
	if err != nil {
		return SelectorQuerySession{}, err
	}
	session, err := requireSelectorQuerySession(callRes)
	if err != nil {
		return SelectorQuerySession{}, err
	}
	target.RuntimeID = firstNonEmpty(target.RuntimeID, session.RuntimeID)
	session.Target = target
	session.RootBackendNodeID = backendNodeID
	if target.ContextID != 0 {
		p.storeExecutionTarget(target)
	}
	return session, nil
}

func parseSelectorQuerySessionValue(callRes map[string]any) SelectorQuerySession {
	value, ok := SafeGet[map[string]any](callRes, "result", "value")
	if !ok {
		return SelectorQuerySession{}
	}
	token, _ := value["token"].(string)
	if token == "" {
		return SelectorQuerySession{}
	}
	runtimeID := readString(value["runtimeId"])
	if runtimeID == "" {
		return SelectorQuerySession{}
	}
	session := SelectorQuerySession{
		Token:     token,
		RuntimeID: runtimeID,
		Truncated: asBool(value["truncated"]),
	}
	session.IDs.Count = readSelectorBucketCount(value, "ids")
	session.Visible.Count = readSelectorBucketCount(value, "visible")
	session.Actionable.Count = readSelectorBucketCount(value, "actionable")
	session.Failure = readSelectorFailure(value["failure"])
	return session
}

func requireSelectorQuerySession(callRes map[string]any) (SelectorQuerySession, error) {
	value, ok := SafeGet[map[string]any](callRes, "result", "value")
	if !ok {
		return SelectorQuerySession{}, errors.New("selector query session missing token")
	}
	if readString(value["token"]) == "" {
		return SelectorQuerySession{}, errors.New("selector query session missing token")
	}
	if readString(value["runtimeId"]) == "" {
		return SelectorQuerySession{}, errors.New("selector query session missing runtimeId")
	}
	return parseSelectorQuerySessionValue(callRes), nil
}

func readSelectorFailure(value any) *SelectorQueryFailure {
	raw, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	failure := &SelectorQueryFailure{
		Kind:         readString(raw["kind"]),
		Summary:      readString(raw["summary"]),
		Detail:       readString(raw["detail"]),
		FrameSummary: readString(raw["frameSummary"]),
		Error:        readBrowserErrorNode(raw["error"]),
	}
	if failure.Kind == "" && failure.Summary == "" && failure.Detail == "" && failure.FrameSummary == "" && failure.Error == nil {
		return nil
	}
	if failure.Error != nil && failure.FrameSummary != "" && failure.Error.FrameSummary == "" {
		failure.Error.FrameSummary = failure.FrameSummary
	}
	return failure
}

func readSelectorBucketCount(value map[string]any, key string) int {
	bucket, ok := value[key].(map[string]any)
	if !ok {
		return 0
	}
	return asInt(bucket["count"])
}

func asInt(value any) int {
	switch value := value.(type) {
	case int:
		return value
	case int32:
		return int(value)
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}

func asBool(value any) bool {
	boolValue, _ := value.(bool)
	return boolValue
}

func readString(value any) string {
	text, _ := value.(string)
	return text
}

func (p *Page) DisposeSelectorQueryInRuntime(ctx context.Context, runtimeID string, token string) {
	if token == "" {
		return
	}
	_, err := p.evaluateInRuntimeContext(ctx, runtimeID, `(() => {
		`+webassets.RuntimeOptionalMethodCall(webassets.RuntimeFFI, "disposeSelectorQuery", webassets.JSLit(token))+`;
		return true;
	})()`, true)
	if err != nil {
		slog.Debug("清理 runtime selector query session 失败", "runtime_id", runtimeID, "token", token, "error", err)
	}
}

func (p *Page) DisposeSelectorQuerySession(ctx context.Context, session SelectorQuerySession) {
	if session.Token == "" {
		return
	}
	if session.RootBackendNodeID > 0 {
		_, err := p.callTargetBackendNodeFunctionContext(ctx, session.Target, session.RootBackendNodeID, true, `function(token) {
			`+webassets.RuntimeOptionalMethodCall(webassets.RuntimeFFI, "disposeSelectorQuery", webassets.JSRaw("token"))+`;
			return true;
		}`, map[string]any{"value": session.Token})
		if err != nil {
			slog.Debug("清理 target selector query session 失败", "runtime_id", session.RuntimeID, "token", session.Token, "error", err)
		}
		return
	}
	if session.Target.ContextID != 0 {
		_, err := p.RuntimeEvaluateOnTarget(ctx, session.Target, `(() => {
			`+webassets.RuntimeOptionalMethodCall(webassets.RuntimeFFI, "disposeSelectorQuery", webassets.JSLit(session.Token))+`;
			return true;
		})()`, true)
		if err != nil {
			slog.Debug("清理 target runtime selector query session 失败", "runtime_id", session.RuntimeID, "token", session.Token, "error", err)
		}
		return
	}
	p.DisposeSelectorQueryInRuntime(ctx, session.RuntimeID, session.Token)
}

type selectorQueryNodeInfo struct {
	Kind      string
	RuntimeID string
	Token     string
	Bucket    SelectorQueryBucket
	Index     int
}

func (p *Page) selectorQueryNodeInfo(ctx context.Context, session SelectorQuerySession, bucket SelectorQueryBucket, index int) (selectorQueryNodeInfo, bool, error) {
	if session.RootBackendNodeID > 0 {
		res, err := p.callTargetBackendNodeFunctionContext(ctx, session.Target, session.RootBackendNodeID, true, `async function(token, bucket, index) {
			const info = await `+webassets.RuntimeOptionalMethodCall(webassets.RuntimeFFI, "selectorQueryNodeInfo", webassets.JSRaw("token"), webassets.JSRaw("bucket"), webassets.JSRaw("index"))+`;
			return info ?? null;
		}`, map[string]any{"value": session.Token}, map[string]any{"value": string(bucket)}, map[string]any{"value": index})
		if err != nil {
			return selectorQueryNodeInfo{}, false, err
		}
		return parseSelectorQueryNodeInfoResult(res)
	}
	var (
		res map[string]any
		err error
	)
	expression := `(async () => {
		const info = await ` + webassets.RuntimeOptionalMethodCall(webassets.RuntimeFFI, "selectorQueryNodeInfo", webassets.JSLit(session.Token), webassets.JSLit(string(bucket)), webassets.JSLit(index)) + `;
		return info ?? null;
	})()`
	if session.Target.ContextID != 0 {
		res, err = p.RuntimeEvaluateOnTarget(ctx, session.Target, expression, true)
	} else {
		res, err = p.evaluateInRuntimeContext(ctx, session.RuntimeID, expression, true)
	}
	if err != nil {
		return selectorQueryNodeInfo{}, false, err
	}
	return parseSelectorQueryNodeInfoResult(res)
}

func parseSelectorQueryNodeInfoResult(res map[string]any) (selectorQueryNodeInfo, bool, error) {
	value, ok := SafeGet[map[string]any](res, "result", "value")
	if !ok || len(value) == 0 {
		return selectorQueryNodeInfo{}, false, nil
	}
	info := selectorQueryNodeInfo{
		Kind:      readString(value["kind"]),
		RuntimeID: readString(value["runtimeId"]),
		Token:     readString(value["token"]),
		Bucket:    SelectorQueryBucket(readString(value["bucket"])),
		Index:     asInt(value["index"]),
	}
	return info, info.RuntimeID != "", nil
}

func (p *Page) evaluateInRuntimeContext(ctx context.Context, runtimeID string, expression string, returnByValue bool) (map[string]any, error) {
	result, _, err := p.evaluateInRuntimeContextTarget(ctx, runtimeID, expression, returnByValue)
	return result, err
}

func (p *Page) evaluateInRuntimeContextTarget(ctx context.Context, runtimeID string, expression string, returnByValue bool) (map[string]any, ExecutionTarget, error) {
	target, err := p.executionTargetForRuntimeContext(ctx, runtimeID)
	if err != nil {
		return nil, ExecutionTarget{}, err
	}
	result, err := p.RuntimeEvaluateOnTarget(ctx, target, expression, returnByValue)
	return result, target, err
}

func (p *Page) selectorQueryBackendNodeID(ctx context.Context, session SelectorQuerySession, bucket SelectorQueryBucket, index int) (ExecutionTarget, int, bool, error) {
	info, ok, err := p.selectorQueryNodeInfo(ctx, session, bucket, index)
	if err != nil || !ok {
		return ExecutionTarget{}, 0, ok, err
	}
	for depth := 0; info.Kind == "remote" && depth < 8; depth++ {
		target, err := p.executionTargetForRuntimeContext(ctx, info.RuntimeID)
		if err != nil {
			return ExecutionTarget{}, 0, false, err
		}
		res, err := p.RuntimeEvaluateOnTarget(ctx, target, `(async () => {
			const info = await `+webassets.RuntimeOptionalMethodCall(webassets.RuntimeFFI, "selectorQueryNodeInfo", webassets.JSLit(info.Token), webassets.JSLit(string(info.Bucket)), webassets.JSLit(info.Index))+`;
			return info ?? null;
		})()`, true)
		if err != nil {
			return ExecutionTarget{}, 0, false, err
		}
		var nextOK bool
		info, nextOK, err = parseSelectorQueryNodeInfoResult(res)
		if err != nil || !nextOK {
			return ExecutionTarget{}, 0, nextOK, err
		}
	}
	if info.Kind == "remote" {
		return ExecutionTarget{}, 0, false, errors.New("selector remote iframe chain is too deep")
	}
	targetRuntimeID := info.RuntimeID
	targetToken := info.Token
	targetBucket := info.Bucket
	targetIndex := info.Index
	var (
		res    map[string]any
		target ExecutionTarget
	)
	if targetRuntimeID == session.RuntimeID && session.Target.ContextID != 0 {
		target = session.Target
		if session.RootBackendNodeID > 0 {
			res, err = p.callTargetBackendNodeFunctionContext(ctx, target, session.RootBackendNodeID, false, `function(token, bucket, index) {
				return `+webassets.RuntimeOptionalMethodCall(webassets.RuntimeFFI, "selectorQueryNode", webassets.JSRaw("token"), webassets.JSRaw("bucket"), webassets.JSRaw("index"))+` ?? null;
			}`, map[string]any{"value": targetToken}, map[string]any{"value": string(targetBucket)}, map[string]any{"value": targetIndex})
			if err != nil {
				return ExecutionTarget{}, 0, false, err
			}
		} else {
			res, err = p.RuntimeEvaluateOnTarget(ctx, target, `(() => {
				return `+webassets.RuntimeOptionalMethodCall(webassets.RuntimeFFI, "selectorQueryNode", webassets.JSLit(targetToken), webassets.JSLit(string(targetBucket)), webassets.JSLit(targetIndex))+` ?? null;
			})()`, false)
			if err != nil {
				return ExecutionTarget{}, 0, false, err
			}
		}
	} else {
		res, target, err = p.evaluateInRuntimeContextTarget(ctx, targetRuntimeID, `(() => {
			return `+webassets.RuntimeOptionalMethodCall(webassets.RuntimeFFI, "selectorQueryNode", webassets.JSLit(targetToken), webassets.JSLit(string(targetBucket)), webassets.JSLit(targetIndex))+` ?? null;
		})()`, false)
		if err != nil {
			return ExecutionTarget{}, 0, false, err
		}
	}
	resultObjectID, ok := SafeGet[string](res, "result", "objectId")
	if !ok {
		return ExecutionTarget{}, 0, false, nil
	}
	desc, err := p.DescribeNodeByObjectIDInTargetContext(ctx, target, resultObjectID)
	defer logReleaseObjectError("释放 selector query 节点对象失败", resultObjectID, p.ReleaseObjectInTargetContext(ctx, target, resultObjectID))
	if err != nil {
		return ExecutionTarget{}, 0, false, err
	}
	p.rememberNodeIdentityInTarget(target, desc)
	_, backendNodeID, ok := parseNodeIdentity(desc)
	if !ok || backendNodeID <= 0 {
		return ExecutionTarget{}, 0, false, errors.New("获取 backendNodeId 失败")
	}
	return target, backendNodeID, true, nil
}

func (p *Page) SelectorQueryRefs(ctx context.Context, parentID int, session SelectorQuerySession, bucket SelectorQueryBucket, limit int) ([]SelectorTargetRef, error) {
	var total int
	switch bucket {
	case SelectorQueryBucketVisible:
		total = session.Visible.Count
	case SelectorQueryBucketActionable:
		total = session.Actionable.Count
	default:
		total = session.IDs.Count
	}
	if total == 0 || session.Token == "" {
		return []SelectorTargetRef{}, nil
	}
	if limit > 0 && total > limit {
		total = limit
	}
	result := make([]SelectorTargetRef, 0, total)
	for i := 0; i < total; i++ {
		target, backendNodeID, ok, err := p.selectorQueryBackendNodeID(ctx, session, bucket, i)
		if err != nil {
			return nil, err
		}
		if !ok || backendNodeID <= 0 {
			continue
		}
		result = append(result, SelectorTargetRef{
			RootID:        parentID,
			Token:         session.Token,
			RuntimeID:     session.RuntimeID,
			Bucket:        bucket,
			Index:         i,
			BackendNodeID: backendNodeID,
			Target:        target,
			Epoch:         p.currentTargetEpoch(target),
		})
	}
	if total > 0 && len(result) == 0 {
		return nil, errors.New("selector query 返回了候选，但无法解析目标节点")
	}
	return result, nil
}

func (p *Page) selectorTargetBackendNodeID(ctx context.Context, ref SelectorTargetRef) (int, error) {
	if ref.BackendNodeID <= 0 {
		return 0, errors.New("selector target 缺少 backendNodeId")
	}
	return ref.BackendNodeID, nil
}

func (p *Page) selectorTargetExecutionTarget(ctx context.Context, ref SelectorTargetRef) (ExecutionTarget, error) {
	target := ref.Target
	if target.RuntimeID == "" {
		target.RuntimeID = ref.RuntimeID
	}
	if target.PageID == "" {
		target.PageID = p.ID
	}
	if target.ContextID == 0 && target.RuntimeID != "" {
		resolved, err := p.executionTargetForRuntimeContext(ctx, target.RuntimeID)
		if err != nil {
			return ExecutionTarget{}, err
		}
		target = resolved
	}
	return target, nil
}

func (p *Page) selectorTargetTargetAndBackendNodeID(ctx context.Context, ref SelectorTargetRef) (ExecutionTarget, int, error) {
	backendNodeID, err := p.selectorTargetBackendNodeID(ctx, ref)
	if err != nil {
		return ExecutionTarget{}, 0, err
	}
	target, err := p.selectorTargetExecutionTarget(ctx, ref)
	if err != nil {
		return ExecutionTarget{}, 0, err
	}
	if ref.Epoch != p.currentTargetEpoch(target) {
		return ExecutionTarget{}, 0, NewBrowserError("target.resolve", "stale_target", "target execution context has changed", nil)
	}
	return target, backendNodeID, nil
}

func (p *Page) selectorTargetBool(ctx context.Context, ref SelectorTargetRef, code string, args ...any) (bool, error) {
	target, backendNodeID, err := p.selectorTargetTargetAndBackendNodeID(ctx, ref)
	if err != nil {
		return false, err
	}
	res, err := p.callTargetBackendNodeFunctionContext(ctx, target, backendNodeID, true, code, args...)
	if err != nil {
		return false, err
	}
	if runtimeErr := runtimeResultError(res); runtimeErr != nil {
		return false, runtimeErr
	}
	value, _ := SafeGet[bool](res, "result", "value")
	return value, nil
}

func (p *Page) selectorTargetVisibleNow(ctx context.Context, ref SelectorTargetRef) (bool, error) {
	return p.selectorTargetBool(ctx, ref, elementPureVisibleScript)
}

func (p *Page) selectorTargetString(ctx context.Context, ref SelectorTargetRef, code string, args ...any) (string, error) {
	target, backendNodeID, err := p.selectorTargetTargetAndBackendNodeID(ctx, ref)
	if err != nil {
		return "", err
	}
	res, err := p.callTargetBackendNodeFunctionContext(ctx, target, backendNodeID, true, code, args...)
	if err != nil {
		return "", err
	}
	if runtimeErr := runtimeResultError(res); runtimeErr != nil {
		return "", runtimeErr
	}
	value, _ := SafeGet[string](res, "result", "value")
	return value, nil
}

func (p *Page) SelectorTargetVisible(ctx context.Context, ref SelectorTargetRef) (bool, error) {
	return p.selectorTargetVisibleNow(ctx, ref)
}

func (p *Page) SelectorTargetInViewport(ctx context.Context, ref SelectorTargetRef) (bool, error) {
	return p.selectorTargetBool(ctx, ref, elementInViewportScript)
}

func (p *Page) SelectorTargetEnsureVisible(ctx context.Context, ref SelectorTargetRef) (bool, error) {
	visible, err := p.SelectorTargetVisible(ctx, ref)
	if err != nil || !visible {
		return visible, err
	}
	inViewport, err := p.SelectorTargetInViewport(ctx, ref)
	if err != nil || inViewport {
		return inViewport, err
	}
	if err := p.scrollIntoViewIfNeededByTargetRef(ctx, ref); err != nil {
		return false, nil
	}
	return p.SelectorTargetInViewport(ctx, ref)
}

func (p *Page) SelectorTargetPrepareInteraction(ctx context.Context, ref SelectorTargetRef) error {
	ok, err := p.SelectorTargetEnsureVisible(ctx, ref)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("元素不可见或无法滚动到视口内")
	}
	return nil
}

func (p *Page) SelectorTargetPrepareInput(ctx context.Context, ref SelectorTargetRef) error {
	diagnostic, err := prepareActionabilityDiagnostic(func(scrollMode actionTargetScrollMode) (ActionabilityDiagnostic, error) {
		return p.selectorTargetActionabilityDiagnostic(ctx, ref, scrollMode, "input")
	})
	if err != nil {
		return err
	}
	if !diagnostic.Actionable {
		return BrowserErrorFromActionability("selector.actionability", diagnostic)
	}
	return nil
}

func (p *Page) SelectorTargetEnabled(ctx context.Context, ref SelectorTargetRef) (bool, error) {
	return p.selectorTargetBool(ctx, ref, `function() {
		return `+webassets.RuntimeOptionalMethodCall(webassets.RuntimeFFI, "locator.isEnabled", webassets.JSRaw("this"))+` ?? false;
	}`)
}

func (p *Page) SelectorTargetEnsureEnabled(ctx context.Context, ref SelectorTargetRef) error {
	diagnostic, diagErr := p.selectorTargetActionabilityDiagnostic(ctx, ref, actionTargetScrollNone, "input")
	if diagErr == nil && diagnostic.Kind == "disabled" {
		return BrowserErrorFromActionability("selector.actionability", diagnostic)
	}
	return diagErr
}

func (p *Page) SelectorTargetChecked(ctx context.Context, ref SelectorTargetRef) (bool, error) {
	return p.selectorTargetBool(ctx, ref, checkedStateScript())
}

func (p *Page) SelectorTargetEditable(ctx context.Context, ref SelectorTargetRef) (bool, error) {
	return p.selectorTargetBool(ctx, ref, `function() {
		if (!this || !(this instanceof Element)) {
			return false;
		}
		if (this instanceof HTMLElement && this.isContentEditable) {
			return true;
		}
		if (this instanceof HTMLInputElement || this instanceof HTMLTextAreaElement || this instanceof HTMLSelectElement) {
			return !this.disabled && !('readOnly' in this && !!this.readOnly);
		}
		return false;
	}`)
}

func (p *Page) SelectorTargetEmpty(ctx context.Context, ref SelectorTargetRef) (bool, error) {
	return p.selectorTargetBool(ctx, ref, `function() {
		if (!this) {
			return true;
		}
		if (this instanceof HTMLInputElement || this instanceof HTMLTextAreaElement) {
			return (this.value || '').trim() === '';
		}
		return (this.textContent || '').trim() === '';
	}`)
}

func (p *Page) SelectorTargetText(ctx context.Context, ref SelectorTargetRef) (string, error) {
	return p.selectorTargetString(ctx, ref, `function() {
		return this?.textContent || '';
	}`)
}

func (p *Page) SelectorTargetHTML(ctx context.Context, ref SelectorTargetRef) (string, error) {
	return p.selectorTargetString(ctx, ref, `function() {
		return this?.innerHTML || '';
	}`)
}

func (p *Page) SelectorTargetValue(ctx context.Context, ref SelectorTargetRef) (string, error) {
	return p.selectorTargetString(ctx, ref, `function() {
		if (this instanceof HTMLInputElement || this instanceof HTMLTextAreaElement || this instanceof HTMLSelectElement) {
			return this.value || '';
		}
		return '';
	}`)
}

func (p *Page) SelectorTargetHasAttr(ctx context.Context, ref SelectorTargetRef, name string) (bool, error) {
	return p.selectorTargetBool(ctx, ref, `function(name) {
		return !!this?.hasAttribute?.(name);
	}`, map[string]any{"value": name})
}

func (p *Page) SelectorTargetStyle(ctx context.Context, ref SelectorTargetRef, name string) (string, error) {
	return p.selectorTargetString(ctx, ref, `function(name) {
		if (!this || !(this instanceof Element)) {
			return '';
		}
		const css = this.ownerDocument?.defaultView?.getComputedStyle?.(this);
		return css?.getPropertyValue(name) || '';
	}`, map[string]any{"value": name})
}

func (p *Page) SelectorTargetHasClass(ctx context.Context, ref SelectorTargetRef, className string) (bool, error) {
	return p.selectorTargetBool(ctx, ref, `function(className) {
		return !!this?.classList?.contains(className);
	}`, map[string]any{"value": className})
}

type ActionabilityDiagnostic struct {
	Actionable    bool
	Kind          string
	Summary       string
	Detail        string
	Element       string
	Culprit       string
	LocalRect     Rect
	TopRect       Rect
	CenterX       float64
	CenterY       float64
	HasLocalRect  bool
	HasTopRect    bool
	HasCenter     bool
	Retriable     bool
	RetryAction   string
	RetryPointX   float64
	RetryPointY   float64
	HasRetryPoint bool
	RetryDelayMs  int
	Stability     *StabilityDiagnostic
	FrameChain    []FrameSegmentDiagnostic
}

type StabilityDiagnostic struct {
	Kind      string
	ElapsedMs int
	MaxDelta  Rect
	Samples   []Rect
}

type FrameSegmentDiagnostic struct {
	FrameElement string
	Summary      string
	Detail       string
}

type actionabilityDiagnosticValue struct {
	Actionable   bool                         `json:"actionable"`
	Kind         string                       `json:"kind"`
	Summary      string                       `json:"summary"`
	Detail       string                       `json:"detail"`
	Element      string                       `json:"element"`
	Culprit      string                       `json:"culprit"`
	LocalRect    *actionabilityRectValue      `json:"localRect"`
	TopRect      *actionabilityRectValue      `json:"topRect"`
	CenterX      *float64                     `json:"centerX"`
	CenterY      *float64                     `json:"centerY"`
	Retriable    bool                         `json:"retriable"`
	RetryAction  string                       `json:"retryAction"`
	RetryPoint   *actionabilityPointValue     `json:"retryPoint"`
	RetryDelayMs int                          `json:"retryDelayMs"`
	Stability    *actionabilityStabilityValue `json:"stability"`
	FrameChain   []FrameSegmentDiagnostic     `json:"frameChain"`
}

type actionabilityPointValue struct {
	X *float64 `json:"x"`
	Y *float64 `json:"y"`
}

type actionabilityRectValue struct {
	X      *float64 `json:"x"`
	Y      *float64 `json:"y"`
	Width  *float64 `json:"width"`
	Height *float64 `json:"height"`
}

func (value *actionabilityRectValue) rect() (Rect, bool) {
	if value == nil || value.X == nil || value.Y == nil || value.Width == nil || value.Height == nil {
		return Rect{}, false
	}
	return Rect{X: *value.X, Y: *value.Y, Width: *value.Width, Height: *value.Height}, true
}

type actionabilityStabilityValue struct {
	Kind      string                    `json:"kind"`
	ElapsedMs int                       `json:"elapsedMs"`
	MaxDelta  *actionabilityRectValue   `json:"maxDelta"`
	Samples   []*actionabilityRectValue `json:"samples"`
}

func selectorTargetHighlightMarkerScript() string {
	return `async function(label, purpose) {
		const cdp = ` + webassets.RuntimeSlotExpr(webassets.RuntimeFFI) + `;
		if (!this || !(this instanceof Element) || !cdp?.canvas) {
			return false;
		}
		const options = purpose ? { purpose, allowOutsideViewport: purpose === 'input' } : undefined;
		const diagnostic = await cdp.actionabilityDiagnostic(this, options);
		if (!diagnostic?.actionable) {
			return false;
		}
		const rect = diagnostic?.topRect || diagnostic?.localRect || null;
		if (!rect || rect.width <= 0 || rect.height <= 0) {
			return false;
		}
		cdp.canvas.setMode('#ff0000', label);
		cdp.canvas.setHighlightTimeout(5000);
		cdp.canvas.draw(rect.x, rect.y, rect.width, rect.height, 0, { owner: 'action-preview', color: '#ff0000', timeoutMs: 5000 });
		return true;
	}`
}

type actionTargetScrollMode string

const (
	actionTargetScrollNone     actionTargetScrollMode = "none"
	actionTargetScrollIfNeeded actionTargetScrollMode = "if-needed"
	actionTargetScrollCenter   actionTargetScrollMode = "center"
)

func selectorTargetActionabilityScript() string {
	return `async function(scrollMode, mode, purpose) {
		const cdp = ` + webassets.RuntimeSlotExpr(webassets.RuntimeFFI) + `;
		if (!this) {
			return null;
		}
		const scrollIntoView = scrollMode !== 'none';
		const repositionObscured = scrollMode === 'center';
		if (scrollIntoView && this instanceof Element && this.scrollIntoView) {
			const rects = Array.from(this.getClientRects ? this.getClientRects() : []);
			const offsets = [[0.5, 0.5], [0.5, 0.25], [0.5, 0.75], [0.25, 0.5], [0.75, 0.5]];
			const hasViewportPoint = rects.some((rect) => rect.width > 0 && rect.height > 0 && offsets.some(([ox, oy]) => {
				const x = rect.left + rect.width * ox;
				const y = rect.top + rect.height * oy;
				return x >= 0 && x <= window.innerWidth && y >= 0 && y <= window.innerHeight;
			}));
			if (repositionObscured || !hasViewportPoint) {
				this.scrollIntoView({ behavior: 'instant', block: 'center', inline: 'center' });
			}
		}
		if (cdp) {
			return await cdp.actionabilityDiagnostic(this, { mode, purpose, allowOutsideViewport: purpose === 'input', scrollIntoView, repositionObscured });
		}
		if (!(this instanceof Element)) {
			return null;
		}
		const local = this.getBoundingClientRect();
		let x = local.x;
		let y = local.y;
		let win = this.ownerDocument && this.ownerDocument.defaultView;
		try {
			while (win && win.parent && win.parent !== win) {
				const frame = win.frameElement;
				if (!frame) break;
				const frameRect = frame.getBoundingClientRect();
				x += frameRect.x + (frame.clientLeft || 0);
				y += frameRect.y + (frame.clientTop || 0);
				win = win.parent;
			}
		} catch (_) {}
		const rect = {x, y, width: local.width, height: local.height};
		const centerX = rect.x + rect.width / 2;
		const centerY = rect.y + rect.height / 2;
		const viewportWidth = window.top ? window.top.innerWidth : window.innerWidth;
		const viewportHeight = window.top ? window.top.innerHeight : window.innerHeight;
		const inViewport = centerX >= 0 && centerY >= 0 && centerX <= viewportWidth && centerY <= viewportHeight;
		return {
			actionable: inViewport && rect.width > 0 && rect.height > 0,
			kind: inViewport ? 'ok' : 'outside_viewport',
			summary: inViewport ? 'Element is actionable' : 'Element center is outside viewport',
			detail: '',
			element: this.tagName ? this.tagName.toLowerCase() : 'element',
			localRect: {x: local.x, y: local.y, width: local.width, height: local.height},
			topRect: rect,
			centerX,
			centerY,
			frameChain: [],
		};
	}`
}

func selectorTargetRectScript() string {
	return `async function(scrollIntoView) {
		const cdp = ` + webassets.RuntimeSlotExpr(webassets.RuntimeFFI) + `;
		if (!this || !(this instanceof Element) || !cdp) {
			return null;
		}
		if (scrollIntoView && this.scrollIntoView) {
			this.scrollIntoView({ block: 'center', inline: 'center' });
		}
		const diagnostic = await cdp.actionabilityDiagnostic(this);
		const rect = diagnostic?.topRect || diagnostic?.localRect;
		if (!rect || rect.width <= 0 || rect.height <= 0) {
			return null;
		}
		return { x: rect.x, y: rect.y, width: rect.width, height: rect.height };
	}`
}

func (d ActionabilityDiagnostic) Error() string {
	if d.Actionable {
		return ""
	}
	parts := make([]string, 0, 5)
	if d.Summary != "" {
		parts = append(parts, d.Summary)
	}
	if d.Element != "" {
		parts = append(parts, "target="+d.Element)
	}
	if d.Detail != "" {
		parts = append(parts, "detail="+d.Detail)
	}
	if len(d.FrameChain) > 0 {
		segments := make([]string, 0, len(d.FrameChain))
		for i, segment := range d.FrameChain {
			part := segment.Summary
			if segment.FrameElement != "" {
				part = segment.FrameElement
				if segment.Detail != "" {
					part += ": " + segment.Detail
				}
			} else if segment.Detail != "" {
				part += ": " + segment.Detail
			}
			segments = append(segments, fmt.Sprintf("[%d] %s", i+1, part))
		}
		parts = append(parts, "frameChain="+strings.Join(segments, " | "))
	}
	if len(parts) == 0 {
		return "element is not actionable"
	}
	return strings.Join(parts, "; ")
}

func parseActionabilityDiagnosticValue(callRes map[string]any) ActionabilityDiagnostic {
	value, ok := SafeGet[map[string]any](callRes, "result", "value")
	if !ok {
		return ActionabilityDiagnostic{}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return ActionabilityDiagnostic{}
	}
	var decoded actionabilityDiagnosticValue
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return ActionabilityDiagnostic{}
	}
	diagnostic := ActionabilityDiagnostic{
		Actionable:    decoded.Actionable,
		Kind:          decoded.Kind,
		Summary:       decoded.Summary,
		Detail:        decoded.Detail,
		Element:       decoded.Element,
		Culprit:       decoded.Culprit,
		Retriable:     decoded.Retriable,
		RetryAction:   decoded.RetryAction,
		RetryDelayMs:  decoded.RetryDelayMs,
		FrameChain:    decoded.FrameChain,
		HasCenter:     decoded.CenterX != nil && decoded.CenterY != nil,
		HasRetryPoint: decoded.RetryPoint != nil && decoded.RetryPoint.X != nil && decoded.RetryPoint.Y != nil,
	}
	if rect, ok := decoded.LocalRect.rect(); ok {
		diagnostic.LocalRect = rect
		diagnostic.HasLocalRect = true
	}
	if rect, ok := decoded.TopRect.rect(); ok {
		diagnostic.TopRect = rect
		diagnostic.HasTopRect = true
	}
	if diagnostic.HasCenter {
		diagnostic.CenterX = *decoded.CenterX
		diagnostic.CenterY = *decoded.CenterY
	}
	if diagnostic.HasRetryPoint {
		diagnostic.RetryPointX = *decoded.RetryPoint.X
		diagnostic.RetryPointY = *decoded.RetryPoint.Y
	}
	if decoded.Stability != nil {
		samples := make([]Rect, 0, min(len(decoded.Stability.Samples), 3))
		for _, sample := range decoded.Stability.Samples {
			if len(samples) == 3 {
				break
			}
			if rect, ok := sample.rect(); ok {
				samples = append(samples, rect)
			}
		}
		diagnostic.Stability = &StabilityDiagnostic{
			Kind:      decoded.Stability.Kind,
			ElapsedMs: decoded.Stability.ElapsedMs,
			Samples:   samples,
		}
		if rect, ok := decoded.Stability.MaxDelta.rect(); ok {
			diagnostic.Stability.MaxDelta = rect
		}
	}
	return diagnostic
}

func actionabilityPreviewBoxes(diagnostic ActionabilityDiagnostic) []Rect {
	if diagnostic.HasTopRect && diagnostic.TopRect.Width > 0 && diagnostic.TopRect.Height > 0 {
		return []Rect{diagnostic.TopRect}
	}
	if diagnostic.HasLocalRect && diagnostic.LocalRect.Width > 0 && diagnostic.LocalRect.Height > 0 {
		return []Rect{diagnostic.LocalRect}
	}
	return nil
}

func actionabilityPoint(diagnostic ActionabilityDiagnostic) (float64, float64, error) {
	if !diagnostic.Actionable {
		return 0, 0, BrowserErrorFromActionability("actionability", diagnostic)
	}
	if !diagnostic.HasCenter {
		return 0, 0, NewBrowserError("actionability", "missing_center", "missing actionable center point", nil)
	}
	return diagnostic.CenterX, diagnostic.CenterY, nil
}

func (d ActionabilityDiagnostic) needsHoverPriming() bool {
	return d.Retriable && d.RetryAction == "hover_priming" && d.HasRetryPoint
}

func (p *Page) hoverPrimingDelay(ctx context.Context, diagnostic ActionabilityDiagnostic) time.Duration {
	if p.fastActionMode() {
		return 24 * time.Millisecond
	}
	if diagnostic.RetryDelayMs > 0 {
		delay := time.Duration(diagnostic.RetryDelayMs) * time.Millisecond
		if delay > 500*time.Millisecond {
			return 500 * time.Millisecond
		}
		return delay
	}
	return 90 * time.Millisecond
}

func (p *Page) applyHoverPriming(ctx context.Context, diagnostic ActionabilityDiagnostic, moveSilently func(x, y float64) error) (bool, error) {
	if !diagnostic.needsHoverPriming() {
		return false, nil
	}
	if err := moveSilently(diagnostic.RetryPointX, diagnostic.RetryPointY); err != nil {
		return false, err
	}
	if delay := p.hoverPrimingDelay(ctx, diagnostic); delay > 0 {
		time.Sleep(delay)
	}
	return true, nil
}

func prepareActionabilityDiagnostic(load func(actionTargetScrollMode) (ActionabilityDiagnostic, error)) (ActionabilityDiagnostic, error) {
	diagnostic, err := load(actionTargetScrollIfNeeded)
	if err != nil || diagnostic.Kind != "obscured" {
		return diagnostic, err
	}
	return load(actionTargetScrollCenter)
}

func (p *Page) selectorTargetActionabilityDiagnostic(ctx context.Context, ref SelectorTargetRef, scrollMode actionTargetScrollMode, purpose ...string) (ActionabilityDiagnostic, error) {
	target, backendNodeID, err := p.selectorTargetTargetAndBackendNodeID(ctx, ref)
	if err != nil {
		return ActionabilityDiagnostic{}, err
	}
	purposeValue := ""
	if len(purpose) > 0 {
		purposeValue = purpose[0]
	}
	res, err := p.callTargetBackendNodeFunctionContext(ctx, target, backendNodeID, true, selectorTargetActionabilityScript(), map[string]any{"value": string(scrollMode)}, map[string]any{"value": string(p.ActionMode())}, map[string]any{"value": purposeValue})
	if err != nil {
		return ActionabilityDiagnostic{}, err
	}
	return parseActionabilityDiagnosticValue(res), nil
}

func (p *Page) nodeActionabilityDiagnostic(ctx context.Context, nodeID int, scrollMode actionTargetScrollMode, purpose ...string) (ActionabilityDiagnostic, error) {
	p.lock.RLock()
	defer p.lock.RUnlock()
	if err := p.checkConnContext(ctx); err != nil {
		return ActionabilityDiagnostic{}, err
	}
	purposeValue := ""
	if len(purpose) > 0 {
		purposeValue = purpose[0]
	}
	res, err := p.callTargetNodeFunction(ctx, topPageExecutionTarget(), nodeID, selectorTargetActionabilityScript(), map[string]any{"value": string(scrollMode)}, map[string]any{"value": string(p.ActionMode())}, map[string]any{"value": purposeValue})
	if err != nil {
		return ActionabilityDiagnostic{}, err
	}
	return parseActionabilityDiagnosticValue(res), nil
}

type actionVisualPolicy string

const (
	actionVisualNone        actionVisualPolicy = ""
	actionVisualMove        actionVisualPolicy = "move"
	actionVisualClick       actionVisualPolicy = "click"
	actionVisualDoubleClick actionVisualPolicy = "double-click"
	actionVisualRightClick  actionVisualPolicy = "right-click"
)

func (p *Page) resolveMouseActionPoint(ctx context.Context, loadDiagnostic func(actionTargetScrollMode) (ActionabilityDiagnostic, error), moveSilently func(x, y float64) error) (ActionabilityDiagnostic, float64, float64, error) {
	diagnostic, err := prepareActionabilityDiagnostic(loadDiagnostic)
	if err != nil {
		return ActionabilityDiagnostic{}, 0, 0, err
	}
	if primed, primeErr := p.applyHoverPriming(ctx, diagnostic, moveSilently); primeErr != nil {
		return ActionabilityDiagnostic{}, 0, 0, primeErr
	} else if primed {
		diagnostic, err = loadDiagnostic(actionTargetScrollNone)
		if err != nil {
			return ActionabilityDiagnostic{}, 0, 0, err
		}
	}
	x, y, err := actionabilityPoint(diagnostic)
	if err != nil {
		return ActionabilityDiagnostic{}, 0, 0, err
	}
	if err := moveSilently(x, y); err != nil {
		return ActionabilityDiagnostic{}, 0, 0, err
	}
	time.Sleep(12 * time.Millisecond)

	hoverDiagnostic, err := loadDiagnostic(actionTargetScrollNone)
	if err != nil {
		return ActionabilityDiagnostic{}, 0, 0, err
	}
	hoverX, hoverY, err := actionabilityPoint(hoverDiagnostic)
	if err != nil {
		return ActionabilityDiagnostic{}, 0, 0, err
	}
	if math.Abs(hoverX-x) <= 1 && math.Abs(hoverY-y) <= 1 {
		return hoverDiagnostic, hoverX, hoverY, nil
	}

	if err := moveSilently(hoverX, hoverY); err != nil {
		return ActionabilityDiagnostic{}, 0, 0, err
	}
	time.Sleep(12 * time.Millisecond)
	finalDiagnostic, err := loadDiagnostic(actionTargetScrollNone)
	if err != nil {
		return ActionabilityDiagnostic{}, 0, 0, err
	}
	finalX, finalY, err := actionabilityPoint(finalDiagnostic)
	if err != nil {
		return ActionabilityDiagnostic{}, 0, 0, err
	}
	if math.Abs(finalX-hoverX) > 1 || math.Abs(finalY-hoverY) > 1 {
		return ActionabilityDiagnostic{}, 0, 0, fmt.Errorf(
			"元素在 hover 后持续改变点击位置: initial=(%.1f,%.1f), hover=(%.1f,%.1f), final=(%.1f,%.1f)",
			x, y, hoverX, hoverY, finalX, finalY,
		)
	}
	return finalDiagnostic, finalX, finalY, nil
}

func (p *Page) performValidatedMouseAction(ctx context.Context, loadDiagnostic func(actionTargetScrollMode) (ActionabilityDiagnostic, error), visual actionVisualPolicy, dispatch func(x, y float64) error) error {
	diagnostic, x, y, err := p.resolveMouseActionPoint(ctx, loadDiagnostic, func(x, y float64) error {
		return p.mouseMove(ctx,
			x, y)
	})
	if err != nil {
		return err
	}
	if visual != actionVisualNone {
		p.showMouseAction(ctx,
			mouseVisualAction{Kind: string(visual), X: x, Y: y})
		if visual != actionVisualMove {
			p.showActionTarget(ctx,
				actionabilityPreviewBoxes(diagnostic))
		}
	}
	err = dispatch(x, y)
	if err == nil && !p.fastActionMode() {
		time.Sleep(24 * time.Millisecond)
	}
	return err
}

func (p *Page) SelectorTargetActionabilityDiagnosticForPurpose(ctx context.Context, ref SelectorTargetRef, purpose string) (ActionabilityDiagnostic, error) {
	return p.selectorTargetActionabilityDiagnostic(ctx, ref, actionTargetScrollNone, purpose)
}

// SelectorTargetRepositionForAction centers a covered target and returns a fresh actionability diagnostic.
func (p *Page) SelectorTargetRepositionForAction(ctx context.Context, ref SelectorTargetRef, purpose string) (ActionabilityDiagnostic, error) {
	return p.selectorTargetActionabilityDiagnostic(ctx, ref, actionTargetScrollCenter, purpose)
}
