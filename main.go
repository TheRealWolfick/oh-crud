package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"lotusforge.au/api-server/auth"
	"lotusforge.au/api-server/datamodel"
	"lotusforge.au/api-server/events"
	"lotusforge.au/api-server/handlers"
	"lotusforge.au/api-server/logging"
	"lotusforge.au/api-server/middleware"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/monitors"
	"lotusforge.au/api-server/queue"
	"lotusforge.au/api-server/registry"
	"lotusforge.au/api-server/schematools"
)

func main() {
	// ---------------------------------------------------------------
	//                Directories
	// ---------------------------------------------------------------
	default_models_dir := "./config/default"  // Server tables
	models_dir := "./config/base-models"      // User defined tables
	functions_dir := "./config/functions"     // Declared functions


	// ---------------------------------------------------------------
	//                Logger
	// ---------------------------------------------------------------
	logger := logging.Load()


	// ---------------------------------------------------------------
	//                Database connection
	// ---------------------------------------------------------------
	pool, err := pgxpool.New(context.Background(), fmt.Sprintf("postgres://%s:%s@%s", os.Getenv("DATABASE_USER"), os.Getenv("DATABASE_PWD"), os.Getenv("DATABASE_URL")))
	if err != nil {
		if os.Getenv("DATABASE_URL") == "" {
			logger.Error("Database connection failed", "error", "DATABASE_URL environment variable missing")
		} else {
			logger.Error("Database connection failed", "database", os.Getenv("DATABASE_URL"), "error", err)
		}
		os.Exit(1)
	}
	logger.Info("Database connection made")


	// ---------------------------------------------------------------
	//                Event Manager
	// ---------------------------------------------------------------
	evh := events.NewEventManager(27, 10, pool)


	// ---------------------------------------------------------------
	//                Queue Manager
	// ---------------------------------------------------------------
	qm := queue.NewQueue(pool, 5, logger, evh)


	// ---------------------------------------------------------------
	//                JWKS Store
	// ---------------------------------------------------------------
	ks := auth.NewKeyStore("https://ohcrud.internal", "oh-crud", pool)            // Create the keystore
	if err := ks.RefreshKeys(); err != nil { logger.Error(err.Error()); os.Exit(1) }    // Load the keys


	// ---------------------------------------------------------------
	//                Server Config Loading
	// ---------------------------------------------------------------
	intial_server_conf, err := datamodel.LoadYAMLIntoModel[models.ServerConfig]("./config/server/server.yaml")
	if err != nil { logger.Error("Failed to load server configuration"); os.Exit(1) }
	// Load the server config into a swappable handler
	server_conf := models.NewSwappableServerConfig(intial_server_conf)
	logger.Debug(logging.PrettyJSON(server_conf.Get()))


	// ---------------------------------------------------------------
	//                Data Models
	// ---------------------------------------------------------------
	default_models := loadModelsFromDir(default_models_dir, logger)   // Server models
	base_models := loadModelsFromDir(models_dir, logger) 							// User models
	all_models := append(default_models, base_models...)

	// Sync database schema for all loaded models.
	// Destructive changes are recorded in the gate and blocked until manually approved.
	gate := schematools.NewPendingApprovalGate()
	schematools.BootstrapModels(context.Background(), pool, all_models, logger, gate)


	// ---------------------------------------------------------------
	//                Middleware
	// ---------------------------------------------------------------
	authMiddleware := middleware.RequireAuth(pool)


	// ---------------------------------------------------------------
	//                Server - Setup
	// ---------------------------------------------------------------
	mux := http.NewServeMux()

	// Create all the registers
	handlerRegister := registry.NewHandlerRegistry(mux)  // End points
	modelRegister := registry.NewModelRegistry()         // Data models
	functionRegister := registry.NewFunctionRegistry()   // Declared Functions


	// ---------------------------------------------------------------
	//                Server - System tables
	// ---------------------------------------------------------------
	for _, dm := range default_models {
		modelRegister.Register(&dm)
		if dm.End_point == nil || *dm.End_point == "" { logger.Debug(fmt.Sprintf("Skipping end point for: %s", *dm.Name)); continue }
		
		handlers.RegisterRoutes(&dm, nil, handlerRegister, authMiddleware, qm, server_conf, evh, gate, modelRegister)
	}

	// Check to ensure the required tables are available. Exit if any are false
	for _, table := range []string{"diffs", "events", "batch_numbers", "signing_keys"} {
		_, found := modelRegister.ByTableName(table)
		if !found { logger.Error("missing required table", "table", table); os.Exit(1) }
	}

	
	// ---------------------------------------------------------------
	//                Server - User tables
	// ---------------------------------------------------------------
	diff_model, _ := modelRegister.ByTableName("diffs")
	for _, dm := range base_models {
		modelRegister.Register(&dm)
		if dm.End_point == nil || *dm.End_point == "" { logger.Debug(fmt.Sprintf("Skipping end point for: %s", *dm.Name)); continue }
		handlers.RegisterRoutes(&dm, diff_model, handlerRegister, authMiddleware, qm, server_conf, evh, gate, modelRegister)
	}


	// ---------------------------------------------------------------
	//                Server - Declared Functions
	// ---------------------------------------------------------------
	// Must happen after the models are registered as each function validates against its declared tables
	for _, fn := range registry.LoadFunctionsFromDir(functions_dir, modelRegister, logger) {
		functionRegister.Register(fn)
		handlers.RegisterFunctionRoutes(fn, modelRegister, handlerRegister, authMiddleware, qm, server_conf, evh)
	}


	// ---------------------------------------------------------------
	//                System End Points
	// ---------------------------------------------------------------
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("OK")) })
	mux.Handle("GET /openapi.json", handlers.NewOpenAPIHandler(modelRegister, functionRegister))


	// ---------------------------------------------------------------
	//                File Watchers (asycronous)
	// ---------------------------------------------------------------
	go monitors.ModelsMonitor(handlerRegister, modelRegister, authMiddleware, qm, gate, server_conf, evh)
	go monitors.FunctionsMonitor(handlerRegister, modelRegister, functionRegister, authMiddleware, qm, server_conf, evh)
	go monitors.ServerConfigMonitor(server_conf, logger)


	// ---------------------------------------------------------------
	//                Custom SQL Functions
	// ---------------------------------------------------------------
	// !! Future Improvement: Make them Declaritive and with a watcher !!
	
	// Batch number generator
	batch_num_func, err := os.ReadFile("./config/server/generate-batch-numbers.sql")
	if err != nil { logger.Error("Failed to load function file to generate batch numbers") } else {
		if _, err := qm.Db.Exec(context.Background(), string(batch_num_func)); err != nil {
			logger.Error("Failed to execute function file to generate batch numbers")
		}
	}


	// ---------------------------------------------------------------
	//                Server Launch
	// ---------------------------------------------------------------
	srv := &http.Server{
		Addr:        ":8080",
		Handler:     mux,
		IdleTimeout: 120 * time.Second,
		ReadTimeout: 30 * time.Second,
		WriteTimeout: 60 * time.Second,
	}
	srv.ListenAndServe()

	// ---------------------------------------------------------------
	//                Cleanup
	// ---------------------------------------------------------------
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
		data, err := datamodel.LoadYAMLIntoModel[models.DataModel](fp)
		if err != nil {
			logger.Warn(fmt.Sprintf("Failed to load config file: %s", info.Name()), "error", err)
			continue
		}
		data.Filepath = &fp
		result = append(result, *data)
		if err := datamodel.ValidateDataModel(*data); err != nil {
			logger.Warn(fmt.Sprintf("Config file failed validation: %s", info.Name()), "error", err)
			continue
		}
		datamodel.ProcessModelAdditionalFields(data)
	}

	return result
}
