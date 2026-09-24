package cdp

import (
	"context"
	"testing"
)

func TestDiagnosticsValidation(t *testing.T) {
	for _, test := range []struct {
		input, want DiagnosticsMode
		valid       bool
	}{{"", DiagnosticsOff, true}, {DiagnosticsOff, DiagnosticsOff, true}, {DiagnosticsRuntime, DiagnosticsRuntime, true}, {"unknown", "", false}} {
		got, err := test.input.normalized()
		if (err == nil) != test.valid || got != test.want {
			t.Errorf("normalize %q = %q, %v", test.input, got, err)
		}
	}
	for _, connect := range []bool{false, true} {
		var err error
		if connect {
			_, err = Connect(context.Background(), "http://127.0.0.1:1", ConnectOptions{Diagnostics: "invalid"})
		} else {
			_, err = Launch(context.Background(), LaunchOptions{Diagnostics: "invalid"})
		}
		if err == nil {
			t.Fatal("invalid diagnostic mode accepted")
		}
	}
}
