package syncutil

import (
	"log/slog"
	"runtime/debug"
)

// Go starts fn in a goroutine and logs any panic with its stack.
func Go(fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("Recovered from panic", "error", r, "stack", string(debug.Stack()))
			}
		}()
		fn()
	}()
}
