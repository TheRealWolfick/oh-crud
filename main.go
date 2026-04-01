package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5/pgxpool"
	"lotusforge.au/api-server/handlers"
	"lotusforge.au/api-server/middleware"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/tools"
)

func main() {
	// Directories
	models_dir := "./config/base-models"
	special_models_dir := "./config/special-models"

	// Load the logger
	logger := tools.LoadLogger()

	// Connect to database
	pool, err := pgxpool.New(context.Background(), fmt.Sprintf("postgres://%s:%s@%s", os.Getenv("DATABASE_USER"), os.Getenv("DATABASE_PWD"), os.Getenv("DATABASE_URL")))
	if err != nil {
		if os.Getenv("DATABASE_URL") == "" {
			logger.Error("Database connection failed", "error", "Database variable not found")
		} else {
			logger.Error("Database connection failed", "database", os.Getenv("DATABASE_URL"), "error", err)
		}
		os.Exit(1)
	}
	logger.Info("Database connection made")

	// Create the queue for handling jobs
	qm := tools.NewQueue(pool, 5, logger)

	// Load config-driven models from base-models dir
	all_models := loadModelsFromDir(models_dir, logger)

	// Load config-driven models from special-models dir
	all_models = append(all_models, loadModelsFromDir(special_models_dir, logger)...)

	// Make the handlers
	authMiddleware := middleware.RequireAuth(pool)
	userHandler := handlers.NewUserHandler(logger, pool)

	// Setup the server
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("OK")) })

	mux.Handle("GET /user", authMiddleware(http.HandlerFunc(userHandler.GetUserInfo)))
	mux.Handle("PUT /user", authMiddleware(http.HandlerFunc(userHandler.UpdateUserInfo)))

	// Register config-driven routes
	for _, dm := range all_models {
		handlers.RegisterRoutes(&dm, mux, authMiddleware, qm)
	}

	// Launch the server
	http.ListenAndServe(":8080", mux)

	// Close database connection
	defer pool.Close()
}

func loadModelsFromDir(dir string, logger interface{ Warn(string, ...any); Error(string, ...any) }) []models.DataModel {
	result := make([]models.DataModel, 0)

	entries, err := os.ReadDir(dir)
	if err != nil {
		logger.Error("Error reading model config files", "dir", dir, "error", err)
		return result
	}

	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			logger.Warn("Error reading config file info", "error", err)
			continue
		}
		if filepath.Ext(info.Name()) != ".yaml" {
			continue
		}
		data, err := tools.LoadModel_YAML(fmt.Sprintf("%s/%s", dir, info.Name()))
		if err != nil {
			logger.Warn(fmt.Sprintf("Failed to load config file: %s", info.Name()), "error", err)
			continue
		}
		result = append(result, *data)
	}

	return result
}
