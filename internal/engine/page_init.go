package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

func (p *Page) cleanupInitScript(ctx context.Context, script InitScript) error {
	script.Namespace = normalizeRuntimeNamespace(script.Namespace)
	if strings.TrimSpace(script.Cleanup) == "" {
		return nil
	}
	if p == nil {
		return errors.New("page is nil")
	}
	reg := NamespaceRegistration{Namespace: NamespacePageMain, MainWorld: true}
	if p.manager != nil {
		reg = p.manager.namespaceRegistration(script.Namespace)
	}
	if reg.Namespace == NamespaceOverlay {
		return nil
	}
	source := script.cleanupSource()
	if p.manager != nil {
		source = p.manager.initScriptSource(script, "", "cleanup")
	}
	if reg.MainWorld {
		return p.EvalfContext(ctx, "%s", source)
	}
	return p.EvalAllRuntimeContextsContext(ctx, script.Namespace, source)
}

func (p *Page) ensureInitScriptInjectedInRuntimeContexts(ctx context.Context, script InitScript, action string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	script.Namespace = normalizeRuntimeNamespace(script.Namespace)
	contexts := p.runtimeContexts(script.Namespace)
	if len(contexts) == 0 {
		return p.ensureInitScriptInjectedContext(ctx, script, action)
	}
	var errs []error
	for _, runtimeContext := range contexts {
		var err error
		if runtimeContext.SessionID != "" && p.manager != nil {
			err = p.manager.ensureInitScriptInjectedInTargetSessionContext(ctx, runtimeContext.SessionID, runtimeContext.ContextID, script, action)
		} else {
			err = p.ensureInitScriptInjectedInContext(ctx, runtimeContext.ContextID, script, action)
		}
		if err == nil || isSupersededDocumentError(err) {
			continue
		}
		errs = append(errs, fmt.Errorf("context %d frame %s: %w", runtimeContext.ContextID, runtimeContext.FrameID, err))
	}
	return errors.Join(errs...)
}

// ensureInitScriptInjectedContext resolves the namespace when no context is cached.
func (p *Page) ensureInitScriptInjectedContext(ctx context.Context, script InitScript, action string) error {
	if p.manager == nil {
		return ErrBrowserClosed
	}
	script.Namespace = normalizeRuntimeNamespace(script.Namespace)
	reg := p.manager.namespaceRegistration(script.Namespace)
	if reg.Namespace == NamespaceOverlay {
		return nil
	}
	key := initScriptEnsureKey{PageID: p.ID, ScriptName: script.Name}
	evaluate := func(ctx context.Context, source string) (map[string]any, error) {
		return p.EvalfResultContext(ctx, "%s", source)
	}
	if !reg.MainWorld {
		handle, err := p.ContextWithContext(ctx, RuntimeContextOptions{Namespace: script.Namespace})
		if err != nil {
			return err
		}
		key.SessionID, key.ContextID = handle.Info.SessionID, handle.Info.ContextID
		evaluate = handle.eval
	}
	return p.manager.ensureInitScriptRuntime(ctx, key, script, action, evaluate)
}

func (p *Page) ensureInitScriptInjectedInContext(ctx context.Context, contextID int, script InitScript, action string) error {
	if contextID == 0 {
		return errors.New("execution context id is empty")
	}
	if p.manager == nil {
		return ErrBrowserClosed
	}
	script.Namespace = normalizeRuntimeNamespace(script.Namespace)
	if info, ok := p.executionContextInfo(contextID); ok && !namespaceMatchesContext(p.manager.namespaceRegistration(script.Namespace), info) {
		return nil
	}
	return p.manager.ensureInitScriptRuntime(ctx, initScriptEnsureKey{PageID: p.ID, ContextID: contextID, ScriptName: script.Name}, script, action, func(ctx context.Context, source string) (map[string]any, error) {
		return p.evaluateInitScriptInContext(ctx, contextID, source)
	})
}

// ensureInitScriptRuntime shares readiness probing and execution across root and
// iframe contexts. Transport selection stays with the caller.
func (r *BrowserManager) ensureInitScriptRuntime(ctx context.Context, key initScriptEnsureKey, script InitScript, action string, evaluate func(context.Context, string) (map[string]any, error)) error {
	return r.ensureInitScriptOnce(ctx, key, func(ctx context.Context) error {
		if script.Probe != "" {
			result, err := evaluate(ctx, r.initScriptSource(script, action, "probe"))
			if err == nil {
				err = runtimeResultError(result)
			}
			if err == nil {
				ready, ok := SafeGet[bool](result, "result", "value")
				if ok && ready {
					return nil
				}
				if !ok {
					err = errors.New("init script probe did not return a boolean")
				}
			}
			if err != nil {
				slog.Debug("init script probe failed, retry installation", "page_id", key.PageID, "session_id", key.SessionID, "context_id", key.ContextID, "script", script.Name, "error", err)
			}
		}
		result, err := evaluate(ctx, r.initScriptSource(script, action, "exec"))
		if err != nil {
			return err
		}
		return runtimeResultError(result)
	})
}

func (p *Page) evaluateInitScriptInContext(ctx context.Context, contextID int, expression string) (map[string]any, error) {
	if p == nil || p.CdpConn == nil {
		return nil, ErrBrowserClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	result, err := p.CdpConn.SendMessageContext(ctx, "Runtime.evaluate", map[string]any{
		"expression":      expression,
		"awaitPromise":    true,
		"generatePreview": false,
		"returnByValue":   true,
		"contextId":       contextID,
	})
	return result, BrowserErrorFromCDP("Runtime.evaluate", ExecutionTarget{PageID: p.ID, ContextID: contextID}, err)
}
