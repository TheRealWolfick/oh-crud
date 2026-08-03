package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"lotusforge.au/api-server/handlers"
	"lotusforge.au/api-server/middleware"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/monitors"
	"lotusforge.au/api-server/schematools"
	"lotusforge.au/api-server/tools"
)

func main() {
	// Directories
	default_models_dir := "./config/default"
	models_dir := "./config/base-models"
	// special_models_dir := "./config/special-models"
	functions_dir := "./config/functions"

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

	// Create the events handler
	evh := tools.NewEventManager(27, 10, pool)

	// Create the queue for handling jobs
	qm := tools.NewQueue(pool, 5, logger, evh)

	// Load the server config
	intial_server_conf, err := tools.LoadYAMLIntoModel[models.ServerConfig]("./config/server/server.yaml")
	if err != nil {
		logger.Error("Failed to load server configuration")
		os.Exit(1)
	}
	server_conf := models.NewSwappableServerConfig(intial_server_conf)
	logger.Debug(tools.DereferencedString(server_conf.Get()))


	// Load default models from default, base-model, and special-models dir
	all_models := loadModelsFromDir(default_models_dir, logger)
	all_models = append(all_models, loadModelsFromDir(models_dir, logger)...)
	// all_models = append(all_models, loadModelsFromDir(special_models_dir, logger)...)

	// Sync database schema for all loaded models.
	// Destructive changes are recorded in the gate and blocked until manually approved.
	gate := schematools.NewPendingApprovalGate()
	schematools.BootstrapModels(context.Background(), pool, all_models, logger, gate)

	// Make the handlers
	authMiddleware := middleware.RequireAuth(pool)

	// Setup the server
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("OK")) })

	// Register config-driven routes
	handlerRegister := tools.NewHandlerRegistry(mux)
	modelRegister := tools.NewModelRegistry()
	for _, dm := range all_models {
		modelRegister.Register(&dm)
		if dm.End_point == nil || *dm.End_point == "" {
			logger.Debug(fmt.Sprintf("Skipping end point for: %s", *dm.Name))
			continue
		}
		handlers.RegisterRoutes(&dm, handlerRegister, authMiddleware, qm, server_conf, evh, gate, modelRegister)
	}

	// Load and register declarative functions. Must happen after models are
	// registered because each function validates against its bound model.
	functionRegister := tools.NewFunctionRegistry()
	for _, fn := range tools.LoadFunctionsFromDir(functions_dir, modelRegister, logger) {
		functionRegister.Register(fn)
		handlers.RegisterFunctionRoutes(fn, modelRegister, handlerRegister, authMiddleware, qm, server_conf, evh)
	}

	// OpenAPI spec endpoint
	mux.Handle("GET /openapi.json", handlers.NewOpenAPIHandler(modelRegister))

	// Load the file watchers
	go monitors.ModelsMonitor(handlerRegister, modelRegister, authMiddleware, qm, gate, server_conf, evh)
	go monitors.FunctionsMonitor(handlerRegister, modelRegister, functionRegister, authMiddleware, qm, server_conf, evh)
	go monitors.ServerConfigMonitor(server_conf, logger)

	// Launch the server
	srv := &http.Server{
		Addr:        ":8080",
		Handler:     mux,
		IdleTimeout: 120 * time.Second,
		ReadTimeout: 30 * time.Second,
		WriteTimeout: 60 * time.Second,
	}
	srv.ListenAndServe()

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
		fp := fmt.Sprintf("%s/%s", dir, info.Name())
		data, err := tools.LoadYAMLIntoModel[models.DataModel](fp)
		data.Filepath = &fp
		result = append(result, *data)
		if err != nil {
			logger.Warn(fmt.Sprintf("Failed to load config file: %s", info.Name()), "error", err)
			continue
		}
		if err := tools.ValidateDataModel(*data); err != nil {
			logger.Warn(fmt.Sprintf("Config file failed validation: %s", info.Name()), "error", err)
			continue
		}
		tools.ProcessModelAdditionalFields(data)
	}

	return result
}
