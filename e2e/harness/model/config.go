// Package model defines shared E2E harness configuration.
package model

// BrowserConfig controls the browser process used by E2E scenarios.
type BrowserConfig struct {
	Diagnostics     string
	Enabled         bool
	Executable      string
	Headless        bool
	ExtraArgs       []string
	KeepUserDataDir bool
	UserDataDir     string
}
