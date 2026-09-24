package library

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"gopkg.d7z.net/cdp"
)

func expectPanicMessage(run func()) (msg string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			msg = fmt.Sprint(recovered)
		}
	}()
	run()
	return ""
}

func expectPanicError(run func()) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if recoveredErr, ok := recovered.(error); ok {
				err = recoveredErr
				return
			}
			err = fmt.Errorf("%v", recovered)
		}
	}()
	run()
	return nil
}

func TestPageSetContent(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := acquireSession(t).Page()
	mustOK(page.SetContent(context.Background(), `<!doctype html><html><body><div id="msg" data-testid="msg">hello-e2e</div></body></html>`))
	got := must(page.Locator("#msg").TextContent(context.Background()))

	if got != "hello-e2e" {
		t.Fatalf("unexpected text content: %q", got)
	}
}

func TestPageEval(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/eval")
	evalText(page, `window.__fixtureState.status = "updated"; document.querySelector('#app').textContent = "eval-updated";`)
	got := must(page.Locator("#app").TextContent(context.Background()))

	if got != "eval-updated" {
		t.Fatalf("unexpected app text: %q", got)
	}
}

func TestPageEvalValue(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/eval")

	if got := evalValue(page, `return null`); got != "null" {
		t.Fatalf("EvalValue(null) = %q, want %q", got, "null")
	}
	if got := evalValue(page, `return "null"`); got != `"null"` {
		t.Fatalf("EvalValue(\"null\") = %q, want %q", got, `"null"`)
	}
	if got := evalValue(page, `return {status: window.__fixtureState.status, count: window.__fixtureState.count}`); got != `{"count":1,"status":"booted"}` {
		t.Fatalf("EvalValue(object) = %q", got)
	}
	if got := evalValue(page, `return Promise.resolve(window.__fixtureState.count + 1)`); got != "2" {
		t.Fatalf("EvalValue(Promise.resolve) = %q, want %q", got, "2")
	}
	if got := evalValue(page, `
const count = await Promise.resolve(window.__fixtureState.count + 2)
return {status: window.__fixtureState.status, count}
`); got != `{"count":3,"status":"booted"}` {
		t.Fatalf("EvalValue(await object) = %q", got)
	}
	if got := evalValue(page, `
await new Promise((resolve) => setTimeout(resolve, 0))
return undefined
`); got != "null" {
		t.Fatalf("EvalValue(async undefined) = %q, want %q", got, "undefined")
	}
	if got := evalValue(page, `return undefined`); got != "null" {
		t.Fatalf("EvalValue(undefined) = %q, want %q", got, "undefined")
	}
	msg := expectPanicMessage(func() {
		evalValue(page, `await Promise.reject(new Error("page-eval-rejected"))`)
	})
	if msg == "" || !strings.Contains(msg, "page-eval-rejected") {
		t.Fatalf("EvalValue(rejected promise) panic = %q", msg)
	}
	err := expectPanicError(func() {
		evalValue(page, `await Promise.reject(new Error("page-eval-layered"))`)
	})
	var opErr *cdp.OperationError
	if !errors.As(err, &opErr) || opErr.Op != "eval" {
		t.Fatalf("expected page.eval_value OperationError, got %#v", err)
	}
	var browserErr *cdp.BrowserError
	if !errors.As(err, &browserErr) || browserErr.Kind != "javascript_exception" || !strings.Contains(browserErr.Message, "page-eval-layered") {
		t.Fatalf("expected BrowserError javascript_exception cause, got %#v", err)
	}
}

func TestRuntimeContextNamespaceEval(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/eval")
	if got := evalValue(page, `return '__cdp_ffi' in window`); got != "false" {
		t.Fatalf("main world should not expose isolated ffi: %q", got)
	}
	iso := must(page.ExecutionContext(context.Background(), cdp.ExecutionContextOptions{World: cdp.WorldCore}))
	if got := evalValue(iso, `return typeof window.__cdp_ffi?.runtimeInfo === 'function'`); got != "true" {
		t.Fatalf("isolated core ffi unavailable: %q", got)
	}
	if got := evalValue(iso, `return document.querySelector('#app')?.textContent`); got != `"fixture-eval"` {
		t.Fatalf("isolated context should still access DOM: %q", got)
	}
	handle, err := page.ExecutionContext(context.Background(), cdp.ExecutionContextOptions{World: cdp.WorldCore})
	if err != nil {
		t.Fatal(err)
	}
	for expression, want := range map[string]string{
		"return null":                        "null",
		"return undefined":                   "null",
		`return {title: "Report", count: 2}`: `{"count":2,"title":"Report"}`,
	} {
		if got, err := handle.EvalJSON(context.Background(), expression); err != nil || string(got) != want {
			t.Fatalf("runtime JSON %q = %q, %v; want %q", expression, got, err, want)
		}
	}
	if _, err := handle.EvalJSON(context.Background(), `throw new Error("runtime-json-failure")`); err == nil || !strings.Contains(err.Error(), "runtime-json-failure") {
		t.Fatalf("runtime JSON error = %v", err)
	}
}

func TestSelectorEval(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/eval")
	got := evalText(page.Locator("#name"), `return this.value + "-suffix"`)
	if got != "alpha-suffix" {
		t.Fatalf("unexpected selector eval result: %q", got)
	}
}

func TestSelectorEvalValue(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/eval")
	selector := page.Locator("#name")

	if got := evalValue(selector, `return this.value`); got != `"alpha"` {
		t.Fatalf("selector EvalValue(value) = %q, want %q", got, `"alpha"`)
	}
	if got := evalValue(selector, `return {value: this.value, tag: this.tagName}`); got != `{"value":"alpha","tag":"INPUT"}` {
		t.Fatalf("selector EvalValue(object) = %q", got)
	}
	if got := evalValue(selector, `return Promise.resolve(this.value + "-async")`); got != `"alpha-async"` {
		t.Fatalf("selector EvalValue(Promise.resolve) = %q, want %q", got, `"alpha-async"`)
	}
	if got := evalValue(selector, `
const tag = await Promise.resolve(this.tagName)
return {value: this.value, tag}
`); got != `{"value":"alpha","tag":"INPUT"}` {
		t.Fatalf("selector EvalValue(await object) = %q", got)
	}
	if got := evalValue(selector, `
await new Promise((resolve) => setTimeout(resolve, 0))
return undefined
`); got != "null" {
		t.Fatalf("selector EvalValue(async undefined) = %q, want %q", got, "undefined")
	}
	if got := evalValue(selector, `return null`); got != "null" {
		t.Fatalf("selector EvalValue(null) = %q, want %q", got, "null")
	}
	if got := evalValue(selector, `return undefined`); got != "null" {
		t.Fatalf("selector EvalValue(undefined) = %q, want %q", got, "undefined")
	}
	msg := expectPanicMessage(func() {
		evalValue(selector, `await Promise.reject(new Error("selector-eval-rejected"))`)
	})
	if msg == "" || !strings.Contains(msg, "selector-eval-rejected") {
		t.Fatalf("selector EvalValue(rejected promise) panic = %q", msg)
	}
	err := expectPanicError(func() {
		evalValue(selector, `await Promise.reject(new Error("selector-eval-layered"))`)
	})
	var selectorErr *cdp.LocatorError
	if !errors.As(err, &selectorErr) || !strings.Contains(selectorErr.Selector, "#name") || selectorErr.Op != "locator.eval" {
		t.Fatalf("expected LocatorError with selector, got %#v", err)
	}
	var browserErr *cdp.BrowserError
	if !errors.As(err, &browserErr) || browserErr.Kind != "javascript_exception" || !strings.Contains(browserErr.Message, "selector-eval-layered") {
		t.Fatalf("expected BrowserError javascript_exception cause, got %#v", err)
	}
}

func TestSelectorEvalValueNonUnique(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/selector")
	msg := expectPanicMessage(func() {
		evalValue(page.Locator("[data-testid='item']"), `return this.textContent`)
	})
	if msg == "" {
		t.Fatal("EvalValue on non-unique selector did not panic")
	}
}
