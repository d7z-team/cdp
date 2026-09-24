// Package webassets embeds page-side runtime bundles and builds namespace-aware calls.
package webassets

import (
	_ "embed"
)

// MainRuntimeJs contains the on-demand main-world runtime bundle.
//
//go:embed dest/main_runtime/index.js
var MainRuntimeJs string

// MainRuntimeJsProbe reports whether the main-world runtime is installed.
var MainRuntimeJsProbe = `(() => typeof __MAIN_RUNTIME_SLOT__ !== "undefined")()`

// MainRuntimeJsCleanup destroys the main-world runtime.
var MainRuntimeJsCleanup = `(async () => { if (typeof __MAIN_RUNTIME_SLOT__ !== "undefined") { try { __MAIN_RUNTIME_SLOT__.destroy(); } finally { __MAIN_RUNTIME_SLOT__ = undefined; } } })()`

// CoreJs contains the isolated core runtime bundle.
//
//go:embed dest/core/index.js
var CoreJs string

// CoreJsProbe checks installation and announces readiness to attaching managers.
var CoreJsProbe = `(() => {
	const installed = ` + RuntimeInstalledProbe(RuntimeFFI) + `;
	if (installed && !window.__cdp_ffi.isCurrentDocument()) { window.__cdp_ffi.destroy(); return false; }
	if (installed) { window.__cdp_ffi.bridge.addTransportKey("__FRAME_TRANSPORT_KEY__"); ` + RuntimeOptionalMethodCall(RuntimeFFI, "announceRuntimeReady") + `; }
	return installed;
})()`

// CoreJsCleanup destroys the isolated core runtime.
var CoreJsCleanup = `(async () => {
	await ` + RuntimeOptionalMethodCall(RuntimeFFI, "destroy") + `;
})()`
