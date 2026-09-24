package model

import "testing"

func TestLoadBrowserConfig(t *testing.T) {
	env := map[string]string{
		EnvE2EBrowser:    "1",
		EnvE2EChrome:     "/tmp/chrome",
		EnvE2EChromeArgs: "--headless=new,--lang=zh-CN",
	}
	cfg := LoadBrowserConfig(func(key string) (string, bool) {
		val, ok := env[key]
		return val, ok
	})

	if !cfg.Enabled || cfg.Executable != "/tmp/chrome" || !cfg.Headless {
		t.Fatalf("unexpected browser config: %+v", cfg)
	}
	if len(cfg.ExtraArgs) != 2 {
		t.Fatalf("unexpected extra args: %+v", cfg.ExtraArgs)
	}
}

func TestHeadlessDefaultsTrue(t *testing.T) {
	cfg := LoadBrowserConfig(func(string) (string, bool) { return "", false })
	if !cfg.Headless {
		t.Fatalf("headless should default to true, got %v", cfg.Headless)
	}
}

func TestHeadlessExplicitFalse(t *testing.T) {
	cfg := LoadBrowserConfig(func(key string) (string, bool) {
		if key == EnvE2EHeadless {
			return "0", true
		}
		return "", false
	})
	if cfg.Headless {
		t.Fatalf("headless should be false when set to 0, got %v", cfg.Headless)
	}
}
