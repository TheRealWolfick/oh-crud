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
	writer_file, err := os.OpenFile("/opt/myapy/logs/log.json", os.O_APPEND|os.O_CREATE, 0644)
	logger := slog.New( slog.NewJSONHandler(io.MultiWriter(os.Stderr, writer_file),nil) )

	conn, err := pgx.Connect(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		if os.Getenv("DATABASE_URL") == "" {
			logger.Error("Database connection failed", "error", "Database variable not found")
		} else {
			logger.Error("Database connection failed", "database", os.Getenv("DATABASE_URL"), "error", err)
		}
	}

	// Close database connection
	defer conn.Close(context.Background())
}
