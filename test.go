package main

import (
	"context"
	"io"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5"
)

func main() {
	// Create logger to write to both stderr and a logging file`
	writer_file, err := os.OpenFile("log.json", os.O_APPEND|os.O_CREATE, 644)
	writer_sderr, err := os.OpenFile(os.Stderr, os.O_APPEND)
	logger := slog.New( slog.NewJSONHandler(io.MultiWriter(writer_sderr, writer_file),nil) )

	conn, err := pgx.Connect(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		if os.Getenv("DATABASE_URL") == "" {
			logger.Error("Database connection failed", "error", "Database variable not found")
		} else {
			logger.Error("Database connection failed", "database", os.Getenv("DATABASE_URL"), "error", err)
		}
	}
}
