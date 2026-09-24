package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

func loadBrowserServiceState(baseURL string) *browserServiceState {
	if state, ok := browserServiceStates.Load(baseURL); ok {
		return state.(*browserServiceState)
	}
	state := &browserServiceState{}
	actual, _ := browserServiceStates.LoadOrStore(baseURL, state)
	return actual.(*browserServiceState)
}

func (r *BrowserManager) activeConn() (*CdpConn, error) {
	if r == nil {
		return nil, ErrBrowserClosed
	}
	if r.conn == nil {
		return nil, ErrBrowserClosed
	}
	if err := r.conn.unavailableErr(); err != nil {
		return nil, err
	}
	return r.conn, nil
}

func (r *BrowserManager) activateTarget(ctx context.Context, targetID string) error {
	conn, err := r.activeConn()
	if err != nil {
		return err
	}
	return BrowserErrorFromCDP(
		"Target.activateTarget",
		ExecutionTarget{TargetID: targetID},
		conn.SendPacketContext(ctx, "Target.activateTarget", map[string]any{"targetId": targetID}),
	)
}

func (r *BrowserManager) serviceStateRef() *browserServiceState {
	if r == nil {
		return nil
	}
	if r.serviceState == nil {
		r.serviceState = &browserServiceState{}
	}
	return r.serviceState
}

func (r *BrowserManager) GetLastActivePageID() string {
	return r.getLastActivePageID(true)
}

func (r *BrowserManager) getLastActivePageID(refresh bool) string {
	if r == nil {
		return ""
	}
	if refresh {
		r.refreshPageTargetInfoFromList()
	}
	val := r.lastActivePageID.Load()
	id := ""
	if val != nil {
		id, _ = val.(string)
		id = strings.TrimSpace(id)
	}
	if id != "" && r.isUsableActivePage(id) {
		return id
	}
	if id != "" {
		r.lastActivePageID.Store("")
	}

	id = r.mostRecentUsablePageID()
	if id != "" {
		r.SetLastActivePageID(id)
	}
	return id
}

func (r *BrowserManager) isUsableActivePage(id string) bool {
	if r == nil || strings.TrimSpace(id) == "" {
		return false
	}
	page, ok := r.GetPage(id)
	if !ok || page == nil || page.CdpConn == nil {
		return false
	}
	state := r.snapshotPageState(id)
	return IsExecutablePageURL(state.url)
}

func (r *BrowserManager) mostRecentUsablePageID() string {
	if r == nil || r.sessions == nil {
		return ""
	}
	var id string
	var newest time.Time
	r.sessions.Range(func(key string, value *Page) bool {
		if strings.TrimSpace(key) == "" || value == nil || value.CdpConn == nil {
			return true
		}
		state := r.snapshotPageState(key)
		if !IsExecutablePageURL(state.url) {
			return true
		}
		candidateTime := state.activeAt
		if candidateTime.IsZero() {
			candidateTime = value.CreatedAt
		}
		if id == "" || candidateTime.After(newest) || (candidateTime.Equal(newest) && key < id) {
			id = key
			newest = candidateTime
		}
		return true
	})
	return id
}

func (r *BrowserManager) refreshPageTargetInfoFromList() {
	if r == nil || r.client == nil || strings.TrimSpace(r.baseURL) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	pages, err := r.ListPages(ctx)
	if err != nil {
		r.log(slog.LevelDebug, "refresh page target info failed", "error", err)
		return
	}
	activeID := r.currentActivePageID()
	for _, page := range pages {
		if page == nil {
			continue
		}
		info := TargetInfo{
			TargetID: page.ID,
			Title:    page.Title,
			Type:     page.Type,
			URL:      page.URL,
		}
		r.updatePageTargetInfo(info)
		if activeID == page.ID && !IsExecutablePageURL(page.URL) {
			r.clearActivePage(page.ID, LifecycleSourceTarget, "target_not_executable")
		}
	}
}

func noUsablePageError() error {
	return errors.New("当前没有可操作页面，请打开业务页面或调用 NewPage/AutoPage")
}

var (
	errPageUnavailable      = errors.New("page is no longer available")
	errPageConnectionClosed = errors.New("page CDP connection closed")
)

func (r *BrowserManager) GetLastActivePage() (*Page, error) {
	if r == nil || r.ctx == nil {
		return nil, ErrBrowserClosed
	}
	id := r.GetLastActivePageID()
	if id == "" {
		return nil, noUsablePageError()
	}
	ctx, cancel := context.WithTimeout(r.ctx, pageLoadTimeout)
	defer cancel()
	page, err := r.LoadPageContext(ctx, id)
	if err != nil && !errors.Is(err, errPageUnavailable) {
		return nil, fmt.Errorf("load active page %s: %w", id, err)
	}
	if page == nil {
		// 当前页可能已关闭，尝试重新获取一个
		r.lastActivePageID.Store("")
		id = r.GetLastActivePageID()
		if id != "" {
			page, err = r.LoadPageContext(ctx, id)
			if err != nil && !errors.Is(err, errPageUnavailable) {
				return nil, fmt.Errorf("load active page %s: %w", id, err)
			}
		}
	}
	if page == nil {
		return nil, noUsablePageError()
	}
	return page, nil
}
