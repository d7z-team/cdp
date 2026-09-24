package library

import (
	"context"
	"strings"
	"testing"
	"time"

	"gopkg.d7z.net/cdp"
)

func TestCookiesPreserveDuplicateNamesAndAttributes(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	session := acquireSession(t)
	page := must(session.Open("/cookies"))
	ctx := context.Background()
	mustOK(execBrowser.APIBrowser.ClearCookies(ctx))
	cookies := []cdp.Cookie{
		{Name: "duplicate", Value: "root", URL: session.FixtureURL("/"), Path: "/", SameSite: "Lax"},
		{Name: "duplicate", Value: "nested", URL: session.FixtureURL("/cookies"), Path: "/cookies", SameSite: "Strict", HTTPOnly: true, Expires: time.Date(2038, 1, 19, 0, 0, 0, 0, time.UTC)},
	}
	mustOK(page.SetCookies(ctx, cookies))
	got := must(page.Cookies(ctx))
	if len(got) != 2 {
		t.Fatalf("duplicate names lost: %+v", got)
	}
	for _, c := range got {
		if c.Domain == "" || c.Size <= 0 {
			t.Fatalf("missing metadata: %+v", c)
		}
		if c.Path == "/cookies" && (!c.HTTPOnly || c.Session || c.Expires.IsZero() || c.SameSite != "Strict") {
			t.Fatalf("cookie attributes lost: %+v", c)
		}
	}
	visible := evalText(page, "return document.cookie")
	if !strings.Contains(visible, "duplicate=root") || strings.Contains(visible, "nested") {
		t.Fatalf("HTTPOnly semantics: %s", visible)
	}
	mustOK(execBrowser.APIBrowser.ClearCookies(ctx))
	if got := must(page.Cookies(ctx)); len(got) != 0 {
		t.Fatalf("cookies not cleared: %+v", got)
	}
}

func TestClearStorage(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	session := acquireSession(t)
	page := must(session.Open("/cookies"))
	evalText(page, `localStorage.setItem("test","value")`)
	if evalText(page, `return localStorage.getItem("test")`) != "value" {
		t.Fatal("storage was not set")
	}
	mustOK(page.ClearStorage(context.Background(), session.FixtureURL("/")))
	if evalValue(page, `return localStorage.getItem("test")`) != "null" {
		t.Fatal("storage not cleared")
	}
}
