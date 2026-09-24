package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestEnsureEndpointRefusedPortAttemptsLaunch(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	profile := t.TempDir()
	if err := os.WriteFile(filepath.Join(profile, "DevToolsActivePort"), []byte(fmt.Sprintf("%d\n", port)), 0600); err != nil {
		t.Fatal(err)
	}
	b := &Browser{
		ctx:     context.Background(),
		config:  Config{ChromeUserDir: profile, ChromeExecutable: filepath.Join(profile, "missing-browser.exe")},
		extPath: filepath.Join(profile, "extensions"),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = b.EnsureEndpointContext(ctx)
	if err == nil || !strings.Contains(err.Error(), "launch browser:") || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refused stale endpoint should attempt launch and preserve launch error: %v", err)
	}
}

func TestEnsureEndpointContextCancellationWhileStartupIsOwned(t *testing.T) {
	b := &Browser{ctx: context.Background(), endpointWait: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := b.EnsureEndpointContext(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting for startup owner: %v", err)
	}
	if b.endpointWait == nil || b.process != nil {
		t.Fatal("canceled waiter changed startup ownership")
	}
}

func TestBuildLaunchArgsKeepsAutomationControlledDisabled(t *testing.T) {
	b := &Browser{config: Config{
		ChromeUserDir: "/tmp/cdp-profile",
		CustomArgs: []string{
			"--headless=new",
			"--disable-blink-features=Foo,AutomationControlled,Bar",
		},
	}}
	args, err := b.buildLaunchArgs()
	if err != nil {
		t.Fatalf("buildLaunchArgs: %v", err)
	}
	disableArgs := make([]string, 0)
	for _, arg := range args {
		if strings.HasPrefix(arg, "--disable-blink-features=") {
			disableArgs = append(disableArgs, arg)
		}
	}
	if len(disableArgs) != 1 {
		t.Fatalf("disable blink args = %+v, want exactly one", disableArgs)
	}
	if !chromeFeatureArgContains(disableArgs[0], "AutomationControlled") ||
		!chromeFeatureArgContains(disableArgs[0], "Foo") ||
		!chromeFeatureArgContains(disableArgs[0], "Bar") {
		t.Fatalf("disable blink args missing features: %+v", disableArgs)
	}
}

func TestBuildLaunchArgsRejectsAutomationControlledEnable(t *testing.T) {
	b := &Browser{config: Config{
		ChromeUserDir: "/tmp/cdp-profile",
		CustomArgs:    []string{"--enable-blink-features=Foo,AutomationControlled"},
	}}
	if _, err := b.buildLaunchArgs(); err == nil || !strings.Contains(err.Error(), "AutomationControlled") {
		t.Fatalf("buildLaunchArgs error = %v, want AutomationControlled rejection", err)
	}
}

func TestSendBrowserCommandContextIgnoresCanceledBrowserContext(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json/version":
			wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/browser/test"
			if err := json.NewEncoder(w).Encode(browserVersionInfo{WebSocketDebuggerURL: wsURL}); err != nil {
				t.Errorf("encode version response: %v", err)
			}
		case "/devtools/browser/test":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept websocket: %v", err)
				return
			}
			defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

			var req cdpRequest
			if err := wsjson.Read(context.Background(), conn, &req); err != nil {
				t.Errorf("read websocket request: %v", err)
				return
			}
			if req.Method != "Browser.close" {
				t.Errorf("method = %q, want Browser.close", req.Method)
			}
			if err := wsjson.Write(context.Background(), conn, cdpResponse{
				ID:     req.ID,
				Result: map[string]any{"ok": true},
			}); err != nil {
				t.Errorf("write websocket response: %v", err)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	browserCtx, cancel := context.WithCancel(context.Background())
	cancel()
	b := &Browser{ctx: browserCtx}

	if _, err := b.sendBrowserCommand(server.URL, "Browser.close", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("sendBrowserCommand error = %v, want canceled context", err)
	}
	if _, err := b.sendBrowserCommandContext(context.Background(), server.URL, "Browser.close", nil); err != nil {
		t.Fatalf("sendBrowserCommandContext returned error: %v", err)
	}
}
