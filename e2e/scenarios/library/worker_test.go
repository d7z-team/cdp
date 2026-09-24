package library

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"gopkg.d7z.net/cdp"
)

func TestWorkerCommunicationAcrossNavigation(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/workers")
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	for range 2 {
		var results map[string]int
		mustOK(page.Eval(ctx, `return await runWorkers()`, &results))
		if len(results) != 6 {
			t.Fatalf("worker results: %v", results)
		}
		for kind, result := range results {
			if result != 42 {
				t.Errorf("%s returned %d", kind, result)
			}
		}
		// Exercise termination while attach/resume is in flight before navigating.
		mustOK(page.Eval(ctx, `for(let i=0;i<20;i++){const w=new Worker('/assets/worker.js');w.terminate()} return true`, nil))
		mustOK(page.Navigate(ctx, fixtureRT.Server.URL+"/workers", cdp.NavigateOptions{}))
	}
}

func TestCrossOriginFrameWorkerCommunication(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/iframe")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	mustOK(page.Eval(ctx, `const frame=document.querySelector('#demo-frame');
 const url=new URL('/workers',location.href);
 url.hostname=url.hostname==='localhost'?'127.0.0.1':'localhost';
 await new Promise(resolve=>{frame.onload=resolve;frame.src=url.href}); return true`, nil))
	frame := page.FrameLocator("#demo-frame")
	mustOK(frame.Locator("#run").Click(ctx))
	output := frame.Locator("#result[data-ready=true]")
	// Querying the completed output also checks the iframe runtime after workers run.
	var results map[string]int
	for {
		if n := must(output.Count(ctx)); n > 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	text := must(output.TextContent(ctx))
	if err := json.Unmarshal([]byte(text), &results); err != nil {
		t.Fatalf("worker output %q: %v", text, err)
	}
	if len(results) != 6 {
		t.Fatalf("worker results: %v", results)
	}
	for kind, result := range results {
		if result != 42 {
			t.Errorf("iframe %s returned %d", kind, result)
		}
	}
}

func TestWorkerPageClose(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	session := acquireSession(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	for range 2 {
		page := must(session.Browser.APIBrowser.NewPage(ctx))
		t.Cleanup(func() { _ = page.Close(context.Background()) })
		mustOK(page.Navigate(ctx, fixtureRT.Server.URL+"/workers", cdp.NavigateOptions{}))
		var results map[string]int
		mustOK(page.Eval(ctx, `return await runWorkers()`, &results))
		if len(results) != 6 {
			t.Fatalf("worker results: %v", results)
		}
		for kind, result := range results {
			if result != 42 {
				t.Errorf("%s returned %d", kind, result)
			}
		}
		mustOK(page.Eval(ctx, `for(let i=0;i<20;i++) new Worker('/assets/worker.js'); return true`, nil))
		mustOK(page.Close(ctx))
	}
}
