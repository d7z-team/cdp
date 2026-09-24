package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"gopkg.d7z.net/cdp"
)

type inputProbeEvent struct {
	Type      string `json:"type"`
	IsTrusted bool   `json:"isTrusted"`
	InputType string `json:"inputType"`
	Data      string `json:"data"`
	Key       string `json:"key"`
	Code      string `json:"code"`
	CtrlKey   bool   `json:"ctrlKey"`
	Value     string `json:"value"`
}

func readInputProbeLog(t *testing.T, page evaluator, name string) []inputProbeEvent {
	t.Helper()
	raw := evalValue(page, fmt.Sprintf(`return window.__inputProbe[%q] || []`, name))
	var events []inputProbeEvent
	if err := json.Unmarshal([]byte(raw), &events); err != nil {
		t.Fatalf("unmarshal input probe %s: %v raw=%q", name, err, raw)
	}
	return events
}

func hasProbeEvent(events []inputProbeEvent, eventType string) bool {
	for _, event := range events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}

func readKeyInputEvents(t *testing.T, page evaluator) []inputProbeEvent {
	t.Helper()
	raw := evalValue(page, `return window.__keyInputEvents || []`)
	var events []inputProbeEvent
	if err := json.Unmarshal([]byte(raw), &events); err != nil {
		t.Fatalf("unmarshal key input events: %v raw=%q", err, raw)
	}
	return events
}

func eventIndex(events []inputProbeEvent, eventType string) int {
	for i, event := range events {
		if event.Type == eventType {
			return i
		}
	}
	return -1
}

func TestSelectorDoubleClick(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/events")
	btn := page.ByTestID("dblclick-btn")
	mustOK(btn.DoubleClick(context.Background()))

	count := attribute(btn, "data-count")
	if count != "1" {
		t.Fatalf("double-click count = %q, want 1", count)
	}
}

func TestSelectorRightClick(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/events")
	btn := page.ByTestID("rightclick-area")
	mustOK(btn.RightClick(t.Context()))
	if attribute(btn, "data-rightclicked") != "true" {
		t.Fatal("right click event not triggered")
	}
}

func TestCoordinateMouseClicks(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	for _, mode := range []cdp.ActionMode{cdp.ActionFast, cdp.ActionStrict} {
		t.Run(string(mode), func(t *testing.T) {
			page := configuredPage(t, openFixture(t, "/events"), cdp.ConnectOptions{ActionMode: mode})
			for _, tc := range []struct {
				name, testID, attribute, want string
				click                         func(context.Context, float64, float64) error
			}{
				{"left", "key-checkbox", "", "true", page.MouseClick},
				{"double", "dblclick-btn", "data-count", "1", page.MouseDoubleClick},
				{"right", "rightclick-area", "data-rightclicked", "true", page.MouseRightClick},
			} {
				t.Run(tc.name, func(t *testing.T) {
					target := page.ByTestID(tc.testID)
					var point struct{ X, Y float64 }
					mustOK(target.Eval(t.Context(), `const rect = this.getBoundingClientRect(); return {x: rect.left + rect.width / 2, y: rect.top + rect.height / 2}`, &point))
					mustOK(tc.click(t.Context(), point.X, point.Y))
					got := ""
					if tc.attribute == "" {
						got = evalValue(target, `return this.checked`)
					} else {
						got = attribute(target, tc.attribute)
					}
					if got != tc.want {
						t.Fatalf("%s result = %q, want %q", tc.name, got, tc.want)
					}
				})
			}
		})
	}
}

func TestSelectorDragTo(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/events")
	coreRuntime := must(page.ExecutionContext(context.Background(), cdp.ExecutionContextOptions{World: cdp.WorldCore}))
	evalText(coreRuntime, `
		const ffi = window.__cdp_ffi;
		ffi.__dragVisualProbe = [];
		const original = ffi.showMouseAction.bind(ffi);
		ffi.showMouseAction = function(action) {
			ffi.__dragVisualProbe.push(structuredClone(action));
			return original(action);
		};
	`)
	source := page.ByTestID("drag-source")
	target := page.ByTestID("drag-target")
	mustOK(source.DragTo(context.Background(), target))
	afterSource := attribute(source, "data-dragged")
	afterTarget := attribute(target, "data-drop")
	if afterTarget != "received" {
		t.Fatalf("drag-to: source data-dragged=%q, target data-drop=%q", afterSource, afterTarget)
	}
	if got := evalValue(coreRuntime, `
		return window.__cdp_ffi.__dragVisualProbe.length === 1 &&
			window.__cdp_ffi.__dragVisualProbe[0].kind === 'drag' &&
			window.__cdp_ffi.__dragVisualProbe[0].fromX !== window.__cdp_ffi.__dragVisualProbe[0].toX;
	`); got != "true" {
		t.Fatalf("drag should emit one semantic visual action, got %q", got)
	}
}

func TestSelectorDragToNilTarget(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/events")
	defer mustPanic(t)
	mustOK(page.ByTestID("drag-source").DragTo(context.Background(), nil))
}

func TestPageKeyboardType(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/events")
	if got := page.ActionMode(); got != "fast" {
		t.Fatalf("e2e page action mode = %q, want fast", got)
	}
	mustOK(page.ByTestID("key-input").Click(context.Background()))
	mustOK(page.Type(context.Background(), "Hello"))
	val := must(page.ByTestID("key-input").Value(context.Background()))

	if val != "Hello" {
		t.Fatalf("Type result = %q, want Hello", val)
	}
}

func TestPageKeyboardPress(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/events")
	inp := page.ByTestID("key-input")
	mustOK(inp.Press(context.Background(), "a", cdp.KeyOptions{}))

	val := must(page.ByTestID("key-input").Value(context.Background()))

	if val != "a" {
		t.Fatalf("Press result = %q, want a", val)
	}
	events := readKeyInputEvents(t, page)
	clickIndex := eventIndex(events, "click")
	keyIndex := eventIndex(events, "keydown")
	if clickIndex >= 0 {
		t.Fatalf("Press should focus without implicit click, events=%+v", events)
	}
	if keyIndex < 0 {
		t.Fatalf("Press should dispatch keydown, events=%+v", events)
	}
}

func TestPageKeyboardShortcut(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/events")
	inp := page.ByTestID("key-input")
	mustOK(inp.Click(context.Background()))
	mustOK(page.Type(context.Background(), "select me"))
	mustOK(page.Press(context.Background(), "a", cdp.KeyOptions{Modifiers: []cdp.Modifier{"Control"}}))
	mustOK(page.Press(context.Background(), "Backspace", cdp.KeyOptions{}))

	if val := must(inp.Value(context.Background())); val != "" {
		t.Fatalf("Ctrl+A then Backspace should clear input, got %q", val)
	}
}

func TestSelectorShortcutFocusesWithoutImplicitClick(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/events")
	inp := page.ByTestID("key-input")
	mustOK(inp.Fill(context.Background(), "shortcut me"))
	evalValue(page, `return (window.__keyInputEvents = [])`)
	mustOK(inp.Press(context.Background(), "a", cdp.KeyOptions{Modifiers: []cdp.Modifier{"Control"}}))
	mustOK(page.Press(context.Background(), "Backspace", cdp.KeyOptions{}))

	if val := must(inp.Value(context.Background())); val != "" {
		t.Fatalf("selector Shortcut then Backspace should clear input, got %q", val)
	}
	events := readKeyInputEvents(t, page)
	clickIndex := eventIndex(events, "click")
	keyIndex := eventIndex(events, "keydown")
	if clickIndex >= 0 {
		t.Fatalf("Shortcut should not click an already focused input, events=%+v", events)
	}
	if keyIndex < 0 {
		t.Fatalf("Shortcut should dispatch keydown, events=%+v", events)
	}
}

func TestSelectorPressDoesNotClickNonInputTarget(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/events")
	mustOK(page.ByTestID("key-button").Press(context.Background(), "a", cdp.KeyOptions{}))

	raw := evalValue(page, `return window.__keyboardTargetEvents.filter((event) => event.name === 'button')`)
	var events []inputProbeEvent
	if err := json.Unmarshal([]byte(raw), &events); err != nil {
		t.Fatalf("unmarshal button keyboard events: %v raw=%q", err, raw)
	}
	if hasProbeEvent(events, "click") {
		t.Fatalf("Press should not click non-input keyboard target, events=%+v", events)
	}
	if !hasProbeEvent(events, "keydown") {
		t.Fatalf("Press should still dispatch keydown to non-input keyboard target, events=%+v", events)
	}
}

func TestSelectorShortcutDoesNotToggleCheckbox(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/events")
	checkbox := page.ByTestID("key-checkbox")
	mustOK(checkbox.Press(context.Background(), "a", cdp.KeyOptions{Modifiers: []cdp.Modifier{"Control"}}))
	if must(checkbox.IsChecked(context.Background())) {
		t.Fatalf("Shortcut should not toggle checkbox checked state")
	}
	raw := evalValue(page, `return window.__keyboardTargetEvents.filter((event) => event.name === 'checkbox')`)
	var events []inputProbeEvent
	if err := json.Unmarshal([]byte(raw), &events); err != nil {
		t.Fatalf("unmarshal checkbox keyboard events: %v raw=%q", err, raw)
	}
	if hasProbeEvent(events, "click") {
		t.Fatalf("Shortcut should not click checkbox before keydown, events=%+v", events)
	}
	if !hasProbeEvent(events, "keydown") {
		t.Fatalf("Shortcut should still dispatch keydown to checkbox, events=%+v", events)
	}
}

func TestSelectorFillUsesNativeInputEvents(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/events")
	input := page.ByTestID("fill-input")
	mustOK(input.Fill(context.Background(), "native-fill"))
	if got := must(input.Value(context.Background())); got != "native-fill" {
		t.Fatalf("Fill value = %q, want native-fill", got)
	}
	events := readInputProbeLog(t, page, "fill")
	if !hasProbeEvent(events, "beforeinput") {
		t.Fatalf("Fill should use browser input path and emit beforeinput, events=%+v", events)
	}
	if hasProbeEvent(events, "change") {
		t.Fatalf("native Fill should not synthesize immediate change, events=%+v", events)
	}
}

func TestSelectorFillReplacesExistingValue(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/events")
	input := page.ByTestID("replace-input")
	mustOK(input.Fill(context.Background(), "new-value"))
	if got := must(input.Value(context.Background())); got != "new-value" {
		t.Fatalf("Fill should replace existing value, got %q", got)
	}
	events := readInputProbeLog(t, page, "replace")
	if !hasProbeEvent(events, "beforeinput") {
		t.Fatalf("replacement Fill should use browser input path, events=%+v", events)
	}
}

func TestSelectorFillFocusedInputDoesNotClickAgain(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/events")
	input := page.ByTestID("fill-input")
	mustOK(input.Click(context.Background()))
	evalValue(page, `return (window.__inputProbe.fill = [])`)
	mustOK(input.Fill(context.Background(), "already-focused"))
	if got := must(input.Value(context.Background())); got != "already-focused" {
		t.Fatalf("Fill should replace focused input value, got %q", got)
	}
	events := readInputProbeLog(t, page, "fill")
	if hasProbeEvent(events, "click") {
		t.Fatalf("Fill should not click an already focused input again, events=%+v", events)
	}
	if !hasProbeEvent(events, "beforeinput") {
		t.Fatalf("focused Fill should use native input path, events=%+v", events)
	}
}

func TestSelectorFillUsesTargetSelectionBeforeShortcutFallback(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/events")
	input := page.ByTestID("masked-input")
	mustOK(input.Fill(context.Background(), "masked-new"))
	if got := must(input.Value(context.Background())); got != "masked-new" {
		t.Fatalf("Fill should replace masked input value, got %q", got)
	}
	events := readInputProbeLog(t, page, "masked")
	for _, event := range events {
		if event.Type == "keydown" && event.CtrlKey && event.Key == "a" {
			t.Fatalf("Fill should select the target directly before Ctrl+A fallback, events=%+v", events)
		}
	}
	if !hasProbeEvent(events, "beforeinput") {
		t.Fatalf("masked Fill should use browser input path, events=%+v", events)
	}
}

func TestSelectorFillUsesInputActionableFallback(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/events")
	mustOK(page.Locator(
		"[data-testid='hidden-backing-input']",
		"[data-testid='fallback-visible-input']",
	).Fill(context.Background(), "visible-target"))
	if got := must(page.ByTestID("fallback-visible-input").Value(context.Background())); got != "visible-target" {
		t.Fatalf("Fill should use visible input fallback, got %q", got)
	}
	if got := must(page.ByTestID("hidden-backing-input").Value(context.Background())); got != "hidden-old" {
		t.Fatalf("Fill should not write hidden backing input, got %q", got)
	}
	events := readInputProbeLog(t, page, "visibleFallback")
	if !hasProbeEvent(events, "beforeinput") {
		t.Fatalf("visible fallback Fill should use native input path, events=%+v", events)
	}
}

func TestSelectorFillMultipleInputActionableFails(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/events")
	err := page.Locator("[data-testid='fill-input'], [data-testid='replace-input']").Fill(context.Background(), "ambiguous")
	var detail *cdp.LocatorError
	if !errors.As(err, &detail) || detail.Counts.Actionable != 2 {
		t.Fatalf("expected two actionable matches: %v", err)
	}

}

func TestSelectorFillContentEditable(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/events")
	editable := page.ByTestID("editable-input")
	mustOK(editable.Fill(context.Background(), "editable text"))
	if got := must(editable.TextContent(context.Background())); got != "editable text" {
		t.Fatalf("contenteditable Fill text = %q", got)
	}
	events := readInputProbeLog(t, page, "editable")
	if !hasProbeEvent(events, "beforeinput") {
		t.Fatalf("contenteditable Fill should use browser input path, events=%+v", events)
	}
}

func TestSelectorFillNestedContentEditable(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/events")
	editable := page.ByTestID("editable-nested")
	mustOK(editable.Fill(context.Background(), "nested text"))
	if got := must(editable.TextContent(context.Background())); got != "nested text" {
		t.Fatalf("nested contenteditable Fill text = %q", got)
	}
	events := readInputProbeLog(t, page, "editableNested")
	if !hasProbeEvent(events, "beforeinput") {
		t.Fatalf("nested contenteditable Fill should use browser input path, events=%+v", events)
	}
}

func TestSelectorFillRichTextWrapperPrefersEditableEndpoint(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/events")
	mustOK(page.ByTestID("rich-editor-wrapper").Fill(context.Background(), "rich text"))
	if got := must(page.ByTestID("rich-editor").TextContent(context.Background())); got != "rich text" {
		t.Fatalf("rich editor Fill text = %q", got)
	}
	if got := must(page.ByTestID("rich-backing-textarea").Value(context.Background())); got != "hidden-rich" {
		t.Fatalf("Fill should not write rich backing textarea, got %q", got)
	}
	events := readInputProbeLog(t, page, "rich")
	if !hasProbeEvent(events, "beforeinput") {
		t.Fatalf("rich editor Fill should use browser input path, events=%+v", events)
	}
}

func TestSelectorFillSameOriginIframeRichText(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/events")
	mustOK(page.ByTestID("iframe-rich-wrapper").Fill(context.Background(), "frame rich text"))
	if got := must(page.ByTestID("rich-editor-frame").ContentFrame().Locator("body").TextContent(context.Background())); got != "frame rich text" {
		t.Fatalf("iframe rich editor Fill text = %q", got)
	}
	mustOK(page.ByTestID("rich-editor-frame").Fill(context.Background(), "direct frame rich text"))
	if got := must(page.ByTestID("rich-editor-frame").ContentFrame().Locator("body").TextContent(context.Background())); got != "direct frame rich text" {
		t.Fatalf("direct iframe rich editor Fill text = %q", got)
	}
}

func TestSelectorFillEditorLikeContainer(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/events")
	mustOK(page.ByTestID("editor-like-leaf").Fill(context.Background(), "select 1;\nselect 2;"))
	if got := must(page.ByTestID("editor-like-hidden").Value(context.Background())); got != "select 1;\nselect 2;" {
		t.Fatalf("editor-like hidden textarea value = %q", got)
	}
	if got := must(page.ByTestID("editor-like-content").TextContent(context.Background())); !strings.Contains(got, "select 1;") || !strings.Contains(got, "select 2;") {
		t.Fatalf("editor-like visible content = %q", got)
	}
	events := readInputProbeLog(t, page, "editorLike")
	if !hasProbeEvent(events, "beforeinput") {
		t.Fatalf("editor-like Fill should use browser keyboard text path, events=%+v", events)
	}
}

func TestSelectorFillPlainDivStillFails(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/events")
	msg := expectPanicMessage(func() {
		mustOK(page.ByTestID("plain-non-editor-div").Fill(context.Background(), "should fail"))
	})
	if !strings.Contains(msg, "Element has no editable input target") {
		t.Fatalf("expected plain div to stay non-editable, got %q", msg)
	}
}

func TestSelectorFillFallsBackToDirectDOMWhenNativeInputPrevented(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/events")
	input := page.ByTestID("fallback-input")
	mustOK(input.Fill(context.Background(), "fallback-value"))
	if got := must(input.Value(context.Background())); got != "fallback-value" {
		t.Fatalf("fallback Fill value = %q, want fallback-value", got)
	}
	events := readInputProbeLog(t, page, "fallback")
	if !hasProbeEvent(events, "beforeinput") {
		t.Fatalf("fallback case should attempt native input first, events=%+v", events)
	}
	if !hasProbeEvent(events, "change") {
		t.Fatalf("fallback case should keep direct DOM compatibility and emit change, events=%+v", events)
	}
}

func TestPageInputReplacesExistingText(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/events")
	element := must(page.ByTestID("replace-input").All(context.Background()))[0]
	if err := element.Fill(context.Background(), "page-input"); err != nil {
		t.Fatalf("Page.Input: %v", err)
	}
	if got := must(page.ByTestID("replace-input").Value(context.Background())); got != "page-input" {
		t.Fatalf("Page.Input should replace existing value, got %q", got)
	}
}

func TestSelectorUpload(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/events")
	fi := page.ByTestID("file-input")
	mustOK(fi.Upload(context.Background(), "test.txt", []byte("hello world")))
}
