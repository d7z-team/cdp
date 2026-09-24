package engine

import (
	"context"
	"errors"
	"fmt"
)

func (p *Page) EvalfContext(ctx context.Context, cmd string, args ...any) error {
	_, err := p.EvalfResultContext(ctx, cmd, args...)
	return err
}

func (p *Page) EvalString(cmd string, args ...any) (string, error) {
	res, err := p.EvalfResult(cmd, args...)
	if err != nil {
		return "", err
	}
	if v, ok := SafeGet[string](res, "result", "value"); ok {
		return v, nil
	}
	return "", fmt.Errorf("执行结果不是字符串: %v", res)
}

func (p *Page) EvalfResult(cmd string, args ...any) (result map[string]any, err error) {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return p.evalfResultContext(context.Background(), cmd, args...)
}

func (p *Page) evalfResult(cmd string, args ...any) (map[string]any, error) {
	return p.evalfResultContext(context.Background(), cmd, args...)
}

func (p *Page) EvalfResultContext(ctx context.Context, cmd string, args ...any) (result map[string]any, err error) {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return p.evalfResultContext(ctx, cmd, args...)
}

func (p *Page) evalfResultContext(ctx context.Context, cmd string, args ...any) (map[string]any, error) {
	if err := p.checkConnContext(ctx); err != nil {
		return nil, err
	}
	express := cmd
	if len(args) > 0 {
		express = fmt.Sprintf(cmd, args...)
	}
	message, err := p.RuntimeEvaluateContext(ctx, express, true)
	if err != nil {
		return message, err
	}
	if runtimeErr := runtimeResultError(message); runtimeErr != nil {
		return message, runtimeErr
	}
	return message, nil
}

func runtimeResultError(result map[string]any) error {
	if res, ok := result["result"].(map[string]any); ok && res["subtype"] == "error" {
		desc, _ := res["description"].(string)
		if desc != "" {
			return BrowserErrorFromNode(BrowserErrorNode{
				Op:      "browser.runtime",
				Kind:    "javascript_exception",
				Message: desc,
				Data:    runtimeResultObjectData(res),
			}, nil)
		}
	}
	if details, ok := result["exceptionDetails"].(map[string]any); ok {
		data := runtimeExceptionDetailsData(details)
		if exception, ok := details["exception"].(map[string]any); ok {
			if desc, _ := exception["description"].(string); desc != "" {
				return BrowserErrorFromNode(BrowserErrorNode{
					Op:      "browser.runtime",
					Kind:    "javascript_exception",
					Message: desc,
					Data:    data,
				}, nil)
			}
			if preview, ok := exception["preview"].(map[string]any); ok {
				if desc, _ := preview["description"].(string); desc != "" {
					return BrowserErrorFromNode(BrowserErrorNode{
						Op:      "browser.runtime",
						Kind:    "javascript_exception",
						Message: desc,
						Data:    data,
					}, nil)
				}
			}
		}
		if text, _ := details["text"].(string); text != "" {
			return BrowserErrorFromNode(BrowserErrorNode{
				Op:      "browser.runtime",
				Kind:    "javascript_exception",
				Message: text,
				Data:    data,
			}, nil)
		}
	}
	return nil
}

func runtimeResultObjectData(raw map[string]any) map[string]any {
	data := map[string]any{}
	for _, key := range []string{"className", "description", "type", "subtype", "value"} {
		if value, ok := raw[key]; ok {
			data[key] = value
		}
	}
	return data
}

func runtimeExceptionDetailsData(details map[string]any) map[string]any {
	data := map[string]any{}
	for _, key := range []string{"text", "url", "scriptId"} {
		if value, ok := details[key]; ok {
			data[key] = value
		}
	}
	for _, key := range []string{"lineNumber", "columnNumber"} {
		if value, ok := details[key]; ok {
			data[key] = value
		}
	}
	if exception, ok := details["exception"].(map[string]any); ok {
		for _, key := range []string{"className", "description", "type", "subtype", "value"} {
			if value, ok := exception[key]; ok {
				data["exception."+key] = value
			}
		}
	}
	if stackTrace, ok := details["stackTrace"].(map[string]any); ok {
		if description, ok := stackTrace["description"]; ok {
			data["stack.description"] = description
		}
		if callFrames, ok := stackTrace["callFrames"].([]any); ok {
			frames := make([]any, 0, min(len(callFrames), browserErrorArrayLimit))
			for i := 0; i < len(callFrames) && i < browserErrorArrayLimit; i++ {
				frame, ok := callFrames[i].(map[string]any)
				if !ok {
					continue
				}
				frames = append(frames, map[string]any{
					"functionName": frame["functionName"],
					"url":          frame["url"],
					"lineNumber":   frame["lineNumber"],
					"columnNumber": frame["columnNumber"],
				})
			}
			if len(frames) > 0 {
				data["stack.callFrames"] = frames
			}
		}
	}
	return data
}

func (p *Page) DeliverBindingResultInSessionContext(ctx context.Context, sessionID string, preferredContextID int, slotName string, sessionKey string, rawPayload string) (bool, error) {
	if ctx == nil {
		ctx = p.ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	p.lock.RLock()
	defer p.lock.RUnlock()
	if err := p.checkConn(); err != nil {
		return false, err
	}
	if preferredContextID == 0 {
		return false, errors.New("binding execution context is required")
	}
	expr := `(function(slotName, sessionKey, rawPayload) {
const slot = globalThis[slotName];
if (!slot || slot.sessionKey !== sessionKey || typeof slot.deliver !== "function") {
	return false;
}
return !!slot.deliver(rawPayload);
})(%s, %s, %s)`
	result, err := p.sendTargetMessage(ctx,
		ExecutionTarget{SessionID: sessionID, ContextID: preferredContextID},
		"Runtime.evaluate", map[string]any{
			"expression":    fmt.Sprintf(expr, jsStringLiteral(slotName), jsStringLiteral(sessionKey), jsStringLiteral(rawPayload)),
			"returnByValue": true, "awaitPromise": true, "contextId": preferredContextID,
		})
	if err != nil {
		return false, err
	}
	if err := runtimeResultError(result); err != nil {
		return false, err
	}
	delivered, _ := SafeGet[bool](result, "result", "value")
	return delivered, nil
}
