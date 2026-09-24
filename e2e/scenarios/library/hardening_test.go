package library

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"gopkg.d7z.net/cdp"
)

func TestClosedShadowQueryAndActions(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	p := openFixture(t, "/closed-shadow")
	ctx := t.Context()
	if n := must(p.Locator(".shadow-action").Count(ctx)); n != 3 {
		t.Fatalf("shadow buttons: %d", n)
	}
	if value := must(p.Locator(".missing", ".shadow-action").Nth(1).TextContent(ctx)); value != "Nested action" {
		t.Fatalf("ordered shadow fallback: %q", value)
	}
	if value := must(p.Locator(".shadow-action").Last().TextContent(ctx)); value != "Open action" {
		t.Fatalf("last shadow match: %q", value)
	}
	mustOK(p.Locator("#closed").Locator("button").First().Click(ctx))
	if value := must(p.Locator("#count").TextContent(ctx)); value != "1" {
		t.Fatalf("click result: %q", value)
	}
	mustOK(p.Locator("#closed").Locator("#shadow-input").First().Fill(ctx, "closed value"))
	mustOK(p.Locator("#add").Click(ctx))
	if n := must(p.Locator(".shadow-action").Count(ctx)); n != 4 {
		t.Fatalf("dynamic roots: %d", n)
	}
	mustOK(p.Locator("#closed").Locator("xpath=.//button").First().Click(ctx))
	if value := must(p.Locator("#count").TextContent(ctx)); value != "2" {
		t.Fatalf("XPath action: %q", value)
	}
	document := must(p.Snapshot(ctx))
	var closedRef string
	slottedNodes := 0
	for _, node := range document.Nodes {
		if node.Text == "Slotted label" {
			slottedNodes++
		}
		if node.Name == "Closed action" {
			closedRef = document.Ref(node.ID)
		}
	}
	if closedRef == "" {
		t.Fatalf("closed button missing from snapshot: %+v", document.Nodes)
	}
	if slottedNodes != 1 {
		t.Fatalf("slot appears %d times in snapshot", slottedNodes)
	}
	element := must(p.Element(ctx, closedRef))
	mustOK(element.Click(ctx))
	image := must(element.Screenshot(ctx))
	if image.Bounds().Dx() == 0 || image.Bounds().Dy() == 0 {
		t.Fatal("empty shadow screenshot")
	}
	if value := must(p.Locator("#count").TextContent(ctx)); value != "3" {
		t.Fatalf("snapshot ref action: %q", value)
	}
	if got := evalValue(p, `return document.querySelector('#closed').shadowRoot === null`); got != "true" {
		t.Fatalf("closed mode: %s", got)
	}
}

func TestDiagnosticsModesAndBindingNavigation(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	for _, mode := range []cdp.DiagnosticsMode{cdp.DiagnosticsOff, cdp.DiagnosticsRuntime} {
		t.Run(string(mode), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			calls := make(chan cdp.BindingCall, 10)
			b := must(cdp.Connect(ctx, execBrowser.APIBrowser.Endpoint(), cdp.ConnectOptions{Diagnostics: mode, Initialize: func(i *cdp.Initializer) error {
				return i.RegisterBinding(cdp.Binding{Name: "testDocumentBinding", World: cdp.WorldMain, Handle: func(_ context.Context, call cdp.BindingCall) error { calls <- call; return nil }})
			}}))
			t.Cleanup(func() { mustOK(b.Close()) })
			p := must(b.NewPage(ctx))
			t.Cleanup(func() { _ = p.Close(context.Background()) })
			for range 2 {
				mustOK(p.Navigate(ctx, fixtureRT.Server.URL+"/home", cdp.NavigateOptions{}))
				mustOK(p.Eval(ctx, `testDocumentBinding('ready');return true`, nil))
				select {
				case call := <-calls:
					if call.Page.ID() != p.ID() || call.Payload != "ready" {
						t.Fatalf("binding: %+v", call)
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			sub, err := p.Session().Subscribe(ctx, "Runtime.consoleAPICalled")
			if mode == cdp.DiagnosticsOff {
				if !errors.Is(err, cdp.ErrDiagnosticsDisabled) {
					t.Fatalf("diagnostic error: %v", err)
				}
				return
			}
			mustOK(err)
			defer sub.Close()
			exceptions := must(p.Session().Subscribe(ctx, "Runtime.exceptionThrown"))
			defer exceptions.Close()
			mustOK(p.Eval(ctx, `console.log('diagnostic message', {value:42});return true`, nil))
			select {
			case event := <-sub.Events():
				if event.Method != "Runtime.consoleAPICalled" {
					t.Fatalf("event: %+v", event)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			mustOK(p.Eval(ctx, `setTimeout(() => { throw new Error('diagnostic exception'); }, 0); return true`, nil))
			select {
			case event := <-exceptions.Events():
				if event.Method != "Runtime.exceptionThrown" || event.Params["exceptionDetails"] == nil {
					t.Fatalf("exception event: %+v", event)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		})
	}
}

func TestFrameMainWorldAndContextLifetime(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	p := openFixture(t, "/iframe")
	ctx := t.Context()
	var tree struct {
		FrameTree struct {
			ChildFrames []struct {
				Frame struct {
					ID string `json:"id"`
				} `json:"frame"`
			} `json:"childFrames"`
		} `json:"frameTree"`
	}
	mustOK(p.Session().Call(ctx, "Page.getFrameTree", nil, &tree))
	if len(tree.FrameTree.ChildFrames) == 0 {
		t.Fatal("missing child frame")
	}
	child := must(p.ExecutionContext(ctx, cdp.ExecutionContextOptions{World: cdp.WorldMain, FrameID: tree.FrameTree.ChildFrames[0].Frame.ID}))
	var frame bool
	mustOK(child.Eval(ctx, `window.frameMarker = 42; return window !== window.top`, &frame))
	if !frame {
		t.Fatal("child main evaluation ran in root")
	}
	if got := evalValue(p, `return window.frameMarker === undefined && document.querySelector('#demo-frame').contentWindow.frameMarker === 42`); got != "true" {
		t.Fatalf("realm identity: %s", got)
	}
	root := must(p.ExecutionContext(ctx, cdp.ExecutionContextOptions{}))
	mustOK(p.Reload(ctx))
	if err := root.Eval(ctx, `window.staleMutation = true`, nil); !errors.Is(err, cdp.ErrStaleElement) {
		t.Fatalf("old context: %v", err)
	}
	if got := evalValue(p, `return window.staleMutation === undefined`); got != "true" {
		t.Fatalf("stale side effect: %s", got)
	}
}

func TestClosedShadowFrameTransport(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	for _, path := range []string{"/closed-shadow", "/closed-shadow?cross=1"} {
		t.Run(path, func(t *testing.T) {
			p := openFixture(t, path)
			ctx := t.Context()
			mustOK(p.Locator("#add-frame").Click(ctx))
			button := p.FrameLocator("#shadow-frame").ByTestID("frame-btn")
			mustOK(button.Click(ctx))
			if text := must(button.TextContent(ctx)); text != "frame-clicked" {
				t.Fatalf("shadow frame click: %s", text)
			}
			patch := p.FrameLocator("#shadow-frame").ByTestID("rect-item-a")
			mustOK(patch.SetStyle(ctx, "background", "rgb(38, 172, 222)"))
			shot := must(patch.Screenshot(ctx))
			pixel := shot.RGBAAt(shot.Bounds().Dx()-2, shot.Bounds().Dy()-2)
			// Desktop compositing and screencast encoding can shift RGB slightly.
			if pixel.R < 30 || pixel.R > 46 || pixel.G < 164 || pixel.G > 180 || pixel.B < 214 || pixel.B > 230 {
				t.Fatalf("shadow frame screenshot missed target: %+v", pixel)
			}
			document := must(p.Snapshot(ctx))
			found := false
			for _, node := range document.Nodes {
				if node.Name == "frame-clicked" {
					found = true
				}
			}
			if !found {
				t.Fatalf("shadow frame snapshot: %+v", document.Nodes)
			}
		})
	}
}

func TestStrictCSPWithIsolatedRuntime(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	p := openFixture(t, "/strict-csp")
	ctx := t.Context()
	mustOK(p.Locator("#allowed").Click(ctx))
	if value := must(p.Locator("#value").TextContent(ctx)); value != "1" {
		t.Fatalf("CSP allowed action: %s", value)
	}
	if got := evalValue(p, `return window.disallowedScriptRan === undefined`); got != "true" {
		t.Fatalf("page CSP did not block inline script: %s", got)
	}
}

func TestSecurePageBlocksActiveMixedContent(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	ctx := t.Context()
	var requests atomic.Int64
	insecure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte("window.mixedLoaded=true"))
	}))
	defer insecure.Close()
	secure := httptest.NewTLSServer(fixtureRT.Server.HTTP.Config.Handler)
	defer secure.Close()
	fingerprint := sha256.Sum256(secure.Certificate().RawSubjectPublicKeyInfo)
	browser := must(cdp.Launch(ctx, cdp.LaunchOptions{ExecutablePath: execBrowser.Config.Executable, Headful: !execBrowser.Config.Headless, CertificateFingerprints: []string{base64.StdEncoding.EncodeToString(fingerprint[:])}, HostRules: map[string]string{"insecure.test": "127.0.0.1"}}))
	defer browser.Close()
	p := must(browser.NewPage(ctx))
	address := must(url.Parse(insecure.URL))
	mustOK(p.Navigate(ctx, secure.URL+"/mixed-content?port="+address.Port(), cdp.NavigateOptions{}))
	if value := must(p.Locator("#status").TextContent(ctx)); value != "blocked" {
		t.Fatalf("mixed script state: %s", value)
	}
	if requests.Load() != 0 {
		t.Fatalf("active mixed-content requests reached server: %d", requests.Load())
	}
	if got := evalValue(p, `return window.mixedLoaded`); got != "false" {
		t.Fatalf("mixed script executed: %s", got)
	}
}

func TestCrossOriginExecutionWorlds(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	p := openFixture(t, "/iframe")
	ctx := t.Context()
	mustOK(p.Eval(ctx, `const frame=document.querySelector('#demo-frame');const url=new URL(frame.src);url.hostname=location.hostname==='localhost'?'127.0.0.1':'localhost';await new Promise(resolve=>{frame.onload=resolve;frame.src=url.href});return true`, nil))
	var root struct {
		Root struct {
			NodeID int `json:"nodeId"`
		} `json:"root"`
	}
	mustOK(p.Session().Call(ctx, "DOM.getDocument", nil, &root))
	var owner struct {
		NodeID int `json:"nodeId"`
	}
	mustOK(p.Session().Call(ctx, "DOM.querySelector", map[string]any{"nodeId": root.Root.NodeID, "selector": "#demo-frame"}, &owner))
	var description struct {
		Node struct {
			FrameID string `json:"frameId"`
		} `json:"node"`
	}
	mustOK(p.Session().Call(ctx, "DOM.describeNode", map[string]any{"nodeId": owner.NodeID}, &description))
	if description.Node.FrameID == "" {
		t.Fatal("missing cross-origin frame ID")
	}
	for _, world := range []cdp.World{cdp.WorldMain, cdp.WorldCore} {
		handle := must(p.ExecutionContext(ctx, cdp.ExecutionContextOptions{World: world, FrameID: description.Node.FrameID}))
		var valid bool
		mustOK(handle.Eval(ctx, `return window!==window.top && location.pathname==='/frame-content'`, &valid))
		if !valid {
			t.Fatalf("wrong realm for %s", world)
		}
	}
}

func TestNestedFrameCanvasDelivery(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	p := openFixture(t, "/iframe-nested")
	ctx := t.Context()
	_ = must(p.Locator("iframe").Count(ctx))
	var tree struct {
		FrameTree struct {
			ChildFrames []struct {
				ChildFrames []struct {
					Frame struct {
						ID string `json:"id"`
					} `json:"frame"`
				} `json:"childFrames"`
			} `json:"childFrames"`
		} `json:"frameTree"`
	}
	mustOK(p.Session().Call(ctx, "Page.getFrameTree", nil, &tree))
	if len(tree.FrameTree.ChildFrames) != 1 || len(tree.FrameTree.ChildFrames[0].ChildFrames) != 1 {
		t.Fatalf("nested frame tree: %+v", tree)
	}
	child := must(p.ExecutionContext(ctx, cdp.ExecutionContextOptions{World: cdp.WorldCore, FrameID: tree.FrameTree.ChildFrames[0].ChildFrames[0].Frame.ID}))
	mustOK(child.Eval(ctx, `window.__cdp_ffi.canvas.draw(20,20,100,100,0);return true`, nil))
	deadline := time.Now().Add(2 * time.Second)
	for {
		if err := assertCoreScreenshotOverlayVisible(p, "nested frame canvas"); err == nil {
			break
		} else if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSrcdocDocumentReplacement(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	p := openFixture(t, "/runtime-documents")
	ctx := t.Context()
	button := p.FrameLocator("#srcdoc").Locator("#value")
	mustOK(button.Click(ctx))
	if value := must(button.TextContent(ctx)); value != "clicked" {
		t.Fatalf("srcdoc action: %s", value)
	}
	mustOK(p.Locator("#replace").Click(ctx))
	if value := must(button.TextContent(ctx)); value != "replacement" {
		t.Fatalf("document.open content: %s", value)
	}
	mustOK(button.Click(ctx))
	if value := must(button.TextContent(ctx)); value != "clicked" {
		t.Fatalf("replacement action: %s", value)
	}
}

func TestSynchronousPopupDocument(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	p := openFixture(t, "/runtime-documents")
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	before := map[string]bool{}
	for _, page := range must(execBrowser.APIBrowser.Pages(ctx)) {
		before[page.ID()] = true
	}
	mustOK(p.Locator("#popup").Click(ctx))
	var popup *cdp.Page
	for popup == nil {
		for _, page := range must(execBrowser.APIBrowser.Pages(ctx)) {
			if !before[page.ID()] {
				popup = page
				break
			}
		}
		if popup == nil {
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	defer popup.Close(context.Background())
	mustOK(popup.Locator("#value").Click(ctx))
	if value := must(popup.Locator("#value").TextContent(ctx)); value != "clicked" {
		t.Fatalf("popup action: %s", value)
	}
}

func TestBackForwardRestoredDocument(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	p := openFixture(t, "/runtime-documents")
	ctx := t.Context()
	old := must(p.ExecutionContext(ctx, cdp.ExecutionContextOptions{World: cdp.WorldCore}))
	mustOK(p.Navigate(ctx, fixtureRT.Server.URL+"/home", cdp.NavigateOptions{}))
	mustOK(p.Back(ctx))
	var persisted bool
	mustOK(p.Eval(ctx, `return window.restoredFromCache`, &persisted))
	if !persisted {
		t.Fatal("history navigation did not restore the BFCache document")
	}
	mustOK(p.FrameLocator("#srcdoc").Locator("#value").Click(ctx))
	if value := must(p.FrameLocator("#srcdoc").Locator("#value").TextContent(ctx)); value != "clicked" {
		t.Fatalf("restored frame: %q", value)
	}
	if err := old.Eval(ctx, `return true`, nil); err == nil {
		t.Fatal("old context handle survived history navigation")
	}
}
