package engine

import (
	"context"
)

func (r *BrowserManager) JavaScriptDialogPolicy() JavaScriptDialogPolicy {
	if r == nil {
		return JavaScriptDialogPolicyAutoHandle
	}
	r.dialogPolicyMu.RLock()
	policy := r.dialogPolicy
	r.dialogPolicyMu.RUnlock()
	return normalizeJavaScriptDialogPolicy(policy)
}

func (r *BrowserManager) SetJavaScriptDialogPolicyAll(policy JavaScriptDialogPolicy) {
	if r == nil {
		return
	}
	policy = normalizeJavaScriptDialogPolicy(policy)
	r.dialogPolicyMu.Lock()
	r.dialogPolicy = policy
	r.dialogPolicyMu.Unlock()
	for _, page := range r.BoundPages() {
		page.SetJavaScriptDialogPolicy(policy)
	}
}

func (r *BrowserManager) PrintPolicy() PrintPolicy {
	if r == nil {
		return PrintPolicyIntercept
	}
	r.printPolicyMu.RLock()
	policy := r.printPolicy
	r.printPolicyMu.RUnlock()
	return normalizePrintPolicy(policy)
}

func (r *BrowserManager) SetPrintPolicyAll(policy PrintPolicy) {
	if r == nil {
		return
	}
	policy = normalizePrintPolicy(policy)
	r.printPolicyMu.Lock()
	r.printPolicy = policy
	r.printPolicyMu.Unlock()
	for _, page := range r.BoundPages() {
		page.SetPrintPolicy(policy)
	}
}

func (r *BrowserManager) Close(id string) error {
	return r.TargetCloseTarget(id)
}

func (r *BrowserManager) TargetSetDiscoverTargets(discover bool) (<-chan CDPResponse, func(), error) {
	conn, err := r.activeConn()
	if err != nil {
		return nil, func() {}, err
	}
	return conn.Event("Target.setDiscoverTargets", map[string]any{"discover": discover})
}

func (r *BrowserManager) TargetSetAutoAttach(autoAttach, waitForDebuggerOnStart, flatten bool) error {
	conn, err := r.activeConn()
	if err != nil {
		return err
	}
	return conn.SendPacket("Target.setAutoAttach", map[string]any{
		"autoAttach":             autoAttach,
		"waitForDebuggerOnStart": waitForDebuggerOnStart,
		"flatten":                flatten,
	})
}

func (r *BrowserManager) TargetCloseTarget(targetID string) error {
	conn, err := r.activeConn()
	if err != nil {
		return err
	}
	return conn.SendPacket("Target.closeTarget", map[string]any{"targetId": targetID})
}

type ListTarget struct {
	Description         string `json:"description"`
	DevtoolsFrontendURL string `json:"devtoolsFrontendUrl"`
	FaviconURL          string `json:"faviconUrl"`
	ID                  string `json:"id"`
	Title               string `json:"title"`
	Type                string `json:"type"`
	URL                 string `json:"url"`
}

type GoCallContext struct {
	context.Context
	Page               *Page
	Manager            *BrowserManager
	SessionID          string
	TargetID           string
	TargetType         string
	ExecutionContextID int
}
