package engine

import (
	"strings"
	"testing"

	"gopkg.d7z.net/cdp/internal/webassets"
)

func TestFFIElementFunctionScriptsReturnRuntimeCall(t *testing.T) {
	required := ffiElementRequiredFunction("token, tile", "applyElementScreenshotTile", "missing", webassets.JSRaw("token"), webassets.JSRaw("tile"))
	for _, want := range []string{
		"function(token, tile) { return (() => {",
		"const slot = window.__cdp_ffi;",
		"const receiver = slot;",
		"const fn = receiver?.applyElementScreenshotTile;",
		"return fn.call(receiver, this, token, tile);",
	} {
		if !strings.Contains(required, want) {
			t.Fatalf("required script missing %q in:\n%s", want, required)
		}
	}

	optional := ffiOptionalFunction("token", "finishElementScreenshot", webassets.JSRaw("token"))
	for _, want := range []string{
		"function(token) { return (() => {",
		"const fn = receiver?.finishElementScreenshot;",
		"return fn.call(receiver, token);",
	} {
		if !strings.Contains(optional, want) {
			t.Fatalf("optional script missing %q in:\n%s", want, optional)
		}
	}
	if strings.Contains(optional, "return true") {
		t.Fatalf("optional script should return the runtime call promise, got:\n%s", optional)
	}
}
