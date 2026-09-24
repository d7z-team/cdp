package library

import (
	"context"
	"encoding/json"
	"testing"

	"gopkg.d7z.net/cdp"
)

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
func mustOK(err error) {
	if err != nil {
		panic(err)
	}
}

type evaluator interface {
	EvalJSON(context.Context, string) (json.RawMessage, error)
}

func evalValue(e evaluator, script string) string {
	return string(must(e.EvalJSON(context.Background(), script)))
}
func evalText(e evaluator, script string) string {
	raw := must(e.EvalJSON(context.Background(), script))
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	return string(raw)
}
func attribute(l interface {
	Attribute(context.Context, string) (string, bool, error)
}, name string) string {
	v, _, err := l.Attribute(context.Background(), name)
	mustOK(err)
	return v
}
func waitLocator(l *cdp.Locator, state cdp.ElementState) *cdp.Locator {
	mustOK(l.Wait(context.Background(), state))
	return l
}
func mustPanic(t *testing.T) {
	t.Helper()
	if r := recover(); r == nil {
		t.Fatal("expected failing operation")
	}
}

func configuredPage(t *testing.T, page *cdp.Page, opts cdp.ConnectOptions) *cdp.Page {
	t.Helper()
	browser := must(cdp.Connect(context.Background(), execBrowser.APIBrowser.Endpoint(), opts))
	t.Cleanup(func() { mustOK(browser.Close()) })
	return must(browser.Page(context.Background(), page.ID()))
}

type metrics struct {
	CSSContentSize struct{ Width, Height float64 } `json:"cssContentSize"`
	ContentSize    struct{ Width, Height float64 } `json:"contentSize"`
	VisualViewport struct {
		Scale float64 `json:"scale"`
	} `json:"visualViewport"`
}

func layoutMetrics(p *cdp.Page) (metrics, error) {
	var result metrics
	err := p.Session().Call(context.Background(), "Page.getLayoutMetrics", nil, &result)
	return result, err
}
