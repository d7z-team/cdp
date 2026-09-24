package engine

import (
	"context"
	"errors"
	"log/slog"
)

// Logger returns the optional instance logger. It also supports partially
// initialized managers during shutdown and cleanup.
func (r *BrowserManager) Logger() *slog.Logger {
	if r == nil {
		return nil
	}
	return r.logger
}

func (r *BrowserManager) log(level slog.Level, message string, args ...any) {
	logger := r.Logger()
	if logger == nil {
		return
	}
	if level >= slog.LevelWarn {
		for i := 0; i+1 < len(args); i += 2 {
			key, ok := args[i].(string)
			if !ok || key != "error" {
				continue
			}
			err, ok := args[i+1].(error)
			if ok && (errors.Is(err, ErrBrowserClosed) || errors.Is(err, context.Canceled)) {
				return
			}
		}
	}
	logger.Log(context.Background(), level, message, args...)
}

func (p *Page) log(level slog.Level, message string, args ...any) {
	p.manager.log(level, message, args...)
}

func (c *CdpConn) log(level slog.Level, message string, args ...any) {
	if c.logger != nil {
		c.logger.Log(context.Background(), level, message, args...)
	}
}
