package engine

import (
	"testing"

	"gopkg.d7z.net/cdp/internal/syncutil"
)

func TestBrowserManagerSetPrintPolicyAllUpdatesPages(t *testing.T) {
	manager := &BrowserManager{
		sessions:    syncutil.NewSyncMap[string, *Page](),
		printPolicy: PrintPolicyIntercept,
	}
	first := &Page{}
	second := &Page{}
	manager.sessions.Store("first", first)
	manager.sessions.Store("second", second)

	manager.SetPrintPolicyAll(PrintPolicyPassthrough)

	if got := manager.PrintPolicy(); got != PrintPolicyPassthrough {
		t.Fatalf("manager print policy = %q, want %q", got, PrintPolicyPassthrough)
	}
	if got := first.PrintPolicy(); got != PrintPolicyPassthrough {
		t.Fatalf("first page print policy = %q, want %q", got, PrintPolicyPassthrough)
	}
	if got := second.PrintPolicy(); got != PrintPolicyPassthrough {
		t.Fatalf("second page print policy = %q, want %q", got, PrintPolicyPassthrough)
	}
}
