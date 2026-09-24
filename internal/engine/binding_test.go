package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"gopkg.d7z.net/cdp/internal/syncutil"
)

func TestBrowserSetupHonorsCancellationAndConfigurationError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	want := errors.New("invalid runtime registration")
	_, err := NewBrowserManagerWithConfig(ctx, "http://127.0.0.1:1", BrowserManagerConfig{
		Initialize: func(manager *BrowserManager) error {
			if len(manager.BoundPages()) != 0 {
				t.Fatal("pages were bound before runtime registration")
			}
			cancel()
			return want
		},
	})
	defer cancel()
	if !errors.Is(err, want) || !errors.Is(err, context.Canceled) {
		t.Fatalf("setup error lost cause: %v", err)
	}
	_, err = NewBrowserManager(ctx, "http://127.0.0.1:1")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled connect: %v", err)
	}
}

func TestRegisterInitScriptDefersUnconnectedPage(t *testing.T) {
	manager := &BrowserManager{
		sessions:          syncutil.NewSyncMap[string, *Page](),
		initScriptsByName: map[string]initScriptRegistration{},
	}
	manager.sessions.Store("newtab", &Page{ID: "newtab"})

	if err := manager.RegisterInitScript(InitScript{Name: "test-runtime.js", Exec: "true"}); err != nil {
		t.Fatalf("register init script: %v", err)
	}
	scripts := manager.initScriptsSnapshot()
	if len(scripts) != 1 || scripts[0].Name != "test-runtime.js" {
		t.Fatalf("registered scripts = %+v, want test-runtime.js", scripts)
	}
	if installs := manager.registeredInitScriptsSnapshot(); len(installs) != 0 {
		t.Fatalf("unconnected page installed scripts early: %+v", installs)
	}
}

func TestEnsureInitScriptRequiresRegistrationAndHonorsContext(t *testing.T) {
	manager := &BrowserManager{
		ctx:               context.Background(),
		sessions:          syncutil.NewSyncMap[string, *Page](),
		pageBinds:         map[string]*pageBindState{},
		initScriptsByName: map[string]initScriptRegistration{},
	}
	if err := manager.EnsureInitScript(context.Background(), "page-1", "test-runtime.js"); err == nil {
		t.Fatal("unregistered script should fail")
	}
	manager.initScriptsByName["test-runtime.js"] = initScriptRegistration{script: InitScript{Name: "test-runtime.js", Exec: "true"}}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := manager.EnsureInitScript(ctx, "page-1", "test-runtime.js"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("EnsureInitScript error = %v, want context deadline exceeded", err)
	}
}

func TestEnsureInitScriptReturnsPageBindFailure(t *testing.T) {
	want := errors.New("runtime initialization failed")
	manager := &BrowserManager{
		ctx:      context.Background(),
		sessions: syncutil.NewSyncMap[string, *Page](),
		pageBinds: map[string]*pageBindState{
			"page-1": {lastErr: want},
		},
		initScriptsByName: map[string]initScriptRegistration{
			"test-runtime.js": {script: InitScript{Name: "test-runtime.js", Exec: "true"}},
		},
	}

	if err := manager.EnsureInitScript(context.Background(), "page-1", "test-runtime.js"); !errors.Is(err, want) {
		t.Fatalf("EnsureInitScript error = %v, want bind failure %v", err, want)
	}
}
