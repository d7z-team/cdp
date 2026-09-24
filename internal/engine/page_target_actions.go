package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gopkg.d7z.net/cdp/internal/webassets"
)

func (p *Page) SelectorTargetClickContext(ctx context.Context, ref SelectorTargetRef) error {
	return p.runForegroundInteractionContext(ctx, func() error {
		return p.selectorTargetClick(ctx, ref, actionVisualClick)
	})
}

func (p *Page) selectorTargetClick(ctx context.Context, ref SelectorTargetRef, visual actionVisualPolicy) error {
	return p.performValidatedMouseAction(ctx, func(scrollMode actionTargetScrollMode) (ActionabilityDiagnostic, error) {
		return p.selectorTargetActionabilityDiagnostic(ctx, ref, scrollMode, "click")
	},
		visual,
		func(x, y float64) error {
			return p.dispatchMouseClick(ctx,
				x, y, "left", 14*time.Millisecond)
		},
	)
}

func (p *Page) SelectorTargetDoubleClick(ctx context.Context, ref SelectorTargetRef) error {
	return p.runForegroundInteractionContext(ctx, func() error {
		return p.performValidatedMouseAction(ctx, func(scrollMode actionTargetScrollMode) (ActionabilityDiagnostic, error) {
			return p.selectorTargetActionabilityDiagnostic(ctx, ref, scrollMode, "click")
		},
			actionVisualDoubleClick,
			func(x, y float64) error {
				return p.dispatchMouseDoubleClick(ctx,
					x, y)
			},
		)
	})
}

func (p *Page) SelectorTargetRightClick(ctx context.Context, ref SelectorTargetRef) error {
	return p.runForegroundInteractionContext(ctx, func() error {
		return p.performValidatedMouseAction(ctx, func(scrollMode actionTargetScrollMode) (ActionabilityDiagnostic, error) {
			return p.selectorTargetActionabilityDiagnostic(ctx, ref, scrollMode, "click")
		},
			actionVisualRightClick,
			func(x, y float64) error {
				return p.dispatchMouseClick(ctx,
					x, y, "right", 14*time.Millisecond)
			},
		)
	})
}

func (p *Page) SelectorTargetHoverContext(ctx context.Context, ref SelectorTargetRef) error {
	return p.runForegroundInteractionContext(ctx, func() error {
		return p.performValidatedMouseAction(ctx, func(scrollMode actionTargetScrollMode) (ActionabilityDiagnostic, error) {
			return p.selectorTargetActionabilityDiagnostic(ctx, ref, scrollMode, "hover")
		},
			actionVisualMove,
			func(x, y float64) error {
				return p.mouseMove(ctx,
					x, y)
			},
		)
	})
}

func (p *Page) SelectorTargetDragContext(ctx context.Context, source, target SelectorTargetRef) error {
	return p.runForegroundInteractionContext(ctx, func() error {
		sourceDiagnostic, err := prepareActionabilityDiagnostic(func(scrollMode actionTargetScrollMode) (ActionabilityDiagnostic, error) {
			return p.selectorTargetActionabilityDiagnostic(ctx, source, scrollMode, "drag")
		})
		if err != nil {
			return err
		}
		fromX, fromY, err := actionabilityPoint(sourceDiagnostic)
		if err != nil {
			return err
		}
		targetDiagnostic, err := prepareActionabilityDiagnostic(func(scrollMode actionTargetScrollMode) (ActionabilityDiagnostic, error) {
			return p.selectorTargetActionabilityDiagnostic(ctx, target, scrollMode, "drag")
		})
		if err != nil {
			return err
		}
		toX, toY, err := actionabilityPoint(targetDiagnostic)
		if err != nil {
			return err
		}
		return p.mouseDrag(ctx,
			fromX, fromY, toX, toY)
	})
}

func (p *Page) selectorTargetInputClick(ctx context.Context, ref SelectorTargetRef, visual actionVisualPolicy) error {
	return p.performValidatedMouseAction(ctx, func(scrollMode actionTargetScrollMode) (ActionabilityDiagnostic, error) {
		return p.selectorTargetActionabilityDiagnostic(ctx, ref, scrollMode, "input")
	},
		visual,
		func(x, y float64) error {
			return p.dispatchMouseClick(ctx,
				x, y, "left", 14*time.Millisecond)
		},
	)
}

func (p *Page) selectorTargetFocus(ctx context.Context, ref SelectorTargetRef) error {
	_, err := p.selectorTargetBool(ctx, ref, `function() {
		if (!this || !this.focus) {
			return false;
		}
		this.focus();
		return true;
	}`)
	return err
}

func (p *Page) selectorTargetFocusForKeyboard(ctx context.Context, ref SelectorTargetRef) error {
	if err := p.selectorTargetFocus(ctx, ref); err != nil {
		return err
	}
	info, err := p.selectorTargetInputInfo(ctx, ref)
	if err != nil || !info.IsEditable {
		return nil
	}
	active, err := p.selectorTargetInputActive(ctx, ref)
	if err != nil || active {
		return err
	}
	if err := p.selectorTargetClick(ctx, ref, actionVisualNone); err != nil {
		return nil
	}
	ok, err := p.selectorTargetFocusInput(ctx, ref)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("keyboard target is not focusable")
	}
	return nil
}

func (p *Page) SelectorTargetPressKeyContext(ctx context.Context, ref SelectorTargetRef, key string, modifiers ...string) error {
	return p.runForegroundInteractionContext(ctx, func() error {
		if err := p.selectorTargetFocusForKeyboard(ctx, ref); err != nil {
			return err
		}
		return p.pressKey(ctx,
			key, modifiers...)
	})
}

func (p *Page) SelectorTargetInsertTextContext(ctx context.Context, ref SelectorTargetRef, text string) error {
	return p.runForegroundInteractionContext(ctx, func() error {
		if err := p.selectorTargetFocusForKeyboard(ctx, ref); err != nil {
			return err
		}
		return p.inputInsertText(ctx,
			text)
	})
}

func (p *Page) SelectorTargetSetStyle(ctx context.Context, ref SelectorTargetRef, name string, value string) error {
	_, err := p.selectorTargetBool(ctx, ref, `function(name, value) {
		if (!this || !this.style?.setProperty) {
			return false;
		}
		this.style.setProperty(name, value);
		return true;
	}`, map[string]any{"value": name}, map[string]any{"value": value})
	return err
}

func (p *Page) SelectorTargetRemoveStyle(ctx context.Context, ref SelectorTargetRef, name string) error {
	_, err := p.selectorTargetBool(ctx, ref, `function(name) {
		if (!this || !this.style?.removeProperty) {
			return false;
		}
		this.style.removeProperty(name);
		return true;
	}`, map[string]any{"value": name})
	return err
}

func (p *Page) SelectorTargetSetText(ctx context.Context, ref SelectorTargetRef, text string) error {
	_, err := p.selectorTargetBool(ctx, ref, `function(text) {
		if (!this) {
			return false;
		}
		this.textContent = text;
		this.dispatchEvent(new Event('input', { bubbles: true }));
		this.dispatchEvent(new Event('change', { bubbles: true }));
		return true;
	}`, map[string]any{"value": text})
	return err
}

func (p *Page) SelectorTargetSetCheckedContext(ctx context.Context, ref SelectorTargetRef, checked bool) error {
	return p.runForegroundInteractionContext(ctx, func() error {
		return p.selectorTargetSetChecked(ctx, ref, checked)
	})
}

func (p *Page) selectorTargetSetChecked(ctx context.Context, ref SelectorTargetRef, checked bool) error {
	current, err := p.SelectorTargetChecked(ctx, ref)
	if err != nil {
		return err
	}
	if current == checked {
		return nil
	}
	if err := p.SelectorTargetPrepareInteraction(ctx, ref); err != nil {
		return err
	}
	if err := p.SelectorTargetEnsureEnabled(ctx, ref); err != nil {
		return err
	}
	if err := p.selectorTargetClick(ctx, ref, actionVisualNone); err == nil {
		matches, verifyErr := p.selectorTargetCheckedMatches(ctx, ref, checked)
		if verifyErr == nil && matches {
			return nil
		}
	}
	return p.selectorTargetSetCheckedDirectAndVerify(ctx, ref, checked)
}

func (p *Page) SelectorTargetSelectContext(ctx context.Context, ref SelectorTargetRef, values []string) error {
	return p.runForegroundInteractionContext(ctx, func() error {
		return p.selectorTargetSelect(ctx, ref, values)
	})
}

func (p *Page) selectorTargetSelect(ctx context.Context, ref SelectorTargetRef, values []string) error {
	if err := p.SelectorTargetPrepareInteraction(ctx, ref); err != nil {
		return err
	}
	if err := p.SelectorTargetEnsureEnabled(ctx, ref); err != nil {
		return err
	}
	_, err := p.selectorTargetBool(ctx, ref, `function(values) {
		if (!(this instanceof HTMLSelectElement)) {
			return false;
		}
		const selected = new Set(values);
		for (const option of Array.from(this.options)) {
			option.selected = selected.has(option.value) || selected.has(option.label) || selected.has(option.text);
		}
		this.dispatchEvent(new Event('input', { bubbles: true }));
		this.dispatchEvent(new Event('change', { bubbles: true }));
		return true;
	}`, map[string]any{"value": values})
	if err != nil {
		return err
	}
	matches, err := p.selectorTargetSelectValueMatches(ctx, ref, values)
	if err != nil {
		return err
	}
	if !matches {
		return fmt.Errorf("select value mismatch")
	}
	return nil
}

func (p *Page) SelectorTargetInputContext(ctx context.Context, ref SelectorTargetRef, text string) error {
	return p.runForegroundInteractionContext(ctx, func() error {
		return p.selectorTargetInput(ctx, ref, text)
	})
}

func (p *Page) selectorTargetInput(ctx context.Context, ref SelectorTargetRef, text string) error {
	if err := p.SelectorTargetPrepareInput(ctx, ref); err != nil {
		return err
	}
	info, err := p.selectorTargetInputInfo(ctx, ref)
	if err != nil {
		return err
	}
	if info.Type == "number" {
		text, err = normalizeNumberInputValue(text)
		if err != nil {
			return err
		}
	}
	if shouldSetInputValueDirectly(info) || info.Type == "number" {
		return p.selectorTargetSetInputValueDirectAndVerify(ctx, ref, text)
	}
	if info.NeedsKeyboardInput {
		return p.selectorTargetInputEditorLike(ctx, ref, text)
	}

	nativeErr := p.selectorTargetInputNatively(ctx, ref, text)
	if nativeErr == nil {
		matches, verifyErr := p.selectorTargetInputValueMatches(ctx, ref, text)
		if verifyErr == nil && matches {
			return nil
		}
		if verifyErr != nil {
			nativeErr = verifyErr
		} else {
			nativeErr = fmt.Errorf("native input value mismatch")
		}
	}
	if info.IsRichText {
		if nativeErr != nil {
			return nativeErr
		}
		return fmt.Errorf("rich text input value mismatch")
	}
	if directErr := p.selectorTargetSetInputValueDirectAndVerify(ctx, ref, text); directErr != nil {
		if nativeErr != nil {
			return errors.Join(nativeErr, directErr)
		}
		return directErr
	}
	return nil
}

func (p *Page) selectorTargetInputNatively(ctx context.Context, ref SelectorTargetRef, text string) error {
	active, activeErr := p.selectorTargetInputActive(ctx, ref)
	if activeErr != nil {
		return activeErr
	}
	if !active {
		if err := p.selectorTargetInputClick(ctx, ref, actionVisualNone); err != nil {
			return err
		}
	}
	ok, err := p.selectorTargetFocusInput(ctx, ref)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("input target is not editable")
	}
	selected, err := p.selectorTargetSelectInputText(ctx, ref)
	if err == nil && selected {
		return p.insertTextIntoCurrentSelection(ctx, text)
	}
	return p.replaceFocusedTextByShortcut(ctx, text)
}

func (p *Page) selectorTargetInputEditorLike(ctx context.Context, ref SelectorTargetRef, text string) error {
	nativeErr := p.selectorTargetInputNatively(ctx, ref, text)
	if nativeErr == nil {
		matches, verifyErr := p.selectorTargetInputValueMatches(ctx, ref, text)
		if verifyErr == nil && matches {
			return nil
		}
		if verifyErr != nil {
			return nil
		}
		if !shouldFallbackToRawKeyboardForEditor(text) {
			return nil
		}
	} else if !shouldFallbackToRawKeyboardForEditor(text) {
		return nativeErr
	}
	active, activeErr := p.selectorTargetInputActive(ctx, ref)
	if activeErr != nil {
		return activeErr
	}
	if !active {
		if nativeErr != nil {
			return nativeErr
		}
		return errors.New("editor input target is not focused")
	}
	if err := p.pressCombination(ctx,
		"a", "Control"); err != nil {
		return err
	}
	if err := p.typeText(ctx,
		text); err != nil {
		return err
	}
	matches, verifyErr := p.selectorTargetInputValueMatches(ctx, ref, text)
	if verifyErr == nil && matches {
		return nil
	}
	if nativeErr != nil {
		return nativeErr
	}
	return nil
}

func (p *Page) SelectorTargetEvalJSON(ctx context.Context, ref SelectorTargetRef, code string) (string, error) {
	return p.selectorTargetString(ctx, ref, `async function(code) {
		if (!this) {
			return "undefined";
		}
		const AsyncFunction = Object.getPrototypeOf(async function(){}).constructor;
		const fn = new AsyncFunction(code);
		const value = await fn.call(this);
		if (value === undefined) {
			return "undefined";
		}
		const json = JSON.stringify(value);
		if (json === undefined) {
			throw new Error("EvalValue result is not JSON-serializable");
		}
		return json;
	}`, map[string]any{"value": code})
}

func (p *Page) SelectorTargetScrollPositionContext(ctx context.Context, ref SelectorTargetRef, x, y float64) error {
	return p.runForegroundInteractionContext(ctx, func() error {
		_, err := p.selectorTargetBool(ctx, ref, `function(x, y) {
			this.scrollTo(x, y);
			return true;
		}`, map[string]any{"value": x}, map[string]any{"value": y})
		return err
	})
}

func (p *Page) SelectorTargetScrollByContext(ctx context.Context, ref SelectorTargetRef, deltaX, deltaY float64) error {
	return p.runForegroundInteractionContext(ctx, func() error {
		_, err := p.selectorTargetBool(ctx, ref, `function(x, y) {
			this.scrollBy(x, y);
			return true;
		}`, map[string]any{"value": deltaX}, map[string]any{"value": deltaY})
		return err
	})
}

func (p *Page) SelectorTargetHighlight(ctx context.Context, ref SelectorTargetRef, label string) error {
	_, err := p.selectorTargetBool(ctx, ref, selectorTargetHighlightMarkerScript(), map[string]any{"value": label})
	return err
}

func parseRectValue(callRes map[string]any) (Rect, bool) {
	value, ok := SafeGet[map[string]any](callRes, "result", "value")
	if !ok {
		return Rect{}, false
	}
	rect := Rect{}
	if x, ok := value["x"].(float64); ok {
		rect.X = x
	}
	if y, ok := value["y"].(float64); ok {
		rect.Y = y
	}
	if width, ok := value["width"].(float64); ok {
		rect.Width = width
	}
	if height, ok := value["height"].(float64); ok {
		rect.Height = height
	}
	return rect, true
}

func (p *Page) SelectorTargetRect(ctx context.Context, ref SelectorTargetRef, scrollIntoView bool) (Rect, bool, error) {
	return p.selectorTargetRectContext(ctx, ref, scrollIntoView)
}

func (p *Page) selectorTargetRectContext(ctx context.Context, ref SelectorTargetRef, scrollIntoView bool) (Rect, bool, error) {
	target, backendNodeID, err := p.selectorTargetTargetAndBackendNodeID(ctx, ref)
	if err != nil {
		return Rect{}, false, err
	}
	res, err := p.callTargetBackendNodeFunctionContext(ctx, target, backendNodeID, true, selectorTargetRectScript(), map[string]any{"value": scrollIntoView})
	if err != nil {
		return Rect{}, false, err
	}
	rect, ok := parseRectValue(res)
	return rect, ok, nil
}

func (p *Page) SelectorTargetScrollIntoView(ctx context.Context, ref SelectorTargetRef) error {
	return p.runForegroundInteractionContext(ctx, func() error {
		_, _, err := p.SelectorTargetRect(ctx, ref, true)
		return err
	})
}

func (p *Page) SelectorTargetSetFileInputFilesContext(ctx context.Context, ref SelectorTargetRef, files []string) error {
	return p.runForegroundInteractionContext(ctx, func() error {
		return p.selectorTargetSetFileInputFiles(ctx, ref, files)
	})
}

func (p *Page) selectorTargetSetFileInputFiles(ctx context.Context, ref SelectorTargetRef, files []string) error {
	p.lock.RLock()
	defer p.lock.RUnlock()
	if err := p.checkConnContext(ctx); err != nil {
		return err
	}

	target, backendNodeID, err := p.selectorTargetTargetAndBackendNodeID(ctx, ref)
	if err != nil {
		return err
	}
	backendNodeID, err = p.resolveUploadFileInputBackendNodeID(ctx, target, backendNodeID)
	if err != nil {
		return err
	}
	return p.setFileInputFilesInTarget(ctx, target, backendNodeID, files)
}

func (p *Page) setFileInputFilesInTarget(ctx context.Context, target ExecutionTarget, backendNodeID int, files []string) error {
	return p.sendTargetPacket(ctx, target, "DOM.setFileInputFiles", map[string]any{
		"backendNodeId": backendNodeID,
		"files":         files,
	})
}

func (p *Page) resolveUploadFileInputBackendNodeID(ctx context.Context, target ExecutionTarget, backendNodeID int) (int, error) {
	res, err := p.callTargetBackendNodeFunctionContext(ctx, target, backendNodeID, false, `function() {
		return `+webassets.RuntimeRequiredMethodCall(webassets.RuntimeFFI, "resolveUploadFileInput", "上传 helper 未就绪，无法解析文件输入框", webassets.JSRaw("this"))+`;
	}`)
	if err != nil {
		return 0, err
	}
	if runtimeErr := runtimeResultError(res); runtimeErr != nil {
		return 0, runtimeErr
	}
	objectID, ok := SafeGet[string](res, "result", "objectId")
	if !ok || objectID == "" {
		return 0, errors.New("上传目标未返回文件输入框")
	}
	desc, err := p.DescribeNodeByObjectIDInTargetContext(ctx, target, objectID)
	defer func() {
		p.logReleaseObjectError(objectID, p.ReleaseObjectInTargetContext(ctx, target, objectID))
	}()
	if err != nil {
		return 0, err
	}
	_, fileInputBackendNodeID, ok := parseNodeIdentity(desc)
	if !ok || fileInputBackendNodeID <= 0 {
		return 0, errors.New("获取文件输入框 backendNodeId 失败")
	}
	return fileInputBackendNodeID, nil
}
