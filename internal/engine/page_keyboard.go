package engine

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"
)

func normalizeModifierName(mod string) string {
	switch strings.TrimSpace(mod) {
	case "Ctrl":
		return "Control"
	default:
		return strings.TrimSpace(mod)
	}
}

func modifierBit(mod string) int {
	switch normalizeModifierName(mod) {
	case "Alt":
		return 1
	case "Control":
		return 2
	case "Meta":
		return 4
	case "Shift":
		return 8
	default:
		return 0
	}
}

func (p *Page) keyInfoFor(key string) struct {
	key     string
	code    string
	keyCode int
} {
	keyMap := map[string]struct {
		key     string
		code    string
		keyCode int
	}{
		"Enter":      {"Enter", "Enter", 13},
		"Tab":        {"Tab", "Tab", 9},
		"Escape":     {"Escape", "Escape", 27},
		"Backspace":  {"Backspace", "Backspace", 8},
		"Delete":     {"Delete", "Delete", 46},
		"ArrowUp":    {"ArrowUp", "ArrowUp", 38},
		"ArrowDown":  {"ArrowDown", "ArrowDown", 40},
		"ArrowLeft":  {"ArrowLeft", "ArrowLeft", 37},
		"ArrowRight": {"ArrowRight", "ArrowRight", 39},
		"Home":       {"Home", "Home", 36},
		"End":        {"End", "End", 35},
		"PageUp":     {"PageUp", "PageUp", 33},
		"PageDown":   {"PageDown", "PageDown", 34},
		"F1":         {"F1", "F1", 112},
		"F2":         {"F2", "F2", 113},
		"F3":         {"F3", "F3", 114},
		"F4":         {"F4", "F4", 115},
		"F5":         {"F5", "F5", 116},
		"F6":         {"F6", "F6", 117},
		"F7":         {"F7", "F7", 118},
		"F8":         {"F8", "F8", 119},
		"F9":         {"F9", "F9", 120},
		"F10":        {"F10", "F10", 121},
		"F11":        {"F11", "F11", 122},
		"F12":        {"F12", "F12", 123},
		"Space":      {" ", "Space", 32},
		"Control":    {"Control", "ControlLeft", 17},
		"Shift":      {"Shift", "ShiftLeft", 16},
		"Alt":        {"Alt", "AltLeft", 18},
		"Meta":       {"Meta", "MetaLeft", 91},
		"a":          {"a", "KeyA", 65},
		"b":          {"b", "KeyB", 66},
		"c":          {"c", "KeyC", 67},
		"d":          {"d", "KeyD", 68},
		"e":          {"e", "KeyE", 69},
		"f":          {"f", "KeyF", 70},
		"g":          {"g", "KeyG", 71},
		"h":          {"h", "KeyH", 72},
		"i":          {"i", "KeyI", 73},
		"j":          {"j", "KeyJ", 74},
		"k":          {"k", "KeyK", 75},
		"l":          {"l", "KeyL", 76},
		"m":          {"m", "KeyM", 77},
		"n":          {"n", "KeyN", 78},
		"o":          {"o", "KeyO", 79},
		"p":          {"p", "KeyP", 80},
		"q":          {"q", "KeyQ", 81},
		"r":          {"r", "KeyR", 82},
		"s":          {"s", "KeyS", 83},
		"t":          {"t", "KeyT", 84},
		"u":          {"u", "KeyU", 85},
		"v":          {"v", "KeyV", 86},
		"w":          {"w", "KeyW", 87},
		"x":          {"x", "KeyX", 88},
		"y":          {"y", "KeyY", 89},
		"z":          {"z", "KeyZ", 90},
		"0":          {"0", "Digit0", 48},
		"1":          {"1", "Digit1", 49},
		"2":          {"2", "Digit2", 50},
		"3":          {"3", "Digit3", 51},
		"4":          {"4", "Digit4", 52},
		"5":          {"5", "Digit5", 53},
		"6":          {"6", "Digit6", 54},
		"7":          {"7", "Digit7", 55},
		"8":          {"8", "Digit8", 56},
		"9":          {"9", "Digit9", 57},
		".":          {".", "Period", 190},
		",":          {",", "Comma", 188},
		";":          {";", "Semicolon", 186},
		"'":          {"'", "Quote", 222},
		"[":          {"[", "BracketLeft", 219},
		"]":          {"]", "BracketRight", 221},
		"\\":         {"\\", "Backslash", 220},
		"/":          {"/", "Slash", 191},
		"`":          {"`", "Backquote", 192},
		"=":          {"=", "Equal", 187},
		"-":          {"-", "Minus", 189},
	}

	if info, ok := keyMap[key]; ok {
		return info
	}
	if len(key) == 1 {
		keyCode := int(key[0])
		return struct {
			key     string
			code    string
			keyCode int
		}{
			key:     key,
			code:    "Key" + strings.ToUpper(key),
			keyCode: keyCode,
		}
	}
	return struct {
		key     string
		code    string
		keyCode int
	}{
		key:     key,
		code:    "",
		keyCode: 0,
	}
}

func (p *Page) dispatchKeyWithType(ctx context.Context,

	eventType string, key string, modifierFlags int) error {
	keyInfo := p.keyInfoFor(key)
	params := map[string]any{
		"type":                  eventType,
		"key":                   keyInfo.key,
		"code":                  keyInfo.code,
		"modifiers":             modifierFlags,
		"windowsVirtualKeyCode": keyInfo.keyCode,
		"nativeVirtualKeyCode":  keyInfo.keyCode,
	}
	if eventType == "keyDown" && utf8.RuneCountInString(keyInfo.key) == 1 && modifierFlags&(modifierBit("Control")|modifierBit("Alt")|modifierBit("Meta")) == 0 {
		params["text"] = keyInfo.key
		params["unmodifiedText"] = keyInfo.key
	}
	return p.inputDispatchKeyEvent(ctx,
		params)
}

// PressKey 方法用于发送键盘按键事件
func (p *Page) PressKey(key string, modifiers ...string) error {
	return p.PressKeyContext(p.ctx, key, modifiers...)
}

func (p *Page) PressKeyContext(ctx context.Context, key string, modifiers ...string) error {
	return p.runForegroundInteractionContext(ctx, func() error {
		return p.pressKey(ctx,
			key, modifiers...)
	})
}

func (p *Page) pressKey(ctx context.Context,

	key string, modifiers ...string) error {
	p.lock.RLock()
	defer p.lock.RUnlock()
	if err := p.checkConnContext(ctx); err != nil {
		return err
	}

	modifierFlags := 0
	for _, mod := range modifiers {
		modifierFlags |= modifierBit(mod)
	}

	err := p.dispatchKeyWithType(ctx,
		"keyDown", key, modifierFlags)
	if err != nil {
		return err
	}

	return p.dispatchKeyWithType(ctx,
		"keyUp", key, modifierFlags)
}

// TypeText 方法用于输入文本
func (p *Page) TypeText(ctx context.Context,

	text string) error {
	return p.runForegroundInteractionContext(ctx,
		func() error {
			return p.typeText(ctx,
				text)
		})
}

func (p *Page) typeText(ctx context.Context,

	text string) error {
	if err := p.checkConnContext(ctx); err != nil {
		return err
	}
	for _, char := range text {
		// 对于非ASCII字符，使用dispatchKeyEvent的char事件
		if char > 127 {
			// 发送char事件用于 Unicode 字符
			err := p.inputDispatchKeyEvent(ctx,
				map[string]any{
					"type": "char",
					"text": string(char),
				})
			if err != nil {
				return err
			}
		} else {
			// 对于ASCII字符，使用PressKey方法
			err := p.pressKey(ctx,
				string(char))
			if err != nil {
				return err
			}
		}
		if !p.fastActionMode() {
			// 添加小延迟以确保事件顺序
			time.Sleep(10 * time.Millisecond)
		}
	}
	return nil
}

// PressCombination 方法用于发送组合键（如Ctrl+C）
func (p *Page) PressCombination(ctx context.Context,

	mainKey string, modifiers ...string) error {
	return p.runForegroundInteractionContext(ctx,
		func() error {
			return p.pressCombination(ctx,
				mainKey, modifiers...)
		})
}

func (p *Page) pressCombination(ctx context.Context,

	mainKey string, modifiers ...string) error {
	p.lock.RLock()
	defer p.lock.RUnlock()
	if err := p.checkConnContext(ctx); err != nil {
		return err
	}

	modifierFlags := 0
	for _, modifier := range modifiers {
		mod := normalizeModifierName(modifier)
		modifierFlags |= modifierBit(mod)
		if err := p.dispatchKeyWithType(ctx,
			"rawKeyDown", mod, modifierFlags); err != nil {
			return err
		}
		if !p.fastActionMode() {
			time.Sleep(10 * time.Millisecond)
		}
	}

	if err := p.dispatchKeyWithType(ctx,
		"keyDown", mainKey, modifierFlags); err != nil {
		return err
	}
	if !p.fastActionMode() {
		time.Sleep(10 * time.Millisecond)
	}
	if err := p.dispatchKeyWithType(ctx,
		"keyUp", mainKey, modifierFlags); err != nil {
		return err
	}

	for i := len(modifiers) - 1; i >= 0; i-- {
		mod := normalizeModifierName(modifiers[i])
		modifierFlags &^= modifierBit(mod)
		if err := p.dispatchKeyWithType(ctx,
			"keyUp", mod, modifierFlags); err != nil {
			return err
		}
		if !p.fastActionMode() {
			time.Sleep(10 * time.Millisecond)
		}
	}

	return nil
}

func (p *Page) inputDispatchKeyEvent(ctx context.Context,

	params map[string]any) error {
	return BrowserErrorFromCDP("Input.dispatchKeyEvent", topPageExecutionTarget(), p.CdpConn.SendPacketContext(ctx,
		"Input.dispatchKeyEvent", params))
}

func (p *Page) InputInsertTextContext(ctx context.Context, text string) error {
	return p.runForegroundInteractionContext(ctx, func() error {
		return p.inputInsertText(ctx,
			text)
	})
}

func (p *Page) inputInsertText(ctx context.Context,

	text string) error {
	return BrowserErrorFromCDP("Input.insertText", topPageExecutionTarget(), p.CdpConn.SendPacketContext(ctx,
		"Input.insertText", map[string]any{"text": text}))
}
