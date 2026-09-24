package mcp

import (
	"context"
	"log"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"gopkg.d7z.net/cdp"
	"gopkg.d7z.net/cdp/e2e/fixture"
	"gopkg.d7z.net/cdp/e2e/harness/model"
	"gopkg.d7z.net/cdp/mcpserver"
)

var (
	browserEnabled bool
	fixtureServer  *fixture.Server
	browserService *cdp.Browser
	httpServer     *httptest.Server
	clientSession  *mcpsdk.ClientSession
)

func TestMain(m *testing.M) {
	os.Exit(runTestMain(m))
}

func runTestMain(m *testing.M) (code int) {
	config := model.LoadBrowserConfigFromEnv()
	code = 1
	if !config.Enabled {
		return m.Run()
	}
	browserEnabled = true
	ctx := context.Background()
	fixtureServer = fixture.Start(ctx)
	userDataDir := config.UserDataDir
	removeUserData := false
	if userDataDir == "" {
		var err error
		userDataDir, err = os.MkdirTemp("", "cdp-mcp-e2e-*")
		if err != nil {
			log.Printf("create user data dir: %v", err)
			return
		}
		removeUserData = true
	}
	defer func() {
		if clientSession != nil {
			_ = clientSession.Close()
		}
		if httpServer != nil {
			httpServer.Close()
		}
		if browserService != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if err := browserService.Shutdown(shutdownCtx); err != nil && code == 0 {
				log.Printf("shutdown browser: %v", err)
				code = 1
			}
			cancel()
		}
		fixtureServer.Close()
		if removeUserData {
			_ = os.RemoveAll(userDataDir)
		}
	}()
	var err error
	browserService, err = cdp.Launch(ctx, cdp.LaunchOptions{Diagnostics: cdp.DiagnosticsRuntime, Headful: !config.Headless, ExecutablePath: config.Executable, UserDataDir: userDataDir, WindowSize: cdp.WindowSize{Width: 1280, Height: 900}})
	if err != nil {
		log.Printf("start MCP browser: %v", err)
		return
	}
	server, err := mcpserver.New(browserService, mcpserver.Options{EnableDebug: true})
	if err != nil {
		log.Printf("configure MCP HTTP server: %v", err)
		return
	}
	defer server.Close()
	httpServer = httptest.NewServer(server.Handler())
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "mcp-e2e", Version: "1"}, nil)
	clientSession, err = client.Connect(ctx, &mcpsdk.StreamableClientTransport{Endpoint: httpServer.URL + "/mcp"}, nil)
	if err != nil {
		log.Printf("connect MCP client: %v", err)
		return
	}
	code = m.Run()
	return
}
