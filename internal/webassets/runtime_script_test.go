package webassets

import (
	"strings"
	"testing"
)

func TestRuntimeOptionalMethodCall(t *testing.T) {
	got := RuntimeOptionalMethodCall(RuntimeFFI, "showMouseAction", JSLit(map[string]any{"kind": "move", "x": 12.5, "y": 8}))
	wantParts := []string{
		"const slot = window.__cdp_ffi;",
		"const receiver = slot;",
		"const fn = receiver?.showMouseAction;",
		`if (typeof fn !== "function")`,
		`return fn.call(receiver, {"kind":"move","x":12.5,"y":8});`,
	}
	for _, want := range wantParts {
		if !strings.Contains(got, want) {
			t.Fatalf("optional call missing %q in:\n%s", want, got)
		}
	}
}

func TestRuntimeRequiredMethodCall(t *testing.T) {
	got := RuntimeRequiredMethodCall(RuntimeFFI, "snapshot", `helper "missing"`, JSLit(map[string]any{"ok": true}))
	wantParts := []string{
		"const slot = window.__cdp_ffi;",
		"const receiver = slot;",
		"const fn = receiver?.snapshot;",
		`throw new Error("helper \"missing\"");`,
		`return fn.call(receiver, {"ok":true});`,
	}
	for _, want := range wantParts {
		if !strings.Contains(got, want) {
			t.Fatalf("required call missing %q in:\n%s", want, got)
		}
	}
}

func TestRuntimeMethodProbe(t *testing.T) {
	got := RuntimeMethodProbe(RuntimeFFI, "destroy")
	wantParts := []string{
		"const slot = window.__cdp_ffi;",
		"const receiver = slot;",
		"const fn = receiver?.destroy;",
		`return typeof fn === "function";`,
	}
	for _, want := range wantParts {
		if !strings.Contains(got, want) {
			t.Fatalf("probe missing %q in:\n%s", want, got)
		}
	}
}

func TestRuntimeMethodReadyProbe(t *testing.T) {
	got := RuntimeMethodReadyProbe(RuntimeFFI)
	wantParts := []string{
		"const slot = window.__cdp_ffi;",
		"const receiver = slot;",
		"const fn = receiver?.runtimeReady;",
		`if (typeof fn !== "function")`,
		`return fn.call(receiver);`,
		`})() === true)`,
	}
	for _, want := range wantParts {
		if !strings.Contains(got, want) {
			t.Fatalf("ready probe missing %q in:\n%s", want, got)
		}
	}
}

func TestRuntimeInstalledProbeAcceptsStartingRuntime(t *testing.T) {
	got := RuntimeInstalledProbe(RuntimeFFI)
	for _, want := range []string{
		"window.__cdp_runtime_lifecycle",
		`["ffi"]`,
		`state === "starting"`,
		`state === "ready"`,
		`typeof runtime?.runtimeReady === "function"`,
		`runtime?.runtimeReady?.() === true`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("installed probe missing %q in:\n%s", want, got)
		}
	}
}

func TestRuntimeMethodRejectsUnsafePath(t *testing.T) {
	for _, method := range []string{"", "pointer?.perform", "canvas[0].draw", "canvas.draw-cursor", "canvas..draw"} {
		method := method
		t.Run(method, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("expected panic for %q", method)
				}
			}()
			_ = RuntimeOptionalMethodCall(RuntimeFFI, method)
		})
	}
}

func TestRuntimeEmbedScriptsUseLifecycleInstalledState(t *testing.T) {
	if !strings.Contains(CoreJsProbe, "window.__cdp_runtime_lifecycle") {
		t.Fatalf("core probe does not use lifecycle installed state: %s", CoreJsProbe)
	}
}
