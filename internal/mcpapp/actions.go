package mcpapp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"gopkg.d7z.net/cdp"
	"gopkg.d7z.net/cdp/snapshot"
)

func (s *Service) runRefAction(ctx context.Context, tabID, refValue, op string, action func(context.Context, *cdp.Page, *cdp.Element) error) (operationResult, TabState, error) {
	runtime, err := s.runtime(ctx, tabID)
	if err != nil {
		return operationResult{}, "", err
	}
	allowFileChooser := op == "browser_upload" && runtime.currentState() == TabFileChooser
	if err := runtime.acquireModalSensitive(ctx, op, allowFileChooser); err != nil {
		return operationResult{}, runtime.currentState(), err
	}
	releaseGate := true
	defer func() {
		if releaseGate {
			runtime.release()
		}
	}()
	state := runtime.currentState()
	before, _ := runtime.page.CurrentSnapshot()
	runtime.setPhase(phaseResolving)
	ref, err := runtime.page.Element(ctx, refValue)
	if err != nil {
		runtime.setPhase(phaseFailed)
		return operationResult{}, state, err
	}
	runtime.setPhase(phaseActionable)
	mark := runtime.observer.mark()
	runtime.setPhase(phaseExecuting)
	ignoreFileChooser := op == "browser_upload" && state == TabFileChooser
	completed, modal, actionDone, err := waitForActionOrModal(ctx, runtime, ignoreFileChooser, func(actionCtx context.Context) error { return action(actionCtx, runtime.page, ref) })
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
		return operationResult{}, runtime.currentState(), err
	}
	if modal != "" {
		progress := "dispatched"
		if completed {
			progress = "completed"
		}
		return operationResult{Warnings: []string{"action " + progress + "; snapshot capture deferred while modal is open"}}, modal, nil
	}
	result, err := settleAndCapture(ctx, runtime, before, mark)
	if err != nil {
		runtime.setPhase(phaseFailed)
		return operationResult{}, runtime.currentState(), err
	}
	runtime.setPhase(phaseCompleted)
	return result, runtime.currentState(), nil
}

func waitForActionOrModal(ctx context.Context, runtime *tabRuntime, ignoreFileChooser bool, action func(context.Context) error) (bool, TabState, <-chan error, error) {
	actionDone := make(chan error, 1)
	actionCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	stopCaller := context.AfterFunc(ctx, cancel)
	lifetime := runtime.lifetime
	if lifetime == nil {
		lifetime = context.Background()
	}
	stopServer := context.AfterFunc(lifetime, cancel)
	go func() { defer cancel(); defer stopCaller(); defer stopServer(); actionDone <- action(actionCtx) }()
	for {
		_, dialogOpen, dialogChanged := runtime.page.DialogState()
		if dialogOpen {
			stopCaller() // The modal transfers the dispatched action to the tab runtime.
			return false, TabDialog, actionDone, nil
		}
		select {
		case err := <-actionDone:
			if err != nil {
				return true, "", nil, err
			}
			state := runtime.currentState()
			if state == TabDialog || state == TabFileChooser {
				return true, state, nil, nil
			}
			return true, "", nil, nil
		case <-runtime.observer.wake:
			state := runtime.currentState()
			if state == TabDialog || state == TabFileChooser && !ignoreFileChooser {
				stopCaller()
				return false, state, actionDone, nil
			}
		case <-dialogChanged:
		case <-ctx.Done():
			return false, "", actionDone, ctx.Err()
		}
	}
}

func settleAndCapture(ctx context.Context, runtime *tabRuntime, before snapshot.Document, requestMark uint64) (operationResult, error) {
	runtime.setPhase(phaseSettling)
	if _, open := runtime.page.CurrentDialog(); open {
		return operationResult{Warnings: []string{"snapshot capture deferred while modal is open"}}, nil
	}
	settleCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	type quietResult struct {
		timedOut bool
		err      error
	}
	quietDone := make(chan quietResult, 1)
	requestDone := make(chan bool, 1)
	go func() {
		quiet, err := runtime.page.WaitForSnapshotQuiet(settleCtx, 250*time.Millisecond)
		quietDone <- quietResult{timedOut: quiet.TimedOut, err: err}
	}()
	go func() { requestDone <- runtime.observer.waitRequests(settleCtx, requestMark) }()
	quiet := <-quietDone
	requestsTimedOut := <-requestDone
	warnings := []string(nil)
	if quiet.err != nil && settleCtx.Err() == nil {
		return operationResult{}, fmt.Errorf("wait for DOM quiet: %w", quiet.err)
	}
	if quiet.timedOut || requestsTimedOut || settleCtx.Err() != nil {
		warnings = append(warnings, "action settle reached the 5 second limit")
	}
	if state := runtime.currentState(); state == TabDialog || state == TabFileChooser {
		return operationResult{Warnings: append(warnings, "snapshot capture deferred while modal is open")}, nil
	}
	runtime.setPhase(phaseCapturing)
	after, err := runtime.page.Snapshot(ctx)
	if err != nil {
		return operationResult{}, fmt.Errorf("capture action result: %w", err)
	}
	return operationResult{Document: after, Delta: snapshot.Diff(before, after), Warnings: append(warnings, after.Warnings...)}, nil
}

func actionResponse(tabID, refValue, op string, state TabState, result operationResult, err error) (*mcp.CallToolResult, ActionOutput, error) {
	if err != nil {
		out := ActionOutput{BaseOutput: failedBase(tabID, state, op, refValue, err)}
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	out := ActionOutput{BaseOutput: BaseOutput{OK: true, TabID: tabID, State: state, Warnings: result.Warnings}}
	if result.Document.ID != 0 {
		out.Delta = &result.Delta
	}
	return nil, out, nil
}

func (s *Service) click(ctx context.Context, _ *mcp.CallToolRequest, input RefInput) (*mcp.CallToolResult, ActionOutput, error) {
	result, state, err := s.runRefAction(ctx, input.TabID, input.Ref, "browser_click", func(ctx context.Context, page *cdp.Page, ref *cdp.Element) error {
		return ref.Click(ctx)
	})
	return actionResponse(input.TabID, input.Ref, "browser_click", state, result, err)
}

func (s *Service) hover(ctx context.Context, _ *mcp.CallToolRequest, input RefInput) (*mcp.CallToolResult, ActionOutput, error) {
	result, state, err := s.runRefAction(ctx, input.TabID, input.Ref, "browser_hover", func(ctx context.Context, page *cdp.Page, ref *cdp.Element) error {
		return ref.Hover(ctx)
	})
	return actionResponse(input.TabID, input.Ref, "browser_hover", state, result, err)
}

func (s *Service) typeText(ctx context.Context, _ *mcp.CallToolRequest, input TypeInput) (*mcp.CallToolResult, ActionOutput, error) {
	if input.Mode == "" {
		input.Mode = "replace"
	}
	if input.Mode != "replace" && input.Mode != "append" {
		return actionResponse(input.TabID, input.Ref, "browser_type", "", operationResult{}, NewToolError("invalid_argument", "browser_type", "mode must be replace or append", nil))
	}
	result, state, err := s.runRefAction(ctx, input.TabID, input.Ref, "browser_type", func(ctx context.Context, page *cdp.Page, ref *cdp.Element) error {
		if input.Mode == "replace" {
			return ref.Fill(ctx, input.Text)
		}
		return ref.Type(ctx, input.Text)
	})
	return actionResponse(input.TabID, input.Ref, "browser_type", state, result, err)
}

func (s *Service) selectOptions(ctx context.Context, _ *mcp.CallToolRequest, input SelectInput) (*mcp.CallToolResult, ActionOutput, error) {
	if len(input.Values) == 0 {
		return actionResponse(input.TabID, input.Ref, "browser_select", "", operationResult{}, NewToolError("invalid_argument", "browser_select", "values must not be empty", nil))
	}
	result, state, err := s.runRefAction(ctx, input.TabID, input.Ref, "browser_select", func(ctx context.Context, page *cdp.Page, ref *cdp.Element) error {
		return ref.Select(ctx, input.Values)
	})
	return actionResponse(input.TabID, input.Ref, "browser_select", state, result, err)
}

func (s *Service) check(ctx context.Context, _ *mcp.CallToolRequest, input CheckInput) (*mcp.CallToolResult, ActionOutput, error) {
	result, state, err := s.runRefAction(ctx, input.TabID, input.Ref, "browser_check", func(ctx context.Context, page *cdp.Page, ref *cdp.Element) error {
		return ref.SetChecked(ctx, input.Checked)
	})
	return actionResponse(input.TabID, input.Ref, "browser_check", state, result, err)
}

func (s *Service) drag(ctx context.Context, _ *mcp.CallToolRequest, input DragInput) (*mcp.CallToolResult, ActionOutput, error) {
	result, state, err := s.runRefAction(ctx, input.TabID, input.SourceRef, "browser_drag", func(ctx context.Context, page *cdp.Page, source *cdp.Element) error {
		target, resolveErr := page.Element(ctx, input.TargetRef)
		if resolveErr != nil {
			return resolveErr
		}
		return source.DragTo(ctx, target)
	})
	return actionResponse(input.TabID, input.SourceRef, "browser_drag", state, result, err)
}

func (s *Service) pressKey(ctx context.Context, _ *mcp.CallToolRequest, input PressKeyInput) (*mcp.CallToolResult, ActionOutput, error) {
	if input.Key == "" {
		return actionResponse(input.TabID, input.Ref, "browser_press_key", "", operationResult{}, NewToolError("invalid_argument", "browser_press_key", "key is required", nil))
	}
	if input.Ref != "" {
		result, state, err := s.runRefAction(ctx, input.TabID, input.Ref, "browser_press_key", func(ctx context.Context, page *cdp.Page, ref *cdp.Element) error {
			return ref.Press(ctx, input.Key, keyOptions(input.Modifiers))
		})
		return actionResponse(input.TabID, input.Ref, "browser_press_key", state, result, err)
	}
	return s.runPageAction(ctx, input.TabID, "browser_press_key", func(ctx context.Context, page *cdp.Page) error {
		return page.Press(ctx, input.Key, keyOptions(input.Modifiers))
	})
}

func (s *Service) runPageAction(ctx context.Context, tabID, op string, action func(context.Context, *cdp.Page) error) (*mcp.CallToolResult, ActionOutput, error) {
	runtime, err := s.runtime(ctx, tabID)
	if err != nil {
		return actionResponse(tabID, "", op, "", operationResult{}, err)
	}
	if err := runtime.acquireModalSensitive(ctx, op, false); err != nil {
		return actionResponse(tabID, "", op, runtime.currentState(), operationResult{}, err)
	}
	releaseGate := true
	defer func() {
		if releaseGate {
			runtime.release()
		}
	}()
	before, _ := runtime.page.CurrentSnapshot()
	mark := runtime.observer.mark()
	runtime.setPhase(phaseExecuting)
	completed, modal, actionDone, err := waitForActionOrModal(ctx, runtime, false, func(actionCtx context.Context) error { return action(actionCtx, runtime.page) })
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
		return actionResponse(tabID, "", op, runtime.currentState(), operationResult{}, err)
	}
	if modal != "" {
		progress := "dispatched"
		if completed {
			progress = "completed"
		}
		return actionResponse(tabID, "", op, modal, operationResult{Warnings: []string{"action " + progress + "; snapshot capture deferred while modal is open"}}, nil)
	}
	result, err := settleAndCapture(ctx, runtime, before, mark)
	if err != nil {
		runtime.setPhase(phaseFailed)
	} else {
		runtime.setPhase(phaseCompleted)
	}
	return actionResponse(tabID, "", op, runtime.currentState(), result, err)
}

func (s *Service) scroll(ctx context.Context, _ *mcp.CallToolRequest, input ScrollInput) (*mcp.CallToolResult, ActionOutput, error) {
	if (input.X == nil) != (input.Y == nil) {
		return actionResponse(input.TabID, input.Ref, "browser_scroll", "", operationResult{}, NewToolError("invalid_argument", "browser_scroll", "x and y must be provided together", nil))
	}
	if input.Ref != "" {
		result, state, err := s.runRefAction(ctx, input.TabID, input.Ref, "browser_scroll", func(ctx context.Context, page *cdp.Page, ref *cdp.Element) error {
			if input.X != nil {
				return ref.ScrollTo(ctx, *input.X, *input.Y)
			}
			return ref.ScrollBy(ctx, input.DeltaX, input.DeltaY)
		})
		return actionResponse(input.TabID, input.Ref, "browser_scroll", state, result, err)
	}
	return s.runPageAction(ctx, input.TabID, "browser_scroll", func(ctx context.Context, page *cdp.Page) error {
		if input.X != nil {
			return page.ScrollTo(ctx, *input.X, *input.Y)
		}
		return page.ScrollBy(ctx, input.DeltaX, input.DeltaY)
	})
}

func (s *Service) upload(ctx context.Context, _ *mcp.CallToolRequest, input UploadInput) (*mcp.CallToolResult, ActionOutput, error) {
	if len(input.Files) == 0 {
		return actionResponse(input.TabID, input.Ref, "browser_upload", "", operationResult{}, NewToolError("invalid_argument", "browser_upload", "files must not be empty", nil))
	}
	result, state, err := s.runRefAction(ctx, input.TabID, input.Ref, "browser_upload", func(ctx context.Context, page *cdp.Page, ref *cdp.Element) error {
		return ref.SetFiles(ctx, input.Files)
	})
	if err == nil && state == TabFileChooser {
		if runtime, runtimeErr := s.runtime(ctx, input.TabID); runtimeErr == nil {
			runtime.setState(TabReady)
			state = TabReady
		}
	}
	return actionResponse(input.TabID, input.Ref, "browser_upload", state, result, err)
}

func (s *Service) wait(ctx context.Context, _ *mcp.CallToolRequest, input WaitInput) (*mcp.CallToolResult, WaitOutput, error) {
	runtime, err := s.runtime(ctx, input.TabID)
	if err != nil {
		out := WaitOutput{BaseOutput: failedBase(input.TabID, "", "browser_wait", "", err)}
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	if err := runtime.acquireModalSensitive(ctx, "browser_wait", false); err != nil {
		out := WaitOutput{BaseOutput: failedBase(input.TabID, runtime.currentState(), "browser_wait", "", err)}
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	defer runtime.release()
	runtime.setPhase(phaseSettling)
	timeout := time.Duration(input.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	switch input.Condition {
	case "time":
		duration, parseErr := parseTimeWait(input.Value)
		if parseErr != nil {
			err = parseErr
			break
		}
		timer := time.NewTimer(duration)
		select {
		case <-waitCtx.Done():
			err = waitCtx.Err()
		case <-timer.C:
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	case "network_idle":
		if runtime.observer.waitRequests(waitCtx, 0) {
			err = NewToolError("timeout", "browser_wait", "network did not become idle", waitCtx.Err())
		}
	case "text", "url":
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for err == nil {
			handle, resolveErr := runtime.page.ExecutionContext(waitCtx, cdp.ExecutionContextOptions{World: cdp.WorldMain})
			if resolveErr == nil {
				expression := "return location.href.includes(" + jsString(input.Value) + ")"
				if input.Condition == "text" {
					expression = "return (document.body?.innerText || '').includes(" + jsString(input.Value) + ")"
				}
				raw, evalErr := handle.EvalJSON(waitCtx, expression)
				if evalErr == nil && string(raw) == "true" {
					break
				}
			}
			select {
			case <-waitCtx.Done():
				err = NewToolError("timeout", "browser_wait", input.Condition+" condition was not met", waitCtx.Err())
			case <-ticker.C:
			}
		}
	default:
		err = NewToolError("invalid_argument", "browser_wait", "condition must be time, text, url, or network_idle", nil)
	}
	if err != nil {
		runtime.setPhase(phaseFailed)
		out := WaitOutput{BaseOutput: failedBase(input.TabID, runtime.currentState(), "browser_wait", "", err)}
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	runtime.setPhase(phaseCapturing)
	document, err := runtime.page.Snapshot(ctx)
	if err != nil {
		runtime.setPhase(phaseFailed)
		out := WaitOutput{BaseOutput: failedBase(input.TabID, runtime.currentState(), "browser_wait.capture", "", err)}
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	view, err := renderSnapshot(document, "", 0)
	if err != nil {
		runtime.setPhase(phaseFailed)
		out := WaitOutput{BaseOutput: failedBase(input.TabID, runtime.currentState(), "browser_wait.render", "", err)}
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	runtime.setPhase(phaseCompleted)
	return nil, WaitOutput{BaseOutput: BaseOutput{OK: true, TabID: input.TabID, State: runtime.currentState(), Warnings: document.Warnings}, Snapshot: view}, nil
}

func jsString(value string) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func keyOptions(values []string) cdp.KeyOptions {
	opts := cdp.KeyOptions{}
	for _, v := range values {
		opts.Modifiers = append(opts.Modifiers, cdp.Modifier(v))
	}
	return opts
}
