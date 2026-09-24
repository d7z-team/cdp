package engine

import (
	"context"
	"strconv"
)

func (p *Page) ExpectDialog(accept bool, promptText string) {
	p.setNextDialogHandler(accept, promptText)
}

func (p *Page) ScrollTo(x, y int) error {
	return p.ScrollToPosition(float64(x), float64(y))
}

func jsNumberLiteral(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func (p *Page) ScrollToPosition(x, y float64) error {
	return p.ScrollToPositionContext(p.ctx, x, y)
}

func (p *Page) ScrollToPositionContext(ctx context.Context, x, y float64) error {
	return p.runForegroundInteractionContext(ctx, func() error {
		return p.EvalfContext(ctx, "window.scrollTo(%s, %s)", jsNumberLiteral(x), jsNumberLiteral(y))
	})
}

func (p *Page) ScrollBy(deltaX, deltaY float64) error {
	return p.ScrollByContext(p.ctx, deltaX, deltaY)
}

func (p *Page) ScrollByContext(ctx context.Context, deltaX, deltaY float64) error {
	return p.runForegroundInteractionContext(ctx, func() error {
		return p.EvalfContext(ctx, "window.scrollBy(%s, %s)", jsNumberLiteral(deltaX), jsNumberLiteral(deltaY))
	})
}

func (p *Page) BringToFront() error {
	return p.Activate()
}

func (p *Page) ActivateContext(ctx context.Context) error {
	return p.runForegroundInteractionContext(ctx, nil)
}

func (p *Page) EnsureMainRuntime(ctx context.Context) error { return p.ensureMainRuntime(ctx) }
