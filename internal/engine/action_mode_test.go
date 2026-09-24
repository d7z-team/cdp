package engine

import (
	"strings"
	"testing"
)

func TestPageActionModeDefaultsToStrict(t *testing.T) {
	var p Page
	if got := p.ActionMode(); got != ActionModeStrict {
		t.Fatalf("ActionMode() = %q, want %q", got, ActionModeStrict)
	}
}

func TestPageSetActionModeFast(t *testing.T) {
	var p Page
	if err := p.SetActionMode(ActionModeFast); err != nil {
		t.Fatalf("SetActionMode(fast) error: %v", err)
	}
	if got := p.ActionMode(); got != ActionModeFast {
		t.Fatalf("ActionMode() = %q, want %q", got, ActionModeFast)
	}
}

func TestPageSetActionModeRejectsInvalid(t *testing.T) {
	var p Page
	if err := p.SetActionMode("turbo"); err == nil {
		t.Fatal("expected invalid action mode error")
	}
	if got := p.ActionMode(); got != ActionModeStrict {
		t.Fatalf("invalid SetActionMode changed mode to %q", got)
	}
}

func TestBrowserManagerDefaultActionModeDefaultsToStrict(t *testing.T) {
	var manager BrowserManager
	if got := manager.DefaultActionMode(); got != ActionModeStrict {
		t.Fatalf("DefaultActionMode() = %q, want %q", got, ActionModeStrict)
	}
}

func TestBrowserManagerSetDefaultActionModeFast(t *testing.T) {
	var manager BrowserManager
	if err := manager.SetDefaultActionMode(ActionModeFast); err != nil {
		t.Fatalf("SetDefaultActionMode(fast) error: %v", err)
	}
	if got := manager.DefaultActionMode(); got != ActionModeFast {
		t.Fatalf("DefaultActionMode() = %q, want %q", got, ActionModeFast)
	}
}

func TestBrowserManagerSetDefaultActionModeRejectsInvalid(t *testing.T) {
	var manager BrowserManager
	if err := manager.SetDefaultActionMode(ActionModeFast); err != nil {
		t.Fatalf("SetDefaultActionMode(fast) error: %v", err)
	}
	if err := manager.SetDefaultActionMode("turbo"); err == nil {
		t.Fatal("expected invalid action mode error")
	}
	if got := manager.DefaultActionMode(); got != ActionModeFast {
		t.Fatalf("invalid SetDefaultActionMode changed mode to %q", got)
	}
}

func TestNewBrowserManagerWithConfigRejectsInvalidActionMode(t *testing.T) {
	_, err := NewBrowserManagerWithConfig(t.Context(), "http://127.0.0.1:1", BrowserManagerConfig{
		DefaultActionMode: "turbo",
	})
	if err == nil {
		t.Fatal("expected invalid action mode error")
	}
	if !strings.Contains(err.Error(), "unsupported action mode") {
		t.Fatalf("error = %v, want unsupported action mode", err)
	}
}
