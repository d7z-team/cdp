package syncutil

import (
	"log/slog"
	"runtime/debug"
)

// Go starts fn in a goroutine and logs any panic with its stack.
// A nil logger falls back to slog.Default so recovered panics remain visible.
func Go(logger *slog.Logger, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				if logger == nil {
					logger = slog.Default()
				}
				logger.Error("goroutine panic recovered", "error", r, "stack", string(debug.Stack()))
			}
		}()
		fn()
	}()
}
