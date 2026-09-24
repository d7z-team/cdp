package engine

import (
	"errors"
	"strings"
)

// Navigate 方法用于导航到指定URL
func (p *Page) Navigate(url string) error {
	p.lock.RLock()
	defer p.lock.RUnlock()
	if err := p.checkConn(); err != nil {
		return err
	}
	_, err := p.PageNavigate(url)
	return err
}

func (p *Page) Reload() error {
	p.lock.RLock()
	defer p.lock.RUnlock()
	if err := p.checkConn(); err != nil {
		return err
	}
	return p.PageReload()
}

func parseNavigationHistoryEntry(raw map[string]any) navigationHistoryEntry {
	entry := navigationHistoryEntry{}
	entry.URL, _ = raw["url"].(string)
	entry.UserTypedURL, _ = raw["userTypedURL"].(string)
	entry.TransitionType, _ = raw["transitionType"].(string)
	entry.URL = strings.TrimSpace(entry.URL)
	entry.UserTypedURL = strings.TrimSpace(entry.UserTypedURL)
	entry.TransitionType = strings.TrimSpace(entry.TransitionType)
	return entry
}

func (p *Page) currentNavigationHistoryEntry() (navigationHistoryEntry, error) {
	history, err := p.PageGetNavigationHistory()
	if err != nil {
		return navigationHistoryEntry{}, err
	}
	currentIndex, ok := SafeGet[float64](history, "currentIndex")
	if !ok {
		return navigationHistoryEntry{}, errors.New("未找到当前历史记录索引")
	}
	entries, ok := history["entries"].([]any)
	if !ok || len(entries) == 0 {
		return navigationHistoryEntry{}, errors.New("未找到历史记录")
	}
	index := int(currentIndex)
	if index < 0 || index >= len(entries) {
		return navigationHistoryEntry{}, errors.New("当前历史记录索引越界")
	}
	entry, ok := entries[index].(map[string]any)
	if !ok {
		return navigationHistoryEntry{}, errors.New("历史记录项格式错误")
	}
	return parseNavigationHistoryEntry(entry), nil
}

func (p *Page) resolveNavigationReasonFromHistory(pageURL string) string {
	entry, err := p.currentNavigationHistoryEntry()
	if err != nil {
		return ""
	}
	pageURL = strings.TrimSpace(pageURL)
	if pageURL != "" && entry.URL != "" && entry.URL != pageURL {
		return ""
	}
	if pageURL != "" && entry.UserTypedURL != "" && entry.UserTypedURL != pageURL && entry.URL != pageURL {
		return ""
	}
	return navigationReasonFromTransitionType(entry.TransitionType)
}

func (p *Page) GetURL() (string, error) {
	return p.EvalString("window.location.href")
}

func (p *Page) GetTitle() (string, error) {
	return p.EvalString("document.title")
}
