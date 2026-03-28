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

	// Load the logger
	logger := tools.LoadLogger()

	// Connect to database
	pool, err := pgxpool.New(context.Background(),  fmt.Sprintf("postgres://%s:%s@%s", os.Getenv("DATABASE_USER"), os.Getenv("DATABASE_PWD"), os.Getenv("DATABASE_URL")))
	if err != nil{
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

	// Define datahandler end point types
	allow_all := map[string]bool{"ALL": true}
	disallow_delete := map[string]bool{"GET": true, "PUT": true, "POST": true, "POST-GROUP": true, "DELETE": false, "PUT-GROUP": true}
	get_only := map[string]bool{"GET": true}
	//get_post := map[string]bool{"GET": true, "POST": true}
	get_post_put := map[string]bool{"GET": true, "POST": true, "PUT": true}

	// Load the model files <testing>
	base_models := make([]models.BaseModel, 0)
	base_model_configs, err := os.ReadDir(models_dir)
	if err != nil {
		logger.Error("Error reading model config files. Shutting down server!", "error", err)
		return
	} else {
		for _, config_file := range base_model_configs {
			info, err := config_file.Info()
			if err != nil {
				logger.Warn("Error reading the base model config file", "error", err)
				continue
			}
			if filepath.Ext(info.Name()) == ".yaml" {
				data, err := tools.LoadModel_YAML(fmt.Sprint(models_dir, "/", info.Name()))
				if err != nil {
					logger.Warn(fmt.Sprintf("Failed to load config file: %s", info.Name()), "error", err)
					continue
				} else {
					base_models = append(base_models, *data)
					fmt.Print(base_models)
				}
			}
		}
	}
	return

	// Make the handlers
	authMiddleware := middleware.RequireAuth(pool)
	userHandler := handlers.NewUserHandler(logger, pool)
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
