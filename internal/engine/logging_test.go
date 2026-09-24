package engine

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestInstanceLogRouting(t *testing.T) {
	var first, second bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&first, &slog.HandlerOptions{Level: slog.LevelDebug}))
	manager := &BrowserManager{logger: logger}
	page := &Page{manager: manager}
	conn := &CdpConn{logger: logger}
	other := &BrowserManager{logger: slog.New(slog.NewTextHandler(&second, nil))}
	manager.log(slog.LevelDebug, "manager event")
	page.log(slog.LevelWarn, "page event", "page_id", "p1")
	conn.log(slog.LevelError, "connection event")
	other.log(slog.LevelDebug, "filtered event")
	other.log(slog.LevelInfo, "other event")
	for _, message := range []string{"manager event", "page event", "page_id=p1", "connection event"} {
		if !strings.Contains(first.String(), message) {
			t.Fatalf("instance log missing %q: %s", message, first.String())
		}
	}
	if strings.Contains(first.String(), "other event") || strings.Contains(second.String(), "page event") {
		t.Fatal("logs crossed instance boundaries")
	}
	if !strings.Contains(second.String(), "other event") || strings.Contains(second.String(), "filtered event") {
		t.Fatalf("handler level was not respected: %s", second.String())
	}
}
