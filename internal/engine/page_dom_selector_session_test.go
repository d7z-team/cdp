package engine

import (
	"errors"
	"strings"
	"testing"
)

func TestParseSelectorQuerySessionValue(t *testing.T) {
	callRes := map[string]any{
		"result": map[string]any{
			"value": map[string]any{
				"token":     "query_session_1",
				"runtimeId": "runtime_1",
				"ids": map[string]any{
					"count": float64(3),
				},
				"visible": map[string]any{
					"count": float64(2),
				},
				"actionable": map[string]any{
					"count": float64(1),
				},
				"truncated": true,
				"failure": map[string]any{
					"kind":         "bridge_timeout",
					"summary":      "Frame query unavailable",
					"detail":       "child runtime timed out",
					"frameSummary": "iframe#child",
					"error": map[string]any{
						"op":           "selector.frame_bridge",
						"kind":         "bridge_timeout",
						"message":      "Frame query unavailable",
						"detail":       strings.Repeat("x", browserErrorTextLimit+20),
						"frameSummary": "iframe#child",
						"data": map[string]any{
							"attempts": []any{
								map[string]any{
									"selector": ".missing-primary",
									"kind":     "empty",
									"summary":  "No elements matched this fallback option",
								},
								map[string]any{
									"selector":     "iframe >> .submit",
									"kind":         "bridge_timeout",
									"summary":      "Frame query unavailable",
									"detail":       "child runtime timed out",
									"frameSummary": "iframe#child",
								},
							},
						},
						"cause": map[string]any{
							"op":      "selector.segment",
							"kind":    "syntax",
							"message": "invalid selector",
						},
					},
				},
			},
		},
	}

	session := parseSelectorQuerySessionValue(callRes)
	if session.Token != "query_session_1" {
		t.Fatalf("unexpected token: %q", session.Token)
	}
	if session.RuntimeID != "runtime_1" {
		t.Fatalf("unexpected runtime id: %q", session.RuntimeID)
	}
	if session.IDs.Count != 3 || session.Visible.Count != 2 || session.Actionable.Count != 1 {
		t.Fatalf("unexpected counts: %+v", session)
	}
	if !session.Truncated {
		t.Fatal("expected truncated=true")
	}
	if session.Failure == nil || session.Failure.Kind != "bridge_timeout" {
		t.Fatalf("unexpected failure: %+v", session.Failure)
	}
	var browserErr *BrowserError
	if !errors.As(session.Failure.BrowserError(), &browserErr) {
		t.Fatalf("expected BrowserError cause, got %#v", session.Failure.BrowserError())
	}
	if browserErr.Op != "selector.frame_bridge" || browserErr.FrameSummary != "iframe#child" {
		t.Fatalf("unexpected browser error: %+v", browserErr)
	}
	if len(browserErr.Detail) > browserErrorTextLimit+len("...(truncated)") {
		t.Fatalf("browser error detail was not capped: %d", len(browserErr.Detail))
	}
	attempts, ok := browserErr.Data["attempts"].([]any)
	if !ok || len(attempts) != 2 {
		t.Fatalf("expected attempts in browser error data, got %+v", browserErr.Data["attempts"])
	}
	var causeErr *BrowserError
	if !errors.As(browserErr.Cause, &causeErr) || causeErr.Kind != "syntax" {
		t.Fatalf("unexpected browser error cause: %+v", browserErr.Cause)
	}
}

func TestParseSelectorQuerySessionValueMissingReturnsZero(t *testing.T) {
	callRes := map[string]any{
		"result": map[string]any{
			"value": "query_session_token_only",
		},
	}

	session := parseSelectorQuerySessionValue(callRes)
	if session != (SelectorQuerySession{}) {
		t.Fatalf("expected zero session, got %+v", session)
	}
}

func TestParseSelectorQuerySessionValueWithoutRuntimeIDReturnsZero(t *testing.T) {
	callRes := map[string]any{
		"result": map[string]any{
			"value": map[string]any{
				"token": "query_session_without_runtime",
			},
		},
	}

	session := parseSelectorQuerySessionValue(callRes)
	if session != (SelectorQuerySession{}) {
		t.Fatalf("expected zero session, got %+v", session)
	}
}

func TestRequireSelectorQuerySessionRejectsMissingRuntimeID(t *testing.T) {
	callRes := map[string]any{
		"result": map[string]any{
			"value": map[string]any{
				"token": "query_session_without_runtime",
			},
		},
	}

	_, err := requireSelectorQuerySession(callRes)
	if err == nil || !strings.Contains(err.Error(), "runtimeId") {
		t.Fatalf("expected missing runtimeId error, got %v", err)
	}
}

func TestSelectorQueryFailureBrowserErrorRequiresErrorNode(t *testing.T) {
	failure := &SelectorQueryFailure{
		Kind:         "syntax",
		Summary:      "invalid selector",
		Detail:       "bad token",
		FrameSummary: "iframe#missing-error-node",
	}

	if err := failure.BrowserError(); err != nil {
		t.Fatalf("expected nil BrowserError without latest error node, got %v", err)
	}
}

func TestSelectorQueryFailureBrowserErrorIncludesFallbackAttempts(t *testing.T) {
	failure := &SelectorQueryFailure{
		Kind:    "no_match",
		Summary: "All selector fallback options returned no matches",
		Error: &BrowserErrorNode{
			Op:      "selector.query",
			Kind:    "no_match",
			Message: "All selector fallback options returned no matches",
			Data: map[string]any{
				"attempts": []any{
					map[string]any{"selector": "#primary", "kind": "empty", "summary": "No elements matched this fallback option"},
					map[string]any{"selector": "#secondary", "kind": "empty", "summary": "No elements matched this fallback option"},
				},
			},
		},
	}

	err := failure.BrowserError()
	var browserErr *BrowserError
	if !errors.As(err, &browserErr) {
		t.Fatalf("expected BrowserError, got %#v", err)
	}
	attempts, ok := browserErr.Data["attempts"].([]any)
	if !ok || len(attempts) != 2 {
		t.Fatalf("expected attempts data, got %+v", browserErr.Data)
	}
	first, _ := attempts[0].(map[string]any)
	if first["selector"] != "#primary" || first["kind"] != "empty" {
		t.Fatalf("unexpected first attempt: %+v", first)
	}
}
