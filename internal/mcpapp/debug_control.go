package mcpapp

import (
	"context"
	"math"

	"gopkg.d7z.net/cdp"
)

func (s *Service) debugControl(ctx context.Context, tabID string, command debugCommand, deliveredFrames *debugFrameState) debugControlOutput {
	runtime, err := s.runtime(ctx, tabID)
	if err != nil {
		return debugControlOutput{BaseOutput: failedBase(tabID, "", "debug.control", "", err)}
	}
	coordinateAction := false
	switch command.Action {
	case "move", "click", "double_click", "right_click", "drag", "wheel":
		coordinateAction = true
	case "key":
		if command.Key == "" {
			return debugControlOutput{BaseOutput: failedBase(tabID, runtime.currentState(), "debug.control.key", "", NewToolError("invalid_argument", "debug.control.key", "key is required", nil))}
		}
	case "text":
	default:
		return debugControlOutput{BaseOutput: failedBase(tabID, runtime.currentState(), "debug.control", "", NewToolError("invalid_argument", "debug.control", "unknown control action", nil))}
	}
	if coordinateAction {
		deliveredFrame := deliveredFrames.load()
		if deliveredFrame == nil || command.FrameSequence == 0 || command.FrameSequence != deliveredFrame.Sequence {
			return debugControlOutput{BaseOutput: failedBase(tabID, runtime.currentState(), "debug.control", "", NewToolError("stale_frame", "debug.control", "the displayed debug frame is stale", nil))}
		}
		coordinates := []float64{command.X, command.Y}
		if command.Action == "drag" {
			coordinates = append(coordinates, command.ToX, command.ToY)
		}
		if command.Action == "wheel" {
			coordinates = append(coordinates, command.DeltaX, command.DeltaY)
		}
		for _, coordinate := range coordinates {
			if math.IsNaN(coordinate) || math.IsInf(coordinate, 0) {
				return debugControlOutput{BaseOutput: failedBase(tabID, runtime.currentState(), "debug.control", "", NewToolError("invalid_argument", "debug.control", "coordinates must be finite", nil))}
			}
		}
		if command.X < 0 || command.X > deliveredFrame.CSSViewportWidth || command.Y < 0 || command.Y > deliveredFrame.CSSViewportHeight ||
			(command.Action == "drag" && (command.ToX < 0 || command.ToX > deliveredFrame.CSSViewportWidth || command.ToY < 0 || command.ToY > deliveredFrame.CSSViewportHeight)) {
			return debugControlOutput{BaseOutput: failedBase(tabID, runtime.currentState(), "debug.control", "", NewToolError("invalid_argument", "debug.control", "coordinates are outside the displayed viewport", nil))}
		}
	}
	if err := runtime.acquireModalSensitive(ctx, "debug.control", false); err != nil {
		return debugControlOutput{BaseOutput: failedBase(tabID, runtime.currentState(), "debug.control", "", err)}
	}
	releaseGate := true
	defer func() {
		if releaseGate {
			runtime.release()
		}
	}()
	if coordinateAction {
		deliveredFrame := deliveredFrames.load()
		if deliveredFrame == nil || command.FrameSequence != deliveredFrame.Sequence {
			return debugControlOutput{BaseOutput: failedBase(tabID, runtime.currentState(), "debug.control", "", NewToolError("stale_frame", "debug.control", "the displayed debug frame is no longer current", nil))}
		}
	}

	before, _ := runtime.page.CurrentSnapshot()
	mark := runtime.observer.mark()
	runtime.setPhase(phaseExecuting)
	completed, modal, actionDone, err := waitForActionOrModal(ctx, runtime, false, func(ctx context.Context) error {
		switch command.Action {
		case "move":
			return runtime.page.MouseMove(ctx, command.X, command.Y)
		case "click":
			return runtime.page.MouseClick(ctx, command.X, command.Y)
		case "double_click":
			return runtime.page.MouseDoubleClick(ctx, command.X, command.Y)
		case "right_click":
			return runtime.page.MouseRightClick(ctx, command.X, command.Y)
		case "drag":
			return runtime.page.MouseDrag(ctx, command.X, command.Y, command.ToX, command.ToY)
		case "wheel":
			return runtime.page.MouseWheel(ctx, command.X, command.Y, command.DeltaX, command.DeltaY)
		case "key":
			return runtime.page.Press(ctx, command.Key, keyOptions(command.Modifiers))
		case "text":
			return runtime.page.Type(ctx, command.Text)
		}
		return nil
	})
	if !completed {
		runtime.transferGateToPending(actionDone)
		releaseGate = false
	}
	if err != nil {
		if ctx.Err() != nil {
			runtime.setPhase(phaseCanceled)
		} else {
			runtime.setPhase(phaseFailed)
		}
		return debugControlOutput{BaseOutput: failedBase(tabID, runtime.currentState(), "debug.control."+command.Action, "", err)}
	}
	if modal != "" {
		progress := "dispatched"
		if completed {
			progress = "completed"
		}
		return debugControlOutput{BaseOutput: BaseOutput{OK: true, TabID: tabID, State: modal, Warnings: []string{"action " + progress + "; snapshot capture deferred while modal is open"}}}
	}
	if command.Action == "move" {
		runtime.setPhase(phaseCompleted)
		return debugControlOutput{BaseOutput: BaseOutput{OK: true, TabID: tabID, State: runtime.currentState()}}
	}
	result, err := settleAndCapture(ctx, runtime, before, mark)
	if err != nil {
		runtime.setPhase(phaseFailed)
		return debugControlOutput{BaseOutput: failedBase(tabID, runtime.currentState(), "debug.control."+command.Action, "", err)}
	}
	runtime.setPhase(phaseCompleted)
	output := debugControlOutput{BaseOutput: BaseOutput{OK: true, TabID: tabID, State: runtime.currentState(), Warnings: result.Warnings}}
	if result.Document.ID != 0 {
		output.Delta = &result.Delta
	}
	return output
}

func (s *Service) debugStatus(ctx context.Context, tabID string) (debugTabStatus, error) {
	runtime, err := s.runtime(ctx, tabID)
	if err != nil {
		return debugTabStatus{}, err
	}
	dialog, open, state := runtime.currentDialogState()
	status := debugTabStatus{Diagnostics: s.browser.browser.Diagnostics(), ConsoleCollected: s.browser.browser.Diagnostics() == cdp.DiagnosticsRuntime, TabID: tabID, State: state, Phase: runtime.currentPhase()}
	if open {
		status.Dialog = &dialog
	}
	if document, ok := runtime.page.CurrentSnapshot(); ok {
		status.SnapshotID = document.ID
		status.URL = document.URL
		status.Title = document.Title
	}
	if status.URL == "" && s.browser != nil {
		tabs, listErr := s.browser.ListTabs(ctx, s.activeID())
		if listErr == nil {
			for _, tab := range tabs {
				if tab.ID == tabID {
					status.URL, status.Title = tab.URL, tab.Title
					break
				}
			}
		}
	}
	return status, nil
}
