package mcp

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.d7z.net/cdp"
	"gopkg.d7z.net/cdp/mcpserver"
)

func TestMCPDiagnosticsOff(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	ctx := t.Context()
	browser, err := cdp.Connect(ctx, browserService.Endpoint(), cdp.ConnectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Close()
	server, err := mcpserver.New(browser, mcpserver.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "diagnostics-off", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{Endpoint: httpServer.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	page, err := browser.NewPage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer page.Close(context.Background())
	result, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "browser_console", Arguments: map[string]any{"tab_id": page.ID()}})
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	raw, _ := json.Marshal(result.StructuredContent)
	if err = json.Unmarshal(raw, &output); err != nil {
		t.Fatal(err)
	}
	if !result.IsError || output.Error.Code != "diagnostics_disabled" {
		t.Fatalf("console response: %s", raw)
	}
	result, err = session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "browser_evaluate", Arguments: map[string]any{"tab_id": page.ID(), "expression": "6 * 7"}})
	if err != nil || result.IsError {
		t.Fatalf("off evaluation: %v, %+v", err, result)
	}
}
