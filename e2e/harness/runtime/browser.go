package runtime

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"gopkg.d7z.net/cdp"
	"gopkg.d7z.net/cdp/e2e/harness/model"
)

type BrowserRuntime struct {
	Config     model.BrowserConfig
	APIBrowser *cdp.Browser
}

func StartBrowserRuntime(ctx context.Context, cfg model.BrowserConfig) (*BrowserRuntime, error) {
	if !cfg.Enabled {
		return nil, errors.New("browser runtime disabled")
	}
	if cfg.KeepUserDataDir && cfg.UserDataDir == "" {
		dir, err := os.MkdirTemp("", "cdp-e2e-keep-*")
		if err != nil {
			return nil, err
		}
		cfg.UserDataDir = dir
	}
	opts := BuildBrowserConfig(cfg, cfg.UserDataDir)
	b, err := cdp.Launch(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &BrowserRuntime{Config: cfg, APIBrowser: b}, nil
}
func BuildBrowserConfig(cfg model.BrowserConfig, dir string) cdp.LaunchOptions {
	size := cdp.WindowSize{Width: 1440, Height: 960}
	for _, arg := range cfg.ExtraArgs {
		if strings.HasPrefix(arg, "--window-size=") {
			size = cdp.WindowSize{}
		}
	}
	return cdp.LaunchOptions{Diagnostics: cdp.DiagnosticsMode(cfg.Diagnostics), ExecutablePath: cfg.Executable, UserDataDir: dir, Headful: !cfg.Headless, Args: append([]string(nil), cfg.ExtraArgs...), WindowSize: size, ActionMode: cdp.ActionFast, Timeouts: cdp.Timeouts{Action: 3 * time.Second, Read: 3 * time.Second}}
}
func (r *BrowserRuntime) Close() error {
	if r == nil {
		return nil
	}
	return r.APIBrowser.Close()
}
