package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"gopkg.d7z.net/cdp/internal/binding"
)

type BindingHandler interface {
	Name() string
	Handle(ctx BindingContext, event *binding.BindingCalledEvent) error
}

type BindingContext struct {
	Context            context.Context
	Page               *Page
	Manager            *BrowserManager
	SessionID          string
	TargetID           string
	TargetType         string
	ExecutionContextID int
}

func (r *BrowserManager) RegisterBinding(h BindingHandler) error {
	return r.RegisterBindingInNamespace(NamespaceIsolatedCore, h)
}

func (r *BrowserManager) RegisterBindingInNamespace(namespace RuntimeNamespace, h BindingHandler) error {
	if r == nil {
		return errors.New("browser manager is nil")
	}
	if h == nil {
		return errors.New("binding handler is nil")
	}
	name := strings.TrimSpace(h.Name())
	if name == "" {
		return errors.New("binding name is empty")
	}

	r.bindingMu.Lock()
	if r.bindingHandlers == nil {
		r.bindingHandlers = map[string]BindingHandler{}
	}
	if r.bindingNamespaces == nil {
		r.bindingNamespaces = map[string]RuntimeNamespace{}
	}
	if _, exists := r.bindingHandlers[name]; exists {
		r.bindingMu.Unlock()
		return errors.New("binding already registered: " + name)
	}
	namespace = normalizeRuntimeNamespace(namespace)
	r.bindingHandlers[name] = h
	r.bindingNamespaces[name] = namespace
	r.bindingMu.Unlock()
	for _, page := range r.managedPagesSnapshot() {
		if page == nil || page.waitInitContext(r.ctx) != nil {
			continue
		}
		if err := page.addBinding(name, namespace); err != nil {
			slog.Warn("register binding on page failed", "binding", name, "page_id", page.ID, "error", err)
		}
	}
	for _, session := range r.targetSessionsSnapshot() {
		if session.Type != "iframe" {
			continue
		}
		if err := r.addBindingToTargetSession(r.ctx, session.SessionID, name, namespace); err != nil {
			slog.Warn("register binding on target session failed", "binding", name, "session_id", session.SessionID, "target_id", session.TargetID, "target_type", session.Type, "error", err)
		}
	}
	return nil
}

func (r *BrowserManager) registerBindings(ctx context.Context, page *Page) error {
	if page == nil {
		return errors.New("page is nil")
	}
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
	for _, name := range names {
		if err := page.addBindingContext(ctx, name, registrations[name]); err != nil {
			return err
		}
	}
	return nil
}

func (r *BrowserManager) reconcilePageRegistrations(ctx context.Context, page *Page) error {
	if r == nil || page == nil || !r.isCurrentManagedPage(page) {
		return nil
	}
	return r.reconcileDocumentRuntime(ctx, page, "")
}

func (r *BrowserManager) registerPageBindingsAndScripts(ctx context.Context, page *Page, action string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := r.registerBindings(ctx, page); err != nil {
		return fmt.Errorf("register bindings: %w", err)
	}
	for _, script := range r.initScriptsSnapshot() {
		if err := page.registerInitScriptToEvaluateOnNewDocument(ctx, script, action); err != nil {
			return fmt.Errorf("register init script %s: %w", script.Name, err)
		}
	}
	return nil
}

func (r *BrowserManager) addBindingOnConn(ctx context.Context, conn *CdpConn, sessionID string, name string, namespace RuntimeNamespace) error {
	if conn == nil {
		return ErrBrowserClosed
	}
	params := map[string]any{"name": name}
	params["executionContextName"] = r.namespaceWorldName(namespace)
	if sessionID != "" {
		return conn.SendSessionPacket(ctx, sessionID, "Runtime.addBinding", params)
	}
	_, err := conn.SendMessageContext(ctx, "Runtime.addBinding", params)
	return err
}

func (r *BrowserManager) handleBindingCalled(p *Page, event *binding.BindingCalledEvent) error {
	return r.handleBindingCalledWithContext(BindingContext{
		Page:    p,
		Manager: r,
	}, event)
}

func (r *BrowserManager) handleBindingCalledWithContext(ctx BindingContext, event *binding.BindingCalledEvent) error {
	r.bindingMu.RLock()
	handler, ok := r.bindingHandlers[event.Name]
	r.bindingMu.RUnlock()
	if !ok {
		return nil
	}
	ctx.Manager = r
	ctx.ExecutionContextID = event.ExecutionContextID
	return handler.Handle(ctx, event)
}

func (r *BrowserManager) RegisterInitScript(script InitScript) error {
	if r == nil {
		return errors.New("browser manager is nil")
	}
	script.Name = strings.TrimSpace(script.Name)
	if script.Name == "" {
		return errors.New("script name is empty")
	}
	script.Namespace = normalizeRuntimeNamespace(script.Namespace)
	if strings.TrimSpace(script.Exec) == "" {
		return errors.New("script is empty")
	}

	r.scriptMu.Lock()
	if r.initScriptsByName == nil {
		r.initScriptsByName = map[string]initScriptRegistration{}
	}
	if _, exists := r.initScriptsByName[script.Name]; exists {
		r.scriptMu.Unlock()
		return errors.New("init script already registered: " + script.Name)
	}
	r.initScriptsByName[script.Name] = initScriptRegistration{
		script: script,
	}
	r.initScripts = append(r.initScripts, script)
	r.scriptMu.Unlock()
	if script.OnDemand {
		return nil
	}
	for _, page := range r.managedPagesSnapshot() {
		if page == nil || page.waitInitContext(r.ctx) != nil {
			continue
		}
		page.lock.RLock()
		err := page.addInitScriptToEvaluateOnNewDocument(script, "register_init_new_doc")
		page.lock.RUnlock()
		if err != nil {
			slog.Warn("register init script for new document failed", "script", script.Name, "page_id", page.ID, "error", err)
		}
		if err := page.ensureInitScriptInjectedInRuntimeContexts(context.Background(), script, "register_init"); err != nil {
			slog.Warn("inject init script into page failed", "script", script.Name, "page_id", page.ID, "error", err)
		}
	}
	for _, session := range r.targetSessionsSnapshot() {
		if session.Type != "iframe" {
			continue
		}
		if err := r.addInitScriptToTargetSession(r.ctx, session.SessionID, script, "register_init_new_doc"); err != nil {
			slog.Warn("register init script on target session failed", "script", script.Name, "session_id", session.SessionID, "target_id", session.TargetID, "target_type", session.Type, "error", err)
		}
	}
	return nil
}

// EnsureInitScript waits for pageID to bind and ensures a previously
// registered init script is available in the page's current document.
func (r *BrowserManager) EnsureInitScript(ctx context.Context, pageID, scriptName string) error {
	if r == nil {
		return ErrBrowserClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	pageID = strings.TrimSpace(pageID)
	scriptName = strings.TrimSpace(scriptName)
	if pageID == "" {
		return errors.New("page id is empty")
	}
	if scriptName == "" {
		return errors.New("init script name is empty")
	}

	r.scriptMu.RLock()
	registration, ok := r.initScriptsByName[scriptName]
	r.scriptMu.RUnlock()
	if !ok {
		return fmt.Errorf("init script not registered: %s", scriptName)
	}
	page, err := r.LoadPageContext(ctx, pageID)
	if err != nil {
		return err
	}
	if page == nil {
		return fmt.Errorf("page not available: %s", pageID)
	}
	if err := page.checkConnContext(ctx); err != nil {
		return fmt.Errorf("wait for page %s binding: %w", pageID, err)
	}
	if registration.script.OnDemand {
		if err := page.registerInitScriptToEvaluateOnNewDocument(ctx, registration.script, "ensure_init"); err != nil {
			return err
		}
	}
	if err := page.ensureInitScriptInjectedInRuntimeContexts(ctx, registration.script, "ensure_init"); err != nil {
		return fmt.Errorf("ensure init script %s on page %s: %w", scriptName, pageID, err)
	}
	return nil
}

func (r *BrowserManager) initScriptsSnapshot() []InitScript {
	r.scriptMu.RLock()
	scripts := make([]InitScript, 0, len(r.initScripts))
	for _, script := range r.initScripts {
		if !script.OnDemand {
			scripts = append(scripts, script)
		}
	}
	r.scriptMu.RUnlock()
	return scripts
}

func (r *BrowserManager) cleanupScriptsSnapshot() []InitScript {
	r.scriptMu.RLock()
	defer r.scriptMu.RUnlock()
	return append([]InitScript(nil), r.initScripts...)
}
