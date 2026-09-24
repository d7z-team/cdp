package webassets

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// RuntimeSlot identifies a page-side runtime facade.
type RuntimeSlot string

const (
	// RuntimeFFI is the isolated core facade.
	RuntimeFFI RuntimeSlot = "ffi"
)

// JSArg is a serialized JavaScript expression used by runtime call builders.
type JSArg struct {
	source string
}

// JSLit encodes value as a JavaScript literal.
func JSLit(value any) JSArg {
	raw, err := json.Marshal(value)
	if err != nil {
		return JSArg{source: "null"}
	}
	return JSArg{source: string(raw)}
}

// JSRaw wraps an already validated JavaScript expression.
func JSRaw(expr string) JSArg {
	return JSArg{source: expr}
}

// RuntimeSlotExpr returns the global expression for slot.
func RuntimeSlotExpr(slot RuntimeSlot) string {
	validateRuntimeSlot(slot)
	switch slot {
	case RuntimeFFI:
		return "window.__cdp_ffi"
	default:
		panic(fmt.Sprintf("invalid CDP runtime slot %q", slot))
	}
}

// RuntimeMethodProbe builds a script that checks whether a runtime method exists.
func RuntimeMethodProbe(slot RuntimeSlot, method string) string {
	target := runtimeMethodTarget(slot, method)
	return fmt.Sprintf(`(() => {
	const slot = %s;
	const receiver = %s;
	const fn = %s;
	return typeof fn === "function";
})()`, target.slotExpr, target.receiverExpr, target.fnExpr)
}

// RuntimeMethodReadyProbe builds a script that checks runtime readiness.
func RuntimeMethodReadyProbe(slot RuntimeSlot) string {
	return "(" + RuntimeOptionalMethodCall(slot, "runtimeReady") + " === true)"
}

// RuntimeInstalledProbe builds a script that checks runtime lifecycle state.
func RuntimeInstalledProbe(slot RuntimeSlot) string {
	validateRuntimeSlot(slot)
	return fmt.Sprintf(`(() => {
	const state = window.__cdp_runtime_lifecycle?.[%s]?.state;
	const runtime = %s;
	if (state === "starting") {
		return typeof runtime?.runtimeReady === "function";
	}
	return state === "ready" && runtime?.runtimeReady?.() === true;
})()`, JSLit(string(slot)).source, RuntimeSlotExpr(slot))
}

// RuntimeOptionalMethodCall builds a call that returns undefined when the method is absent.
func RuntimeOptionalMethodCall(slot RuntimeSlot, method string, args ...JSArg) string {
	target := runtimeMethodTarget(slot, method)
	return fmt.Sprintf(`(() => {
	const slot = %s;
	const receiver = %s;
	const fn = %s;
	if (typeof fn !== "function") {
		return undefined;
	}
	return fn.call(receiver%s);
})()`, target.slotExpr, target.receiverExpr, target.fnExpr, runtimeArgsSource(args))
}

// RuntimeRequiredMethodCall builds a call that throws missingMessage when the method is absent.
func RuntimeRequiredMethodCall(slot RuntimeSlot, method string, missingMessage string, args ...JSArg) string {
	target := runtimeMethodTarget(slot, method)
	return fmt.Sprintf(`(() => {
	const slot = %s;
	const receiver = %s;
	const fn = %s;
	if (typeof fn !== "function") {
		throw new Error(%s);
	}
	return fn.call(receiver%s);
})()`, target.slotExpr, target.receiverExpr, target.fnExpr, JSLit(missingMessage).source, runtimeArgsSource(args))
}

type runtimeMethodRef struct {
	slotExpr     string
	receiverExpr string
	fnExpr       string
}

func runtimeMethodTarget(slot RuntimeSlot, method string) runtimeMethodRef {
	validateRuntimeSlot(slot)
	parts := splitRuntimeMethod(method)
	slotExpr := RuntimeSlotExpr(slot)
	receiverExpr := "slot"
	if len(parts) > 1 {
		receiverExpr = "slot?." + strings.Join(parts[:len(parts)-1], "?.")
	}
	return runtimeMethodRef{
		slotExpr:     slotExpr,
		receiverExpr: receiverExpr,
		fnExpr:       "receiver?." + parts[len(parts)-1],
	}
}

func runtimeArgsSource(args []JSArg) string {
	if len(args) == 0 {
		return ""
	}
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		parts = append(parts, arg.source)
	}
	return ", " + strings.Join(parts, ", ")
}

func validateRuntimeSlot(slot RuntimeSlot) {
	switch slot {
	case RuntimeFFI:
		return
	default:
		panic(fmt.Sprintf("invalid CDP runtime slot %q", slot))
	}
}

func splitRuntimeMethod(method string) []string {
	parts := strings.Split(method, ".")
	if len(parts) == 0 {
		panic("empty CDP runtime method path")
	}
	for _, part := range parts {
		if !isJSIdentifier(part) {
			panic(fmt.Sprintf("invalid CDP runtime method path %q", method))
		}
	}
	return parts
}

func isJSIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for i, r := range value {
		if i == 0 {
			if r != '_' && r != '$' && !unicode.IsLetter(r) {
				return false
			}
			continue
		}
		if r != '_' && r != '$' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
