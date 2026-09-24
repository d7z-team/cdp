package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type inputTargetInfo struct {
	TagName            string `json:"tagName"`
	Type               string `json:"type"`
	IsTextInput        bool   `json:"isTextInput"`
	IsTextArea         bool   `json:"isTextArea"`
	IsContentEditable  bool   `json:"isContentEditable"`
	IsEditable         bool   `json:"isEditable"`
	IsRichText         bool   `json:"isRichText"`
	IsEditorLike       bool   `json:"isEditorLike"`
	NeedsKeyboardInput bool   `json:"needsKeyboardInput"`
	ResolvedBy         string `json:"resolvedBy"`
}

type inputTargetValue struct {
	Value string `json:"value"`
	Text  string `json:"text"`
}

func shouldSetInputValueDirectly(info inputTargetInfo) bool {
	if info.TagName != "input" {
		return false
	}
	switch info.Type {
	case "hidden", "date", "datetime-local", "month", "time", "week", "color", "range":
		return true
	default:
		return false
	}
}

func normalizeNumberInputValue(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", nil
	}
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9':
		case r == '+' || r == '-' || r == '.' || r == ',':
		default:
			return "", fmt.Errorf("invalid_number_input: unsupported number format %q", raw)
		}
	}

	sign := ""
	if strings.HasPrefix(value, "+") || strings.HasPrefix(value, "-") {
		if value[0] == '-' {
			sign = "-"
		}
		value = value[1:]
	}
	if value == "" || strings.ContainsAny(value, "+-") {
		return "", fmt.Errorf("invalid_number_input: unsupported number format %q", raw)
	}
	if strings.Count(value, ".") > 1 {
		return "", fmt.Errorf("invalid_number_input: unsupported number format %q", raw)
	}

	if strings.Contains(value, ",") {
		if strings.Contains(value, ".") {
			intPart, fracPart, _ := strings.Cut(value, ".")
			if !isThousandsGroupedDecimal(intPart) || !isDigitString(fracPart) {
				return "", fmt.Errorf("ambiguous_localized_number: unsupported localized number format %q", raw)
			}
			value = strings.ReplaceAll(intPart, ",", "") + "." + fracPart
		} else {
			if isThousandsGroupedDecimal(value) {
				value = strings.ReplaceAll(value, ",", "")
			} else {
				return "", fmt.Errorf("ambiguous_localized_number: unsupported localized number format %q", raw)
			}
		}
	}

	if strings.HasPrefix(value, ".") {
		value = "0" + value
	}
	value = strings.TrimSuffix(value, ".")
	if value == "" || value == "." {
		return "", fmt.Errorf("invalid_number_input: unsupported number format %q", raw)
	}
	intPart, fracPart, hasDot := strings.Cut(value, ".")
	if intPart == "" {
		intPart = "0"
	}
	if !isDigitString(intPart) {
		return "", fmt.Errorf("invalid_number_input: unsupported number format %q", raw)
	}
	if hasDot {
		if fracPart == "" {
			return sign + intPart, nil
		}
		if !isDigitString(fracPart) {
			return "", fmt.Errorf("invalid_number_input: unsupported number format %q", raw)
		}
		return sign + intPart + "." + fracPart, nil
	}
	return sign + intPart, nil
}

func isDigitString(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isThousandsGroupedDecimal(value string) bool {
	parts := strings.Split(value, ",")
	if len(parts) < 2 {
		return false
	}
	if len(parts[0]) < 1 || len(parts[0]) > 3 || !isDigitString(parts[0]) {
		return false
	}
	for _, part := range parts[1:] {
		if len(part) != 3 || !isDigitString(part) {
			return false
		}
	}
	return true
}

func (p *Page) insertTextIntoCurrentSelection(ctx context.Context, text string) error {
	if text == "" {
		return p.pressKey(ctx,
			"Backspace")
	}
	return p.inputInsertText(ctx,
		text)
}

func (p *Page) replaceFocusedTextByShortcut(ctx context.Context, text string) error {
	if err := p.pressCombination(ctx,
		"a", "Control"); err != nil {
		return err
	}
	return p.insertTextIntoCurrentSelection(ctx, text)
}

func inputTargetScript(body string) string {
	return `function() {
		const viewOf = (el) => el?.ownerDocument?.defaultView || window;
		const isHTMLInput = (el) => !!el && el instanceof viewOf(el).HTMLInputElement;
		const isHTMLTextArea = (el) => !!el && el instanceof viewOf(el).HTMLTextAreaElement;
		const isHTMLSelect = (el) => !!el && el instanceof viewOf(el).HTMLSelectElement;
		const isHTMLElement = (el) => !!el && el instanceof viewOf(el).HTMLElement;
		const isElement = (el) => !!el && el instanceof viewOf(el).Element;
		const isFrameElement = (el) => !!el && (el instanceof viewOf(el).HTMLIFrameElement || el instanceof viewOf(el).HTMLFrameElement);
		const isTextInput = (el) => {
			if (!isHTMLInput(el)) return false;
			const type = (el.type || '').toLowerCase();
			return !['button', 'checkbox', 'color', 'file', 'hidden', 'image', 'radio', 'range', 'reset', 'submit'].includes(type);
		};
		const hasGeometry = (el) => {
			if (!isElement(el)) return false;
			for (const rect of Array.from(el.getClientRects())) {
				if (rect.width > 0 && rect.height > 0) return true;
			}
			const rect = el.getBoundingClientRect();
			return rect.width > 0 && rect.height > 0;
		};
		const isVisible = (el) => {
			if (!isElement(el) || !el.isConnected || !hasGeometry(el)) return false;
			let current = el;
			while (current) {
				const view = current.ownerDocument?.defaultView;
				const style = view?.getComputedStyle?.(current);
				if (!style) return false;
				if (style.display === 'none' || style.visibility === 'hidden' || style.visibility === 'collapse') return false;
				if (parseFloat(style.opacity || '1') === 0) return false;
				current = current.parentElement;
			}
			return true;
		};
		const isRichTextEndpoint = (el) => {
			if (!isHTMLElement(el)) return false;
			const className = typeof el.className === 'string' ? el.className.toLowerCase() : '';
			return el.isContentEditable && /(ql-editor|prosemirror|ck-editor__editable|wangeditor-txt|fr-element|note-editable)/.test(className);
		};
		const isEditable = (el) => !!el && !el.disabled && !el.readOnly && (
			isHTMLTextArea(el) ||
			isTextInput(el) ||
			el.isContentEditable
		);
		const isVisibleEditable = (el) => isEditable(el) && isVisible(el);
		const editorHostSelector = [
			'.monaco-editor',
			'.CodeMirror',
			'.cm-editor',
			'.ace_editor',
			'[role="textbox"]',
			'[aria-multiline="true"]'
		].join(', ');
		const editorLikeSelector = [
			editorHostSelector,
			'.view-lines',
			'.monaco-scrollable-element',
			'.inputarea',
			'.cm-content',
			'.ace_text-input'
		].join(', ');
		const isEditorLikeContainer = (el) => isElement(el) && el.matches?.(editorLikeSelector) && isVisible(el);
		const findEditorLikeContainer = (root) => {
			if (!root || !root.querySelectorAll) return null;
			const closestHost = root.closest?.(editorHostSelector);
			if (closestHost && isEditorLikeContainer(closestHost)) return closestHost;
			if (isEditorLikeContainer(root)) return root;
			for (const candidate of Array.from(root.querySelectorAll(editorHostSelector))) {
				if (isEditorLikeContainer(candidate)) return candidate;
			}
			for (const candidate of Array.from(root.querySelectorAll(editorLikeSelector))) {
				if (isEditorLikeContainer(candidate)) return candidate;
			}
			return null;
		};
		const editorOwnsFocus = (editor, doc) => {
			if (!editor || !doc) return false;
			const active = doc.activeElement;
			if (active === editor || !!editor.contains?.(active)) return true;
			const className = typeof editor.className === 'string' ? editor.className.toLowerCase() : '';
			if (/\b(focused|focus|cm-focused)\b/.test(className)) return true;
			return editor.getAttribute?.('aria-focused') === 'true';
		};
		const firstVisibleEditable = (root, selector) => {
			if (!root || !root.querySelectorAll) return null;
			for (const candidate of Array.from(root.querySelectorAll(selector))) {
				if (isVisibleEditable(candidate)) return candidate;
			}
			return null;
		};
		const firstFrameEditable = (root, selector) => {
			if (!root || !root.querySelectorAll) return null;
			for (const candidate of Array.from(root.querySelectorAll(selector))) {
				if (candidate?.isConnected && isEditable(candidate)) return candidate;
			}
			return null;
		};
		const sameOriginFrameDocument = (frame) => {
			try {
				return frame.contentDocument || frame.contentWindow?.document || null;
			} catch (_) {
				return null;
			}
		};
		const findEditable = (root) => {
			if (!root || !root.querySelectorAll) return null;
			const rich = firstVisibleEditable(root,
				'[contenteditable]:not([contenteditable="false"]), ' +
				'.ql-editor, .ProseMirror, .ck-editor__editable, .tox-edit-area [contenteditable], ' +
				'.w-e-text-container [contenteditable], .wangEditor-txt, .fr-element, .note-editable'
			);
			if (rich) return { target: rich, interactionTarget: rich, resolvedBy: 'descendant' };
			const nestedFrames = Array.from(root.querySelectorAll('iframe, frame'));
			const frames = isFrameElement(root) ? [root, ...nestedFrames] : nestedFrames;
			for (const frame of frames) {
				if (!isVisible(frame)) continue;
				const doc = sameOriginFrameDocument(frame);
				if (!doc) continue;
				const endpoint = firstFrameEditable(doc, 'body[contenteditable]:not([contenteditable="false"]), [contenteditable]:not([contenteditable="false"])');
				if (endpoint) return { target: endpoint, interactionTarget: frame, resolvedBy: 'frame' };
			}
			const form = firstVisibleEditable(root, 'textarea, input');
			if (form) return { target: form, interactionTarget: form, resolvedBy: 'descendant' };
			return null;
		};
		const resolveTarget = (root, preferActive) => {
			const doc = root && root.ownerDocument ? root.ownerDocument : document;
			const active = doc.activeElement;
			if (preferActive && isVisibleEditable(active) && (active === root || !!root?.contains?.(active))) {
				return { target: active, interactionTarget: active, resolvedBy: 'active', doc, isEditorLike: false };
			}
			const editorRoot = findEditorLikeContainer(root);
			if (preferActive && editorRoot && isEditable(active) && (active === editorRoot || !!editorRoot.contains?.(active))) {
				return { target: active, interactionTarget: editorRoot, resolvedBy: 'editor-active', doc, isEditorLike: true };
			}
			let target = root;
			let interactionTarget = root;
			let resolvedBy = 'self';
			if (!isVisibleEditable(target)) {
				const nested = findEditable(root);
				if (nested) return { target: nested.target, interactionTarget: nested.interactionTarget, resolvedBy: nested.resolvedBy, doc: nested.target.ownerDocument || doc, isEditorLike: false };
				if (editorRoot) return { target: editorRoot, interactionTarget: editorRoot, resolvedBy: 'editor', doc, isEditorLike: true };
			}
			return { target, interactionTarget, resolvedBy, doc, isEditorLike: false };
		};
		const targetValue = (target) => {
			if (isHTMLInput(target) || isHTMLTextArea(target) || isHTMLSelect(target)) {
				return String(target.value ?? '');
			}
			if (isHTMLElement(target) && target.isContentEditable) {
				return target.textContent || '';
			}
			return '';
		};
		const targetText = (target) => isHTMLElement(target) ? (target.textContent || '') : '';
		const editorReadableText = (editor) => {
			if (!isElement(editor)) return '';
			const cmContent = editor.matches?.('.cm-content') ? editor : editor.querySelector?.('.cm-content');
			if (cmContent) return cmContent.textContent || '';
			const monacoLines = editor.querySelectorAll?.('.view-line');
			if (monacoLines && monacoLines.length > 0) {
				return Array.from(monacoLines).map((line) => line.textContent || '').join('\n');
			}
			const aceLines = editor.querySelectorAll?.('.ace_line');
			if (aceLines && aceLines.length > 0) {
				return Array.from(aceLines).map((line) => line.textContent || '').join('\n');
			}
			return targetText(editor);
		};
` + body + `
	}`
}

func inputTargetInfoScript() string {
	return inputTargetScript(`
		const { target, resolvedBy, isEditorLike } = resolveTarget(this, true);
		return JSON.stringify({
			tagName: target && target.tagName ? target.tagName.toLowerCase() : '',
			type: target && target.type ? String(target.type).toLowerCase() : '',
			isTextInput: isTextInput(target),
			isTextArea: isHTMLTextArea(target),
			isContentEditable: !!(target && target.isContentEditable),
			isEditable: isEditable(target),
			isRichText: isRichTextEndpoint(target),
			isEditorLike: !!isEditorLike,
			needsKeyboardInput: !!isEditorLike && !isEditable(target),
			resolvedBy: resolvedBy,
		});`)
}

func focusInputTargetScript() string {
	return inputTargetScript(`
		const { target, doc, isEditorLike } = resolveTarget(this, true);
		if (isEditorLike && !isEditable(target)) {
			if (target.focus) {
				try { target.focus(); } catch (_) {}
			}
			return editorOwnsFocus(target, doc);
		}
		if (!isEditable(target)) {
			return false;
		}
		if (doc.activeElement !== target && target.focus) {
			target.focus();
		}
		return doc.activeElement === target || target === this;`)
}

func inputTargetActiveScript() string {
	return inputTargetScript(`
		const { target, doc, interactionTarget, isEditorLike } = resolveTarget(this, true);
		if (isEditorLike) {
			return editorOwnsFocus(interactionTarget || target, doc);
		}
		return !!target && doc.activeElement === target;`)
}

func selectInputTargetTextScript() string {
	return inputTargetScript(`
		const { target, doc, isEditorLike, interactionTarget } = resolveTarget(this, true);
		if (isEditorLike && !isEditable(target)) {
			if (target.focus) {
				try { target.focus(); } catch (_) {}
			}
			return false;
		}
		if (!isEditable(target)) {
			return false;
		}
		if (doc.activeElement !== target && target.focus) {
			target.focus();
		}
		if (doc.activeElement !== target) {
			return false;
		}
		if (isHTMLInput(target) || isHTMLTextArea(target)) {
			const value = String(target.value ?? '');
			if (typeof target.setSelectionRange === 'function') {
				try {
					target.setSelectionRange(0, value.length);
					return target.selectionStart === 0 && target.selectionEnd === value.length;
				} catch (_) {}
			}
			if (typeof target.select === 'function') {
				try {
					target.select();
					return true;
				} catch (_) {}
			}
			return false;
		}
		if (isHTMLElement(target) && target.isContentEditable) {
			const selection = doc.defaultView && doc.defaultView.getSelection ? doc.defaultView.getSelection() : null;
			if (!selection) {
				return false;
			}
			const range = doc.createRange();
			range.selectNodeContents(target);
			selection.removeAllRanges();
			selection.addRange(range);
			return selection.rangeCount > 0;
		}
		return false;`)
}

func inputTargetValueScript() string {
	return inputTargetScript(`
		const { target, interactionTarget, isEditorLike } = resolveTarget(this, true);
		const editor = isEditorLike ? (interactionTarget || target) : null;
		return JSON.stringify({
			value: targetValue(target),
			text: isEditorLike ? editorReadableText(editor) : targetText(target),
		});`)
}

func setInputValueDirectScript(valueExpr string) string {
	return inputTargetScript(`
		const { target } = resolveTarget(this, false);
		if (!target) {
			throw new Error('Element is not an input-like control');
		}
		if (isHTMLInput(target) || isHTMLTextArea(target) || isHTMLSelect(target)) {
			target.value = ` + valueExpr + `;
		} else if (isHTMLElement(target) && target.isContentEditable) {
			target.textContent = ` + valueExpr + `;
		} else {
			throw new Error('Element is not an input-like control');
		}
		target.dispatchEvent(new Event('input', { bubbles: true }));
		target.dispatchEvent(new Event('change', { bubbles: true }));
		return true;`)
}

func parseInputTargetInfo(raw string) (inputTargetInfo, error) {
	if strings.TrimSpace(raw) == "" {
		return inputTargetInfo{}, errors.New("未返回输入目标信息")
	}
	var info inputTargetInfo
	if err := json.Unmarshal([]byte(raw), &info); err != nil {
		return inputTargetInfo{}, err
	}
	return info, nil
}

func parseInputTargetValue(raw string) (inputTargetValue, error) {
	if strings.TrimSpace(raw) == "" {
		return inputTargetValue{}, errors.New("未返回输入目标值")
	}
	var value inputTargetValue
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return inputTargetValue{}, err
	}
	return value, nil
}

func inputTargetValueMatches(value inputTargetValue, info inputTargetInfo, expected string) bool {
	if info.IsEditorLike {
		if value.Value == expected || value.Text == expected {
			return true
		}
		return expected != "" && strings.Contains(value.Text, expected)
	}
	if info.IsContentEditable {
		return value.Text == expected || value.Value == expected
	}
	return value.Value == expected
}

func shouldFallbackToRawKeyboardForEditor(text string) bool {
	const maxRawKeyboardFallbackRunes = 512
	return len([]rune(text)) <= maxRawKeyboardFallbackRunes
}

func (p *Page) selectorTargetInputInfo(ctx context.Context, ref SelectorTargetRef) (inputTargetInfo, error) {
	raw, err := p.selectorTargetString(ctx, ref, inputTargetInfoScript())
	if err != nil {
		return inputTargetInfo{}, err
	}
	return parseInputTargetInfo(raw)
}

func (p *Page) selectorTargetInputValueMatches(ctx context.Context, ref SelectorTargetRef, expected string) (bool, error) {
	info, err := p.selectorTargetInputInfo(ctx, ref)
	if err != nil {
		return false, err
	}
	raw, err := p.selectorTargetString(ctx, ref, inputTargetValueScript())
	if err != nil {
		return false, err
	}
	value, err := parseInputTargetValue(raw)
	if err != nil {
		return false, err
	}
	return inputTargetValueMatches(value, info, expected), nil
}

func (p *Page) selectorTargetFocusInput(ctx context.Context, ref SelectorTargetRef) (bool, error) {
	return p.selectorTargetBool(ctx, ref, focusInputTargetScript())
}

func (p *Page) selectorTargetInputActive(ctx context.Context, ref SelectorTargetRef) (bool, error) {
	return p.selectorTargetBool(ctx, ref, inputTargetActiveScript())
}

func (p *Page) selectorTargetSelectInputText(ctx context.Context, ref SelectorTargetRef) (bool, error) {
	return p.selectorTargetBool(ctx, ref, selectInputTargetTextScript())
}

func (p *Page) selectorTargetSetInputValueDirect(ctx context.Context, ref SelectorTargetRef, text string) error {
	_, err := p.selectorTargetBool(ctx, ref, setInputValueDirectScript("arguments[0]"), map[string]any{"value": text})
	return err
}

func (p *Page) selectorTargetSetInputValueDirectAndVerify(ctx context.Context, ref SelectorTargetRef, text string) error {
	if err := p.selectorTargetSetInputValueDirect(ctx, ref, text); err != nil {
		return err
	}
	matches, err := p.selectorTargetInputValueMatches(ctx, ref, text)
	if err != nil {
		return err
	}
	if !matches {
		return fmt.Errorf("direct input value mismatch")
	}
	return nil
}

func (p *Page) selectorTargetCheckedMatches(ctx context.Context, ref SelectorTargetRef, checked bool) (bool, error) {
	current, err := p.SelectorTargetChecked(ctx, ref)
	if err != nil {
		return false, err
	}
	return current == checked, nil
}

func (p *Page) selectorTargetSetCheckedDirectAndVerify(ctx context.Context, ref SelectorTargetRef, checked bool) error {
	_, err := p.selectorTargetBool(ctx, ref, setCheckedDirectScript("arguments[0]"), map[string]any{"value": checked})
	if err != nil {
		return err
	}
	matches, err := p.selectorTargetCheckedMatches(ctx, ref, checked)
	if err != nil {
		return err
	}
	if !matches {
		return fmt.Errorf("checked state mismatch")
	}
	return nil
}

func (p *Page) selectorTargetSelectValueMatches(ctx context.Context, ref SelectorTargetRef, values []string) (bool, error) {
	return p.selectorTargetBool(ctx, ref, selectValueMatchesScript("arguments[0]"), map[string]any{"value": values})
}

func checkedStateScript() string {
	return `function() {
		if (!this) {
			return false;
		}
		if (typeof this.checked === 'boolean') {
			return this.checked;
		}
		return this.getAttribute ? this.getAttribute('aria-checked') === 'true' : false;
	}`
}

func setCheckedDirectScript(valueExpr string) string {
	return `function() {
		const checked = ` + valueExpr + `;
		if (!this) {
			return false;
		}
		if (typeof this.checked === 'boolean') {
			this.checked = checked;
		} else if (this.setAttribute) {
			this.setAttribute('aria-checked', checked ? 'true' : 'false');
		} else {
			return false;
		}
		this.dispatchEvent(new Event('input', { bubbles: true }));
		this.dispatchEvent(new Event('change', { bubbles: true }));
		return true;
	}`
}

func selectValueMatchesScript(valueExpr string) string {
	return `function() {
		const values = ` + valueExpr + `;
		if (!(this instanceof HTMLSelectElement)) {
			return false;
		}
		const selected = Array.from(this.selectedOptions || []).map((option) => ({
			value: option.value,
			label: option.label,
			text: option.text,
		}));
		const optionMatches = (option, value) => option.value === value || option.label === value || option.text === value;
		if (this.multiple) {
			return values.every((value) => selected.some((option) => optionMatches(option, value)));
		}
		return values.length === 0 ? selected.length === 0 : selected.some((option) => values.some((value) => optionMatches(option, value)));
	}`
}
