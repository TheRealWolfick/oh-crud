// Package safe keeps a panic in a background goroutine from crashing the server.
// net/http recovers panics in request handlers, but any goroutine the server starts
// itself takes the whole process down if it panics.
package safe

import (
	"fmt"
	"log/slog"
	"runtime/debug"
)

// Recover stops a panic and logs it with its stack. Defer it directly at the top of a
// goroutine. onPanic, if given, runs after the panic is logged so the goroutine can
// clean up or report the failure. A panic inside onPanic is logged, not raised.
func Recover(log *slog.Logger, name string, onPanic func(err error)) {
	r := recover()
	if r == nil { return }
	if log == nil { log = slog.Default() }

	log.Error("GOROUTINE_PANIC", "goroutine", name, "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
	if onPanic == nil { return }

	defer func() {
		if r := recover(); r != nil {
			log.Error("GOROUTINE_PANIC", "goroutine", name+" (recovery)", "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
		}
	}()
	onPanic(fmt.Errorf("panic in %s: %v", name, r))
}

// Go runs fn in a new goroutine that recovers from panics
func Go(log *slog.Logger, name string, fn func()) {
	go func() {
		defer Recover(log, name, nil)
		fn()
	}()
}
