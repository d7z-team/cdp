package runtime

import (
	"testing"

	"gopkg.d7z.net/cdp/e2e/harness/model"
)

func TestBuildBrowserConfig(t *testing.T) {
	cfg := BuildBrowserConfig(model.BrowserConfig{Executable: "/tmp/chrome", Headless: true, ExtraArgs: []string{"--lang=en-US"}}, "/tmp/profile")
	if cfg.ExecutablePath != "/tmp/chrome" || cfg.Headful || cfg.UserDataDir != "/tmp/profile" || cfg.WindowSize.Width != 1440 || len(cfg.Args) != 1 {
		t.Fatalf("config: %+v", cfg)
	}
	empty := BuildBrowserConfig(model.BrowserConfig{}, "")
	if empty.ExecutablePath != "" {
		t.Fatal("harness must leave browser discovery to library")
	}
}
