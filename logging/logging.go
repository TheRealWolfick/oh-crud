package logging

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
)

func Load() *slog.Logger {
	logType := os.Getenv("LOG_TYPE")
	level := parseLogLevel(os.Getenv("LOG_LEVEL"))

	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	if logType == "production" {
		f, err := os.OpenFile("/opt/myapi/logs/log.json", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			handler = slog.NewJSONHandler(os.Stderr, opts)
			slog.New(handler).Error("failed to open log file, writing to stderr only", "error", err)
		} else {
			handler = slog.NewJSONHandler(io.MultiWriter(os.Stderr, f), opts)
		}
	} else {
		handler = slog.NewTextHandler(os.Stderr, opts)
	}

	return slog.New(handler)
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
			slog.Default().Warn("unrecognised LOG_LEVEL, defaulting to info", "value", s)
		}
		return slog.LevelInfo
	}
}

// Basic returns a default logger that writes to stdout.
func Basic() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

func BasicDebug() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// PrettyJSON returns v as indented JSON, for debug logging.
func PrettyJSON[T any](v T) string {
	data, _ := json.MarshalIndent(v, "", "  ")
	return string(data)
}
