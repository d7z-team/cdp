package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"gopkg.d7z.net/cdp"
)

func callTool(t *testing.T, name string, arguments map[string]any) (*mcpsdk.CallToolResult, map[string]any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	result, err := clientSession.CallTool(ctx, &mcpsdk.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("%s protocol error: %v", name, err)
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("%s marshal structured content: %v", name, err)
	}
	var output map[string]any
	if err := json.Unmarshal(raw, &output); err != nil {
		t.Fatalf("%s decode structured content: %v", name, err)
	}
	return result, output
}

func requireOK(t *testing.T, name string, result *mcpsdk.CallToolResult, output map[string]any) {
	t.Helper()
	if result.IsError || output["ok"] != true {
		t.Fatalf("%s failed: result=%+v output=%v", name, result, output)
	}
}

func findRefs(t *testing.T, tabID string, query map[string]any) []string {
	t.Helper()
	query["tab_id"] = tabID
	result, output := callTool(t, "browser_find", query)
	requireOK(t, "browser_find", result, output)
	items, _ := output["matches"].([]any)
	refs := make([]string, 0, len(items))
	for _, item := range items {
		match, _ := item.(map[string]any)
		if ref, _ := match["ref"].(string); ref != "" {
			refs = append(refs, ref)
		}
	}
	return refs
}

func requireRef(t *testing.T, tabID string, query map[string]any) string {
	t.Helper()
	refs := findRefs(t, tabID, query)
	if len(refs) == 0 {
		t.Fatalf("no ref for query %v", query)
	}
	return refs[0]
}

func evaluate(t *testing.T, tabID, expression string) any {
	t.Helper()
	result, output := callTool(t, "browser_evaluate", map[string]any{"tab_id": tabID, "expression": expression})
	requireOK(t, "browser_evaluate", result, output)
	return output["value"]
}

func TestMCPCanceledOperationDoesNotPoisonNextInteraction(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	result, output := callTool(t, "browser_navigate", map[string]any{"url": fixtureServer.URL + "/mcp-runtime"})
	requireOK(t, "browser_navigate", result, output)
	tabID, _ := output["tab_id"].(string)

	hoverRef := requireRef(t, tabID, map[string]any{"role": "button", "name": "Hover Target"})
	actionCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := clientSession.CallTool(actionCtx, &mcpsdk.CallToolParams{
		Name:      "browser_hover",
		Arguments: map[string]any{"tab_id": tabID, "ref": hoverRef},
	})
	if err == nil || !errors.Is(actionCtx.Err(), context.DeadlineExceeded) {
		t.Fatalf("canceled hover error = %v", err)
	}

	counterRef := requireRef(t, tabID, map[string]any{"role": "button", "name": "Counter"})
	result, output = callTool(t, "browser_click", map[string]any{"tab_id": tabID, "ref": counterRef})
	requireOK(t, "browser_click after cancellation", result, output)
	if got := evaluate(t, tabID, "document.querySelector('#counter').textContent"); got != "Clicked 1" {
		t.Fatalf("counter after canceled operation recovery = %v", got)
	}
}

func TestMCPCompleteWorkflow(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	result, output := callTool(t, "browser_navigate", map[string]any{"url": fixtureServer.URL + "/mcp-runtime"})
	requireOK(t, "browser_navigate", result, output)
	tabID, _ := output["tab_id"].(string)
	snapshot, _ := output["snapshot"].(map[string]any)
	markdown, _ := snapshot["markdown"].(string)
	for _, text := range []string{"MCP Runtime", "Offscreen Action", "Disabled Action", "Shadow Action", "frame-button"} {
		if !strings.Contains(markdown, text) {
			t.Fatalf("snapshot missing %q:\n%s", text, markdown)
		}
	}
	if scrollY := evaluate(t, tabID, "window.scrollY"); scrollY != float64(0) {
		t.Fatalf("snapshot changed scroll position: %v", scrollY)
	}
	result, output = callTool(t, "browser_snapshot", map[string]any{"tab_id": tabID, "max_runes": 240})
	requireOK(t, "browser_snapshot", result, output)
	pageOne := output["snapshot"].(map[string]any)
	cursor, _ := pageOne["cursor"].(string)
	if cursor == "" || pageOne["has_more"] != true {
		t.Fatalf("snapshot did not paginate: %v", pageOne)
	}
	result, output = callTool(t, "browser_snapshot", map[string]any{"tab_id": tabID, "max_runes": 240, "cursor": cursor})
	requireOK(t, "browser_snapshot cursor", result, output)
	disabledRefs := findRefs(t, tabID, map[string]any{"text": "Disabled Action", "states": []string{"disabled"}})
	if len(disabledRefs) != 1 {
		t.Fatalf("disabled matches = %v", disabledRefs)
	}

	counterRef := requireRef(t, tabID, map[string]any{"role": "button", "name": "Counter"})
	result, output = callTool(t, "browser_click", map[string]any{"tab_id": tabID, "ref": counterRef})
	requireOK(t, "browser_click", result, output)
	if got := evaluate(t, tabID, "document.querySelector('#counter').textContent"); got != "Clicked 1" {
		t.Fatalf("counter = %v", got)
	}
	staleResult, staleOutput := callTool(t, "browser_click", map[string]any{"tab_id": tabID, "ref": counterRef})
	staleError, _ := staleOutput["error"].(map[string]any)
	if !staleResult.IsError || staleError["code"] != "stale_target" {
		t.Fatalf("old ref was not stale: result=%+v output=%v", staleResult, staleOutput)
	}

	emailRef := requireRef(t, tabID, map[string]any{"role": "textbox", "name": "Email"})
	result, output = callTool(t, "browser_type", map[string]any{"tab_id": tabID, "ref": emailRef, "text": "ai@example.com", "mode": "replace"})
	requireOK(t, "browser_type", result, output)
	if got := evaluate(t, tabID, "document.querySelector('#email').value"); got != "ai@example.com" {
		t.Fatalf("email = %v", got)
	}
	rememberRef := requireRef(t, tabID, map[string]any{"role": "checkbox", "name": "Remember me"})
	result, output = callTool(t, "browser_check", map[string]any{"tab_id": tabID, "ref": rememberRef, "checked": true})
	requireOK(t, "browser_check", result, output)
	countryRef := requireRef(t, tabID, map[string]any{"role": "combobox", "name": "Country"})
	result, output = callTool(t, "browser_select", map[string]any{"tab_id": tabID, "ref": countryRef, "values": []string{"us"}})
	requireOK(t, "browser_select", result, output)
	if got := evaluate(t, tabID, "document.querySelector('#country').value"); got != "us" {
		t.Fatalf("country = %v", got)
	}
	hoverRef := requireRef(t, tabID, map[string]any{"role": "button", "name": "Hover Target"})
	result, output = callTool(t, "browser_hover", map[string]any{"tab_id": tabID, "ref": hoverRef})
	requireOK(t, "browser_hover", result, output)
	if got := evaluate(t, tabID, "document.querySelector('#hover-output').textContent"); got != "Hovered" {
		t.Fatalf("hover output = %v", got)
	}
	result, output = callTool(t, "browser_press_key", map[string]any{"tab_id": tabID, "key": "F2"})
	requireOK(t, "browser_press_key", result, output)
	if got := evaluate(t, tabID, "document.querySelector('#key-output').textContent"); got != "F2" {
		t.Fatalf("key output = %v", got)
	}

	shadowRef := requireRef(t, tabID, map[string]any{"role": "button", "name": "Shadow Action"})
	result, output = callTool(t, "browser_click", map[string]any{"tab_id": tabID, "ref": shadowRef})
	requireOK(t, "shadow click", result, output)
	if got := evaluate(t, tabID, "document.querySelector('snapshot-shadow').shadowRoot.querySelector('output').textContent"); got != "Shadow clicked" {
		t.Fatalf("shadow result = %v", got)
	}
	frameRef := requireRef(t, tabID, map[string]any{"role": "button", "text": "frame-button"})
	result, output = callTool(t, "browser_click", map[string]any{"tab_id": tabID, "ref": frameRef})
	requireOK(t, "same frame click", result, output)
	frameDelta, err := json.Marshal(output["delta"])
	if err != nil || !strings.Contains(string(frameDelta), "frame-settled") {
		t.Fatalf("same frame did not settle recursively: delta=%s err=%v", frameDelta, err)
	}
	remainingFrameRefs := findRefs(t, tabID, map[string]any{"role": "button", "text": "frame-button"})
	if len(remainingFrameRefs) == 0 {
		t.Fatal("cross-origin frame ref missing after same-origin frame click")
	}
	result, output = callTool(t, "browser_click", map[string]any{"tab_id": tabID, "ref": remainingFrameRefs[0]})
	requireOK(t, "cross frame click", result, output)

	xhrRef := requireRef(t, tabID, map[string]any{"role": "button", "name": "Load Data"})
	result, output = callTool(t, "browser_click", map[string]any{"tab_id": tabID, "ref": xhrRef})
	requireOK(t, "xhr click", result, output)
	xhrDelta, err := json.Marshal(output["delta"])
	if err != nil || !strings.Contains(string(xhrDelta), "Loaded") {
		t.Fatalf("leaf text change missing from delta: delta=%s err=%v", xhrDelta, err)
	}
	if got := evaluate(t, tabID, "document.querySelector('#xhr-output').textContent"); got != "Loaded" {
		t.Fatalf("XHR did not settle: %v", got)
	}

	dialogRef := requireRef(t, tabID, map[string]any{"role": "button", "name": "Open Dialog"})
	result, output = callTool(t, "browser_click", map[string]any{"tab_id": tabID, "ref": dialogRef})
	requireOK(t, "dialog click", result, output)
	if output["state"] != "dialog" {
		t.Fatalf("dialog state = %v", output)
	}
	result, output = callTool(t, "browser_dialog", map[string]any{"tab_id": tabID, "action": "status"})
	requireOK(t, "browser_dialog status", result, output)
	dialog, _ := output["dialog"].(map[string]any)
	if output["open"] != true || output["state"] != "dialog" || dialog["id"].(float64) <= 0 || dialog["type"] != "alert" || dialog["message"] != "MCP dialog" {
		t.Fatalf("dialog status = %v", output)
	}
	blockedResult, blockedOutput := callTool(t, "browser_evaluate", map[string]any{"tab_id": tabID, "expression": "document.title"})
	blockedError, _ := blockedOutput["error"].(map[string]any)
	if !blockedResult.IsError || blockedError["code"] != "dialog_open" {
		t.Fatalf("evaluate was not blocked by dialog: result=%+v output=%v", blockedResult, blockedOutput)
	}
	blockedResult, blockedOutput = callTool(t, "browser_navigate", map[string]any{"tab_id": tabID, "url": fixtureServer.URL + "/mcp-runtime"})
	blockedError, _ = blockedOutput["error"].(map[string]any)
	if !blockedResult.IsError || blockedError["code"] != "dialog_open" {
		t.Fatalf("navigate was not blocked by dialog: result=%+v output=%v", blockedResult, blockedOutput)
	}
	result, output = callTool(t, "browser_dialog", map[string]any{"tab_id": tabID, "action": "dismiss"})
	requireOK(t, "browser_dialog", result, output)
	if output["open"] != false || output["state"] != "ready" || evaluate(t, tabID, "document.title") != "MCP Runtime" {
		t.Fatalf("dialog did not return to ready: %v", output)
	}

	promptRef := requireRef(t, tabID, map[string]any{"role": "button", "name": "Open Prompt"})
	result, output = callTool(t, "browser_click", map[string]any{"tab_id": tabID, "ref": promptRef})
	requireOK(t, "prompt click", result, output)
	result, output = callTool(t, "browser_dialog", map[string]any{"tab_id": tabID, "action": "status"})
	requireOK(t, "prompt status", result, output)
	dialog, _ = output["dialog"].(map[string]any)
	if dialog["type"] != "prompt" || dialog["message"] != "MCP prompt" || dialog["default_prompt"] != "default answer" {
		t.Fatalf("prompt detail = %v", output)
	}
	result, output = callTool(t, "browser_dialog", map[string]any{"tab_id": tabID, "action": "accept", "prompt_text": "typed answer"})
	requireOK(t, "prompt accept", result, output)
	if got := evaluate(t, tabID, "document.querySelector('#prompt-output').textContent"); got != "typed answer" {
		t.Fatalf("prompt output = %v", got)
	}

	confirmRef := requireRef(t, tabID, map[string]any{"role": "button", "name": "Open Confirm"})
	result, output = callTool(t, "browser_click", map[string]any{"tab_id": tabID, "ref": confirmRef})
	requireOK(t, "confirm click", result, output)
	result, output = callTool(t, "browser_dialog", map[string]any{"tab_id": tabID, "action": "dismiss"})
	requireOK(t, "confirm dismiss", result, output)
	if got := evaluate(t, tabID, "document.querySelector('#confirm-output').textContent"); got != "dismissed" {
		t.Fatalf("confirm output = %v", got)
	}

	sequentialRef := requireRef(t, tabID, map[string]any{"role": "button", "name": "Open Sequential Dialogs"})
	result, output = callTool(t, "browser_click", map[string]any{"tab_id": tabID, "ref": sequentialRef})
	requireOK(t, "sequential dialog click", result, output)
	result, output = callTool(t, "browser_dialog", map[string]any{"tab_id": tabID, "action": "status"})
	requireOK(t, "first sequential dialog status", result, output)
	dialog, _ = output["dialog"].(map[string]any)
	if dialog["message"] != "MCP first" {
		t.Fatalf("first sequential dialog = %v", output)
	}
	result, output = callTool(t, "browser_dialog", map[string]any{"tab_id": tabID, "action": "accept"})
	requireOK(t, "first sequential dialog accept", result, output)
	dialog, _ = output["dialog"].(map[string]any)
	if output["open"] != true || output["state"] != "dialog" || dialog["message"] != "MCP second" {
		t.Fatalf("second sequential dialog = %v", output)
	}
	result, output = callTool(t, "browser_dialog", map[string]any{"tab_id": tabID, "action": "dismiss"})
	requireOK(t, "second sequential dialog dismiss", result, output)
	if output["open"] != false || output["state"] != "ready" || evaluate(t, tabID, "document.querySelector('#sequential-output').textContent") != "complete" {
		t.Fatalf("sequential dialogs did not complete: %v", output)
	}

	attachCtx, cancelAttach := context.WithTimeout(context.Background(), 10*time.Second)
	latePage, err := browserService.NewPage(attachCtx)
	if err == nil {
		err = latePage.Navigate(attachCtx, fixtureServer.URL+"/mcp-runtime", cdp.NavigateOptions{})
	}
	if err == nil {
		err = latePage.SetDialogPolicy(cdp.DialogPassthrough)
	}
	if err != nil {
		cancelAttach()
		t.Fatal(err)
	}
	_, _, lateDialogChanged := latePage.DialogState()
	if err := latePage.Eval(attachCtx, `setTimeout(() => alert('dialog before runtime attach'), 0)`, nil); err != nil {
		cancelAttach()
		t.Fatal(err)
	}
	select {
	case <-lateDialogChanged:
	case <-attachCtx.Done():
		cancelAttach()
		t.Fatal("dialog did not open before runtime attach")
	}
	result, output = callTool(t, "browser_dialog", map[string]any{"tab_id": latePage.ID(), "action": "status"})
	requireOK(t, "late-attached dialog status", result, output)
	dialog, _ = output["dialog"].(map[string]any)
	if output["state"] != "dialog" || dialog["message"] != "dialog before runtime attach" {
		cancelAttach()
		t.Fatalf("late-attached dialog = %v", output)
	}
	result, output = callTool(t, "browser_dialog", map[string]any{"tab_id": latePage.ID(), "action": "dismiss"})
	requireOK(t, "late-attached dialog dismiss", result, output)
	if err := latePage.Close(attachCtx); err != nil {
		cancelAttach()
		t.Fatal(err)
	}
	cancelAttach()

	uploadPath := filepath.Join(t.TempDir(), "fixture.txt")
	if err := os.WriteFile(uploadPath, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	uploadRef := requireRef(t, tabID, map[string]any{"name": "Upload"})
	result, output = callTool(t, "browser_click", map[string]any{"tab_id": tabID, "ref": uploadRef})
	requireOK(t, "browser_click file input", result, output)
	if output["state"] != "file_chooser" {
		t.Fatalf("file chooser state = %v", output)
	}
	result, output = callTool(t, "browser_upload", map[string]any{"tab_id": tabID, "ref": uploadRef, "files": []string{uploadPath}})
	requireOK(t, "browser_upload", result, output)
	if got := evaluate(t, tabID, "document.querySelector('#upload').files[0].name"); got != "fixture.txt" {
		t.Fatalf("upload name = %v", got)
	}

	scrollRef := requireRef(t, tabID, map[string]any{"name": "Scrollable"})
	result, output = callTool(t, "browser_scroll", map[string]any{"tab_id": tabID, "ref": scrollRef, "x": 40, "y": 60})
	requireOK(t, "browser_scroll", result, output)
	if got := evaluate(t, tabID, "document.querySelector('#scroll-box').scrollTop"); got != float64(60) {
		t.Fatalf("scrollTop = %v", got)
	}

	sourceRef := requireRef(t, tabID, map[string]any{"role": "button", "name": "Drag Source"})
	targetRef := requireRef(t, tabID, map[string]any{"role": "button", "name": "Drop Target"})
	result, output = callTool(t, "browser_drag", map[string]any{"tab_id": tabID, "source_ref": sourceRef, "target_ref": targetRef})
	requireOK(t, "browser_drag", result, output)
	if got := evaluate(t, tabID, "document.querySelector('#drop-output').textContent"); got != "Dropped" {
		t.Fatalf("drop output = %v", got)
	}

	result, output = callTool(t, "browser_screenshot", map[string]any{"tab_id": tabID, "format": "png"})
	requireOK(t, "browser_screenshot", result, output)
	if len(result.Content) != 1 {
		t.Fatalf("screenshot content = %+v", result.Content)
	}
	image, ok := result.Content[0].(*mcpsdk.ImageContent)
	if !ok || image.MIMEType != "image/png" || len(image.Data) == 0 {
		t.Fatalf("screenshot is not ImageContent: %#v", result.Content[0])
	}
	if got := evaluate(t, tabID, "Promise.resolve(42)"); got != float64(42) {
		t.Fatalf("async evaluate = %v", got)
	}
	counterRef = requireRef(t, tabID, map[string]any{"role": "button", "name": "Counter"})
	result, output = callTool(t, "browser_evaluate", map[string]any{"tab_id": tabID, "ref": counterRef, "expression": "this.id"})
	requireOK(t, "target evaluate", result, output)
	if output["value"] != "counter" {
		t.Fatalf("target evaluate = %v", output["value"])
	}
	result, output = callTool(t, "browser_screenshot", map[string]any{"tab_id": tabID, "ref": counterRef, "format": "png"})
	requireOK(t, "element screenshot", result, output)
	if image, ok := result.Content[0].(*mcpsdk.ImageContent); !ok || len(image.Data) == 0 {
		t.Fatalf("element screenshot content = %#v", result.Content)
	}
	_ = evaluate(t, tabID, "(setTimeout(() => { document.querySelector('#xhr-output').textContent = 'Waited' }, 80), true)")
	result, output = callTool(t, "browser_wait", map[string]any{"tab_id": tabID, "condition": "text", "value": "Waited", "timeout_ms": 1000})
	requireOK(t, "browser_wait text", result, output)
	result, output = callTool(t, "browser_wait", map[string]any{"tab_id": tabID, "condition": "url", "value": "/mcp-runtime", "timeout_ms": 1000})
	requireOK(t, "browser_wait url", result, output)
	result, output = callTool(t, "browser_wait", map[string]any{"tab_id": tabID, "condition": "network_idle", "timeout_ms": 1000})
	requireOK(t, "browser_wait network", result, output)
	_, _ = callTool(t, "browser_evaluate", map[string]any{"tab_id": tabID, "expression": "(console.log('mcp-console'), fetch('/api/data').then(r => r.json()))"})
	result, output = callTool(t, "browser_console", map[string]any{"tab_id": tabID, "cursor": 0})
	requireOK(t, "browser_console", result, output)
	if events, _ := output["events"].([]any); len(events) == 0 {
		t.Fatal("console events are empty")
	} else {
		found := false
		for _, raw := range events {
			event, _ := raw.(map[string]any)
			data, _ := event["data"].(map[string]any)
			if data["text"] == "mcp-console" && data["level"] == "log" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("structured console event missing: %v", events)
		}
	}
	result, output = callTool(t, "browser_requests", map[string]any{"tab_id": tabID, "cursor": 0})
	requireOK(t, "browser_requests", result, output)
	if events, _ := output["events"].([]any); len(events) == 0 {
		t.Fatal("request events are empty")
	} else {
		found := false
		for _, raw := range events {
			event, _ := raw.(map[string]any)
			data, _ := event["data"].(map[string]any)
			url, _ := data["url"].(string)
			if strings.Contains(url, "/api/data") && data["resource_type"] == "fetch" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("structured fetch request missing: %v", events)
		}
	}
	downloadRef := requireRef(t, tabID, map[string]any{"role": "link", "name": "Download report"})
	result, output = callTool(t, "browser_click", map[string]any{"tab_id": tabID, "ref": downloadRef})
	requireOK(t, "download click", result, output)
	offscreenRef := requireRef(t, tabID, map[string]any{"role": "button", "name": "Offscreen Action"})
	_ = evaluate(t, tabID, "(document.querySelector('#offscreen').remove(), true)")
	detachedResult, detachedOutput := callTool(t, "browser_click", map[string]any{"tab_id": tabID, "ref": offscreenRef})
	detachedError, _ := detachedOutput["error"].(map[string]any)
	if !detachedResult.IsError || detachedError["code"] != "stale_target" {
		t.Fatalf("detached ref result=%+v output=%v", detachedResult, detachedOutput)
	}
	result, output = callTool(t, "browser_snapshot", map[string]any{"tab_id": tabID})
	requireOK(t, "snapshot after detach", result, output)

	debugPage, err := http.Get(httpServer.URL + "/debug")
	if err != nil {
		t.Fatal(err)
	}
	debugHTML, err := io.ReadAll(debugPage.Body)
	_ = debugPage.Body.Close()
	if err != nil || debugPage.StatusCode != http.StatusOK || !bytes.Contains(debugHTML, []byte(`id="viewport"`)) {
		t.Fatalf("debug page status=%d bytes=%d err=%v", debugPage.StatusCode, len(debugHTML), err)
	}

	debugCtx, cancelDebug := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancelDebug()
	streamURL := strings.Replace(httpServer.URL, "http://", "ws://", 1) + "/api/debug/stream?tab_id=" + tabID
	stream, _, err := websocket.Dial(debugCtx, streamURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.CloseNow() }()
	stream.SetReadLimit(16 << 20)
	stateSeen := false
	var firstFrameSequence float64
	var currentFrameSequence float64
	for firstFrameSequence == 0 {
		messageType, payload, readErr := stream.Read(debugCtx)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if messageType != websocket.MessageText {
			continue
		}
		var message map[string]any
		if err := json.Unmarshal(payload, &message); err != nil {
			t.Fatal(err)
		}
		if message["type"] == "state" {
			stateSeen = true
		}
		if message["type"] != "frame" {
			continue
		}
		frame, _ := message["frame"].(map[string]any)
		firstFrameSequence, _ = frame["sequence"].(float64)
		messageType, payload, readErr = stream.Read(debugCtx)
		if readErr != nil || messageType != websocket.MessageBinary || len(payload) < 100 || payload[0] != 0xff || payload[1] != 0xd8 {
			t.Fatalf("initial debug frame type=%v bytes=%d err=%v", messageType, len(payload), readErr)
		}
		currentFrameSequence = firstFrameSequence
	}
	for !stateSeen {
		messageType, payload, readErr := stream.Read(debugCtx)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if messageType == websocket.MessageText {
			var message map[string]any
			if json.Unmarshal(payload, &message) == nil && message["type"] == "state" {
				stateSeen = true
			}
		}
	}

	healthResponse, err := http.Get(httpServer.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	var health map[string]any
	err = json.NewDecoder(healthResponse.Body).Decode(&health)
	_ = healthResponse.Body.Close()
	if err != nil || health["browser_alive"] != true || health["viewers"].(float64) < 1 || len(health["tools"].([]any)) != 20 {
		t.Fatalf("debug health=%v err=%v", health, err)
	}

	currentResponse, err := http.Get(httpServer.URL + "/api/debug/current?tab_id=" + tabID)
	if err != nil {
		t.Fatal(err)
	}
	var current map[string]any
	err = json.NewDecoder(currentResponse.Body).Decode(&current)
	_ = currentResponse.Body.Close()
	currentSnapshot, _ := current["snapshot"].(map[string]any)
	if err != nil || currentResponse.StatusCode != http.StatusOK || !strings.Contains(fmt.Sprint(currentSnapshot["markdown"]), "MCP Runtime") {
		t.Fatalf("debug current status=%d output=%v err=%v", currentResponse.StatusCode, current, err)
	}

	toolResponse, err := http.Post(httpServer.URL+"/api/debug/tools/browser_evaluate", "application/json", strings.NewReader(fmt.Sprintf(`{"tab_id":%q,"expression":"6*7"}`, tabID)))
	if err != nil {
		t.Fatal(err)
	}
	var toolOutput map[string]any
	err = json.NewDecoder(toolResponse.Body).Decode(&toolOutput)
	_ = toolResponse.Body.Close()
	if err != nil || toolResponse.StatusCode != http.StatusOK || toolOutput["ok"] != true || toolOutput["value"] != float64(42) {
		t.Fatalf("debug tool status=%d output=%v err=%v", toolResponse.StatusCode, toolOutput, err)
	}

	screenshotResponse, err := http.Get(httpServer.URL + "/api/debug/screenshot?tab_id=" + tabID + "&format=png")
	if err != nil {
		t.Fatal(err)
	}
	screenshotData, err := io.ReadAll(screenshotResponse.Body)
	_ = screenshotResponse.Body.Close()
	if err != nil || screenshotResponse.StatusCode != http.StatusOK || screenshotResponse.Header.Get("Content-Type") != "image/png" || len(screenshotData) < 100 {
		t.Fatalf("debug screenshot status=%d type=%q bytes=%d err=%v", screenshotResponse.StatusCode, screenshotResponse.Header.Get("Content-Type"), len(screenshotData), err)
	}
	resumed := false
	for !resumed {
		messageType, payload, readErr := stream.Read(debugCtx)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if messageType != websocket.MessageText {
			continue
		}
		var message map[string]any
		if json.Unmarshal(payload, &message) != nil || message["type"] != "frame" {
			continue
		}
		frame, _ := message["frame"].(map[string]any)
		if frame["sequence"].(float64) <= firstFrameSequence {
			continue
		}
		messageType, payload, readErr = stream.Read(debugCtx)
		if readErr != nil || messageType != websocket.MessageBinary || len(payload) < 100 {
			t.Fatalf("resumed frame type=%v bytes=%d err=%v", messageType, len(payload), readErr)
		}
		currentFrameSequence = frame["sequence"].(float64)
		resumed = true
	}

	eventSeen, burstEventCount := false, 0
	consumeEvents := func(message map[string]any) {
		if message["type"] != "events" {
			return
		}
		for _, rawEvent := range message["events"].([]any) {
			event, _ := rawEvent.(map[string]any)
			data, _ := event["data"].(map[string]any)
			text := fmt.Sprint(data["text"])
			if strings.Contains(text, "debug-stream-event") {
				eventSeen = true
			}
			if strings.HasPrefix(text, "debug-burst-") {
				burstEventCount++
			}
		}
	}
	readFrameAfter := func(after float64) {
		for currentFrameSequence <= after {
			messageType, payload, readErr := stream.Read(debugCtx)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if messageType != websocket.MessageText {
				continue
			}
			var message map[string]any
			if json.Unmarshal(payload, &message) != nil || message["type"] != "frame" {
				continue
			}
			frame, _ := message["frame"].(map[string]any)
			sequence, _ := frame["sequence"].(float64)
			messageType, payload, readErr = stream.Read(debugCtx)
			if readErr != nil || messageType != websocket.MessageBinary || len(payload) < 100 {
				t.Fatalf("debug frame type=%v bytes=%d err=%v", messageType, len(payload), readErr)
			}
			currentFrameSequence = sequence
		}
	}
	debuggedPage, err := browserService.Page(debugCtx, tabID)
	if err != nil {
		t.Fatal(err)
	}
	syncCurrentFrame := func() {
		for {
			sequence := debuggedPage.ScreencastSequence()
			for uint64(currentFrameSequence) < sequence {
				readFrameAfter(currentFrameSequence)
			}
			time.Sleep(25 * time.Millisecond)
			if debuggedPage.ScreencastSequence() == sequence && uint64(currentFrameSequence) == sequence {
				return
			}
		}
	}
	readResult := func(id string) map[string]any {
		for {
			messageType, payload, readErr := stream.Read(debugCtx)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if messageType != websocket.MessageText {
				continue
			}
			var message map[string]any
			if err := json.Unmarshal(payload, &message); err != nil {
				t.Fatal(err)
			}
			consumeEvents(message)
			if message["type"] == "frame" {
				frame, _ := message["frame"].(map[string]any)
				sequence, _ := frame["sequence"].(float64)
				messageType, payload, readErr = stream.Read(debugCtx)
				if readErr != nil || messageType != websocket.MessageBinary || len(payload) < 100 {
					t.Fatalf("debug frame while waiting result type=%v bytes=%d err=%v", messageType, len(payload), readErr)
				}
				currentFrameSequence = sequence
			}
			if message["type"] == "result" && message["id"] == id {
				return message
			}
		}
	}
	sendCommand := func(command map[string]any) map[string]any {
		if err := wsjson.Write(debugCtx, stream, command); err != nil {
			t.Fatal(err)
		}
		return readResult(command["id"].(string))
	}
	control := func(id, action string, values map[string]any) map[string]any {
		command := map[string]any{"id": id, "type": "control", "action": action}
		if action == "move" || action == "click" || action == "double_click" || action == "right_click" || action == "drag" || action == "wheel" {
			syncCurrentFrame()
			command["frame_sequence"] = currentFrameSequence
		}
		for key, value := range values {
			command[key] = value
		}
		message := sendCommand(command)
		if message["ok"] != true {
			t.Fatalf("debug control %s failed: %v", action, message)
		}
		return message
	}
	center := func(selector string) map[string]any {
		value := evaluate(t, tabID, fmt.Sprintf(`(() => { const r = document.querySelector(%q).getBoundingClientRect(); return {x:r.left+r.width/2,y:r.top+r.height/2} })()`, selector))
		point, _ := value.(map[string]any)
		if point["x"] == nil || point["y"] == nil {
			t.Fatalf("no center for %s: %v", selector, value)
		}
		return point
	}

	target := center("#remote-target")
	staleCommand := map[string]any{"id": "control-stale", "type": "control", "action": "click", "frame_sequence": currentFrameSequence + 1000, "x": target["x"], "y": target["y"]}
	staleFrameResult := sendCommand(staleCommand)
	staleFrameError, _ := staleFrameResult["error"].(map[string]any)
	if staleFrameResult["ok"] == true || staleFrameError["code"] != "stale_frame" || evaluate(t, tabID, "document.querySelector('#remote-output').textContent") != "remote-idle" {
		t.Fatalf("stale debug frame had a side effect: %v", staleFrameResult)
	}
	control("control-click", "click", target)
	if got := evaluate(t, tabID, "document.querySelector('#remote-output').textContent"); got != "remote-click" {
		t.Fatalf("remote click output=%v", got)
	}
	control("control-double", "double_click", target)
	if got := evaluate(t, tabID, "document.querySelector('#remote-output').textContent"); got != "remote-double" {
		t.Fatalf("remote double output=%v", got)
	}
	control("control-right", "right_click", target)
	if got := evaluate(t, tabID, "document.querySelector('#remote-output').textContent"); got != "remote-right" {
		t.Fatalf("remote right output=%v", got)
	}
	hoverTarget := center("#hover")
	control("control-move", "move", hoverTarget)
	if got := evaluate(t, tabID, "document.querySelector('#hover-output').textContent"); got != "Hovered" {
		t.Fatalf("remote move did not dispatch mouse movement: %v", got)
	}

	emailPoint := center("#email")
	control("control-focus", "click", emailPoint)
	control("control-text", "text", map[string]any{"text": " remote"})
	if got := fmt.Sprint(evaluate(t, tabID, "document.querySelector('#email').value")); !strings.Contains(got, "remote") {
		t.Fatalf("remote text value=%q", got)
	}
	control("control-key", "key", map[string]any{"key": "F4"})
	if got := evaluate(t, tabID, "document.querySelector('#key-output').textContent"); got != "F4" {
		t.Fatalf("remote key output=%v", got)
	}

	scrollPoint := center("#scroll-box")
	control("control-wheel", "wheel", map[string]any{"x": scrollPoint["x"], "y": scrollPoint["y"], "delta_y": 120})
	if got, _ := evaluate(t, tabID, "document.querySelector('#scroll-box').scrollTop").(float64); got <= 60 {
		t.Fatalf("remote wheel scrollTop=%v", got)
	}
	_ = evaluate(t, tabID, "(document.querySelector('#drop-output').textContent='', true)")
	dragSource, dragTarget := center("#drag-source"), center("#drag-target")
	control("control-drag", "drag", map[string]any{"x": dragSource["x"], "y": dragSource["y"], "to_x": dragTarget["x"], "to_y": dragTarget["y"]})
	if got := evaluate(t, tabID, "document.querySelector('#drop-output').textContent"); got != "Dropped" {
		t.Fatalf("remote drag output=%v", got)
	}

	dialogPoint := center("#dialog")
	dialogResult := control("control-dialog", "click", dialogPoint)
	dialogOutput, _ := dialogResult["output"].(map[string]any)
	if dialogOutput["state"] != "dialog" {
		t.Fatalf("remote dialog state=%v", dialogResult)
	}
	dismissResult := sendCommand(map[string]any{"id": "tool-dialog", "type": "tool", "name": "browser_dialog", "arguments": map[string]any{"action": "dismiss"}})
	if dismissResult["ok"] != true {
		t.Fatalf("debug dialog dismiss=%v", dismissResult)
	}

	uploadRef = requireRef(t, tabID, map[string]any{"name": "Upload"})
	uploadPoint := center("#upload")
	uploadOpen := control("control-upload", "click", uploadPoint)
	uploadOpenOutput, _ := uploadOpen["output"].(map[string]any)
	if uploadOpenOutput["state"] != "file_chooser" {
		t.Fatalf("remote upload state=%v", uploadOpen)
	}
	var uploadBody bytes.Buffer
	uploadWriter := multipart.NewWriter(&uploadBody)
	_ = uploadWriter.WriteField("tab_id", tabID)
	_ = uploadWriter.WriteField("ref", uploadRef)
	uploadPart, err := uploadWriter.CreateFormFile("files", "remote.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = uploadPart.Write([]byte("remote upload"))
	if err := uploadWriter.Close(); err != nil {
		t.Fatal(err)
	}
	uploadRequest, _ := http.NewRequestWithContext(debugCtx, http.MethodPost, httpServer.URL+"/api/debug/upload", &uploadBody)
	uploadRequest.Header.Set("Content-Type", uploadWriter.FormDataContentType())
	uploadResponse, err := http.DefaultClient.Do(uploadRequest)
	if err != nil {
		t.Fatal(err)
	}
	var uploadOutput map[string]any
	err = json.NewDecoder(uploadResponse.Body).Decode(&uploadOutput)
	_ = uploadResponse.Body.Close()
	if err != nil || uploadOutput["ok"] != true || evaluate(t, tabID, "document.querySelector('#upload').files[0].name") != "remote.txt" {
		t.Fatalf("debug upload status=%d output=%v err=%v", uploadResponse.StatusCode, uploadOutput, err)
	}

	streamEvaluate := sendCommand(map[string]any{"id": "tool-evaluate", "type": "tool", "name": "browser_evaluate", "arguments": map[string]any{"expression": "(console.log('debug-stream-event'), 84)"}})
	streamEvaluateOutput, _ := streamEvaluate["output"].(map[string]any)
	if streamEvaluate["ok"] != true || streamEvaluateOutput["value"] != float64(84) {
		t.Fatalf("debug stream typed tool=%v", streamEvaluate)
	}
	nullArguments := sendCommand(map[string]any{"id": "tool-null", "type": "tool", "name": "browser_tabs", "arguments": nil})
	if nullArguments["ok"] != true {
		t.Fatalf("debug stream null arguments=%v", nullArguments)
	}
	for !eventSeen {
		messageType, payload, readErr := stream.Read(debugCtx)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if messageType != websocket.MessageText {
			continue
		}
		var message map[string]any
		if json.Unmarshal(payload, &message) != nil {
			continue
		}
		consumeEvents(message)
	}
	burstResult := sendCommand(map[string]any{
		"id": "tool-event-burst", "type": "tool", "name": "browser_evaluate",
		"arguments": map[string]any{"expression": "(() => { for (let i = 0; i < 150; i++) console.log('debug-burst-' + i); return 150 })()"},
	})
	if burstResult["ok"] != true {
		t.Fatalf("debug event burst command=%v", burstResult)
	}
	for burstEventCount < 150 {
		messageType, payload, readErr := stream.Read(debugCtx)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if messageType != websocket.MessageText {
			continue
		}
		var message map[string]any
		if json.Unmarshal(payload, &message) == nil {
			consumeEvents(message)
		}
	}
	if burstEventCount != 150 {
		t.Fatalf("debug event burst count=%d", burstEventCount)
	}

	gateStarted := time.Time{}
	gateDone := make(chan error, 1)
	go func() {
		waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		waitResult, waitErr := clientSession.CallTool(waitCtx, &mcpsdk.CallToolParams{Name: "browser_evaluate", Arguments: map[string]any{
			"tab_id":     tabID,
			"expression": "(async () => { console.log('debug-gate-started'); await new Promise(resolve => setTimeout(resolve, 250)); return 1 })()",
		}})
		if waitErr == nil && waitResult.IsError {
			waitErr = &toolCallError{name: "browser_evaluate"}
		}
		gateDone <- waitErr
	}()
	for {
		messageType, payload, readErr := stream.Read(debugCtx)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if messageType != websocket.MessageText {
			continue
		}
		var message map[string]any
		if json.Unmarshal(payload, &message) == nil && message["type"] == "events" {
			for _, rawEvent := range message["events"].([]any) {
				event := rawEvent.(map[string]any)
				data, _ := event["data"].(map[string]any)
				if strings.Contains(fmt.Sprint(data["text"]), "debug-gate-started") {
					gateStarted = time.Now()
					break
				}
			}
		}
		if !gateStarted.IsZero() {
			break
		}
	}
	gateResult := sendCommand(map[string]any{"id": "tool-gate", "type": "tool", "name": "browser_evaluate", "arguments": map[string]any{"expression": "21*2"}})
	if err := <-gateDone; err != nil {
		t.Fatal(err)
	}
	gateOutput, _ := gateResult["output"].(map[string]any)
	if gateResult["ok"] != true || gateOutput["value"] != float64(42) || time.Since(gateStarted) < 180*time.Millisecond {
		t.Fatalf("same-tab gate result=%v elapsed=%v", gateResult, time.Since(gateStarted))
	}

	result, output = callTool(t, "browser_navigate", map[string]any{"url": fixtureServer.URL + "/mcp-test"})
	requireOK(t, "second navigate", result, output)
	tabB := output["tab_id"].(string)
	start := time.Now()
	var wg sync.WaitGroup
	errorsCh := make(chan error, 2)
	for _, id := range []string{tabID, tabB} {
		wg.Add(1)
		go func(tab string) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			waitResult, waitErr := clientSession.CallTool(ctx, &mcpsdk.CallToolParams{
				Name: "browser_wait", Arguments: map[string]any{"tab_id": tab, "condition": "time", "value": "200", "timeout_ms": 1000},
			})
			if waitErr != nil {
				errorsCh <- waitErr
				return
			}
			if waitResult.IsError {
				errorsCh <- &toolCallError{name: "browser_wait"}
			}
		}(id)
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 600*time.Millisecond {
		t.Fatalf("different tabs were serialized: %v", elapsed)
	}
	result, output = callTool(t, "browser_navigate", map[string]any{"tab_id": tabB, "url": fixtureServer.URL + "/mcp-hidden"})
	requireOK(t, "existing tab navigate", result, output)
	result, output = callTool(t, "browser_history", map[string]any{"tab_id": tabB, "action": "back"})
	requireOK(t, "browser_history", result, output)
	result, output = callTool(t, "browser_tabs", map[string]any{"action": "select", "tab_id": tabID})
	requireOK(t, "browser_tabs select", result, output)

	result, output = callTool(t, "browser_tabs", map[string]any{"action": "list"})
	requireOK(t, "browser_tabs", result, output)
	if tabs, _ := output["tabs"].([]any); len(tabs) < 2 {
		t.Fatalf("tabs = %v", output["tabs"])
	}
	result, output = callTool(t, "browser_tabs", map[string]any{"action": "close", "tab_id": tabB})
	requireOK(t, "browser_tabs close", result, output)
	result, output = callTool(t, "browser_tabs", map[string]any{"action": "new"})
	requireOK(t, "browser_tabs new", result, output)
	newTabID, _ := output["tab_id"].(string)
	result, output = callTool(t, "browser_tabs", map[string]any{"action": "close", "tab_id": newTabID})
	requireOK(t, "browser_tabs close new", result, output)
	result, output = callTool(t, "browser_tabs", map[string]any{"action": "close", "tab_id": tabID})
	requireOK(t, "browser_tabs close debugged", result, output)
	closeCtx, cancelClose := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelClose()
	for {
		if _, _, err := stream.Read(closeCtx); err != nil {
			break
		}
	}
}

type toolCallError struct{ name string }

func (e *toolCallError) Error() string { return e.name + " returned an error result" }
