package engine

import (
	"strings"
	"testing"
)

func TestRuntimeFieldSetAvoidsStableCDPNames(t *testing.T) {
	fields, err := newRuntimeFieldSet()
	if err != nil {
		t.Fatal(err)
	}
	values := []string{fields.MainRuntime, fields.ScriptLabel}
	seen := map[string]bool{}
	for _, value := range values {
		if value == "" {
			t.Fatal("runtime field is empty")
		}
		if runtimeFieldNameHasForbiddenToken(value) {
			t.Fatalf("runtime field contains forbidden token: %q", value)
		}
		if seen[value] {
			t.Fatalf("runtime field is not unique: %q", value)
		}
		seen[value] = true
	}
}

func TestRenderMainWorldScriptReplacesSlots(t *testing.T) {
	fields := RuntimeFieldSet{
		MainRuntime: "_runtimeSlot",
	}
	got := renderMainWorldScript(`window["__MAIN_RUNTIME_SLOT__"];`, fields)
	if strings.Contains(got, "__MAIN_RUNTIME_SLOT__") {
		t.Fatalf("placeholder leaked after render: %s", got)
	}
	if !strings.Contains(got, "_runtimeSlot") {
		t.Fatalf("random slots missing after render: %s", got)
	}
}

func TestMainRuntimeMethodCallUsesConfiguredSlot(t *testing.T) {
	got, err := mainRuntimeMethodCall("_runtimeSlot", "fullscreenState")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `typeof _runtimeSlot`) {
		t.Fatalf("main runtime call does not use slot lookup:\n%s", got)
	}
}

func TestInitScriptSourceURLUsesConfiguredLabel(t *testing.T) {
	script := InitScript{
		Name:  "main_runtime.js",
		Exec:  "void 0;",
		Label: "_randomLabel",
	}
	got := script.execSourceWithAction("main_runtime")
	if !strings.Contains(got, "//# sourceURL=randomlabel_init.js") {
		t.Fatalf("sourceURL does not use random label:\n%s", got)
	}
}

func TestIsolatedInitScriptSourceTracksManagerOwnership(t *testing.T) {
	manager := &BrowserManager{
		namespaces:    defaultNamespaceRegistrations(),
		runtimeFields: RuntimeFieldSet{ScriptLabel: "_managerOwner"},
	}
	script := InitScript{
		Name:      "test-runtime.js",
		Namespace: NamespaceIsolatedCore,
		Exec:      `(async () => { window.testRuntimeStarted = true; })()`,
		Probe:     `(async () => true)()`,
		Cleanup:   `(async () => { window.testRuntimeStarted = false; })()`,
		Label:     "test-runtime",
	}

	execSource := manager.initScriptSource(script, "bind", "exec")
	if !strings.Contains(execSource, `owners["test-runtime.js"] = "_managerOwner"`) ||
		!strings.Contains(execSource, script.Exec) {
		t.Fatalf("isolated exec source does not claim ownership:\n%s", execSource)
	}
	probeSource := manager.initScriptSource(script, "bind", "probe")
	if !strings.Contains(probeSource, `if (ready === true) owners["test-runtime.js"] = "_managerOwner"`) {
		t.Fatalf("successful probe does not claim ownership:\n%s", probeSource)
	}
	verifySource := manager.initScriptSource(script, "ensure_ready", "verify")
	if strings.Contains(verifySource, `owners["test-runtime.js"]`) ||
		strings.Contains(verifySource, `"_managerOwner"`) ||
		!strings.Contains(verifySource, script.Probe) {
		t.Fatalf("runtime verification is not read-only:\n%s", verifySource)
	}
	cleanupSource := manager.initScriptSource(script, "", "cleanup")
	if !strings.Contains(cleanupSource, `if (owners["test-runtime.js"] !== "_managerOwner") return false`) ||
		!strings.Contains(cleanupSource, `if (owners["test-runtime.js"] === "_managerOwner") delete owners["test-runtime.js"]`) {
		t.Fatalf("cleanup source does not compare and delete ownership:\n%s", cleanupSource)
	}
}

func TestMainWorldInitScriptSourceDoesNotTrackOwnership(t *testing.T) {
	manager := &BrowserManager{
		namespaces:    defaultNamespaceRegistrations(),
		runtimeFields: RuntimeFieldSet{ScriptLabel: "_managerOwner"},
	}
	script := InitScript{Name: "main.js", Namespace: NamespacePageMain, Exec: "void 0", Label: "main"}
	got := manager.initScriptSource(script, "bind", "exec")
	if strings.Contains(got, "__cdp_runtime_lifecycle") || got != script.Exec {
		t.Fatalf("main-world source was ownership wrapped:\n%s", got)
	}
}

func TestFrameTransportKeysAreIndependent(t *testing.T) {
	first, err := newRuntimeFieldSet()
	if err != nil {
		t.Fatal(err)
	}
	second, err := newRuntimeFieldSet()
	if err != nil {
		t.Fatal(err)
	}
	if len(first.FrameTransportKey) != 64 || first.FrameTransportKey == second.FrameTransportKey {
		t.Fatal("transport keys must be independent 256-bit keys")
	}
	source := renderCoreScript(`"__FRAME_TRANSPORT_KEY__"`, first)
	if source != `"`+first.FrameTransportKey+`"` {
		t.Fatalf("core transport configuration: %s", source)
	}
}
