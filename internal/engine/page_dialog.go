package engine

import (
	"context"
)

func (p *Page) setNextDialogHandler(accept bool, promptText string) {
	p.dialogLock.Lock()
	defer p.dialogLock.Unlock()
	p.nextDialogHandler = &dialogHandler{
		accept:     accept,
		promptText: promptText,
	}
}

func (p *Page) takeNextDialogHandler() *dialogHandler {
	p.dialogLock.Lock()
	defer p.dialogLock.Unlock()
	handler := p.nextDialogHandler
	p.nextDialogHandler = nil
	return handler
}

func (p *Page) nextDialogHandlerOrDefault() dialogHandler {
	handler := p.takeNextDialogHandler()
	if handler == nil {
		return dialogHandler{accept: false}
	}
	return *handler
}

func normalizeJavaScriptDialogPolicy(policy JavaScriptDialogPolicy) JavaScriptDialogPolicy {
	switch policy {
	case JavaScriptDialogPolicyPassthrough:
		return JavaScriptDialogPolicyPassthrough
	default:
		return JavaScriptDialogPolicyAutoHandle
	}
}

func normalizePrintPolicy(policy PrintPolicy) PrintPolicy {
	switch policy {
	case PrintPolicyPassthrough:
		return PrintPolicyPassthrough
	default:
		return PrintPolicyIntercept
	}
}

func (p *Page) SetJavaScriptDialogPolicy(policy JavaScriptDialogPolicy) {
	if p == nil {
		return
	}
	p.dialogPolicyMu.Lock()
	p.dialogPolicy = normalizeJavaScriptDialogPolicy(policy)
	p.dialogPolicyMu.Unlock()
}

func (p *Page) JavaScriptDialogPolicy() JavaScriptDialogPolicy {
	if p == nil {
		return JavaScriptDialogPolicyAutoHandle
	}
	p.dialogPolicyMu.RLock()
	policy := p.dialogPolicy
	p.dialogPolicyMu.RUnlock()
	return normalizeJavaScriptDialogPolicy(policy)
}

func (p *Page) SetPrintPolicy(policy PrintPolicy) {
	if p == nil {
		return
	}
	p.printPolicyMu.Lock()
	p.printPolicy = normalizePrintPolicy(policy)
	p.printPolicyMu.Unlock()
}

func (p *Page) PrintPolicy() PrintPolicy {
	if p == nil {
		return PrintPolicyIntercept
	}
	p.printPolicyMu.RLock()
	policy := p.printPolicy
	p.printPolicyMu.RUnlock()
	return normalizePrintPolicy(policy)
}

func (p *Page) handleJavaScriptDialogOpening(params map[string]any) error {
	p.dialogLock.Lock()
	p.dialogSequence++
	p.currentDialog = &JavaScriptDialog{
		ID:         p.dialogSequence,
		Type:       readString(params["type"]),
		Message:    readString(params["message"]),
		Default:    readString(params["defaultPrompt"]),
		HasHandler: params["hasBrowserHandler"] == true,
	}
	p.signalJavaScriptDialogChangeLocked()
	p.dialogLock.Unlock()
	if p.JavaScriptDialogPolicy() == JavaScriptDialogPolicyPassthrough {
		return nil
	}
	handler := p.nextDialogHandlerOrDefault()
	return p.PageHandleJavaScriptDialog(handler.accept, handler.promptText)
}

func (p *Page) handleJavaScriptDialogClosed() {
	p.dialogLock.Lock()
	p.currentDialog = nil
	p.signalJavaScriptDialogChangeLocked()
	p.dialogLock.Unlock()
}

func (p *Page) signalJavaScriptDialogChangeLocked() {
	if p.dialogChanged != nil {
		close(p.dialogChanged)
	}
	p.dialogChanged = make(chan struct{})
}

func (p *Page) javascriptDialogSnapshot() (JavaScriptDialog, bool, <-chan struct{}) {
	p.dialogLock.Lock()
	defer p.dialogLock.Unlock()
	if p.dialogChanged == nil {
		p.dialogChanged = make(chan struct{})
	}
	if p.currentDialog == nil {
		return JavaScriptDialog{}, false, p.dialogChanged
	}
	return *p.currentDialog, true, p.dialogChanged
}

func (p *Page) CurrentJavaScriptDialog() (JavaScriptDialog, bool) {
	if p == nil {
		return JavaScriptDialog{}, false
	}
	dialog, open, _ := p.JavaScriptDialogState()
	return dialog, open
}

// JavaScriptDialogState returns one atomic dialog snapshot and a channel that
// closes when that snapshot changes.
func (p *Page) JavaScriptDialogState() (JavaScriptDialog, bool, <-chan struct{}) {
	if p == nil {
		closed := make(chan struct{})
		close(closed)
		return JavaScriptDialog{}, false, closed
	}
	return p.javascriptDialogSnapshot()
}

// HandleJavaScriptDialog handles one exact dialog and waits until it closes or
// is replaced by a subsequent dialog.
func (p *Page) HandleJavaScriptDialog(ctx context.Context, dialogID uint64, accept bool, promptText string) (JavaScriptDialog, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	current, open, _ := p.javascriptDialogSnapshot()
	if !open {
		return JavaScriptDialog{}, false, ErrJavaScriptDialogNotOpen
	}
	if current.ID != dialogID {
		return current, true, ErrJavaScriptDialogChanged
	}
	if err := p.checkConnContext(ctx); err != nil {
		return current, true, err
	}
	p.lock.RLock()
	conn := p.CdpConn
	p.lock.RUnlock()
	if conn == nil {
		return current, true, ErrBrowserClosed
	}
	_, err := conn.SendMessageContext(ctx, "Page.handleJavaScriptDialog", map[string]any{
		"accept":     accept,
		"promptText": promptText,
	})
	if err != nil {
		return current, true, BrowserErrorFromCDP("Page.handleJavaScriptDialog", topPageExecutionTarget(), err)
	}
	for {
		current, open, changed := p.javascriptDialogSnapshot()
		if !open || current.ID != dialogID {
			return current, open, nil
		}
		select {
		case <-ctx.Done():
			return current, true, ctx.Err()
		case <-p.Done():
			return JavaScriptDialog{}, false, ErrBrowserClosed
		case <-changed:
		}
	}
}
