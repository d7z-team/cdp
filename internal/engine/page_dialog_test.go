package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"gopkg.d7z.net/cdp/internal/syncutil"
)

func TestBrowserManagerSetJavaScriptDialogPolicyAllUpdatesPages(t *testing.T) {
	manager := &BrowserManager{
		sessions:     syncutil.NewSyncMap[string, *Page](),
		dialogPolicy: JavaScriptDialogPolicyAutoHandle,
	}
	first := &Page{}
	second := &Page{}
	manager.sessions.Store("first", first)
	manager.sessions.Store("second", second)

	manager.SetJavaScriptDialogPolicyAll(JavaScriptDialogPolicyPassthrough)

	if got := manager.JavaScriptDialogPolicy(); got != JavaScriptDialogPolicyPassthrough {
		t.Fatalf("manager policy = %q, want %q", got, JavaScriptDialogPolicyPassthrough)
	}
	if got := first.JavaScriptDialogPolicy(); got != JavaScriptDialogPolicyPassthrough {
		t.Fatalf("first page policy = %q, want %q", got, JavaScriptDialogPolicyPassthrough)
	}
	if got := second.JavaScriptDialogPolicy(); got != JavaScriptDialogPolicyPassthrough {
		t.Fatalf("second page policy = %q, want %q", got, JavaScriptDialogPolicyPassthrough)
	}

	manager.SetJavaScriptDialogPolicyAll("")
	if got := manager.JavaScriptDialogPolicy(); got != JavaScriptDialogPolicyAutoHandle {
		t.Fatalf("manager policy after reset = %q, want %q", got, JavaScriptDialogPolicyAutoHandle)
	}
	if got := first.JavaScriptDialogPolicy(); got != JavaScriptDialogPolicyAutoHandle {
		t.Fatalf("first page policy after reset = %q, want %q", got, JavaScriptDialogPolicyAutoHandle)
	}
	if got := second.JavaScriptDialogPolicy(); got != JavaScriptDialogPolicyAutoHandle {
		t.Fatalf("second page policy after reset = %q, want %q", got, JavaScriptDialogPolicyAutoHandle)
	}
}

func TestJavaScriptDialogSnapshotLifecycle(t *testing.T) {
	page := NewPageWithContext(context.Background())
	page.SetJavaScriptDialogPolicy(JavaScriptDialogPolicyPassthrough)
	_, _, initialChange := page.javascriptDialogSnapshot()
	if err := page.handleJavaScriptDialogOpening(map[string]any{
		"type": "prompt", "message": "first", "defaultPrompt": "default",
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-initialChange:
	case <-time.After(time.Second):
		t.Fatal("dialog opening did not notify waiters")
	}
	first, open, firstChange := page.javascriptDialogSnapshot()
	if !open || first.ID == 0 || first.Type != "prompt" || first.Message != "first" || first.Default != "default" {
		t.Fatalf("first dialog = %+v open=%v", first, open)
	}
	if err := page.handleJavaScriptDialogOpening(map[string]any{"type": "alert", "message": "second"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-firstChange:
	case <-time.After(time.Second):
		t.Fatal("replacement dialog did not notify waiters")
	}
	second, open := page.CurrentJavaScriptDialog()
	if !open || second.ID <= first.ID || second.Message != "second" {
		t.Fatalf("second dialog = %+v open=%v", second, open)
	}
	page.handleJavaScriptDialogClosed()
	if _, open := page.CurrentJavaScriptDialog(); open {
		t.Fatal("dialog remained open after close")
	}
}

func TestJavaScriptDialogHandleValidationAndUnbindWake(t *testing.T) {
	page := NewPageWithContext(context.Background())
	page.SetJavaScriptDialogPolicy(JavaScriptDialogPolicyPassthrough)
	if _, _, err := page.HandleJavaScriptDialog(context.Background(), 1, false, ""); !errors.Is(err, ErrJavaScriptDialogNotOpen) {
		t.Fatalf("closed dialog error = %v", err)
	}
	if err := page.handleJavaScriptDialogOpening(map[string]any{"type": "alert", "message": "open"}); err != nil {
		t.Fatal(err)
	}
	dialog, _ := page.CurrentJavaScriptDialog()
	if _, _, err := page.HandleJavaScriptDialog(context.Background(), dialog.ID+1, false, ""); !errors.Is(err, ErrJavaScriptDialogChanged) {
		t.Fatalf("stale dialog error = %v", err)
	}
	_, _, changed := page.javascriptDialogSnapshot()
	page.unbind()
	select {
	case <-changed:
	case <-time.After(time.Second):
		t.Fatal("page unbind did not wake dialog waiters")
	}
}
