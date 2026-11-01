package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
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
	"lotusforge.au/api-server/safe"
	"lotusforge.au/api-server/schematools"
)

// Compile-time check that the event manager can be handed to the queue as its publisher
var _ queue.Publisher = (*events.EventManager)(nil)

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
	default_models := loadModelsFromDir(default_models_dir, false, logger)   // Server models
	base_models := loadModelsFromDir(models_dir, true, logger) 					// User models, in any folder below
	all_models := append(default_models, base_models...)

	// Sync database schema for all loaded models.
	// Destructive changes are recorded in the gate and blocked until manually approved.
	gate := schematools.NewPendingApprovalGate()

	// Tables are named <service>_<table-name>. Rename any still under their old bare name
	// first, or the sync below would create empty tables beside them.
	if err := schematools.MigrateToQualifiedNames(context.Background(), pool, all_models, logger); err != nil {
		logger.Error("Failed to rename tables to include their service", "error", err); os.Exit(1)
	}
	schematools.BootstrapModels(context.Background(), pool, all_models, logger, gate)


	// ---------------------------------------------------------------
	//                JWKS Store
	// ---------------------------------------------------------------
	if os.Getenv("OH_CRUD_SECRET") == "" { logger.Error("Must set the OH_CRUD_SECRET environment variable"); os.Exit(1) }
	if len([]byte(os.Getenv("OH_CRUD_SECRET"))) != 32 { logger.Error("OH_CRUD_SECRET must be 32 bytes", "len", len([]byte(os.Getenv("OH_CRUD_SECRET")))); os.Exit(1) }
	ks := auth.NewKeyStore(server_conf.Get().Issuer, "oh-crud", pool, []byte(os.Getenv("OH_CRUD_SECRET")))            // Create the keystore
	if err := ks.RefreshKeys(context.Background()); err != nil { logger.Error(err.Error()); os.Exit(1) }    // Load the keys


	// ---------------------------------------------------------------
	//                Middleware
	// ---------------------------------------------------------------
	oauthHandler := auth.AuthHandler(pool, server_conf, logger, ks)
	authMiddleware := handlers.ModelAuth(oauthHandler.ForModel)   // Bearer tokens, then each model's access rule


	// ---------------------------------------------------------------
	//                Event Manager
	// ---------------------------------------------------------------
	evh := events.NewEventManager(27, 10, pool, oauthHandler)


	// ---------------------------------------------------------------
	//                Queue Manager
	// ---------------------------------------------------------------
	qm := queue.NewQueue(pool, 5, logger, evh)                                       // evh satisfies queue.Publisher


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
		if err := modelRegister.Register(&dm); err != nil { logger.Error("Failed to load model", "data-model", *dm.Name, "error", err); continue }
		handlers.RegisterRoutes(&dm, nil, handlerRegister, authMiddleware, qm, server_conf, evh, gate, modelRegister)
	}

	// Check to ensure the required tables are available. Exit if any are false
	for _, table := range []string{"ohc_diffs", "ohc_events", "ohc_batch_numbers", "ohc_signing_keys", "ohc_audit_events"} {
		_, found := modelRegister.ByTableName(table)
		if !found { logger.Error("missing required table", "table", table); os.Exit(1) }
	}

	
	// ---------------------------------------------------------------
	//                Server - User tables
	// ---------------------------------------------------------------
	diff_model, _ := modelRegister.ByTableName("ohc_diffs")
	for _, dm := range base_models {
		if err := modelRegister.Register(&dm); err != nil { logger.Error("Failed to load model", "data-model", *dm.Name, "error", err); continue }
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
	mux.Handle("GET /.well-known/jwks.json", handlers.JWKSHandler(ks))
	mux.Handle("GET /.well-known/openid-configuration", oauthHandler.OpenIDConfig())
	mux.Handle("POST /oauth/token", oauthHandler.TokenEndpoint())


	// ---------------------------------------------------------------
	//                File Watchers (asycronous)
	// ---------------------------------------------------------------
	safe.Go(logger, "models monitor", func() { monitors.ModelsMonitor(handlerRegister, modelRegister, authMiddleware, qm, gate, server_conf, evh) })
	safe.Go(logger, "functions monitor", func() { monitors.FunctionsMonitor(handlerRegister, modelRegister, functionRegister, authMiddleware, qm, server_conf, evh) })
	safe.Go(logger, "server config monitor", func() { monitors.ServerConfigMonitor(server_conf, logger) })


	// ---------------------------------------------------------------
	//                Custom SQL Functions
	// ---------------------------------------------------------------
	// !! Future Improvement: Make them Declaritive and with a watcher !!
	
	// Every .sql file in config/server, in name order: the batch number generator, the
	// audit log's append-only trigger. Each is safe to run again on every start.
	sql_files, _ := filepath.Glob("./config/server/*.sql")
	sort.Strings(sql_files)
	for _, f := range sql_files {
		body, err := os.ReadFile(f)
		if err != nil { logger.Error("Failed to read SQL file", "file", f, "error", err); continue }
		if _, err := qm.Db.Exec(context.Background(), string(body)); err != nil {
			logger.Error("Failed to run SQL file", "file", f, "error", err)
		}
	}


	// ---------------------------------------------------------------
	//                Server Launch
	// ---------------------------------------------------------------
	srv := &http.Server{
		Addr:        ":8080",
		Handler:     middleware.Recover(logger)(mux),
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

func loadModelsFromDir(dir string, recursive bool, logger interface{ Warn(string, ...any); Error(string, ...any) }) []models.DataModel {
	result := make([]models.DataModel, 0)

	files, err := datamodel.YAMLFiles(dir, recursive)
	if err != nil {
		logger.Error("Error reading model config files", "dir", dir, "error", err)
		return result
	}

	for _, fp := range files {
		data, err := datamodel.LoadYAMLIntoModel[models.DataModel](fp)
		if err != nil {
			logger.Warn(fmt.Sprintf("Failed to load config file: %s", fp), "error", err)
			continue
		}
		data.Filepath = &fp
		if err := datamodel.ValidateDataModel(*data); err != nil {
			logger.Warn(fmt.Sprintf("Config file failed validation: %s", fp), "error", err)
			continue
		}
		// Only Oh CRUD's own models may use the system service
		if err := datamodel.ValidateModelOwner(*data); err != nil {
			logger.Warn(fmt.Sprintf("Config file failed validation: %s", fp), "error", err)
			continue
		}
		datamodel.ProcessModelAdditionalFields(data)
		result = append(result, *data)
	}

	return result
}
