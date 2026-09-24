package engine

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
)

func (p *Page) RuntimeEvaluateContext(ctx context.Context, expression string, returnByValue bool) (map[string]any, error) {
	return p.RuntimeEvaluateOnTarget(ctx, topPageExecutionTarget(), expression, returnByValue)
}

func (p *Page) PageNavigate(url string) (map[string]any, error) {
	res, err := p.CdpConn.SendMessage("Page.navigate", map[string]any{"url": url})
	return res, BrowserErrorFromCDP("Page.navigate", topPageExecutionTarget(), err)
}

func (p *Page) PageGetNavigationHistory() (map[string]any, error) {
	res, err := p.CdpConn.SendMessage("Page.getNavigationHistory", nil)
	return res, BrowserErrorFromCDP("Page.getNavigationHistory", topPageExecutionTarget(), err)
}

func (p *Page) PageReload() error {
	return BrowserErrorFromCDP("Page.reload", topPageExecutionTarget(), p.CdpConn.SendPacket("Page.reload", nil))
}

func (p *Page) PrintToPDF(ctx context.Context) (PrintArtifact, error) {
	p.lock.RLock()
	defer p.lock.RUnlock()
	if err := p.checkConn(); err != nil {
		return PrintArtifact{}, err
	}
	result, err := p.CdpConn.SendMessageContext(ctx, "Page.printToPDF", map[string]any{
		"printBackground": true,
	})
	if err != nil {
		return PrintArtifact{}, BrowserErrorFromCDP("Page.printToPDF", topPageExecutionTarget(), err)
	}
	data, ok := SafeGet[string](result, "data")
	if !ok || strings.TrimSpace(data) == "" {
		return PrintArtifact{}, fmt.Errorf("printToPDF missing data: %v", result)
	}
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return PrintArtifact{}, err
	}
	urlText := ""
	titleText := ""
	if currentURL, urlErr := p.GetURL(); urlErr == nil {
		urlText = currentURL
	}
	if currentTitle, titleErr := p.GetTitle(); titleErr == nil {
		titleText = currentTitle
	}
	return PrintArtifact{
		Data:      decoded,
		MimeType:  "application/pdf",
		URL:       urlText,
		Title:     titleText,
		Timestamp: time.Now(),
	}, nil
}

func (p *Page) PageHandleJavaScriptDialog(accept bool, promptText string) error {
	err := p.CdpConn.SendPacket("Page.handleJavaScriptDialog", map[string]any{
		"accept":     accept,
		"promptText": promptText,
	})
	return BrowserErrorFromCDP("Page.handleJavaScriptDialog", topPageExecutionTarget(), err)
}

func (p *Page) pageBringToFront(ctx context.Context) error {
	return BrowserErrorFromCDP("Page.bringToFront", ExecutionTarget{TargetID: p.ID}, p.CdpConn.SendPacketContext(ctx, "Page.bringToFront", nil))
}

func (p *Page) activatePageTarget(ctx context.Context) error {
	if p == nil || p.manager == nil {
		return ErrBrowserClosed
	}
	if err := p.checkConn(); err != nil {
		return err
	}
	if err := p.manager.activateTarget(ctx, p.ID); err != nil {
		return err
	}
	if err := p.pageBringToFront(ctx); err != nil {
		return err
	}
	p.manager.setActivePage(p.ID, LifecycleSourceManager, "page_activate")
	return nil
}

func (p *Page) runForegroundInteraction(action func() error) error {
	return p.runForegroundInteractionContext(p.ctx, action)
}

func (p *Page) runForegroundInteractionContext(ctx context.Context, action func() error) error {
	if p == nil || p.manager == nil {
		return ErrBrowserClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	state := p.manager.serviceStateRef()
	release, err := state.acquireForeground(ctx, p.Done())
	if err != nil {
		return err
	}
	defer release()
	if err := p.activatePageTarget(ctx); err != nil {
		return err
	}
	if action == nil {
		return nil
	}
	return action()
}

func (p *Page) Activate() error {
	return p.runForegroundInteraction(nil)
}
