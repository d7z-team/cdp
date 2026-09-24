package library

import (
	"context"
	"strings"
	"testing"
	"time"

	"gopkg.d7z.net/cdp"
)

func TestShadowHostVisibilityAndInteraction(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/mcp-runtime")
	ctx := context.Background()
	host := page.Locator("snapshot-shadow")
	button := host.Locator("button")
	mustOK(host.SetStyle(ctx, "opacity", "0"))
	if visible := must(button.IsVisible(ctx)); visible {
		t.Fatal("shadow button must inherit its host's visibility")
	}
	mustOK(host.SetStyle(ctx, "opacity", "1"))
	if visible := must(button.IsVisible(ctx)); !visible {
		t.Fatal("shadow button should become visible with its host")
	}
	mustOK(button.Click(ctx))
	if got := must(host.Locator("output").TextContent(ctx)); got != "Shadow clicked" {
		t.Fatalf("shadow interaction result: %q", got)
	}
}

func TestSelectorGetAttribute(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/props")
	el := page.ByTestID("attr-full")
	if val := attribute(el, "id"); val != "attr-id" {
		t.Fatalf("GetAttribute(id) = %q, want attr-id", val)
	}
	if val := attribute(el, "data-custom"); val != "myval" {
		t.Fatalf("GetAttribute(data-custom) = %q, want myval", val)
	}
}

func TestSelectorGetAttributeMissing(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/props")
	el := page.ByTestID("attr-empty")
	if val := attribute(el, "data-nonexistent"); val != "" {
		t.Fatalf("GetAttribute missing = %q, want empty", val)
	}
}

func TestSelectorHasAttribute(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/props")
	el := page.ByTestID("attr-full")
	if !must(el.HasAttribute(context.Background(), "id")) {
		t.Fatal("HasAttribute(id) should be true")
	}
	if must(el.HasAttribute(context.Background(), "nonexistent")) {
		t.Fatal("HasAttribute(nonexistent) should be false")
	}
}

func TestSelectorGetStyle(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/props")
	el := page.ByTestID("style-inline")
	if val := must(el.Style(context.Background(), "color")); val != "rgb(255, 0, 0)" {
		t.Fatalf("GetStyle(color) = %q, want rgb(255, 0, 0)", val)
	}
	if val := must(el.Style(context.Background(), "font-size")); val != "20px" {
		t.Fatalf("GetStyle(font-size) = %q, want 20px", val)
	}
}

func TestSelectorGetStyleComputed(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/props")
	el := page.ByTestID("style-computed")
	val := must(el.Style(context.Background(), "background-color"))
	if val == "" {
		t.Fatal("GetStyle(background-color) should not be empty")
	}
}

func TestSelectorHasStyle(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/props")
	el := page.ByTestID("style-inline")
	if must(el.Style(context.Background(), "color")) == "" {
		t.Fatal("HasStyle(color) should be true")
	}
	if must(el.Style(context.Background(), "nonexistent-prop")) != "" {
		t.Fatal("HasStyle(nonexistent) should be false")
	}
}

func TestSelectorSetStyle(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/props")
	el := page.ByTestID("set-style-target")
	mustOK(el.SetStyle(context.Background(), "color", "rgb(255, 0, 0)"))
	if val := must(el.Style(context.Background(), "color")); val != "rgb(255, 0, 0)" {
		t.Fatalf("SetStyle(color) then Get = %q, want rgb(255, 0, 0)", val)
	}
}

func TestSelectorSetCSS(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/props")
	el := page.ByTestID("set-style-target")
	mustOK(el.SetStyle(context.Background(), "background-color", "rgb(255, 255, 0)"))
	if val := must(el.Style(context.Background(), "background-color")); val != "rgb(255, 255, 0)" {
		t.Fatalf("SetCSS then Get = %q, want rgb(255, 255, 0)", val)
	}
}

func TestSelectorSetStyles(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/props")
	el := page.ByTestID("set-styles-target")
	mustOK(el.SetStyles(context.Background(), map[string]string{
		"color":      "rgb(255, 0, 0)",
		"font-size":  "24px",
		"background": "rgb(0, 255, 0)",
	}))
	if val := must(el.Style(context.Background(), "color")); val != "rgb(255, 0, 0)" {
		t.Fatalf("SetStyles color = %q", val)
	}
	if val := must(el.Style(context.Background(), "font-size")); val != "24px" {
		t.Fatalf("SetStyles font-size = %q", val)
	}
}

func TestSelectorRemoveStyle(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/props")
	el := page.ByTestID("remove-style-target")
	orig := must(el.Style(context.Background(), "color"))
	if orig == "" {
		t.Fatal("element should have color style initially")
	}
	mustOK(el.RemoveStyle(context.Background(), "color"))
	after := must(el.Style(context.Background(), "color"))
	if after == orig {
		t.Fatalf("RemoveStyle(color) should change color, was %q, still %q", orig, after)
	}
}

func TestSelectorHasClass(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/props")
	el := page.ByTestID("has-class-div")
	if !must(el.HasClass(context.Background(), "active")) {
		t.Fatal("HasClass(active) should be true")
	}
	if !must(el.HasClass(context.Background(), "highlighted")) {
		t.Fatal("HasClass(highlighted) should be true")
	}
	if must(el.HasClass(context.Background(), "nonexistent")) {
		t.Fatal("HasClass(nonexistent) should be false")
	}
}

func TestSelectorGetHTML(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/props")
	el := page.ByTestID("html-parent")
	html := must(el.HTML(context.Background()))

	if !strings.Contains(html, "nested") {
		t.Fatalf("GetHTML should contain nested text, got: %s", html)
	}
	if !strings.Contains(html, "<b>bold</b>") {
		t.Fatalf("GetHTML should contain <b> tag, got: %s", html)
	}
}

func TestSelectorSetTextContent(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/props")
	el := page.ByTestID("set-text-target")
	mustOK(el.SetTextContent(context.Background(), "new content"))
	if text := must(el.TextContent(context.Background())); text != "new content" {
		t.Fatalf("SetTextContent then TextContent = %q, want new content", text)
	}
}

func TestSelectorSetText(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/props")
	el := page.ByTestID("set-text-alias")
	mustOK(el.SetTextContent(context.Background(), "alias text"))
	if text := must(el.TextContent(context.Background())); text != "alias text" {
		t.Fatalf("SetText then TextContent = %q, want alias text", text)
	}
}

func TestSelectorExists(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/props")
	if !must(page.ByTestID("always-there").Exists(context.Background())) {
		t.Fatal("Exists should be true for permanent element")
	}
	if must(page.Locator("#nonexistent-id-12345").Exists(context.Background())) {
		t.Fatal("Exists should be false for nonexistent element")
	}
}

func TestSelectorExistsAfterRemove(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/props")
	time.Sleep(time.Duration(500) *
		time.Millisecond)

	if must(page.ByTestID("temp-exists").Exists(context.Background())) {
		t.Fatal("Exists should be false after element removed from DOM")
	}
}

func TestLocatorAllElements(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/props")
	ids := must(page.Locator(".bnid-item").All(context.Background()))
	if len(ids) != 3 {
		t.Fatalf("BackendNodeIDs count = %d, want 3", len(ids))
	}
	for _, id := range ids {
		if tag := evalText(id, "return this.tagName"); tag == "" {
			t.Fatalf("BackendNodeID should be > 0, got %v", id)
		}
	}
}

func TestLocatorAllElementsWithIframeContexts(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/screenshot-scroll-stitch")
	if got := must(page.Locator(".page-marker").Count(context.Background())); got != 4 {
		t.Fatalf("page marker count = %d, want 4", got)
	}
	ids := must(page.Locator(".page-marker").All(context.Background()))
	if len(ids) != 4 {
		t.Fatalf("BackendNodeIDs count = %d, want 4", len(ids))
	}
	for _, id := range ids {
		if tag := evalText(id, "return this.tagName"); tag == "" {
			t.Fatalf("BackendNodeID should be > 0, got %v", id)
		}
	}
}

func TestSelectorGetByPlaceholder(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/props")
	el := page.ByPlaceholder("Enter your name", cdp.TextOptions{})
	if !must(el.Exists(context.Background())) {
		t.Fatal("GetByPlaceholder should find element")
	}
}

func TestSelectorGetByPlaceholderExact(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/props")
	el := page.ByPlaceholder("Exact Match", cdp.TextOptions{Exact: true})
	ex := must(el.Exists(context.Background()))

	t.Logf("GetByPlaceholder exact exists=%v", ex)
}

func TestSelectorNth(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/props")
	el := page.Locator("[data-testid=index-list] .idx-item").Nth(0)
	if text := must(el.TextContent(context.Background())); !strings.Contains(text, "Item 0") {
		t.Fatalf("At(0) = %q, want Item 0", text)
	}
	el = page.Locator("[data-testid=index-list] .idx-item").Nth(2)
	if text := must(el.TextContent(context.Background())); !strings.Contains(text, "Item 2") {
		t.Fatalf("At(2) = %q, want Item 2", text)
	}
}

func TestSelectorAtLast(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/props")
	el := page.Locator("[data-testid=index-list] .idx-item").Last()
	if text := must(el.TextContent(context.Background())); !strings.Contains(text, "Item 3") {
		t.Fatalf("Last() = %q, want Item 3", text)
	}
}

func TestSelectorWaitFor(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/props")
	el := page.ByTestID("will-appear")
	mustOK(el.Wait(context.Background(), cdp.StateVisible))

	if !must(el.IsVisible(context.Background())) {
		t.Fatal("WaitFor should wait until element is visible")
	}
}

func TestSelectorWaitForDisappear(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/props")
	el := page.ByTestID("will-disappear")
	mustOK(el.Wait(context.Background(), cdp.StateDetached))

	if must(el.Exists(context.Background())) {
		t.Fatal("WaitForDetached should wait until element is removed")
	}
}
