package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
	"lotusforge.au/api-server/handlers"
	"lotusforge.au/api-server/middleware"
)

func main() {
	// Create logger to write to both stderr and a logging file`
	// Create logger based on environment
	log_type := os.Getenv("LOG_TYPE")
	var logger *slog.Logger
	if log_type == "production" {
		writer_file, _ := os.OpenFile("/opt/myapi/logs/log.json", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		logger = slog.New( slog.NewJSONHandler(io.MultiWriter(os.Stderr, writer_file),nil) )
	} else {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}

	// Load env variables
	log_level, err := strconv.Atoi(os.Getenv("LOG_LEVEL"))
	if err != nil {
		logger.Error("Could not load logging level. Logging disabled", "error", err)
		log_level = 0
	}

	// Connect to database
	pool, err := pgxpool.New(context.Background(),  fmt.Sprintf("postgres://%s:%s@%s", os.Getenv("DATABASE_USER"), os.Getenv("DATABASE_PWD"), os.Getenv("DATABASE_URL")))
	if err != nil && log_level > 0 {
		if os.Getenv("DATABASE_URL") == "" {
			logger.Error("Database connection failed", "error", "Database variable not found")
		} else {
			logger.Error("Database connection failed", "database", os.Getenv("DATABASE_URL"), "error", err)
		}
		os.Exit(1)
	}
	if log_level > 0 {logger.Info("Database connection made")}

	// Make the handlers
	userHandler := handlers.NewUserHandler(logger, log_level, pool)
	authMiddleware := middleware.RequireAuth(pool)

	// Setup the server
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {w.Write([]byte("OK"))})

	mux.Handle("GET /user", authMiddleware(http.HandlerFunc(userHandler.GetUserInfo)))

	// Launch the server
	http.ListenAndServe(":8080", mux)

	// Close database connection
	defer pool.Close()
}
