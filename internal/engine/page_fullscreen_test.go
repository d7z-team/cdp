package engine

import "testing"

func TestPageFullscreenState(t *testing.T) {
	page := &Page{}
	if page.IsFullscreen() {
		t.Fatalf("expected initial fullscreen state to be false")
	}

	page.setFullscreenState(true, "div#panel")
	if !page.IsFullscreen() {
		t.Fatalf("expected fullscreen state to be true after enter")
	}
	if page.fullscreenElement != "div#panel" {
		t.Fatalf("fullscreen element = %q, want %q", page.fullscreenElement, "div#panel")
	}

	page.setFullscreenState(false, "ignored")
	if page.IsFullscreen() {
		t.Fatalf("expected fullscreen state to be false after exit")
	}
	if page.fullscreenElement != "" {
		t.Fatalf("fullscreen element = %q, want empty", page.fullscreenElement)
	}
}
