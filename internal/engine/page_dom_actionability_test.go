package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestActionabilityDiagnosticError(t *testing.T) {
	diagnostic := ActionabilityDiagnostic{
		Summary: "Element is not visible",
		Element: `button#close [role="button"]`,
		Detail:  `div.modal causes display is none`,
		FrameChain: []FrameSegmentDiagnostic{
			{FrameElement: "iframe#dialog-frame", Detail: "iframe element is visible and hit-testable"},
		},
	}

	got := diagnostic.Error()
	want := `Element is not visible; target=button#close [role="button"]; detail=div.modal causes display is none; frameChain=[1] iframe#dialog-frame: iframe element is visible and hit-testable`
	if got != want {
		t.Fatalf("unexpected diagnostic error string: %q", got)
	}
}

func TestParseActionabilityDiagnosticValue(t *testing.T) {
	callRes := map[string]any{
		"result": map[string]any{
			"value": map[string]any{
				"actionable":  false,
				"kind":        "not_visible",
				"summary":     "Element is not visible",
				"detail":      "display is none",
				"element":     "button#close",
				"culprit":     "div.modal",
				"centerX":     12.0,
				"centerY":     34.0,
				"retriable":   true,
				"retryAction": "hover_priming",
				"retryPoint": map[string]any{
					"x": 56.0,
					"y": 78.0,
				},
				"retryDelayMs": 90.0,
				"stability": map[string]any{
					"kind":      "timeout",
					"elapsedMs": 1200.0,
					"maxDelta": map[string]any{
						"x": 2.5, "y": 3.5, "width": 1.5, "height": 0.5,
					},
					"samples": []any{
						map[string]any{"x": 10.0, "y": 20.0, "width": 100.0, "height": 30.0},
						map[string]any{"x": 12.5, "y": 23.5, "width": 101.5, "height": 30.5},
					},
				},
				"localRect": map[string]any{
					"x":      10.0,
					"y":      20.0,
					"width":  100.0,
					"height": 30.0,
				},
				"topRect": map[string]any{
					"x":      110.0,
					"y":      220.0,
					"width":  100.0,
					"height": 30.0,
				},
				"frameChain": []any{
					map[string]any{
						"frameElement": "iframe#dialog-frame",
						"summary":      "Frame segment passed",
						"detail":       "iframe element is visible and hit-testable",
					},
				},
			},
		},
	}

	got := parseActionabilityDiagnosticValue(callRes)
	if got.Actionable || got.Kind != "not_visible" || got.Summary != "Element is not visible" || got.Detail != "display is none" || got.Element != "button#close" || got.Culprit != "div.modal" || !got.HasCenter || got.CenterX != 12 || got.CenterY != 34 || !got.HasLocalRect || !got.HasTopRect || got.LocalRect.X != 10 || got.TopRect.X != 110 || !got.Retriable || got.RetryAction != "hover_priming" || !got.HasRetryPoint || got.RetryPointX != 56 || got.RetryPointY != 78 || got.RetryDelayMs != 90 || got.Stability == nil || got.Stability.Kind != "timeout" || got.Stability.ElapsedMs != 1200 || got.Stability.MaxDelta.X != 2.5 || len(got.Stability.Samples) != 2 || len(got.FrameChain) != 1 || got.FrameChain[0].FrameElement != "iframe#dialog-frame" {
		t.Fatalf("unexpected diagnostic parse result: %+v", got)
	}
}

func TestActionabilityPreviewBoxesPrefersTopRect(t *testing.T) {
	diagnostic := ActionabilityDiagnostic{
		LocalRect:    Rect{X: 10, Y: 20, Width: 100, Height: 30},
		TopRect:      Rect{X: 110, Y: 220, Width: 100, Height: 30},
		HasLocalRect: true,
		HasTopRect:   true,
	}
	boxes := actionabilityPreviewBoxes(diagnostic)
	if len(boxes) != 1 || boxes[0].X != 110 || boxes[0].Y != 220 {
		t.Fatalf("expected top rect preview box, got %+v", boxes)
	}
}

func TestActionabilityPreviewBoxesFallsBackToLocalRect(t *testing.T) {
	diagnostic := ActionabilityDiagnostic{
		LocalRect:    Rect{X: 10, Y: 20, Width: 100, Height: 30},
		HasLocalRect: true,
	}
	boxes := actionabilityPreviewBoxes(diagnostic)
	if len(boxes) != 1 || boxes[0].X != 10 || boxes[0].Y != 20 {
		t.Fatalf("expected local rect preview box, got %+v", boxes)
	}
}

func TestResolveMouseActionPointRevalidatesHoverPosition(t *testing.T) {
	tests := []struct {
		name      string
		points    [][2]float64
		wantMoves int
		wantX     float64
		wantY     float64
		wantErr   string
	}{
		{name: "unchanged", points: [][2]float64{{10, 20}, {10.5, 20.5}}, wantMoves: 1, wantX: 10.5, wantY: 20.5},
		{name: "settles after one adjustment", points: [][2]float64{{10, 20}, {18, 28}, {18.5, 28.5}}, wantMoves: 2, wantX: 18.5, wantY: 28.5},
		{name: "keeps moving", points: [][2]float64{{10, 20}, {18, 28}, {25, 35}}, wantMoves: 2, wantErr: "hover 后持续改变点击位置"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loads := make([]ActionabilityDiagnostic, 0, len(tt.points))
			for _, point := range tt.points {
				loads = append(loads, actionableDiagnosticAt(point[0], point[1]))
			}
			var moveCalls int
			_, x, y, err := (&Page{}).resolveMouseActionPoint(context.Background(),

				func(actionTargetScrollMode) (ActionabilityDiagnostic, error) {
					if len(loads) == 0 {
						t.Fatal("unexpected diagnostic load")
					}
					next := loads[0]
					loads = loads[1:]
					return next, nil
				}, func(float64, float64) error {
					moveCalls++
					return nil
				})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("resolveMouseActionPoint error = %v, want %q", err, tt.wantErr)
				}
			} else if err != nil || x != tt.wantX || y != tt.wantY {
				t.Fatalf("resolveMouseActionPoint = %.1f,%.1f,%v, want %.1f,%.1f,nil", x, y, err, tt.wantX, tt.wantY)
			}
			if moveCalls != tt.wantMoves {
				t.Fatalf("move calls = %d, want %d", moveCalls, tt.wantMoves)
			}
		})
	}
}

func TestResolveMouseActionPointRepositionsObscuredTarget(t *testing.T) {
	modes := make([]actionTargetScrollMode, 0, 3)
	loads := []ActionabilityDiagnostic{
		{Kind: "obscured", Summary: "Element is obscured"},
		actionableDiagnosticAt(40, 50),
		actionableDiagnosticAt(40, 50),
	}

	_, x, y, err := (&Page{}).resolveMouseActionPoint(context.Background(),

		func(mode actionTargetScrollMode) (ActionabilityDiagnostic, error) {
			modes = append(modes, mode)
			next := loads[0]
			loads = loads[1:]
			return next, nil
		}, func(float64, float64) error { return nil })
	if err != nil || x != 40 || y != 50 {
		t.Fatalf("resolveMouseActionPoint = %.1f,%.1f,%v, want 40,50,nil", x, y, err)
	}
	want := []actionTargetScrollMode{actionTargetScrollIfNeeded, actionTargetScrollCenter, actionTargetScrollNone}
	if len(modes) != len(want) {
		t.Fatalf("scroll modes = %v, want %v", modes, want)
	}
	for i := range want {
		if modes[i] != want[i] {
			t.Fatalf("scroll modes = %v, want %v", modes, want)
		}
	}
}

func actionableDiagnosticAt(x, y float64) ActionabilityDiagnostic {
	return ActionabilityDiagnostic{
		Actionable:   true,
		CenterX:      x,
		CenterY:      y,
		HasCenter:    true,
		LocalRect:    Rect{X: x - 5, Y: y - 5, Width: 10, Height: 10},
		TopRect:      Rect{X: x - 5, Y: y - 5, Width: 10, Height: 10},
		HasLocalRect: true,
		HasTopRect:   true,
	}
}

func TestRuntimeResultErrorReturnsBrowserError(t *testing.T) {
	err := runtimeResultError(map[string]any{
		"exceptionDetails": map[string]any{
			"lineNumber":   float64(12),
			"columnNumber": float64(34),
			"url":          "https://example.test/app.js",
			"exception": map[string]any{
				"className":   "Error",
				"description": strings.Repeat("boom", 200),
			},
			"stackTrace": map[string]any{
				"callFrames": []any{
					map[string]any{"functionName": "run", "url": "https://example.test/app.js", "lineNumber": float64(12), "columnNumber": float64(34)},
				},
			},
		},
	})
	if err == nil {
		t.Fatal("expected runtime error")
	}
	var browserErr *BrowserError
	if !errors.As(err, &browserErr) {
		t.Fatalf("expected BrowserError, got %#v", err)
	}
	if browserErr.Kind != "javascript_exception" || browserErr.Op != "browser.runtime" {
		t.Fatalf("unexpected browser error: %+v", browserErr)
	}
	if len(browserErr.Message) > browserErrorTextLimit+len("...(truncated)") {
		t.Fatalf("runtime browser error was not capped: %d", len(browserErr.Message))
	}
	if browserErr.Data["lineNumber"] != float64(12) || browserErr.Data["exception.className"] != "Error" {
		t.Fatalf("expected runtime exception metadata, got %+v", browserErr.Data)
	}
	if frames, ok := browserErr.Data["stack.callFrames"].([]any); !ok || len(frames) != 1 {
		t.Fatalf("expected capped stack frames, got %+v", browserErr.Data["stack.callFrames"])
	}
}

func TestBrowserErrorFromCDPAddsTargetContext(t *testing.T) {
	rawErr := errors.New("execution context was destroyed")
	err := BrowserErrorFromCDP("Runtime.callFunctionOn", ExecutionTarget{
		SessionID: "session-1",
		RuntimeID: "runtime-1",
		ContextID: 42,
	}, rawErr)

	var browserErr *BrowserError
	if !errors.As(err, &browserErr) {
		t.Fatalf("expected BrowserError, got %#v", err)
	}
	if browserErr.Op != "Runtime.callFunctionOn" || browserErr.Kind != "cdp" || browserErr.RuntimeID != "runtime-1" {
		t.Fatalf("unexpected cdp browser error: %+v", browserErr)
	}
	if browserErr.Data["sessionId"] != "session-1" || browserErr.Data["contextId"] != 42 {
		t.Fatalf("expected target context in error data, got %+v", browserErr.Data)
	}
	if !errors.Is(err, rawErr) {
		t.Fatalf("expected original CDP error to be preserved")
	}
}

func TestBrowserErrorFromActionabilityKeepsStructuredData(t *testing.T) {
	err := BrowserErrorFromActionability("selector.actionability", ActionabilityDiagnostic{
		Kind:          "obscured",
		Summary:       "Element is obscured",
		Detail:        "covered by modal",
		Element:       "button#save",
		Culprit:       "div.modal",
		LocalRect:     Rect{X: 1, Y: 2, Width: 3, Height: 4},
		TopRect:       Rect{X: 11, Y: 12, Width: 13, Height: 14},
		CenterX:       17,
		CenterY:       18,
		HasLocalRect:  true,
		HasTopRect:    true,
		HasCenter:     true,
		Retriable:     true,
		RetryAction:   "hover_priming",
		RetryPointX:   21,
		RetryPointY:   22,
		HasRetryPoint: true,
		Stability: &StabilityDiagnostic{
			Kind:      "timeout",
			ElapsedMs: 1200,
			MaxDelta:  Rect{X: 3, Y: 4, Width: 2, Height: 1},
			Samples: []Rect{
				{X: 1, Y: 2, Width: 3, Height: 4},
				{X: 4, Y: 6, Width: 5, Height: 5},
			},
		},
		FrameChain: []FrameSegmentDiagnostic{
			{FrameElement: "iframe#child", Summary: "Frame segment passed", Detail: "visible"},
		},
	})

	var browserErr *BrowserError
	if !errors.As(err, &browserErr) {
		t.Fatalf("expected BrowserError, got %#v", err)
	}
	if browserErr.Kind != "obscured" || browserErr.Data["element"] != "button#save" || browserErr.Data["culprit"] != "div.modal" {
		t.Fatalf("unexpected actionability browser error: %+v", browserErr)
	}
	if center, ok := browserErr.Data["center"].(map[string]any); !ok || center["x"] != float64(17) {
		t.Fatalf("expected center data, got %+v", browserErr.Data["center"])
	}
	if retryPoint, ok := browserErr.Data["retryPoint"].(map[string]any); !ok || retryPoint["x"] != float64(21) || browserErr.Data["retryAction"] != "hover_priming" {
		t.Fatalf("expected retry actionability data, got %+v", browserErr.Data)
	}
	if stability, ok := browserErr.Data["stability"].(map[string]any); !ok || stability["kind"] != "timeout" || stability["elapsedMs"] != 1200 {
		t.Fatalf("expected stability data, got %+v", browserErr.Data["stability"])
	}
	if frames, ok := browserErr.Data["frameChain"].([]any); !ok || len(frames) != 1 {
		t.Fatalf("expected frame chain data, got %+v", browserErr.Data["frameChain"])
	}
}
