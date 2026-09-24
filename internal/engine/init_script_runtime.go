package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

type initScriptInstallKey struct {
	PageID     string
	SessionID  string
	ScriptName string
	WorldName  string
}

type initScriptInstallState struct {
	Identifier string
}

type initScriptInstallCall struct {
	once       sync.Once
	mu         sync.Mutex
	done       chan struct{}
	err        error
	identifier string
	abandoned  bool
}

type initScriptEnsureKey struct {
	PageID     string
	SessionID  string
	ContextID  int
	ScriptName string
}

type initScriptEnsureCall struct {
	once      sync.Once
	mu        sync.Mutex
	done      chan struct{}
	err       error
	abandoned bool
}

func (c *initScriptInstallCall) finish() {
	c.once.Do(func() {
		close(c.done)
	})
}

func (c *initScriptInstallCall) result() (string, error, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.identifier, c.err, c.abandoned
}

func (c *initScriptInstallCall) setResult(identifier string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.abandoned {
		return
	}
	c.identifier = identifier
	c.err = err
}

func (c *initScriptInstallCall) abandon(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.abandoned = true
	c.err = err
}

func (c *initScriptEnsureCall) finish() {
	c.once.Do(func() {
		close(c.done)
	})
}

func (c *initScriptEnsureCall) result() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *initScriptEnsureCall) setResult(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.abandoned {
		return
	}
	c.err = err
}

func (c *initScriptEnsureCall) abandon(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.abandoned = true
	c.err = err
}

func (r *BrowserManager) installInitScriptOnce(ctx context.Context, key initScriptInstallKey, install func(context.Context) (string, error)) error {
	if r == nil {
		return ErrBrowserClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	key.PageID = strings.TrimSpace(key.PageID)
	key.SessionID = strings.TrimSpace(key.SessionID)
	key.ScriptName = strings.TrimSpace(key.ScriptName)
	key.WorldName = strings.TrimSpace(key.WorldName)
	if key.ScriptName == "" {
		return errors.New("init script name is empty")
	}

	r.initScriptRuntimeMu.Lock()
	if r.initScriptInstalls == nil {
		r.initScriptInstalls = map[initScriptInstallKey]initScriptInstallState{}
	}
	if _, ok := r.initScriptInstalls[key]; ok {
		r.initScriptRuntimeMu.Unlock()
		return nil
	}
	if r.initScriptInstallInflight == nil {
		r.initScriptInstallInflight = map[initScriptInstallKey]*initScriptInstallCall{}
	}
	if call := r.initScriptInstallInflight[key]; call != nil {
		r.initScriptRuntimeMu.Unlock()
		select {
		case <-call.done:
			_, err, _ := call.result()
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	call := &initScriptInstallCall{done: make(chan struct{})}
	r.initScriptInstallInflight[key] = call
	r.initScriptRuntimeMu.Unlock()

	identifier, err := install(ctx)
	call.setResult(identifier, err)

	r.initScriptRuntimeMu.Lock()
	if r.initScriptInstallInflight[key] == call {
		delete(r.initScriptInstallInflight, key)
	}
	identifier, err, abandoned := call.result()
	if err == nil && !abandoned && r.initScriptInstallInflight[key] == nil {
		r.initScriptInstalls[key] = initScriptInstallState{Identifier: identifier}
	}
	call.finish()
	r.initScriptRuntimeMu.Unlock()
	return err
}

func (r *BrowserManager) ensureInitScriptOnce(ctx context.Context, key initScriptEnsureKey, ensure func(context.Context) error) error {
	if r == nil {
		return ErrBrowserClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	key.PageID = strings.TrimSpace(key.PageID)
	key.SessionID = strings.TrimSpace(key.SessionID)
	key.ScriptName = strings.TrimSpace(key.ScriptName)
	if key.ScriptName == "" {
		return errors.New("init script name is empty")
	}

	r.initScriptRuntimeMu.Lock()
	if r.initScriptEnsureInflight == nil {
		r.initScriptEnsureInflight = map[initScriptEnsureKey]*initScriptEnsureCall{}
	}
	if call := r.initScriptEnsureInflight[key]; call != nil {
		r.initScriptRuntimeMu.Unlock()
		select {
		case <-call.done:
			err := call.result()
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	call := &initScriptEnsureCall{done: make(chan struct{})}
	r.initScriptEnsureInflight[key] = call
	r.initScriptRuntimeMu.Unlock()

	err := ensure(ctx)
	call.setResult(err)

	r.initScriptRuntimeMu.Lock()
	if r.initScriptEnsureInflight[key] == call {
		delete(r.initScriptEnsureInflight, key)
	}
	err = call.result()
	call.finish()
	r.initScriptRuntimeMu.Unlock()
	return err
}

func (r *BrowserManager) forgetInitScriptRuntime(pageID, sessionID string) {
	r.forgetInitScriptRuntimeScope(pageID, sessionID, sessionID == "")
}

func (r *BrowserManager) forgetRootInitScriptRuntime(pageID string) {
	r.forgetInitScriptRuntimeScope(pageID, "", false)
}

func (r *BrowserManager) forgetInitScriptRuntimeScope(pageID, sessionID string, allSessions bool) {
	if r == nil {
		return
	}
	pageID = strings.TrimSpace(pageID)
	sessionID = strings.TrimSpace(sessionID)
	r.initScriptRuntimeMu.Lock()
	defer r.initScriptRuntimeMu.Unlock()
	for key := range r.initScriptInstalls {
		if (pageID == "" || key.PageID == pageID) && ((allSessions && sessionID == "") || key.SessionID == sessionID) {
			delete(r.initScriptInstalls, key)
		}
	}
	for key, call := range r.initScriptInstallInflight {
		if (pageID == "" || key.PageID == pageID) && ((allSessions && sessionID == "") || key.SessionID == sessionID) {
			call.abandon(ErrBrowserClosed)
			delete(r.initScriptInstallInflight, key)
			call.finish()
		}
	}
	for key, call := range r.initScriptEnsureInflight {
		if (pageID == "" || key.PageID == pageID) && ((allSessions && sessionID == "") || key.SessionID == sessionID) {
			call.abandon(ErrBrowserClosed)
			delete(r.initScriptEnsureInflight, key)
			call.finish()
		}
	}
}

func (r *BrowserManager) registeredInitScriptsSnapshot() map[initScriptInstallKey]initScriptInstallState {
	r.initScriptRuntimeMu.Lock()
	defer r.initScriptRuntimeMu.Unlock()
	out := make(map[initScriptInstallKey]initScriptInstallState, len(r.initScriptInstalls))
	for key, state := range r.initScriptInstalls {
		out[key] = state
	}
	return out
}

func (r *BrowserManager) registeredInitScriptsSnapshotForPage(pageID string) map[initScriptInstallKey]initScriptInstallState {
	pageID = strings.TrimSpace(pageID)
	snapshot := r.registeredInitScriptsSnapshot()
	if pageID == "" {
		return snapshot
	}
	for key := range snapshot {
		if key.PageID == pageID {
			continue
		}
		session, _ := r.targetSession(key.SessionID)
		if key.SessionID == "" || session == nil || session.PageID != pageID {
			delete(snapshot, key)
		}
	}
	return snapshot
}

func (r *BrowserManager) cleanupRegisteredInitScripts(ctx context.Context) error {
	return r.removeRegisteredInitScripts(ctx, "", nil, false)
}

// removeRegisteredInitScripts removes new-document registrations for one page,
// or every page when pageID is empty. pageOverride keeps failed, unpublished
// pages cleanable through their own still-live CDP connection.
func (r *BrowserManager) removeRegisteredInitScripts(ctx context.Context, pageID string, pageOverride *Page, rootOnly bool) error {
	if r == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	pageID = strings.TrimSpace(pageID)
	var errs []error
	for key, state := range r.registeredInitScriptsSnapshotForPage(pageID) {
		if rootOnly && key.SessionID != "" {
			continue
		}
		if strings.TrimSpace(state.Identifier) == "" {
			continue
		}
		if ctx.Err() != nil {
			break
		}
		params := map[string]any{"identifier": state.Identifier}
		var err error
		if key.SessionID != "" {
			conn, connErr := r.activeConn()
			if connErr != nil {
				err = connErr
			} else {
				err = conn.SendSessionPacket(ctx, key.SessionID, "Page.removeScriptToEvaluateOnNewDocument", params)
				// A detached OOPIF has already discarded its registrations.
				if isSupersededDocumentError(err) {
					err = nil
				}
			}
		} else {
			page := pageOverride
			if page == nil || page.ID != key.PageID {
				page, _ = r.GetPage(key.PageID)
			}
			if page == nil || page.CdpConn == nil {
				err = ErrBrowserClosed
			} else {
				_, err = page.CdpConn.SendMessageContext(ctx, "Page.removeScriptToEvaluateOnNewDocument", params)
			}
		}
		if err == nil {
			r.initScriptRuntimeMu.Lock()
			if current, ok := r.initScriptInstalls[key]; ok && current.Identifier == state.Identifier {
				delete(r.initScriptInstalls, key)
			}
			r.initScriptRuntimeMu.Unlock()
		} else if !errors.Is(err, ErrBrowserClosed) {
			errs = append(errs, fmt.Errorf("remove init script %s page=%s session=%s: %w", key.ScriptName, key.PageID, key.SessionID, err))
		}
	}
	return errors.Join(append(errs, ctx.Err())...)
}
