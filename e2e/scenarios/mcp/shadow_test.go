package mcp

import "testing"

func TestMCPClosedShadowReferences(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	result, output := callTool(t, "browser_navigate", map[string]any{"url": fixtureServer.URL + "/closed-shadow"})
	requireOK(t, "navigate closed shadow", result, output)
	tabID := output["tab_id"].(string)
	ref := requireRef(t, tabID, map[string]any{"role": "button", "name": "Closed action"})
	result, output = callTool(t, "browser_click", map[string]any{"tab_id": tabID, "ref": ref})
	requireOK(t, "closed shadow ref click", result, output)
	if got := evaluate(t, tabID, "document.querySelector('#count').textContent"); got != "1" {
		t.Fatalf("closed ref click result: %v", got)
	}
	if got := evaluate(t, tabID, "document.querySelector('#closed').shadowRoot === null"); got != true {
		t.Fatalf("closed shadow visibility: %v", got)
	}
	result, output = callTool(t, "browser_navigate", map[string]any{"tab_id": tabID, "url": fixtureServer.URL + "/home"})
	requireOK(t, "navigate away", result, output)
	result, output = callTool(t, "browser_click", map[string]any{"tab_id": tabID, "ref": ref})
	if !result.IsError || output["ok"] == true {
		t.Fatalf("stale shadow ref accepted: %v", output)
	}
}
