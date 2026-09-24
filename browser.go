package cdp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	engine "gopkg.d7z.net/cdp/internal/engine"
	launcher "gopkg.d7z.net/cdp/internal/launcher"
)

type Browser struct {
	diagnostics    DiagnosticsMode
	stopping       atomic.Bool
	logger         *slog.Logger
	downloadMu     sync.Mutex
	downloadActive bool
	manager        *engine.BrowserManager
	process        *launcher.Browser
	timeouts       Timeouts
	endpoint       string
	tempDir        string
	cancel         context.CancelFunc
	closeOnce      sync.Once
	closeDone      chan struct{}
	closeErr       error
	pagesMu        sync.Mutex
	pages          map[string]*Page
}

func Launch(ctx context.Context, opts LaunchOptions) (*Browser, error) {
	if ctx == nil {
		return nil, errors.New("nil context")
	}
	diagnostics, err := opts.Diagnostics.normalized()
	if err != nil {
		return nil, err
	}
	t, err := opts.Timeouts.normalized()
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	profile := opts.UserDataDir
	temporary := ""
	if profile == "" {
		profile, err = os.MkdirTemp("", "cdp-*")
		if err != nil {
			return nil, err
		}
		temporary = profile
	}
	life, cancel := context.WithCancel(context.Background())
	cleanup := func(process *launcher.Browser) {
		c, stop := context.WithTimeout(context.Background(), t.Shutdown)
		defer stop()
		if process != nil {
			_ = process.KillOwnedContext(c)
		}
		cancel()
		if temporary != "" {
			_ = os.RemoveAll(temporary)
		}
	}
	cfg := launcher.Config{ChromeExecutable: opts.ExecutablePath, ChromeUserDir: profile, CustomArgs: append([]string(nil), opts.Args...), CAFingerPrints: append([]string(nil), opts.CertificateFingerprints...), FakeHosts: maps.Clone(opts.HostRules)}
	if !opts.Headful {
		cfg.CustomArgs = append(cfg.CustomArgs, "--headless=new")
	}
	if opts.WindowSize.Width > 0 && opts.WindowSize.Height > 0 {
		cfg.CustomArgs = append(cfg.CustomArgs, fmt.Sprintf("--window-size=%d,%d", opts.WindowSize.Width, opts.WindowSize.Height))
	}
	if opts.UserAgent != "" {
		cfg.CustomArgs = append(cfg.CustomArgs, "--user-agent="+opts.UserAgent)
	}
	for _, hook := range opts.Extensions {
		if hook == nil {
			cleanup(nil)
			return nil, errors.New("nil extension hook")
		}
		cfg.ChromeHooks = append(cfg.ChromeHooks, func(manifest map[string]any, dir string) error { return hook(ExtensionManifest(manifest), dir) })
	}
	proc, err := launcher.NewBrowser(life, cfg)
	if err != nil {
		cleanup(nil)
		return nil, operationError("launch", err)
	}
	setup, stop := context.WithTimeout(ctx, t.Connect)
	defer stop()
	if _, err = proc.EndpointContext(setup); err == nil {
		cleanup(nil)
		return nil, ErrProfileInUse
	}
	// A live profile lock is also checked by Chromium; never kill an unowned process.
	endpoint, err := proc.EnsureEndpointContext(setup)
	if err != nil {
		cleanup(proc)
		return nil, operationError("launch", err)
	}
	if !proc.OwnsProcess() {
		cleanup(nil)
		return nil, ErrProfileInUse
	}
	b, err := connect(setup, endpoint, ConnectOptions{Diagnostics: diagnostics, Timeouts: t, ActionMode: opts.ActionMode, Initialize: opts.Initialize, Logger: opts.Logger})
	if err != nil {
		cleanup(proc)
		return nil, err
	}
	b.process = proc
	b.tempDir = temporary
	previous := b.cancel
	b.cancel = func() { previous(); cancel() }
	return b, nil
}
func Connect(ctx context.Context, endpoint string, opts ConnectOptions) (*Browser, error) {
	return connect(ctx, endpoint, opts)
}
func connect(ctx context.Context, endpoint string, opts ConnectOptions) (*Browser, error) {
	if ctx == nil {
		return nil, errors.New("nil context")
	}
	diagnostics, err := opts.Diagnostics.normalized()
	if err != nil {
		return nil, err
	}
	t, err := opts.Timeouts.normalized()
	if err != nil {
		return nil, err
	}
	if opts.ActionMode != "" && opts.ActionMode != ActionStrict && opts.ActionMode != ActionFast {
		return nil, errors.New("invalid action mode")
	}
	life, cancel := context.WithCancel(context.Background())
	b := &Browser{diagnostics: diagnostics, timeouts: t, endpoint: endpoint, cancel: cancel, logger: opts.Logger, closeDone: make(chan struct{}), pages: map[string]*Page{}}
	setup, stop := context.WithTimeout(ctx, t.Connect)
	defer stop()
	manager, err := engine.ConnectManager(setup, life, endpoint, engine.BrowserManagerConfig{RuntimeDiagnostics: diagnostics == DiagnosticsRuntime, ConnectTimeout: t.Connect, DefaultActionMode: engine.ActionMode(opts.ActionMode), Initialize: func(m *engine.BrowserManager) error {
		b.manager = m
		if opts.Initialize != nil {
			return opts.Initialize(&Initializer{browser: b, manager: m})
		}
		return nil
	}})
	if err != nil {
		cancel()
		return nil, operationError("connect", err)
	}
	b.manager = manager
	if b.logger != nil {
		b.logger.Debug("CDP connection established")
	}
	return b, nil
}
func (b *Browser) operation(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc, error) {
	if ctx == nil {
		return nil, nil, errors.New("nil context")
	}
	if b == nil || b.manager == nil || b.stopping.Load() {
		return nil, nil, ErrClosed
	}
	select {
	case <-b.closeDone:
		return nil, nil, ErrClosed
	default:
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	c, cancel := context.WithTimeout(ctx, timeout)
	return c, cancel, nil
}
func (b *Browser) wrap(p *engine.Page) *Page {
	b.pagesMu.Lock()
	defer b.pagesMu.Unlock()
	if old := b.pages[p.ID]; old != nil && old.engine == p {
		return old
	}
	v := &Page{engine: p, browser: b}
	b.pages[p.ID] = v
	go func() {
		<-p.Done()
		b.pagesMu.Lock()
		defer b.pagesMu.Unlock()
		if b.pages[p.ID] == v {
			delete(b.pages, p.ID)
		}
	}()
	return v
}
func (b *Browser) Endpoint() string {
	if b == nil {
		return ""
	}
	return b.endpoint
}
func (b *Browser) Page(ctx context.Context, id string) (*Page, error) {
	c, cancel, err := b.operation(ctx, b.configuredTimeouts().Read)
	if err != nil {
		return nil, err
	}
	defer cancel()
	p, err := b.manager.LoadPageContext(c, id)
	if err != nil {
		return nil, operationError("page", err)
	}
	return b.wrap(p), nil
}
func (b *Browser) NewPage(ctx context.Context) (*Page, error) {
	c, cancel, err := b.operation(ctx, b.configuredTimeouts().Read)
	if err != nil {
		return nil, err
	}
	defer cancel()
	p, err := b.manager.CreatePage(c, "")
	if err != nil {
		return nil, operationError("new_page", err)
	}
	page := b.wrap(p)
	if err := page.Activate(c); err != nil {
		_ = page.Close(context.WithoutCancel(c))
		return nil, err
	}
	return page, nil
}
func (b *Browser) Pages(ctx context.Context) ([]*Page, error) {
	c, cancel, err := b.operation(ctx, b.configuredTimeouts().Read)
	if err != nil {
		return nil, err
	}
	defer cancel()
	items, err := b.manager.ListPages(c)
	if err != nil {
		return nil, operationError("pages", err)
	}
	ids := make([]string, 0, len(items))
	for id := range items {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	pages := make([]*Page, 0, len(ids))
	for _, id := range ids {
		p, err := b.Page(c, id)
		if err != nil {
			return nil, err
		}
		pages = append(pages, p)
	}
	return pages, nil
}
func (b *Browser) ActivePage(ctx context.Context) (*Page, error) {
	if b == nil || b.manager == nil {
		return nil, ErrClosed
	}
	id := b.manager.GetLastActivePageID()
	if id == "" {
		return nil, ErrNotFound
	}
	return b.Page(ctx, id)
}

type PageMatch struct{ URLPrefix string }

func (b *Browser) FindPage(ctx context.Context, match PageMatch) (*Page, error) {
	pages, err := b.Pages(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range pages {
		url, err := p.URL(ctx)
		if err != nil {
			return nil, err
		}
		if len(url) >= len(match.URLPrefix) && url[:len(match.URLPrefix)] == match.URLPrefix {
			return p, nil
		}
	}
	return nil, ErrNotFound
}
func (b *Browser) Close() error {
	if b == nil || b.closeDone == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), b.timeouts.Shutdown)
	defer cancel()
	return b.Shutdown(ctx)
}
func (b *Browser) Shutdown(ctx context.Context) error {
	if b == nil || b.closeDone == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("nil context")
	}
	b.closeOnce.Do(func() {
		b.stopping.Store(true)
		go func() {
			c, cancel := context.WithTimeout(context.Background(), b.timeouts.Shutdown)
			defer cancel()
			var errs []error
			if b.manager != nil {
				errs = append(errs, b.manager.DisconnectContext(c))
			}
			if b.process != nil {
				errs = append(errs, b.process.KillOwnedContext(c))
			}
			if b.cancel != nil {
				b.cancel()
			}
			if b.tempDir != "" {
				errs = append(errs, os.RemoveAll(filepath.Clean(b.tempDir)))
			}
			b.closeErr = errors.Join(errs...)
			if b.logger != nil {
				b.logger.Debug("CDP connection closed", "error", b.closeErr)
			}
			close(b.closeDone)
		}()
	})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.closeDone:
		return b.closeErr
	}
}

func (b *Browser) Alive() bool {
	if b == nil || b.manager == nil || b.stopping.Load() {
		return false
	}
	return b.manager.IsAlive()
}

func (b *Browser) configuredTimeouts() Timeouts {
	if b == nil {
		t, _ := (Timeouts{}).normalized()
		return t
	}
	return b.timeouts
}

// Diagnostics returns the fixed automatic event collection mode.
func (b *Browser) Diagnostics() DiagnosticsMode {
	if b == nil {
		return DiagnosticsOff
	}
	return b.diagnostics
}
