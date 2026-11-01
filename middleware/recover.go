package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
)

// Recover turns a panic in a request into a 500 response, logged with its stack, rather
// than net/http dropping the connection. http.ErrAbortHandler is raised again, as it is
// how a handler deliberately aborts a response.
func Recover(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil { return }
				if rec == http.ErrAbortHandler { panic(rec) }

				log.Error("REQUEST_PANIC", "method", r.Method, "path", r.URL.Path, "panic", fmt.Sprint(rec), "stack", string(debug.Stack()))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"error":"internal server error"}`))
			}()
			next.ServeHTTP(w, r)
		})
	}
}
