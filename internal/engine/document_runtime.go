package engine

import (
	"context"
	"fmt"
	"time"
)

// reconcileDocumentRuntime discovers contexts explicitly. Runtime diagnostic events
// never drive initialization; both diagnostic modes use this path.
func (r *BrowserManager) reconcileDocumentRuntime(ctx context.Context, p *Page, sessionID string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return r.ensureInitScriptOnce(ctx, initScriptEnsureKey{PageID: p.ID, SessionID: sessionID, ScriptName: "document initialization"}, func(ctx context.Context) error { return r.initializeDocumentRuntime(ctx, p, sessionID) })
}

func (r *BrowserManager) initializeDocumentRuntime(ctx context.Context, p *Page, sessionID string) error {
	var conn *CdpConn
	var err error
	if sessionID == "" {
		conn = p.CdpConn
	} else {
		conn, err = r.activeConn()
		if err != nil {
			return err
		}
	}
	if conn == nil {
		return ErrBrowserClosed
	}
	send := func(method string, params map[string]any) (map[string]any, error) {
		if sessionID != "" {
			return conn.SendSessionMessage(ctx, sessionID, method, params)
		}
		return conn.SendMessageContext(ctx, method, params)
	}
	tree, err := send("Page.getFrameTree", nil)
	if err != nil {
		return err
	}
	root, _ := tree["frameTree"].(map[string]any)
	rootID, _ := SafeGet[string](root, "frame", "id")
	if sessionID == "" {
		p.setMainFrame(rootID)
	}
	scripts := r.initScriptsSnapshot()
	remoteFrames := map[string]string{}
	for _, session := range r.targetSessionsSnapshot() {
		if session.Type == "iframe" && session.PageID == p.ID {
			remoteFrames[session.TargetID] = session.SessionID
		}
	}
	worlds := map[string]RuntimeNamespace{}
	for _, script := range scripts {
		if name := r.namespaceWorldName(script.Namespace); name != "" {
			worlds[name] = script.Namespace
		}
	}
	r.bindingMu.RLock()
	bindings := make(map[string]RuntimeNamespace, len(r.bindingNamespaces))
	for name, ns := range r.bindingNamespaces {
		bindings[name] = ns
		if world := r.namespaceWorldName(ns); world != "" {
			worlds[world] = ns
		}
	}
	r.bindingMu.RUnlock()
	var visit func(map[string]any) error
	visit = func(node map[string]any) error {
		frameID, _ := SafeGet[string](node, "frame", "id")
		if frameID == "" {
			return nil
		}
		if owner := remoteFrames[frameID]; owner != "" && owner != sessionID {
			return nil
		}
		for world := range worlds {
			result, err := send("Page.createIsolatedWorld", map[string]any{"frameId": frameID, "worldName": world})
			if err != nil {
				return fmt.Errorf("create world frame %s: %w", frameID, err)
			}
			id, ok := runtimeExecutionContextID(result)
			if !ok || id == 0 {
				return fmt.Errorf("missing context for frame %s", frameID)
			}
			p.storeExecutionContextForSession(sessionID, executionContextInfo{ID: id, FrameID: frameID, Name: world})
			for name, ns := range bindings {
				if r.namespaceWorldName(ns) == world {
					if _, err = send("Runtime.addBinding", map[string]any{"name": name, "executionContextId": id}); err != nil {
						return err
					}
				}
			}
			for _, script := range scripts {
				if r.namespaceWorldName(script.Namespace) != world {
					continue
				}
				if sessionID == "" {
					err = p.ensureInitScriptInjectedInContext(ctx, id, script, "document")
				} else {
					err = r.ensureInitScriptInjectedInTargetSessionContext(ctx, sessionID, id, script, "document")
				}
				if err != nil {
					return err
				}
			}
		}
		children, _ := node["childFrames"].([]any)
		for _, child := range children {
			if child, ok := child.(map[string]any); ok {
				if err := visit(child); err != nil && !isSupersededDocumentError(err) {
					return err
				}
			}
		}
		return nil
	}
	// Empty context name targets existing main worlds without exposing internal
	// bindings to other worlds. Repeat for every discovered document.
	for name, ns := range bindings {
		if r.namespaceRegistration(ns).MainWorld {
			if _, err = send("Runtime.addBinding", map[string]any{"name": name, "executionContextName": ""}); err != nil {
				return err
			}
		}
	}
	return visit(root)
}

// EnsureDocumentReady establishes the current document's bindings and facades.
func (p *Page) EnsureDocumentReady(ctx context.Context) error {
	if err := p.checkConnContext(ctx); err != nil {
		return err
	}
	for {
		err := p.manager.reconcileDocumentRuntime(ctx, p, "")
		if err == nil {
			for _, session := range p.manager.targetSessionsSnapshot() {
				if session.Type == "iframe" && session.PageID == p.ID {
					err = p.manager.reconcileDocumentRuntime(ctx, p, session.SessionID)
					if err != nil {
						break
					}
				}
			}
		}
		if err == nil {
			return nil
		}
		if !isSupersededDocumentError(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-p.Done():
			return ErrBrowserClosed
		case <-time.After(runtimeContextResolveInterval):
		}
	}

}

func (p *Page) forgetFrameContexts(sessionID, frameID string) {
	p.contextMu.Lock()
	if p.frameGenerations == nil {
		p.frameGenerations = make(map[string]uint64)
	}
	p.frameGenerations[frameID]++
	var ids []int
	for key, info := range p.executionContexts {
		if key.SessionID == sessionID && (frameID == "" || info.FrameID == frameID) {
			ids = append(ids, key.ContextID)
		}
	}
	p.contextMu.Unlock()
	for _, id := range ids {
		p.removeExecutionContextForSession(sessionID, id)
	}
}

func (p *Page) frameGeneration(frameID string) uint64 {
	p.contextMu.RLock()
	defer p.contextMu.RUnlock()
	return p.frameGenerations[frameID]
}
