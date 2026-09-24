package cdp

import (
	"fmt"
	"log/slog"
	"time"
)

type Timeouts struct {
	Connect, Action, Read, Navigation, Screenshot, Download, Print, Shutdown time.Duration
}

func (t Timeouts) normalized() (Timeouts, error) {
	values := []*time.Duration{&t.Connect, &t.Action, &t.Read, &t.Navigation, &t.Screenshot, &t.Download, &t.Print, &t.Shutdown}
	defaults := []time.Duration{20 * time.Second, 15 * time.Second, 15 * time.Second, 30 * time.Second, 30 * time.Second, time.Hour, time.Hour, 5 * time.Second}
	for i, p := range values {
		if *p < 0 {
			return t, fmt.Errorf("timeout must not be negative")
		}
		if *p == 0 {
			*p = defaults[i]
		}
	}
	return t, nil
}

type ActionMode string

const (
	ActionStrict ActionMode = "strict"
	ActionFast   ActionMode = "fast"
)

// DiagnosticsMode selects automatic Runtime event collection.
type DiagnosticsMode string

const (
	// DiagnosticsOff preserves native console behavior without automatic Runtime events.
	DiagnosticsOff DiagnosticsMode = "off"
	// DiagnosticsRuntime enables console, exception and context lifecycle events.
	DiagnosticsRuntime DiagnosticsMode = "runtime"
)

func (m DiagnosticsMode) normalized() (DiagnosticsMode, error) {
	if m == "" {
		return DiagnosticsOff, nil
	}
	if m != DiagnosticsOff && m != DiagnosticsRuntime {
		return "", fmt.Errorf("invalid diagnostics mode %q", m)
	}
	return m, nil
}

type WindowSize struct{ Width, Height int }
type LaunchOptions struct {
	Diagnostics                            DiagnosticsMode
	ExecutablePath, UserDataDir, UserAgent string
	Headful                                bool
	WindowSize                             WindowSize
	Args                                   []string
	CertificateFingerprints                []string
	HostRules                              map[string]string
	Extensions                             []ExtensionHook
	Timeouts                               Timeouts
	ActionMode                             ActionMode
	Initialize                             func(*Initializer) error
	Logger                                 *slog.Logger
}
type ConnectOptions struct {
	Diagnostics DiagnosticsMode
	Timeouts    Timeouts
	ActionMode  ActionMode
	Initialize  func(*Initializer) error
	Logger      *slog.Logger
}

// ExtensionManifest is a JSON-compatible Chrome extension manifest.
type ExtensionManifest map[string]any
type ExtensionHook func(manifest ExtensionManifest, directory string) error
