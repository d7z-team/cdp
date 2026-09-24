package library

import (
	"context"
	"encoding/json"
	"testing"

	"gopkg.d7z.net/cdp"
)

type navigatorPluginMimeState struct {
	PDFViewerEnabled       bool     `json:"pdfViewerEnabled"`
	PluginsLength          int      `json:"pluginsLength"`
	MimeTypesLength        int      `json:"mimeTypesLength"`
	NavigatorPluginsOwn    bool     `json:"navigatorPluginsOwn"`
	NavigatorMimeTypesOwn  bool     `json:"navigatorMimeTypesOwn"`
	NavigatorProtoPlugins  bool     `json:"navigatorProtoPlugins"`
	NavigatorProtoMimes    bool     `json:"navigatorProtoMimes"`
	PluginGetterNative     bool     `json:"pluginGetterNative"`
	MimeGetterNative       bool     `json:"mimeGetterNative"`
	PluginsToString        string   `json:"pluginsToString"`
	MimeTypesToString      string   `json:"mimeTypesToString"`
	PluginsInstance        bool     `json:"pluginsInstance"`
	MimeTypesInstance      bool     `json:"mimeTypesInstance"`
	PluginNames            []string `json:"pluginNames"`
	MimeTypes              []string `json:"mimeTypes"`
	PDFMimeEnabledPlugin   bool     `json:"pdfMimeEnabledPlugin"`
	TextPDFMimeEnabledPlug bool     `json:"textPDFMimeEnabledPlugin"`
}

func readNavigatorPluginMimeState(t *testing.T, eval evaluator) navigatorPluginMimeState {
	t.Helper()
	raw := evalValue(eval, `
const pluginDesc = Object.getOwnPropertyDescriptor(Navigator.prototype, "plugins");
const mimeDesc = Object.getOwnPropertyDescriptor(Navigator.prototype, "mimeTypes");
const pdfMime = navigator.mimeTypes.namedItem("application/pdf");
const textPDFMime = navigator.mimeTypes.namedItem("text/pdf");
return {
	pdfViewerEnabled: navigator.pdfViewerEnabled === true,
	pluginsLength: navigator.plugins.length,
	mimeTypesLength: navigator.mimeTypes.length,
	navigatorPluginsOwn: Object.prototype.hasOwnProperty.call(navigator, "plugins"),
	navigatorMimeTypesOwn: Object.prototype.hasOwnProperty.call(navigator, "mimeTypes"),
	navigatorProtoPlugins: !!pluginDesc,
	navigatorProtoMimes: !!mimeDesc,
	pluginGetterNative: !!pluginDesc && typeof pluginDesc.get === "function" && Function.prototype.toString.call(pluginDesc.get).includes("[native code]"),
	mimeGetterNative: !!mimeDesc && typeof mimeDesc.get === "function" && Function.prototype.toString.call(mimeDesc.get).includes("[native code]"),
	pluginsToString: Object.prototype.toString.call(navigator.plugins),
	mimeTypesToString: Object.prototype.toString.call(navigator.mimeTypes),
	pluginsInstance: navigator.plugins instanceof PluginArray,
	mimeTypesInstance: navigator.mimeTypes instanceof MimeTypeArray,
	pluginNames: Array.from(navigator.plugins).map((p) => p.name),
	mimeTypes: Array.from(navigator.mimeTypes).map((m) => m.type),
	pdfMimeEnabledPlugin: !!(pdfMime && pdfMime.enabledPlugin),
	textPDFMimeEnabledPlugin: !!(textPDFMime && textPDFMime.enabledPlugin),
};
`)
	var state navigatorPluginMimeState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		t.Fatalf("decode navigator plugin/mime state %q: %v", raw, err)
	}
	return state
}

func assertNativeNavigatorPluginShape(t *testing.T, state navigatorPluginMimeState) {
	t.Helper()
	if state.NavigatorPluginsOwn || state.NavigatorMimeTypesOwn {
		t.Fatalf("navigator plugins/mimeTypes must not be own properties: %+v", state)
	}
	if !state.NavigatorProtoPlugins || !state.NavigatorProtoMimes || !state.PluginGetterNative || !state.MimeGetterNative {
		t.Fatalf("navigator plugins/mimeTypes descriptors are not native prototype getters: %+v", state)
	}
	if state.PluginsToString != "[object PluginArray]" || state.MimeTypesToString != "[object MimeTypeArray]" {
		t.Fatalf("navigator plugins/mimeTypes native tags mismatch: %+v", state)
	}
	if !state.PluginsInstance || !state.MimeTypesInstance {
		t.Fatalf("navigator plugins/mimeTypes native instances mismatch: %+v", state)
	}
}

func TestNativeNavigatorWebdriver(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/eval")

	got := evalValue(page, `return navigator.webdriver`)
	if got != "false" {
		t.Fatalf("navigator.webdriver = %q, want false", got)
	}
	if inNavigator := evalValue(page, `return "webdriver" in navigator`); inNavigator != "true" {
		t.Fatalf("'webdriver' in navigator = %q, want true", inNavigator)
	}
	desc := evalValue(page, `
const d = Object.getOwnPropertyDescriptor(Navigator.prototype, "webdriver");
return !!d &&
	d.enumerable === true &&
	d.configurable === true &&
	typeof d.get === "function" &&
	Function.prototype.toString.call(d.get).includes("[native code]") &&
	!Object.prototype.hasOwnProperty.call(navigator, "webdriver");
`)
	if desc != "true" {
		t.Fatalf("Navigator.prototype.webdriver descriptor mismatch: %q", desc)
	}
}

func TestNativeNavigatorPlugins(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/eval")
	state := readNavigatorPluginMimeState(t, page)

	assertNativeNavigatorPluginShape(t, state)
	if state.PDFViewerEnabled && state.PluginsLength == 0 {
		t.Fatalf("pdfViewerEnabled=true but navigator.plugins is empty: %+v", state)
	}
}

func TestNativeNavigatorMimeTypes(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/eval")
	state := readNavigatorPluginMimeState(t, page)

	assertNativeNavigatorPluginShape(t, state)
	if state.PDFViewerEnabled && (!state.PDFMimeEnabledPlugin || !state.TextPDFMimeEnabledPlug || state.MimeTypesLength == 0) {
		t.Fatalf("pdfViewerEnabled=true but PDF mimeTypes are incomplete: %+v", state)
	}
}

func TestNativeNavigatorHardwareConcurrency(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/eval")

	got := evalValue(page, `return typeof navigator.hardwareConcurrency === 'number' && navigator.hardwareConcurrency > 0`)
	if got != "true" {
		t.Fatalf("navigator.hardwareConcurrency = %q, expected a positive number", got)
	}
}

func TestNativeNavigatorDeviceMemory(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/eval")

	got := evalValue(page, `return typeof navigator.deviceMemory === 'undefined' || (typeof navigator.deviceMemory === 'number' && navigator.deviceMemory >= 0)`)
	if got != "true" {
		t.Fatalf("navigator.deviceMemory = %q, expected number or undefined", got)
	}
}

func TestNativeChromeExists(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/eval")

	got := evalValue(page, `return typeof window.chrome`)
	if got != `"object"` {
		t.Fatalf("typeof window.chrome = %q, want object", got)
	}

	hasLoadTimes := evalValue(page, `return typeof window.chrome.loadTimes`)
	if hasLoadTimes != `"function"` {
		t.Fatalf("typeof window.chrome.loadTimes = %q, want function", hasLoadTimes)
	}

	hasCsi := evalValue(page, `return typeof window.chrome.csi`)
	if hasCsi != `"function"` {
		t.Fatalf("typeof window.chrome.csi = %q, want function", hasCsi)
	}

	loadTimesResult := evalValue(page, `return "navigationType" in window.chrome.loadTimes()`)
	if loadTimesResult != "true" {
		t.Fatalf("window.chrome.loadTimes() result missing navigationType")
	}

	csiResult := evalValue(page, `return typeof window.chrome.csi().tran`)
	if csiResult != `"number"` {
		t.Fatalf("window.chrome.csi().tran type = %q, want number", csiResult)
	}

	chromeExists := evalValue(page, `return Object.prototype.hasOwnProperty.call(window, 'chrome')`)
	if chromeExists != "true" {
		t.Fatalf("window.chrome should be own property, got %q", chromeExists)
	}
	chromeEnumerable := evalValue(page, `
const d = Object.getOwnPropertyDescriptor(window, "chrome");
return !!d && d.enumerable === true && Object.keys(window).includes("chrome");
`)
	if chromeEnumerable != "true" {
		t.Fatalf("window.chrome should be enumerable, got %q", chromeEnumerable)
	}
}

func TestNativeConsoleFormatting(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/eval")

	got := evalValue(page, `
let conversions = 0;
const value = {toString() { conversions++; return "formatted"; }};
console.log("%s", value);
return {conversions, source: Function.prototype.toString.call(console.log)};
`)
	var result struct {
		Conversions int    `json:"conversions"`
		Source      string `json:"source"`
	}
	mustOK(json.Unmarshal([]byte(got), &result))
	if result.Conversions != 1 || result.Source != "function log() { [native code] }" {
		t.Fatalf("native formatting: %+v", result)
	}
}

func TestNativeIdempotentOnNavigation(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	sess := acquireSession(t)
	page, err := sess.Open("/eval")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	mustOK( // Native navigator behavior persists across navigation
		page.Navigate(context.Background(), sess.FixtureURL("/selector"), cdp.NavigateOptions{}))

	got := evalValue(page, `return navigator.webdriver`)
	if got != "false" {
		t.Fatalf("navigator.webdriver after navigation = %q, want false", got)
	}

	assertNativeNavigatorPluginShape(t, readNavigatorPluginMimeState(t, page))
}
