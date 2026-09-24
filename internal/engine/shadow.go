package engine

import (
	"context"
)

type documentRoots struct {
	target ExecutionTarget
	roots  []any
	frames map[string]any
}

// RefreshShadowRoots shares a DOM traversal across the query batch. The same
// genuine node handles establish window identities for shadow-hosted frames.
func (p *Page) RefreshShadowRoots(ctx context.Context) error {
	if err := p.EnsureDocumentReady(ctx); err != nil {
		return err
	}
	sessions := []string{""}
	for _, session := range p.manager.targetSessionsSnapshot() {
		if session.Type == "iframe" && session.PageID == p.ID {
			sessions = append(sessions, session.SessionID)
		}
	}
	documents := map[string]*documentRoots{}
	parents := map[string]string{}
	for _, sessionID := range sessions {
		target := ExecutionTarget{SessionID: sessionID}
		tree, err := p.sendTargetMessage(ctx, target, "DOM.getDocument", map[string]any{"depth": -1, "pierce": true})
		if err != nil {
			return err
		}
		var visit func(map[string]any, string)
		visit = func(node map[string]any, frameID string) {
			if node["nodeName"] == "#document" {
				if id, _ := node["frameId"].(string); id != "" {
					frameID = id
				}
			}
			doc := documents[frameID]
			if doc == nil {
				doc = &documentRoots{target: target, frames: map[string]any{}}
				documents[frameID] = doc
			}
			if kind, _ := node["shadowRootType"].(string); kind == "closed" {
				doc.roots = append(doc.roots, node["backendNodeId"])
			}
			childID, _ := node["frameId"].(string)
			if childID != "" && (node["nodeName"] == "IFRAME" || node["nodeName"] == "FRAME") {
				parents[childID] = frameID
				doc.frames[childID] = node["backendNodeId"]
			}
			if child, ok := node["contentDocument"].(map[string]any); ok {
				visit(child, childID)
			}
			for _, key := range []string{"children", "shadowRoots"} {
				if nodes, ok := node[key].([]any); ok {
					for _, child := range nodes {
						if child, ok := child.(map[string]any); ok {
							visit(child, frameID)
						}
					}
				}
			}
		}
		rootID := p.mainFrame()
		if sessionID != "" {
			if session, ok := p.manager.targetSession(sessionID); ok {
				rootID = session.TargetID
			}
		}
		if root, ok := tree["root"].(map[string]any); ok {
			visit(root, rootID)
		}
	}
	for _, runtime := range p.runtimeContexts(NamespaceIsolatedCore) {
		doc := documents[runtime.FrameID]
		if doc == nil || doc.target.SessionID != runtime.SessionID {
			continue
		}
		target := ExecutionTarget{SessionID: runtime.SessionID, ContextID: runtime.ContextID, FrameID: runtime.FrameID}
		arguments := []map[string]any{{"value": runtime.FrameID}, {"value": parents[runtime.FrameID]}, {"value": len(doc.roots)}}
		ids := append([]any(nil), doc.roots...)
		frameIDs := []string{}
		for id, backend := range doc.frames {
			ids = append(ids, backend)
			frameIDs = append(frameIDs, id)
		}
		arguments = append(arguments, map[string]any{"value": frameIDs})
		objects := []string{}
		var resolveErr error
		for _, backend := range ids {
			resolved, err := p.sendTargetMessage(ctx, target, "DOM.resolveNode", map[string]any{"backendNodeId": backend, "executionContextId": runtime.ContextID})
			if err != nil {
				resolveErr = err
				break
			}
			object, _ := SafeGet[string](resolved, "object", "objectId")
			objects = append(objects, object)
			arguments = append(arguments, map[string]any{"objectId": object})
		}
		if resolveErr == nil {
			result, err := p.sendTargetMessage(ctx, target, "Runtime.callFunctionOn", map[string]any{"executionContextId": runtime.ContextID, "functionDeclaration": `function(id,parentID,rootCount,frameIDs,...nodes){const ffi=globalThis.__cdp_ffi;if(!ffi)throw new Error('core runtime not ready');ffi.registerFrameIdentity(id,parentID);ffi.registerShadowRoots(nodes.slice(0,rootCount));for(let i=0;i<frameIDs.length;i++)ffi.registerFrameWindow(nodes[rootCount+i],frameIDs[i]);}`, "arguments": arguments, "returnByValue": true})
			resolveErr = err
			if err == nil {
				resolveErr = runtimeResultError(result)
			}
		}
		for _, object := range objects {
			_ = p.sendTargetPacket(ctx, target, "Runtime.releaseObject", map[string]any{"objectId": object})
		}
		if resolveErr != nil {
			return resolveErr
		}
	}
	return nil
}
