package library

import (
	"context"
	"testing"

	"gopkg.d7z.net/cdp"
)

func TestPageRuntimeControlIsolation(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/fullscreen")
	mustOK(page.Eval(context.Background(), `window.controlProbeNames = Object.getOwnPropertyNames(window);`, nil))
	_ = must(page.IsFullscreen(context.Background()))
	if got := evalValue(page, `
 const before = new Set(window.controlProbeNames);
 before.add("controlProbeNames");
 const names = Object.getOwnPropertyNames(window);
 return names.every(name => before.has(name)) && names.every(name => {
   const value = Object.getOwnPropertyDescriptor(window, name)?.value;
   return !(value && typeof value === 'object' && typeof value.consumePrintRequest === 'function');
 });`); got != "true" {
		t.Fatalf("main runtime exposed a window control: %s", got)
	}
	evalText(page, `enterFullscreen()`)
	if !must(page.IsFullscreen(context.Background())) {
		t.Fatal("fullscreen state unavailable through private controller")
	}
	evalText(page, `exitFullscreen()`)
	if must(page.IsFullscreen(context.Background())) {
		t.Fatal("fullscreen controller did not exit")
	}
}

func TestFrameTransportAuthenticatesDelivery(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/transport-security")
	ctx := context.Background()
	mustOK(page.FrameLocator("#frame-0").Locator("#target").Wait(ctx, cdp.StateVisible))
	core := must(page.ExecutionContext(ctx, cdp.ExecutionContextOptions{World: cdp.WorldCore}))
	// Trusted instrumentation lives only in the isolated world. A real bridge
	// request increments a DOM counter so page attempts have observable effects.
	if got := evalValue(core, `
 for (const frame of Array.from(window.frames)) {
   frame.__cdp_ffi.bridge.onRequest('test.increment', () => {
     const body = frame.document.body;
     const count = Number(body.dataset.deliveries || 0) + 1;
     body.dataset.deliveries = String(count);
     return count;
   });
 }
 return await window.__cdp_ffi.bridge.request('test.increment', {}, window.frames[0]);`); got != "1" {
		t.Fatalf("authenticated request: %s", got)
	}
	if got := evalValue(page, `
 const frames = Array.from(window.frames);
 const captured = frames[0].packets.slice();
 if (!captured.length) return 'no captured traffic';
 const forged = {type:'__cdp_bridge__', direction:'request', channel:'test.increment',
   srcRuntimeId:'page-forgery', requestId:'page-probe', payload:{}};
 window.postMessage(forged, '*');
 for (const frame of frames) {
   frame.postMessage(forged, '*');
   for (const packet of captured) {
     frame.postMessage(packet, '*');
     const altered = packet.slice(); altered[altered.length - 1] ^= 1;
     frame.postMessage(altered, '*');
   }
 }
 await new Promise(resolve => setTimeout(resolve, 250));
 return frames[0].document.body.dataset.deliveries === '1'
   && !frames[1].document.body.dataset.deliveries
   && frames.every(frame => frame.plainResponses === 0) && window.plainResponses === 0;
 `); got != "true" {
		t.Fatalf("untrusted/replayed delivery affected runtime: %s", got)
	}
	if got := evalValue(core, `return await window.__cdp_ffi.bridge.request('test.increment', {}, window.frames[0]);`); got != "2" {
		t.Fatalf("trusted communication after attack: %s", got)
	}
	if got := evalValue(page, `
 window.savedPackets = window.frames[0].packets.slice();
 const frame = document.querySelector('#frame-0');
 await new Promise(resolve => {
   frame.addEventListener('load', resolve, {once:true});
   frame.src = frame.src + '&replacement=1';
 });
 return true;`); got != "true" {
		t.Fatal("frame navigation failed")
	}
	mustOK(page.FrameLocator("#frame-0").Locator("#target").Wait(ctx, cdp.StateVisible))
	evalValue(core, `window.frames[0].__cdp_ffi.bridge.onRequest('test.increment', () => {
 const body = window.frames[0].document.body;
 body.dataset.deliveries = String(Number(body.dataset.deliveries || 0) + 1);
 return Number(body.dataset.deliveries);
 }); return true;`)
	if got := evalValue(page, `
 for (const packet of window.savedPackets) window.frames[0].postMessage(packet, '*');
 await new Promise(resolve => setTimeout(resolve, 250));
 return !window.frames[0].document.body.dataset.deliveries;`); got != "true" {
		t.Fatalf("old document replay: %s", got)
	}
	if got := evalValue(core, `return await window.__cdp_ffi.bridge.request('test.increment', {}, window.frames[0]);`); got != "1" {
		t.Fatalf("replacement document request: %s", got)
	}

}

func TestFrameTransportMatchesResponsePeer(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/transport-security")
	ctx := context.Background()
	mustOK(page.FrameLocator("#frame-1").Locator("#target").Wait(ctx, cdp.StateVisible))
	core := must(page.ExecutionContext(ctx, cdp.ExecutionContextOptions{World: cdp.WorldCore}))
	if got := evalValue(core, `
 const bridge = window.__cdp_ffi.bridge;
 const child = window.frames[0].__cdp_ffi.bridge;
 const sibling = window.frames[1].__cdp_ffi.bridge;
 let release;
 child.onRequest('test.response', () => new Promise(resolve => { release = resolve; }));
 const result = bridge.request('test.response', {}, window.frames[0]);
 const requestId = Array.from(bridge.pendingRequests.keys()).find(id => id.startsWith('test.response_'));
 const response = {type:'__cdp_bridge__', direction:'response', channel:'test.response',
   requestId, srcRuntimeId:child.runtimeId, dstRuntimeId:bridge.runtimeId, payload:{ok:true,value:'forged'}};
 await sibling.transport.send(window, response);
 await child.transport.send(window, {...response, channel:'wrong.channel'});
 await child.transport.send(window, {...response, srcRuntimeId:'wrong-runtime'});
 while (!release) await new Promise(resolve => setTimeout(resolve, 10));
 release('legitimate');
 return await result;`); got != `"legitimate"` {
		t.Fatalf("response identity validation: %s", got)
	}
}

func TestCrossOriginFrameTransport(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/iframe")
	ctx := context.Background()
	if got := evalValue(page, `
 const frame = document.querySelector('#demo-frame');
 const url = new URL(frame.src);
 url.hostname = url.hostname === 'localhost' ? '127.0.0.1' : 'localhost';
 await new Promise(resolve => { frame.addEventListener('load', resolve, {once:true}); frame.src = url.href; });
 return url.origin !== location.origin;`); got != "true" {
		t.Fatalf("cross-origin fixture: %s", got)
	}
	button := page.FrameLocator("#demo-frame").ByTestID("frame-btn")
	mustOK(button.Click(ctx))
	if got := must(button.TextContent(ctx)); got != "frame-clicked" {
		t.Fatalf("cross-origin click: %q", got)
	}
	document := must(page.Snapshot(ctx))
	found := false
	for _, node := range document.Nodes {
		if node.Name == "frame-clicked" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("cross-origin snapshot did not include the clicked button")
	}
}
