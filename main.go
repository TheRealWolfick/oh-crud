package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"

	"github.com/jackc/pgx/v5"
)

func main() {
	// Create logger to write to both stderr and a logging file`
	writer_file, err := os.OpenFile("/opt/myapi/logs/log.json", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	logger := slog.New( slog.NewJSONHandler(io.MultiWriter(os.Stderr, writer_file),nil) )

	// Load env variables
	log_level, err := strconv.Atoi(os.Getenv("LOG_LEVEL"))
	if err != nil {
		logger.Error("Could not load logging level. Logging disabled", "error", err)
		log_level = 0
	}

	// Connect to database
	conn, err := pgx.Connect(context.Background(),  fmt.Sprintf("postgres://%s:%s@%s", os.Getenv("DATABASE_USER"), os.Getenv("DATABASE_PWD"), os.Getenv("DATABASE_URL")))
	if err != nil && log_level > 0 {
		if os.Getenv("DATABASE_URL") == "" {
			logger.Error("Database connection failed", "error", "Database variable not found")
		} else {
			logger.Error("Database connection failed", "database", os.Getenv("DATABASE_URL"), "error", err)
		}
		os.Exit(1)
	}
	if log_level > 0 {logger.Info("Database connection made")}

	// Launch server
	

	// Close database connection
	defer conn.Close(context.Background())
}
