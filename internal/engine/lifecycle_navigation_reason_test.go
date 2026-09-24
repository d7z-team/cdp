package engine

import "testing"

func TestPendingNavigationReasonConsumeByFrameAndURL(t *testing.T) {
	manager := &BrowserManager{
		pageLifecycle:            map[string]*pageLifecycleState{},
		pendingNavigationReasons: map[navigationReasonKey]pendingNavigationReason{},
	}

	manager.rememberPendingNavigationReason("page-1", "frame-main", "anchorClick", "https://example.com/search?q=cdp")

	got := manager.consumePendingNavigationReason("page-1", "frame-main", "https://example.com/search?q=cdp")
	if got != "anchorClick" {
		t.Fatalf("consumePendingNavigationReason() = %q, want %q", got, "anchorClick")
	}

	got = manager.consumePendingNavigationReason("page-1", "frame-main", "https://example.com/search?q=cdp")
	if got != "" {
		t.Fatalf("second consumePendingNavigationReason() = %q, want empty", got)
	}
}

func TestPendingNavigationReasonKeepsPageAndFrameIsolation(t *testing.T) {
	manager := &BrowserManager{
		pageLifecycle:            map[string]*pageLifecycleState{},
		pendingNavigationReasons: map[navigationReasonKey]pendingNavigationReason{},
	}

	manager.rememberPendingNavigationReason("page-1", "frame-a", "scriptInitiated", "https://example.com/a")
	manager.rememberPendingNavigationReason("page-1", "frame-b", "anchorClick", "https://example.com/b")

	got := manager.consumePendingNavigationReason("page-1", "frame-b", "https://example.com/b")
	if got != "anchorClick" {
		t.Fatalf("frame-b consume = %q, want %q", got, "anchorClick")
	}

	got = manager.consumePendingNavigationReason("page-1", "frame-a", "https://example.com/a")
	if got != "scriptInitiated" {
		t.Fatalf("frame-a consume = %q, want %q", got, "scriptInitiated")
	}
}

func TestNavigationReasonFromTransitionType(t *testing.T) {
	tests := []struct {
		transitionType string
		want           string
	}{
		{transitionType: "typed", want: "typed"},
		{transitionType: "address_bar", want: "address_bar"},
		{transitionType: "auto_bookmark", want: "auto_bookmark"},
		{transitionType: "generated", want: "generated"},
		{transitionType: "keyword", want: "keyword"},
		{transitionType: "keyword_generated", want: "keyword_generated"},
		{transitionType: "link", want: ""},
		{transitionType: "form_submit", want: ""},
		{transitionType: "reload", want: ""},
	}

	for _, tt := range tests {
		if got := navigationReasonFromTransitionType(tt.transitionType); got != tt.want {
			t.Fatalf("navigationReasonFromTransitionType(%q) = %q, want %q", tt.transitionType, got, tt.want)
		}
	}
}
