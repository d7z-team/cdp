package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"gopkg.d7z.net/cdp/internal/binding"
	"gopkg.d7z.net/cdp/internal/syncutil"
)

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func (r *BrowserManager) rememberTargetSession(session TargetSession) *TargetSession {
	session.SessionID = strings.TrimSpace(session.SessionID)
	session.TargetID = strings.TrimSpace(session.TargetID)
	session.Type = strings.TrimSpace(session.Type)
	if session.PageID == "" {
		session.PageID = r.resolveTargetSessionPageID(session)
	}
	stored := session

	r.targetSessionMu.Lock()
	defer r.targetSessionMu.Unlock()
	if r.targetSessions == nil {
		r.targetSessions = map[string]*TargetSession{}
	}
	if r.targetSessionsByTarget == nil {
		r.targetSessionsByTarget = map[string]string{}
	}
	r.targetSessions[stored.SessionID] = &stored
	if stored.TargetID != "" {
		r.targetSessionsByTarget[stored.TargetID] = stored.SessionID
	}
	return &stored
}

func (r *BrowserManager) removeTargetSession(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	var session *TargetSession
	r.targetSessionMu.Lock()
	session = r.targetSessions[sessionID]
	delete(r.targetSessions, sessionID)
	if session != nil && session.TargetID != "" && r.targetSessionsByTarget[session.TargetID] == sessionID {
		delete(r.targetSessionsByTarget, session.TargetID)
	}
	r.targetSessionMu.Unlock()
	if session != nil && session.PageID != "" {
		r.updateManagedPage(session.PageID, func(page *Page) {
			page.clearExecutionContextsForSession(sessionID)
		})
	}
	if session != nil {
		r.forgetInitScriptRuntime(session.PageID, sessionID)
	}
}

func (r *BrowserManager) targetSession(sessionID string) (*TargetSession, bool) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, false
	}
	r.targetSessionMu.RLock()
	defer r.targetSessionMu.RUnlock()
	session, ok := r.targetSessions[sessionID]
	if !ok || session == nil {
		return nil, false
	}
	copySession := *session
	return &copySession, true
}

func (r *BrowserManager) targetSessionsSnapshot() []TargetSession {
	if r == nil {
		return nil
	}
	r.targetSessionMu.RLock()
	defer r.targetSessionMu.RUnlock()
	sessions := make([]TargetSession, 0, len(r.targetSessions))
	for _, session := range r.targetSessions {
		if session != nil {
			sessions = append(sessions, *session)
		}
	}
	return sessions
}

func (r *BrowserManager) resolveTargetSessionPageID(session TargetSession) string {
	r.targetSessionMu.RLock()
	if session.ParentID != "" {
		if parentSessionID := r.targetSessionsByTarget[session.ParentID]; parentSessionID != "" {
			if parent := r.targetSessions[parentSessionID]; parent != nil && parent.PageID != "" {
				r.targetSessionMu.RUnlock()
				return parent.PageID
			}
		}
	}
	if session.OpenerID != "" {
		if openerSessionID := r.targetSessionsByTarget[session.OpenerID]; openerSessionID != "" {
			if opener := r.targetSessions[openerSessionID]; opener != nil && opener.PageID != "" {
				r.targetSessionMu.RUnlock()
				return opener.PageID
			}
		}
	}
	r.targetSessionMu.RUnlock()

	if session.ParentID != "" {
		if r.hasManagedPageTarget(session.ParentID) {
			return session.ParentID
		}
	}
	if session.OpenerID != "" {
		if r.hasManagedPageTarget(session.OpenerID) {
			return session.OpenerID
		}
	}
	return r.getLastActivePageID(false)
}

// autoAttachFrameChildren pauses frames until their registrations are installed.
// Include workers as well: Chromium can pause excluded workers without emitting
// an attachedToTarget event, leaving no session through which to resume them.
func (r *BrowserManager) autoAttachFrameChildren(ctx context.Context, conn *CdpConn, sessionID string) error {
	return conn.SendSessionPacket(ctx, sessionID, "Target.setAutoAttach", map[string]any{
		"autoAttach": true, "waitForDebuggerOnStart": true, "flatten": true,
		"filter": []map[string]any{
			{"type": "iframe", "exclude": false},
			{"type": "worker", "exclude": false},
			{"type": "shared_worker", "exclude": false},
			{"type": "service_worker", "exclude": false},
			{"exclude": true},
		},
	})
}

func (r *BrowserManager) initTargetSession(ctx context.Context, session *TargetSession) error {
	if r == nil {
		return ErrBrowserClosed
	}
	if session == nil || strings.TrimSpace(session.SessionID) == "" {
		return errors.New("target session is empty")
	}
	conn, err := r.activeConn()
	if err != nil {
		return err
	}
	sessionID := session.SessionID
	var errs []error
	if err := r.enableRuntimeDiagnostics(ctx, conn, sessionID); err != nil {
		errs = append(errs, fmt.Errorf("runtime enable: %w", err))
	}
	if err := conn.SendSessionPacket(ctx, sessionID, "Page.enable", nil); err != nil {
		r.log(slog.LevelDebug, "Page.enable target session failed", "session_id", sessionID, "target_id", session.TargetID, "target_type", session.Type, "error", err)
	}
	if err := conn.SendSessionPacket(ctx, sessionID, "Page.setLifecycleEventsEnabled", map[string]any{"enabled": true}); err != nil {
		r.log(slog.LevelDebug, "Page.setLifecycleEventsEnabled target session failed", "session_id", sessionID, "target_id", session.TargetID, "target_type", session.Type, "error", err)
	}
	if err := r.autoAttachFrameChildren(ctx, conn, sessionID); err != nil {
		r.log(slog.LevelDebug, "recursive Target.setAutoAttach target session failed", "session_id", sessionID, "target_id", session.TargetID, "target_type", session.Type, "error", err)
	}
	if err := r.registerBindingsInTargetSession(ctx, sessionID); err != nil {
		errs = append(errs, fmt.Errorf("register bindings: %w", err))
	}
	for _, script := range r.initScriptsSnapshot() {
		if err := r.addInitScriptToTargetSession(ctx, sessionID, script, "target_session_init"); err != nil {
			errs = append(errs, fmt.Errorf("init script %s: %w", script.Name, err))
		}
	}
	if err := conn.SendSessionPacket(ctx, sessionID, "Runtime.runIfWaitingForDebugger", nil); err != nil {
		r.log(slog.LevelDebug, "Runtime.runIfWaitingForDebugger target session failed", "session_id", sessionID, "target_id", session.TargetID, "target_type", session.Type, "error", err)
	}
	if page, ok := r.managedPage(session.PageID); ok {
		if err := r.reconcileDocumentRuntime(ctx, page, sessionID); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (r *BrowserManager) registerBindingsInTargetSession(ctx context.Context, sessionID string) error {
	r.bindingMu.RLock()
	registrations := make(map[string]RuntimeNamespace, len(r.bindingHandlers))
	for name := range r.bindingHandlers {
		registrations[name] = normalizeRuntimeNamespace(r.bindingNamespaces[name])
	}
	r.bindingMu.RUnlock()

	names := make([]string, 0, len(registrations))
	for name := range registrations {
		names = append(names, name)
	}
	sort.Strings(names)
	var errs []error
	for _, name := range names {
		if err := r.addBindingToTargetSession(ctx, sessionID, name, registrations[name]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (r *BrowserManager) addBindingToTargetSession(ctx context.Context, sessionID, name string, namespace RuntimeNamespace) error {
	conn, err := r.activeConn()
	if err != nil {
		return err
	}
	return r.addBindingOnConn(ctx, conn, sessionID, name, namespace)
}

func (r *BrowserManager) addInitScriptToTargetSession(ctx context.Context, sessionID string, script InitScript, action string) error {
	conn, err := r.activeConn()
	if err != nil {
		return err
	}
	session, _ := r.targetSession(sessionID)
	pageID := ""
	if session != nil {
		pageID = session.PageID
	}
	worldName := r.namespaceWorldName(script.Namespace)
	return r.installInitScriptOnce(ctx, initScriptInstallKey{
		PageID:     pageID,
		SessionID:  sessionID,
		ScriptName: script.Name,
		WorldName:  worldName,
	}, func(ctx context.Context) (string, error) {
		params := map[string]any{
			"source":         r.initScriptSource(script, action, "exec"),
			"runImmediately": true,
		}
		if worldName != "" {
			params["worldName"] = worldName
		}
		res, err := conn.SendSessionMessage(ctx, sessionID, "Page.addScriptToEvaluateOnNewDocument", params)
		if err != nil {
			return "", err
		}
		identifier, _ := SafeGet[string](res, "identifier")
		return identifier, nil
	})
}

func (r *BrowserManager) ensureInitScriptInjectedInTargetSessionContext(ctx context.Context, sessionID string, contextID int, script InitScript, action string) error {
	if contextID == 0 {
		return errors.New("execution context id is empty")
	}
	conn, err := r.activeConn()
	if err != nil {
		return err
	}
	session, ok := r.targetSession(sessionID)
	if !ok {
		return ErrBrowserClosed
	}
	key := initScriptEnsureKey{PageID: session.PageID, SessionID: sessionID, ContextID: contextID, ScriptName: script.Name}
	return r.ensureInitScriptRuntime(ctx, key, script, action, func(ctx context.Context, source string) (map[string]any, error) {
		result, err := conn.SendSessionMessage(ctx, sessionID, "Runtime.evaluate", map[string]any{
			"expression": source, "returnByValue": true, "awaitPromise": true, "generatePreview": false, "contextId": contextID,
		})
		return result, BrowserErrorFromCDP("Runtime.evaluate", ExecutionTarget{PageID: session.PageID, SessionID: sessionID, ContextID: contextID}, err)
	})
}

func (r *BrowserManager) handleAttachedTargetEvent(event CDPResponse, parent *TargetSession) {
	targetInfo, ok := event.Params["targetInfo"].(map[string]any)
	if !ok {
		return
	}
	id, _ := targetInfo["targetId"].(string)
	title, _ := targetInfo["title"].(string)
	targetURL, _ := targetInfo["url"].(string)
	typ, _ := targetInfo["type"].(string)
	openerID, _ := targetInfo["openerId"].(string)
	openerFrameID, _ := targetInfo["openerFrameId"].(string)
	parentID, _ := targetInfo["parentId"].(string)
	parentFrameID, _ := targetInfo["parentFrameId"].(string)
	sessionID, _ := event.Params["sessionId"].(string)

	if typ == "page" {
		info := TargetInfo{
			OpenerFrameID: openerFrameID, OpenerID: openerID,
			ParentID: parentID, ParentFrameID: parentFrameID,
			TargetID: id, Title: title, Type: typ, URL: targetURL,
		}
		// Browser-level auto-attach is not recursive. Keep this session as a
		// routing carrier and attach its OOPIF children before they execute.
		// The root Page still owns its separate runtime connection.
		if sessionID != "" {
			r.rememberTargetSession(TargetSession{TargetID: id, SessionID: sessionID, Type: typ, PageID: id, FrameID: id, URL: targetURL})
			syncutil.Go(r.Logger(), func() {
				conn, err := r.activeConn()
				if err != nil {
					return
				}
				if err := r.autoAttachFrameChildren(r.ctx, conn, sessionID); err != nil {
					r.log(slog.LevelWarn, "attach page iframe targets failed", "page_id", id, "error", err)
				}
			})
		}
		if IsInternalPageURL(targetURL) {
			r.updatePageTargetInfo(info)
			if r.currentActivePageID() == id {
				r.clearActivePage(id, LifecycleSourceTarget, "attached_internal")
			}
			return
		}
		r.markPageDiscovered(info, nil)
		r.requestPageBind(id, false)
		if IsPlaceholderPageURL(targetURL) && r.currentActivePageID() == id {
			r.clearActivePage(id, LifecycleSourceTarget, "attached_placeholder")
		}
		if IsExecutablePageURL(targetURL) && r.getLastActivePageID(false) == "" {
			r.setActivePage(id, LifecycleSourceTarget, "attached_executable")
		}
		return
	}

	if sessionID != "" && (typ == "worker" || typ == "shared_worker" || typ == "service_worker") {
		syncutil.Go(r.Logger(), func() {
			conn, err := r.activeConn()
			if err != nil {
				return
			}
			if waiting, _ := event.Params["waitingForDebugger"].(bool); waiting {
				ctx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
				err := conn.SendSessionPacket(ctx, sessionID, "Runtime.runIfWaitingForDebugger", nil)
				cancel()
				if err != nil && !isSupersededDocumentError(err) {
					r.log(slog.LevelWarn, "resume worker target failed", "target_type", typ, "error", err)
				}
			}
			// Detach through the parent that owns this session, even if resume failed.
			// Workers may finish or be terminated between the two commands.
			ctx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
			defer cancel()
			if err := conn.SendSessionPacket(ctx, event.SessionID, "Target.detachFromTarget", map[string]any{"sessionId": sessionID}); err != nil && !isSupersededDocumentError(err) {
				r.log(slog.LevelWarn, "detach worker target failed", "target_type", typ, "error", err)
			}
		})
		return
	}

	if typ != "iframe" || sessionID == "" {
		return
	}
	if parent != nil {
		parentID = firstNonEmpty(parentID, parent.TargetID)
	}
	pageID := ""
	if parent != nil {
		pageID = parent.PageID
	}
	session := r.rememberTargetSession(TargetSession{
		TargetID:  id,
		SessionID: sessionID,
		Type:      typ,
		URL:       targetURL,
		ParentID:  parentID,
		OpenerID:  openerID,
		FrameID:   firstNonEmpty(parentFrameID, openerFrameID),
		PageID:    pageID,
	})
	if _, err := r.activeConn(); err != nil {
		return
	}
	syncutil.Go(r.Logger(), func() {
		if err := r.initTargetSession(context.Background(), session); err != nil && !errors.Is(err, ErrBrowserClosed) && !errors.Is(err, context.Canceled) {
			r.log(slog.LevelWarn, "init iframe target session failed", "session_id", session.SessionID, "target_id", session.TargetID, "error", err)
		}
	})
}

func (r *BrowserManager) handleTargetSessionEvent(event CDPResponse) {
	session, ok := r.targetSession(event.SessionID)
	if !ok {
		return
	}
	if session.Type != "iframe" && event.Method != "Target.attachedToTarget" && event.Method != "Target.detachedFromTarget" {
		return
	}
	switch event.Method {
	case "Target.attachedToTarget":
		r.handleAttachedTargetEvent(event, session)
	case "Target.detachedFromTarget":
		sessionID, _ := event.Params["sessionId"].(string)
		r.removeTargetSession(sessionID)
	case "Page.frameNavigated", "Page.frameAttached", "Page.frameDetached", "Page.frameStoppedLoading":
		if event.Method == "Page.frameNavigated" {
			r.updateManagedPage(session.PageID, func(page *Page) {
				id, _ := SafeGet[string](event.Params, "frame", "id")
				page.forgetFrameContexts(session.SessionID, id)
				page.bumpTargetEpoch(session.SessionID)
			})
		}
		syncutil.Go(r.Logger(), func() {
			if page, ok := r.managedPage(session.PageID); ok {
				if err := r.reconcileDocumentRuntime(r.ctx, page, session.SessionID); err != nil && !isSupersededDocumentError(err) {
					r.log(slog.LevelDebug, "reconcile iframe runtime", "error", err)
				}
			}
		})
	case "Runtime.executionContextDestroyed":
		if id, ok := runtimeExecutionContextID(event.Params); ok {
			r.updateManagedPage(session.PageID, func(page *Page) { page.removeExecutionContextForSession(session.SessionID, id) })
		}
	case "Runtime.executionContextCreated":
		data, ok := event.Params["context"].(map[string]any)
		if !ok {
			return
		}
		info := parseExecutionContextInfo(data)
		r.updateManagedPage(session.PageID, func(page *Page) {
			page.storeExecutionContextForSession(session.SessionID, info)
			page.storeExecutionTarget(ExecutionTarget{PageID: session.PageID, SessionID: session.SessionID, TargetID: session.TargetID, ContextID: info.ID, FrameID: info.FrameID})
		})
	case "Runtime.bindingCalled":
		var data binding.BindingCalledEvent
		if err := event.ParamsUnmarshal(&data); err != nil {
			r.log(slog.LevelDebug, "parse target session binding event failed", "session_id", session.SessionID, "target_id", session.TargetID, "error", err)
			return
		}
		page, ok := r.managedPage(session.PageID)
		if !ok {
			r.log(slog.LevelDebug, "target session binding page not found", "session_id", session.SessionID, "target_id", session.TargetID, "page_id", session.PageID, "binding", data.Name)
			return
		}
		handle := func() {
			if !r.isCurrentManagedPage(page) {
				return
			}
			if err := r.handleBindingCalledWithContext(BindingContext{
				Page:       page,
				Manager:    r,
				SessionID:  session.SessionID,
				TargetID:   session.TargetID,
				TargetType: session.Type,
			}, &data); err != nil {
				r.log(slog.LevelDebug, "target session binding handler failed", "session_id", session.SessionID, "target_id", session.TargetID, "binding", data.Name, "error", err)
			}
		}
		if bindingCallKind(data.Payload) == "notify" {
			page.enqueueBindingNotification(handle)
		} else {
			syncutil.Go(r.Logger(), handle)
		}
	}
}
