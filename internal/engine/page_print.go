package engine

import (
	"context"
	"errors"
	"strings"
	"time"

	"gopkg.d7z.net/cdp/internal/syncutil"
)

func (p *Page) ExpectPrint(ctx context.Context) (chan printResult, error) {
	if err := p.checkConn(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := p.ensureMainRuntime(ctx); err != nil {
		return nil, err
	}
	p.printMu.Lock()
	defer p.printMu.Unlock()
	if p.nextPrintWaiter != nil {
		return nil, errors.New("a print watcher is already active")
	}
	ch := make(chan printResult, 1)
	p.nextPrintWaiter = ch
	p.startPrintRequestPoller(ctx, ch)
	return ch, nil
}

func (p *Page) takeNextPrintWaiter() chan printResult {
	p.printMu.Lock()
	defer p.printMu.Unlock()
	waiter := p.nextPrintWaiter
	p.nextPrintWaiter = nil
	return waiter
}

func (p *Page) handlePrintRequested(ctx context.Context) error {
	waiter := p.takeNextPrintWaiter()
	if waiter == nil {
		return nil
	}
	artifact, err := p.PrintToPDF(ctx)
	waiter <- printResult{Artifact: artifact, Err: err}
	close(waiter)
	return err
}

func (p *Page) setFullscreenState(active bool, element string) {
	if p == nil {
		return
	}
	p.fullscreenMu.Lock()
	p.fullscreenActive = active
	if active {
		p.fullscreenElement = strings.TrimSpace(element)
	} else {
		p.fullscreenElement = ""
	}
	p.fullscreenMu.Unlock()
}

func (p *Page) IsFullscreen() bool {
	if p == nil {
		return false
	}
	if p.manager != nil && p.ensureMainRuntime(p.ctx) == nil {
		if active, ok := p.queryMainRuntimeFullscreen(); ok {
			p.setFullscreenState(active, "")
			return active
		}
	}
	p.fullscreenMu.RLock()
	active := p.fullscreenActive
	p.fullscreenMu.RUnlock()
	return active
}

func (p *Page) ensureMainRuntime(ctx context.Context) error {
	if p == nil || p.manager == nil {
		return ErrBrowserClosed
	}
	return p.manager.EnsureInitScript(ctx, p.ID, "main_runtime.js")
}

func (p *Page) queryMainRuntimeFullscreen() (bool, bool) {
	if p == nil || p.manager == nil {
		return false, false
	}
	call, err := mainRuntimeMethodCall(p.manager.runtimeFields.MainRuntime, "fullscreenState")
	if err != nil {
		return false, false
	}
	expr := `(() => {
			const state = ` + call + `;
			return !!state?.active;
		})()`
	result, err := p.evalfResult("%s", expr)
	if err != nil {
		return false, false
	}
	value, ok := SafeGet[bool](result, "result", "value")
	return value, ok
}

func (p *Page) startPrintRequestPoller(ctx context.Context, expected chan printResult) {
	syncutil.Go(func() {
		for ctx.Err() == nil {
			p.printMu.Lock()
			waiter := p.nextPrintWaiter
			p.printMu.Unlock()
			if waiter != expected {
				return
			}
			call, buildErr := mainRuntimeMethodCall(p.manager.runtimeFields.MainRuntime, "consumePrintRequest")
			if buildErr != nil {
				return
			}
			result, err := p.evalfResultContext(ctx, "%s", `(() => !!`+call+`)()`)
			if err == nil {
				if consumed, ok := SafeGet[bool](result, "result", "value"); ok && consumed {
					_ = p.handlePrintRequested(ctx)
					return
				}
			}
			if waitInputDelay(ctx, 50*time.Millisecond) != nil {
				return
			}
		}
	})
}

func (p *Page) CancelPrintWait(ch chan printResult) {
	p.printMu.Lock()
	defer p.printMu.Unlock()
	if p.nextPrintWaiter == ch {
		p.nextPrintWaiter = nil
	}
}
