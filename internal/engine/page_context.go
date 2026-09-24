package engine

import (
	"context"
	"errors"
	"strings"
)

func parseExecutionContextInfo(ctxData map[string]any) executionContextInfo {
	info := executionContextInfo{}
	if id, ok := runtimeExecutionContextID(ctxData); ok {
		info.ID = id
	}
	info.Name, _ = ctxData["name"].(string)
	info.FrameID, _ = SafeGet[string](ctxData, "auxData", "frameId")
	if info.FrameID == "" {
		info.FrameID, _ = SafeGet[string](ctxData, "aux", "frameId")
	}
	if isDefault, ok := SafeGet[bool](ctxData, "auxData", "isDefault"); ok {
		info.IsDefault = isDefault
	} else if isDefault, ok := SafeGet[bool](ctxData, "aux", "isDefault"); ok {
		info.IsDefault = isDefault
	}
	return info
}

func runtimeExecutionContextID(data map[string]any) (int, bool) {
	if value, ok := data["id"]; ok {
		switch v := value.(type) {
		case float64:
			return int(v), true
		case int:
			return v, true
		}
	}
	if value, ok := data["executionContextId"]; ok {
		switch v := value.(type) {
		case float64:
			return int(v), true
		case int:
			return v, true
		}
	}
	return 0, false
}

func (p *Page) storeExecutionContext(info executionContextInfo) {
	p.storeExecutionContextForSession("", info)
}

func (p *Page) storeExecutionContextForSession(sessionID string, info executionContextInfo) {
	if info.ID == 0 {
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	key := targetContextKey{SessionID: sessionID, ContextID: info.ID}
	p.contextMu.Lock()
	defer p.contextMu.Unlock()
	if p.executionContexts == nil {
		p.executionContexts = map[targetContextKey]executionContextInfo{}
	}
	if p.frameContexts == nil {
		p.frameContexts = map[string]targetContextKey{}
	}
	p.ensureExecutionTargetMapsLocked()
	p.executionContexts[key] = info
	if strings.TrimSpace(info.FrameID) != "" {
		if info.IsDefault && sessionID == "" {
			p.frameContexts[info.FrameID] = key
		}
		p.storeExecutionTargetLocked(ExecutionTarget{
			PageID:    p.ID,
			SessionID: sessionID,
			ContextID: info.ID,
			FrameID:   info.FrameID,
		})
	}
}

func (p *Page) copyTargetSessionRuntimeTo(target *Page) {
	if p == nil || target == nil || p == target {
		return
	}
	p.contextMu.RLock()
	defer p.contextMu.RUnlock()
	target.contextMu.Lock()
	defer target.contextMu.Unlock()
	if target.executionContexts == nil {
		target.executionContexts = make(map[targetContextKey]executionContextInfo)
	}
	target.ensureExecutionTargetMapsLocked()
	for key, info := range p.executionContexts {
		if key.SessionID != "" {
			target.executionContexts[key] = info
		}
	}
	for key, executionTarget := range p.executionTargetsByContext {
		if key.SessionID != "" {
			target.storeExecutionTargetLocked(executionTarget)
		}
	}
}

func (p *Page) removeExecutionContext(id int) {
	p.removeExecutionContextForSession("", id)
}

func (p *Page) removeExecutionContextForSession(sessionID string, id int) {
	if id == 0 {
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	key := targetContextKey{SessionID: sessionID, ContextID: id}
	p.contextMu.Lock()
	defer p.contextMu.Unlock()
	if info, ok := p.executionContexts[key]; ok && sessionID == "" && info.IsDefault && strings.TrimSpace(info.FrameID) != "" {
		if p.frameContexts != nil && p.frameContexts[info.FrameID] == key {
			delete(p.frameContexts, info.FrameID)
		}
	}
	delete(p.executionContexts, key)
	p.removeExecutionTargetLocked(sessionID, id)
}

func (p *Page) clearExecutionContexts() {
	p.clearExecutionContextsForSession("")
}

func (p *Page) clearExecutionContextsForSession(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	p.contextMu.Lock()
	defer p.contextMu.Unlock()
	for key, info := range p.executionContexts {
		if key.SessionID != sessionID {
			continue
		}
		if sessionID == "" && info.IsDefault && p.frameContexts[info.FrameID] == key {
			delete(p.frameContexts, info.FrameID)
		}
		delete(p.executionContexts, key)
	}
	p.clearExecutionTargetsLocked(sessionID)
	p.bumpTargetEpochLocked(sessionID)
}

func isMissingExecutionContextError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "Cannot find context with specified id") || strings.Contains(err.Error(), "Cannot find execution context with given executionContextId")
}

func isSupersededDocumentError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return isMissingExecutionContextError(err) ||
		strings.Contains(message, "Inspected target navigated or closed") ||
		strings.Contains(message, "No frame for given id found") ||
		strings.Contains(message, "No session with given id") ||
		strings.Contains(message, "Session with given id not found")
}

func (p *Page) addInitScriptToEvaluateOnNewDocument(script InitScript, action string) error {
	return p.registerInitScriptToEvaluateOnNewDocument(context.Background(), script, action)
}

func (p *Page) registerInitScriptToEvaluateOnNewDocument(ctx context.Context, script InitScript, action string) error {
	worldName := ""
	if p.manager != nil {
		worldName = p.manager.namespaceWorldName(script.Namespace)
		return p.manager.installInitScriptOnce(ctx, initScriptInstallKey{
			PageID:     p.ID,
			ScriptName: script.Name,
			WorldName:  worldName,
		}, func(ctx context.Context) (string, error) {
			return p.addScriptToEvaluateOnNewDocumentInWorldContext(ctx, p.manager.initScriptSource(script, action, "exec"), worldName)
		})
	}
	_, err := p.addScriptToEvaluateOnNewDocumentInWorldContext(ctx, script.execSourceWithAction(action), worldName)
	return err
}

func (p *Page) addScriptToEvaluateOnNewDocumentInWorldContext(ctx context.Context, cmd string, worldName string) (string, error) {
	params := map[string]any{
		"source":         cmd,
		"runImmediately": true,
	}
	if strings.TrimSpace(worldName) != "" {
		params["worldName"] = strings.TrimSpace(worldName)
	}
	res, err := p.CdpConn.SendMessageContext(ctx, "Page.addScriptToEvaluateOnNewDocument", params)
	if err != nil {
		return "", BrowserErrorFromCDP("Page.addScriptToEvaluateOnNewDocument", topPageExecutionTarget(), err)
	}
	identifier, _ := SafeGet[string](res, "identifier")
	return identifier, nil
}

func (p *Page) addBinding(name string, namespace RuntimeNamespace) error {
	return p.addBindingContext(context.Background(), name, namespace)
}

func (p *Page) addBindingContext(ctx context.Context, name string, namespace RuntimeNamespace) error {
	if p == nil || p.manager == nil {
		return errors.New("page manager is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	p.lock.RLock()
	defer p.lock.RUnlock()
	p.bindingInstallMu.Lock()
	defer p.bindingInstallMu.Unlock()
	if p.installedBindings == nil {
		p.installedBindings = make(map[string]struct{})
	}
	if _, ok := p.installedBindings[name]; ok {
		return nil
	}
	if err := p.manager.addBindingOnConn(ctx, p.CdpConn, "", name, namespace); err != nil {
		return err
	}
	p.installedBindings[name] = struct{}{}
	return nil
}
