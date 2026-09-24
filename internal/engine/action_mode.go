package engine

import (
	"fmt"
	"strings"
)

type ActionMode string

const (
	ActionModeStrict ActionMode = "strict"
	ActionModeFast   ActionMode = "fast"
)

func normalizeActionMode(mode ActionMode) (ActionMode, error) {
	switch ActionMode(strings.ToLower(strings.TrimSpace(string(mode)))) {
	case "", ActionModeStrict:
		return ActionModeStrict, nil
	case ActionModeFast:
		return ActionModeFast, nil
	default:
		return "", fmt.Errorf("unsupported action mode: %s", mode)
	}
}

func (p *Page) SetActionMode(mode ActionMode) error {
	normalized, err := normalizeActionMode(mode)
	if err != nil {
		return err
	}
	p.actionModeMu.Lock()
	p.actionMode = normalized
	p.actionModeMu.Unlock()
	return nil
}

func (p *Page) ActionMode() ActionMode {
	if p == nil {
		return ActionModeStrict
	}
	p.actionModeMu.RLock()
	mode := p.actionMode
	p.actionModeMu.RUnlock()
	if mode == "" {
		return ActionModeStrict
	}
	return mode
}

func (p *Page) fastActionMode() bool {
	return p.ActionMode() == ActionModeFast
}

// SetDefaultActionMode sets the mode inherited by pages bound after the call.
func (r *BrowserManager) SetDefaultActionMode(mode ActionMode) error {
	normalized, err := normalizeActionMode(mode)
	if err != nil {
		return err
	}
	r.actionModeMu.Lock()
	r.actionMode = normalized
	r.actionModeMu.Unlock()
	return nil
}

// DefaultActionMode returns the mode inherited by newly bound pages.
func (r *BrowserManager) DefaultActionMode() ActionMode {
	if r == nil {
		return ActionModeStrict
	}
	r.actionModeMu.RLock()
	mode := r.actionMode
	r.actionModeMu.RUnlock()
	if mode == "" {
		return ActionModeStrict
	}
	return mode
}
