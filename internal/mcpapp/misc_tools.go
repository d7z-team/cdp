package mcpapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"gopkg.d7z.net/cdp"
)

func (s *Service) dialog(ctx context.Context, _ *mcp.CallToolRequest, input DialogInput) (*mcp.CallToolResult, DialogOutput, error) {
	runtime, err := s.runtime(ctx, input.TabID)
	if err != nil {
		out := DialogOutput{BaseOutput: failedBase(input.TabID, "", "browser_dialog", "", err)}
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	if err := runtime.acquireDialog(ctx); err != nil {
		out := DialogOutput{BaseOutput: failedBase(input.TabID, runtime.currentState(), "browser_dialog", "", err)}
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	defer runtime.releaseDialog()
	dialog, open, state := runtime.currentDialogState()
	if input.Action == "status" || input.Action == "" {
		out := DialogOutput{BaseOutput: BaseOutput{OK: true, TabID: input.TabID, State: state}, Open: open}
		if open {
			out.Dialog = &dialog
		}
		return nil, out, nil
	}
	if input.Action != "accept" && input.Action != "dismiss" {
		runtime.setPhase(phaseFailed)
		out := DialogOutput{BaseOutput: failedBase(input.TabID, state, "browser_dialog", "", NewToolError("invalid_argument", "browser_dialog", "action must be status, accept, or dismiss", nil)), Open: open}
		if open {
			out.Dialog = &dialog
		}
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	if !open {
		runtime.setPhase(phaseFailed)
		out := DialogOutput{BaseOutput: failedBase(input.TabID, state, "browser_dialog", "", NewToolError("invalid_argument", "browser_dialog", "no JavaScript dialog is open", nil))}
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	runtime.setPhase(phaseExecuting)
	next, nextOpen, err := runtime.page.HandleDialog(ctx, dialog.ID, input.Action == "accept", input.PromptText)
	if errors.Is(err, cdp.ErrDialogChanged) {
		err = NewToolError("stale_dialog", "browser_dialog", "the JavaScript dialog changed before it could be handled", err)
	} else if errors.Is(err, cdp.ErrDialogNotOpen) {
		err = NewToolError("stale_dialog", "browser_dialog", "the JavaScript dialog is no longer open", err)
	}
	if err != nil {
		runtime.setPhase(phaseFailed)
		current, currentOpen, currentState := runtime.currentDialogState()
		out := DialogOutput{BaseOutput: failedBase(input.TabID, currentState, "browser_dialog", "", err), Open: currentOpen}
		if currentOpen {
			out.Dialog = &current
		}
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	if !nextOpen {
		next, nextOpen, err = runtime.waitPendingActionOrDialog(ctx)
		if err != nil {
			runtime.setPhase(phaseFailed)
			out := DialogOutput{BaseOutput: failedBase(input.TabID, runtime.currentState(), "browser_dialog", "", fmt.Errorf("complete dialog-triggering action: %w", err)), Open: false}
			return &mcp.CallToolResult{IsError: true}, out, nil
		}
	}
	runtime.setPhase(phaseCompleted)
	state = runtime.stateWithDialog(nextOpen)
	out := DialogOutput{BaseOutput: BaseOutput{OK: true, TabID: input.TabID, State: state}, Open: nextOpen}
	if nextOpen {
		out.Dialog = &next
	}
	return nil, out, nil
}

func (s *Service) screenshot(ctx context.Context, _ *mcp.CallToolRequest, input ScreenshotInput) (*mcp.CallToolResult, ScreenshotOutput, error) {
	runtime, err := s.runtime(ctx, input.TabID)
	if err != nil {
		out := ScreenshotOutput{BaseOutput: failedBase(input.TabID, "", "browser_screenshot", input.Ref, err)}
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	if input.Ref != "" {
		err = runtime.acquireModalSensitive(ctx, "browser_screenshot", false)
	} else {
		err = runtime.acquire(ctx)
	}
	if err != nil {
		out := ScreenshotOutput{BaseOutput: failedBase(input.TabID, runtime.currentState(), "browser_screenshot", input.Ref, err)}
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	defer runtime.release()
	format := strings.ToLower(input.Format)
	if format == "" {
		format = "png"
	}
	if format != "png" && format != "jpeg" {
		runtime.setPhase(phaseFailed)
		out := ScreenshotOutput{BaseOutput: failedBase(input.TabID, runtime.currentState(), "browser_screenshot", input.Ref, NewToolError("invalid_argument", "browser_screenshot", "format must be png or jpeg", nil))}
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	runtime.setPhase(phaseCapturing)
	var imageData *image.RGBA
	if input.Ref == "" {
		imageData, err = runtime.page.Screenshot(ctx)
	} else {
		ref, resolveErr := runtime.page.Element(ctx, input.Ref)
		if resolveErr != nil {
			err = resolveErr
		} else {
			imageData, err = ref.Screenshot(ctx)
		}
	}
	if err != nil {
		runtime.setPhase(phaseFailed)
		out := ScreenshotOutput{BaseOutput: failedBase(input.TabID, runtime.currentState(), "browser_screenshot", input.Ref, err)}
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	var encoded bytes.Buffer
	mimeType := "image/png"
	if format == "jpeg" {
		err = jpeg.Encode(&encoded, imageData, &jpeg.Options{Quality: 85})
		mimeType = "image/jpeg"
	} else {
		err = png.Encode(&encoded, imageData)
	}
	if err != nil {
		runtime.setPhase(phaseFailed)
		out := ScreenshotOutput{BaseOutput: failedBase(input.TabID, runtime.currentState(), "browser_screenshot.encode", input.Ref, err)}
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	runtime.setPhase(phaseCompleted)
	out := ScreenshotOutput{BaseOutput: BaseOutput{OK: true, TabID: input.TabID, State: runtime.currentState()}, MIMEType: mimeType, Width: imageData.Bounds().Dx(), Height: imageData.Bounds().Dy()}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{Data: encoded.Bytes(), MIMEType: mimeType}}}, out, nil
}

func (s *Service) evaluate(ctx context.Context, _ *mcp.CallToolRequest, input EvaluateInput) (*mcp.CallToolResult, EvaluateOutput, error) {
	if strings.TrimSpace(input.Expression) == "" {
		out := EvaluateOutput{BaseOutput: failedBase(input.TabID, "", "browser_evaluate", input.Ref, NewToolError("invalid_argument", "browser_evaluate", "expression is required", nil))}
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	runtime, err := s.runtime(ctx, input.TabID)
	if err != nil {
		out := EvaluateOutput{BaseOutput: failedBase(input.TabID, "", "browser_evaluate", input.Ref, err)}
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	if err := runtime.acquireModalSensitive(ctx, "browser_evaluate", false); err != nil {
		out := EvaluateOutput{BaseOutput: failedBase(input.TabID, runtime.currentState(), "browser_evaluate", input.Ref, err)}
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	defer runtime.release()
	runtime.setPhase(phaseExecuting)
	var raw json.RawMessage
	body := "return await (" + input.Expression + ")"
	if input.Ref == "" {
		handle, resolveErr := runtime.page.ExecutionContext(ctx, cdp.ExecutionContextOptions{World: cdp.WorldMain})
		if resolveErr != nil {
			err = resolveErr
		} else {
			raw, err = handle.EvalJSON(ctx, body)
		}
	} else {
		ref, resolveErr := runtime.page.Element(ctx, input.Ref)
		if resolveErr != nil {
			err = resolveErr
		} else {
			raw, err = ref.EvalJSON(ctx, body)
		}
	}
	if err != nil {
		runtime.setPhase(phaseFailed)
		out := EvaluateOutput{BaseOutput: failedBase(input.TabID, runtime.currentState(), "browser_evaluate", input.Ref, err)}
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	value, err := decodeJSONValue(string(raw))
	if err != nil {
		runtime.setPhase(phaseFailed)
		out := EvaluateOutput{BaseOutput: failedBase(input.TabID, runtime.currentState(), "browser_evaluate.decode", input.Ref, err)}
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	runtime.setPhase(phaseCompleted)
	return nil, EvaluateOutput{BaseOutput: BaseOutput{OK: true, TabID: input.TabID, State: runtime.currentState()}, Value: value}, nil
}

func (s *Service) tabAction(ctx context.Context, _ *mcp.CallToolRequest, input TabsInput) (*mcp.CallToolResult, TabsOutput, error) {
	var err error
	switch input.Action {
	case "", "list":
	case "new":
		var page *cdp.Page
		page, err = s.browser.NewPage(ctx)
		if err == nil {
			runtime := s.attach(page)
			runtime.setPhase(phaseExecuting)
			err = runtime.page.Activate(ctx)
			if err == nil {
				runtime.setPhase(phaseCompleted)
			} else {
				runtime.setPhase(phaseFailed)
			}
		}
	case "select":
		var runtime *tabRuntime
		runtime, err = s.runtime(ctx, input.TabID)
		if err == nil {
			err = runtime.acquire(ctx)
		}
		if err == nil {
			runtime.setPhase(phaseExecuting)
			err = runtime.page.Activate(ctx)
			if err == nil {
				s.setActive(input.TabID)
				runtime.setPhase(phaseCompleted)
			} else {
				runtime.setPhase(phaseFailed)
			}
			runtime.release()
		}
	case "close":
		var runtime *tabRuntime
		runtime, err = s.runtime(ctx, input.TabID)
		if err == nil {
			err = runtime.acquire(ctx)
		}
		if err == nil {
			runtime.setPhase(phaseExecuting)
			err = s.browser.CloseTab(ctx, input.TabID)
			if err != nil {
				runtime.setPhase(phaseFailed)
			}
			runtime.release()
		}
		if err == nil {
			s.mu.Lock()
			runtime.setState(TabClosed)
			delete(s.tabs, input.TabID)
			if s.active == input.TabID {
				s.active = ""
			}
			s.mu.Unlock()
		}
	default:
		err = NewToolError("invalid_argument", "browser_tabs", "action must be list, new, select, or close", nil)
	}
	if err != nil {
		out := TabsOutput{BaseOutput: failedBase(input.TabID, "", "browser_tabs", "", err), Tabs: []TabInfo{}}
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	tabs, err := s.browser.ListTabs(ctx, s.activeID())
	if err != nil {
		out := TabsOutput{BaseOutput: failedBase(input.TabID, "", "browser_tabs.list", "", err), Tabs: []TabInfo{}}
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	return nil, TabsOutput{BaseOutput: BaseOutput{OK: true, TabID: s.activeID(), State: TabReady}, Tabs: tabs}, nil
}

func (s *Service) consoleEvents(ctx context.Context, _ *mcp.CallToolRequest, input EventsInput) (*mcp.CallToolResult, EventsOutput, error) {
	if s.browser.browser.Diagnostics() != cdp.DiagnosticsRuntime {
		err := NewToolError("diagnostics_disabled", "browser_console", "console collection requires a browser created with diagnostics=runtime", cdp.ErrDiagnosticsDisabled)
		return &mcp.CallToolResult{IsError: true}, EventsOutput{BaseOutput: failedBase(input.TabID, "", "browser_console", "", err), Events: []Event{}}, nil
	}
	return s.events(ctx, input, "browser_console", func(event Event) bool { return event.Type == "console" })
}

func (s *Service) requestEvents(ctx context.Context, _ *mcp.CallToolRequest, input EventsInput) (*mcp.CallToolResult, EventsOutput, error) {
	return s.events(ctx, input, "browser_requests", func(event Event) bool {
		switch event.Type {
		case "request", "response", "request_failed", "download":
			return true
		default:
			return false
		}
	})
}

func (s *Service) events(ctx context.Context, input EventsInput, op string, include func(Event) bool) (*mcp.CallToolResult, EventsOutput, error) {
	runtime, err := s.runtime(ctx, input.TabID)
	if err != nil {
		out := EventsOutput{BaseOutput: failedBase(input.TabID, "", op, "", err), Events: []Event{}}
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	events, cursor := runtime.observer.events.after(input.Cursor, input.Limit, include)
	return nil, EventsOutput{BaseOutput: BaseOutput{OK: true, TabID: input.TabID, State: runtime.currentState()}, Events: events, Cursor: cursor, CollectionStartedAt: runtime.observer.collectionStartedAt}, nil
}
