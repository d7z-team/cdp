package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBase62Encode(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{name: "zero", data: []byte{0}, want: "0"},
		{name: "last digit", data: []byte{61}, want: "z"},
		{name: "carry", data: []byte{62}, want: "10"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := base62Encode(tt.data); got != tt.want {
				t.Fatalf("base62Encode(%v) = %q, want %q", tt.data, got, tt.want)
			}
		})
	}
}

func TestStableBindingID(t *testing.T) {
	dir := t.TempDir()
	goFile := filepath.Join(dir, "call.go")
	if err := os.WriteFile(goFile, []byte("package sample\ntype Call struct{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	first, err := stableBindingID(goFile)
	if err != nil {
		t.Fatal(err)
	}
	second, err := stableBindingID(goFile)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("stableBindingID changed without source changes: %q != %q", first, second)
	}
	if !strings.HasPrefix(first, "bind_") {
		t.Fatalf("stableBindingID = %q, want bind_ prefix", first)
	}

	if err := os.WriteFile(goFile, []byte("package sample\ntype Call struct{ Value string }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := stableBindingID(goFile)
	if err != nil {
		t.Fatal(err)
	}
	if changed == first {
		t.Fatalf("stableBindingID did not change after source changed: %q", changed)
	}
}

func TestDTOGeneratesNamedAliasesAndInterface(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/sample\n\ngo 1.24\n")
	writeFile(t, dir, "dto.go", `package sample

type StepAction string
type StepList []StepAction

type StepRequest struct {
	Action StepAction  `+"`json:\"action\"`"+`
	Steps  StepList    `+"`json:\"steps\"`"+`
	Extra  map[string]StepAction `+"`json:\"extra,omitempty\"`"+`
}
`)

	output := withWorkingDir(t, dir, func() string {
		state, err := newParserState(dir, "StepRequest", "")
		if err != nil {
			t.Fatal(err)
		}
		state.tsClassName = "TSRequest"
		spec := state.typeSpecs["StepRequest"]
		if err := state.ensureTSDecl("StepRequest", spec); err != nil {
			t.Fatal(err)
		}
		return string(state.renderDTO())
	})

	assertContains(t, output, "export type StepAction = string;")
	assertContains(t, output, "export type StepList = StepAction[];")
	assertContains(t, output, "export interface TSRequest {")
	assertContains(t, output, "action: StepAction;")
	assertContains(t, output, "steps: StepList;")
	assertContains(t, output, "extra?: Record<string, StepAction>;")
}

func TestDTOGeneratesCrossPackageAliases(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/sample\n\ngo 1.24\n")
	writeFile(t, filepath.Join(dir, "shared"), "shared.go", `package shared

type RemoteMode string
type RemoteModes []RemoteMode
`)
	writeFile(t, dir, "dto.go", `package sample

import "example.com/sample/shared"

type LocalMode = shared.RemoteMode

type Request struct {
	Mode  LocalMode          `+"`json:\"mode\"`"+`
	Modes shared.RemoteModes `+"`json:\"modes\"`"+`
}
`)

	output := withWorkingDir(t, dir, func() string {
		state, err := newParserState(dir, "Request", "")
		if err != nil {
			t.Fatal(err)
		}
		spec := state.typeSpecs["Request"]
		if err := state.ensureTSDecl("Request", spec); err != nil {
			t.Fatal(err)
		}
		return string(state.renderDTO())
	})

	assertContains(t, output, "export type RemoteMode = string;")
	assertContains(t, output, "export type RemoteModes = RemoteMode[];")
	assertContains(t, output, "export type LocalMode = RemoteMode;")
	assertContains(t, output, "mode: LocalMode;")
	assertContains(t, output, "modes: RemoteModes;")
}

func TestGoCallGenerationUsesNamedAliases(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/sample\n\ngo 1.24\n")
	writeFile(t, dir, "call.go", `package sample

type GoCallContext struct{}
type BrowserManager struct{}
type Page struct{}
type BindingContext struct {
	Page    *Page
	Manager *BrowserManager
}

type Call struct{}

type Mode string
type ModeList []Mode

type Request struct {
	Mode Mode `+"`json:\"mode\"`"+`
}

//gocall:name push
func (Call) Push(ctx GoCallContext, req Request, modes ModeList) (Mode, error) {
	return "", nil
}
`)

	type generated struct {
		goSrc string
		tsSrc string
	}
	output := withWorkingDir(t, dir, func() generated {
		state, err := loadPackage(dir, "Call", "Call")
		if err != nil {
			t.Fatal(err)
		}
		state.requestBindingName = "__bind"
		state.deliverySlotName = "__slot"
		state.goBindingType = "CallBinding"
		state.tsClassName = "CallClient"
		return generated{
			goSrc: string(state.renderGo()),
			tsSrc: string(state.renderTS()),
		}
	})

	assertContains(t, output.tsSrc, "export type Mode = string;")
	assertContains(t, output.tsSrc, "export type ModeList = Mode[];")
	assertContains(t, output.tsSrc, "export interface Request {")
	assertContains(t, output.tsSrc, "mode: Mode;")
	assertContains(t, output.tsSrc, "async push(req: Request, modes: ModeList): Promise<Mode> {")
	assertContains(t, output.tsSrc, "function setHiddenGlobal(name: string, value: unknown): void {")
	assertContains(t, output.tsSrc, "enumerable: false,")
	assertContains(t, output.tsSrc, "setHiddenGlobal(goCallDeliverySlotName, channel);")
	assertContains(t, output.goSrc, "var arg0 Request")
	assertContains(t, output.goSrc, "var arg1 ModeList")
	assertContains(t, output.goSrc, "handled, deliverErr := p.DeliverBindingResultInSessionContext(ctx.Context, ctx.SessionID, executionContextID, goCallDeliverySlotName, payload.SessionKey, string(marshal))")
	assertContains(t, output.goSrc, "return api.Push(GoCallContext{Context: ctx.Context, Page: p, Manager: manager, SessionID: ctx.SessionID, TargetID: ctx.TargetID, TargetType: ctx.TargetType, ExecutionContextID: ctx.ExecutionContextID}, arg0, arg1)")
}

func withWorkingDir[T any](t *testing.T, dir string, fn func() T) T {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(wd); err != nil {
			t.Fatalf("restore working dir: %v", err)
		}
	}()
	return fn()
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertContains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("output missing %q\n%s", want, got)
	}
}
