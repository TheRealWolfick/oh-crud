package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
	"gopkg.in/yaml.v3"
	"lotusforge.au/api-server/handlers"
	"lotusforge.au/api-server/middleware"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/tools"
)

func main() {
	// Directories
	models_dir := "./config/base-models"

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

	// Create the queue
	qm := tools.NewQueue(pool, 5, logger)

	// Define datahandler end point types
	allow_all := map[string]bool{"ALL": true}
	disallow_delete := map[string]bool{"GET": true, "PUT": true, "POST": true, "POST-GROUP": true, "DELETE": false, "PUT-GROUP": true}
	get_only := map[string]bool{"GET": true}
	//get_post := map[string]bool{"GET": true, "POST": true}
	get_post_put := map[string]bool{"GET": true, "POST": true, "PUT": true}

	// Load the model files <testing>
	model_configs, err := os.ReadDir(models_dir)
	if err != nil {
		fmt.Println(err)
		return
	} else {
		for _, m_config := range model_configs {
			info, err := m_config.Info()
			if err != nil {
				fmt.Println(err)
				continue
			}
			if filepath.Ext(info.Name()) == ".yaml" {
				data := models.NewBaseModel()
				file, err := os.ReadFile(fmt.Sprint(models_dir, "/", m_config.Name()))
				err = yaml.Unmarshal(file, &data)
				if err != nil {
					println(err)
				}
				fmt.Print(tools.DereferencedString(data))
			}
		}
	}
	return

	// Make the handlers
	authMiddleware := middleware.RequireAuth(pool)
	userHandler := handlers.NewUserHandler(logger, log_level, pool)
	domainHandler := handlers.NewDataHandler[models.Domain](qm, "domains", "domain", allow_all, nil, nil, "")
	buildingHandler := handlers.NewDataHandler[models.Building](qm, "buildings", "building", allow_all, nil, nil, "")
	floorHandler := handlers.NewDataHandler[models.Floors](qm, "floors", "floor", disallow_delete, nil, nil, "")
	buildingFloorHandler := handlers.NewDataHandler[models.Building_Floor](qm, "building_floor_combo", "bfloor", allow_all, nil, nil, "")
	buildingFloorRoomHandler := handlers.NewDataHandler[models.Building_Floor_Room](qm, "building_floor_room_combo", "room", allow_all, nil, nil, "")
	departmentsHandler := handlers.NewDataHandler[models.Departments](qm, "departments", "department", allow_all, nil, nil, "")
	// conditionRatingsHandler := handlers.NewDataHandler[models.Condition_Ratings](qm, "condition_ratings", "condition/rating", allow_all, nil, nil, "")
	assetCategoriesHandler := handlers.NewDataHandler[models.Asset_Categories](qm, "asset_categories", "asset/category", allow_all, nil, nil, "")
	assetDataHandler := handlers.NewDataHandler[models.Asset_Data](qm, "asset_data", "asset/data", allow_all, nil, nil, "")
	unresolvedAssetDataHandler := handlers.NewDataHandler[models.Unresolved_Assets](qm, "items, jsonb_array_elements(items.failed_items)", "asset/unresolved", get_only, map[string]any{"value->>'rectified'": "false"}, models.UnresolvedAssets_CustomSelect(), "WITH items AS (SELECT DISTINCT event_log->'task'->'response'->'failed_items' as failed_items FROM events)")

	assetDiffHandler := handlers.NewDataHandler[models.Asset_Data](qm, "asset_data", "asset/diff", get_post_put, nil, nil, "")

	// Group handlers
	api_handlers := []handlers.DataHandlerInterface{
		domainHandler, buildingHandler, floorHandler, buildingFloorHandler, buildingFloorRoomHandler, departmentsHandler, 
		assetCategoriesHandler, assetDataHandler, unresolvedAssetDataHandler,
	}
	diff_handlers := []handlers.DataHandlerInterface{
		assetDiffHandler,
	}

	// Setup the server
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {w.Write([]byte("OK"))})

	mux.Handle("GET /user", authMiddleware(http.HandlerFunc(userHandler.GetUserInfo)))
	mux.Handle("PUT /user", authMiddleware(http.HandlerFunc(userHandler.UpdateUserInfo)))
	
	// Register the routes for each handler
	for _, handler := range api_handlers {
		handler.RegisterRoutes(mux, authMiddleware, qm)
	}
	for _, handler := range diff_handlers {
		handler.RegisterDiffRoutes(mux, authMiddleware, qm)
	}

	// Launch the server
	http.ListenAndServe(":8080", mux)

	// Close database connection
	defer pool.Close()
}
