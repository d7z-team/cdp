package library

import (
	"context"
	"testing"

	"gopkg.d7z.net/cdp"
)

func TestGetByText(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/accessibility")
	text := must(page.ByText("Hello Runner", cdp.TextOptions{}).TextContent(context.Background()))

	if text != "Hello Runner" {
		t.Fatalf("unexpected get-by-text result: %q", text)
	}
}

func TestGetByRole(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/accessibility")
	text := must(page.ByRole("button", cdp.RoleOptions{Name: "Save changes"}).TextContent(context.Background()))

	if text != "Save" {
		t.Fatalf("unexpected get-by-role result: %q", text)
	}
}

func TestGetByLabel(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/accessibility")
	value := must(page.ByLabel("Email", cdp.TextOptions{}).Value(context.Background()))

	if value != "user@example.com" {
		t.Fatalf("unexpected get-by-label value: %q", value)
	}
}
