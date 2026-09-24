package library

import (
	"context"
	"testing"
)

func TestDialogDefaultDismissConfirm(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/dialog")
	mustOK(page.ByTestID("confirm-open").Click(context.Background()))

	got := must(page.ByTestID("confirm-result").TextContent(context.Background()))

	if got != "dismissed" {
		t.Fatalf("unexpected confirm result without handler: %q", got)
	}
}

func TestDialogPromptAcceptOnce(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/dialog")
	page.ExpectDialog(true, "sample-value")
	mustOK(page.ByTestID("prompt-open").Click(context.Background()))

	first := must(page.ByTestID("prompt-result").TextContent(context.Background()))

	if first != "sample-value" {
		t.Fatalf("unexpected first prompt result: %q", first)
	}
	count := must(page.ByTestID("prompt-count").TextContent(context.Background()))

	if count != "1" {
		t.Fatalf("unexpected prompt count after first open: %q", count)
	}
	mustOK(page.ByTestID("prompt-open").Click(context.Background()))

	second := must(page.ByTestID("prompt-result").TextContent(context.Background()))

	if second != "dismissed" {
		t.Fatalf("unexpected second prompt result after one-shot handler: %q", second)
	}
	count2 := must(page.ByTestID("prompt-count").TextContent(context.Background()))

	if count2 != "2" {
		t.Fatalf("unexpected prompt count after second open: %q", count2)
	}
}
