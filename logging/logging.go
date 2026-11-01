// Package logging builds the server's logger. Every record goes to stderr, and once
// AttachDB has run, records at or above the database level also go to the ohc_logs
// table, so logs can be searched and cleaned up from the admin API instead of growing
// in a file.
package logging

import (
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
)

// shared is the logger Load built, so Basic and BasicDebug reach the same place.
// Until Load runs (in tests, say) it is a plain stdout logger.
var shared atomic.Pointer[slog.Logger]

// Load builds the server logger: stderr at LOG_LEVEL (JSON when LOG_TYPE=production,
// text otherwise), plus a database slot that AttachDB fills once the pool is ready.
// Loggers derived before then start writing to the database too.
func Load() *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLogLevel(os.Getenv("LOG_LEVEL"))}

	var stderr slog.Handler
	if os.Getenv("LOG_TYPE") == "production" {
		stderr = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		stderr = slog.NewTextHandler(os.Stderr, opts)
	}

	logger := slog.New(&teeHandler{primary: stderr, db: newDBHandler(&dbSlot)})
	shared.Store(logger)
	slog.SetDefault(logger)
	return logger
}

func parseLogLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		if s != "" {
			slog.Default().Warn("unrecognised log level, defaulting to info", "value", s)
		}
		return slog.LevelInfo
	}
}

func fallback() *slog.Logger {
	if l := shared.Load(); l != nil { return l }
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

// Basic returns the server logger, or a stdout logger before Load has run
func Basic() *slog.Logger { return fallback() }

// BasicDebug is Basic, kept for the callers that log at debug level. Whether their
// records show depends on the server's levels, like any other.
func BasicDebug() *slog.Logger { return fallback() }

// PrettyJSON returns v as indented JSON, for debug logging.
func PrettyJSON[T any](v T) string {
	data, _ := json.MarshalIndent(v, "", "  ")
	return string(data)
}
