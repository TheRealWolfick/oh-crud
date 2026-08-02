package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"golang.org/x/sync/errgroup"
	"lotusforge.au/api-server/middleware"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/schematools"
	"lotusforge.au/api-server/tools"
)

func RegisterRoutes(
	cfg *models.DataModel, 
	handlerRegistry *tools.HandlerRegistry, 
	auth func(http.Handler) http.Handler, 
	qm *tools.QueueManager, 
	server_conf *models.SwappableServerConfig,
	evh *tools.EventManager,
	gate *schematools.PendingApprovalGate,
) {
	var err error
	qm.Logger.Debug("Dynamic end point generating", "data-model", *cfg.Name)

	// Perform error check OPTIONS handler and soft cancel on error
	err = handlerRegistry.Register(fmt.Sprintf("OPTIONS /%s", *cfg.End_point), middleware.Cors(cfg, server_conf)(emptyResponse()), *cfg.Version)
	if err != nil {
		qm.Logger.Error("Failed to load model", "error", err)
		return
	}

	// Standard set of handlers
	handlerRegistry.Register(fmt.Sprintf("GET /%s", *cfg.End_point), middleware.Cors(cfg, server_conf)(auth(handleGet(cfg, qm, server_conf))), *cfg.Version)
	handlerRegistry.Register(fmt.Sprintf("PUT /%s", *cfg.End_point), middleware.Cors(cfg, server_conf)(auth(handleUpdate(cfg, qm, server_conf))), *cfg.Version)
	handlerRegistry.Register(fmt.Sprintf("PUT /%s/group", *cfg.End_point), middleware.Cors(cfg, server_conf)(auth(handleUpdate_Group(cfg, qm, server_conf))), *cfg.Version)
	handlerRegistry.Register(fmt.Sprintf("POST /%s", *cfg.End_point), middleware.Cors(cfg, server_conf)(auth(handleAddNew(cfg, qm, server_conf))), *cfg.Version)
	handlerRegistry.Register(fmt.Sprintf("POST /%s/group", *cfg.End_point), middleware.Cors(cfg, server_conf)(auth(handleAddNew_Group(cfg, qm, server_conf))), *cfg.Version)
	handlerRegistry.Register(fmt.Sprintf("DELETE /%s", *cfg.End_point), middleware.Cors(cfg, server_conf)(auth(handleDelete(cfg, qm, server_conf))), *cfg.Version)
	handlerRegistry.Register(fmt.Sprintf("DELETE /%s/group", *cfg.End_point), middleware.Cors(cfg, server_conf)(auth(handleDelete_Group(cfg, qm, server_conf))), *cfg.Version)

	// Handle diff routes
	if cfg.Allow_diff != nil && *cfg.Allow_diff {
		handlerRegistry.Register(fmt.Sprintf("GET /%s/diff", *cfg.End_point), auth(dynamicGetDiff(cfg, qm, server_conf.Get())), *cfg.Version)
		handlerRegistry.Register(fmt.Sprintf("POST /%s/diff", *cfg.End_point), auth(dynamicCreateDiff(cfg, qm, server_conf.Get())), *cfg.Version)
		handlerRegistry.Register(fmt.Sprintf("PUT /%s/diff", *cfg.End_point), auth(dynamicActionDiff(cfg, qm, server_conf.Get())), *cfg.Version)
	}

	// Built-in functions live under /fn/{function}. Today this dispatches to
	// the aggregate handler; user-defined functions are registered separately.
	handlerRegistry.Register(fmt.Sprintf("GET /%s/fn/{function}", *cfg.End_point), middleware.Cors(cfg, server_conf)(auth(handleGet(cfg, qm, server_conf))), *cfg.Version)

	// Built in admin panels for this end point
	handlerRegistry.Register(fmt.Sprintf("GET /%s/admin/pending", *cfg.End_point), middleware.CorsAdmin(cfg, server_conf)(auth(handleTableApprovals(qm,server_conf))), *cfg.Version)

	// History route — only when the model opts in via track-history.
	// Path shape mirrors /fn/{function} so a literal segment sits at the same
	// position, avoiding ambiguity with /{key}/history vs /fn/{function}.
	if cfg.Track_history != nil && *cfg.Track_history {
		handlerRegistry.Register(
			fmt.Sprintf("GET /%s/history/{key}", *cfg.End_point),
			middleware.Cors(cfg, server_conf)(auth(handleGetHistory(cfg, qm, server_conf))),
			*cfg.Version,
		)
	}

	// Enable the table for websockets and register end point
	evh.EnableTopic(fmt.Sprintf("table:%s", *cfg.End_point), cfg.Webhooks)
	handlerRegistry.Register(fmt.Sprintf("GET /ws/%s", *cfg.End_point), middleware.Cors(cfg, server_conf)(auth(handleWebsocket(cfg, qm, server_conf, fmt.Sprintf("table:%s", *cfg.End_point), evh))), *cfg.Version)
}

func handleGet(cfg *models.DataModel, qm *tools.QueueManager, svr_cfg *models.SwappableServerConfig) http.HandlerFunc {
	if cfg.End_points_allowed != nil && cfg.End_points_allowed.GET != nil {
		return getResource(qm, cfg, svr_cfg.Get())
	}
	return notAllowed(cfg.End_points_allowed)
}

func handleGetHistory(cfg *models.DataModel, qm *tools.QueueManager, svr_cfg *models.SwappableServerConfig) http.HandlerFunc {
	if cfg.End_points_allowed != nil && cfg.End_points_allowed.GET != nil {
		return getResourceHistory(qm, cfg, svr_cfg.Get())
	}
	return notAllowed(cfg.End_points_allowed)
}

func handleAddNew(cfg *models.DataModel, qm *tools.QueueManager, svr_cfg *models.SwappableServerConfig) http.HandlerFunc {
	if cfg.End_points_allowed != nil && cfg.End_points_allowed.POST != nil {
		return addNewResource(qm, cfg, svr_cfg.Get())
	}
	return notAllowed(cfg.End_points_allowed)
}

func handleAddNew_Group(cfg *models.DataModel, qm *tools.QueueManager, svr_cfg *models.SwappableServerConfig) http.HandlerFunc {
	if cfg.End_points_allowed != nil && cfg.End_points_allowed.POST_GROUP != nil {
		return addNewResources_Group(qm, cfg, svr_cfg.Get())
	}
	return notAllowed(cfg.End_points_allowed)
}

func handleUpdate(cfg *models.DataModel, qm *tools.QueueManager, svr_cfg *models.SwappableServerConfig) http.HandlerFunc {
	if cfg.End_points_allowed != nil && cfg.End_points_allowed.PUT != nil {
		return updateResource(qm, cfg, svr_cfg.Get())
	}
	return notAllowed(cfg.End_points_allowed)
}

func handleUpdate_Group(cfg *models.DataModel, qm *tools.QueueManager, svr_cfg *models.SwappableServerConfig) http.HandlerFunc {
	if cfg.End_points_allowed != nil && cfg.End_points_allowed.PUT_GROUP != nil {
		return updateResource_Group(qm, cfg, svr_cfg.Get())
	}
	return notAllowed(cfg.End_points_allowed)
}

func handleDelete(cfg *models.DataModel, qm *tools.QueueManager, svr_cfg *models.SwappableServerConfig) http.HandlerFunc {
	if cfg.End_points_allowed != nil && cfg.End_points_allowed.DELETE != nil {
		return deleteResource(qm, cfg, svr_cfg.Get())
	}
	return notAllowed(cfg.End_points_allowed)
}

func handleDelete_Group(cfg *models.DataModel, qm *tools.QueueManager, svr_cfg *models.SwappableServerConfig) http.HandlerFunc {
	if cfg.End_points_allowed != nil && cfg.End_points_allowed.DELETE_GROUP != nil {
		return deleteResource_Group(qm, cfg, svr_cfg.Get())
	}
	return notAllowed(cfg.End_points_allowed)
}

func handleWebsocket(cfg *models.DataModel, qm *tools.QueueManager, svr_cfg *models.SwappableServerConfig, topic string, evh *tools.EventManager) http.HandlerFunc {
	if cfg.End_points_allowed != nil && cfg.End_points_allowed.GET != nil {
		return websocketRegister(topic, cfg, svr_cfg.Get(), qm.Logger, evh)
	}
	return notAllowed(cfg.End_points_allowed)
}


func addNewResource(
	qm *tools.QueueManager,
	cfg *models.DataModel,
	svr_cfg *models.ServerConfig,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		task_type := "Add Resource"
		function := "create"
		user_key := middleware.Contextkey("user")
		req_ip := tools.GetIP(r)
		req_id, err := tools.Generate32CharString()
		req_username := r.Context().Value(user_key).(*models.User).Username
		note := r.Header.Get("X-User-Note")
		log := qm.Logger.With("user", req_username, "IP", req_ip, "function", function, "task_type", task_type, "end_point", *cfg.End_point, "table", *cfg.Table_name, "request_id", req_id)
		ctx := middleware.SetLogger(r.Context(), log)

		log.Info("REQUEST_RECEIVED")

		// Response intialization
		response := map[string]any{"task_type": task_type}
		w.Header().Set("Content-Type", "application/json")

		// Check that a user is allowed to inteface with this command
		if !middleware.CheckUserHasAllowedRole(ctx, cfg.End_points_allowed.POST, svr_cfg) {
			log.Warn("REQUEST_UNAUTHORISED", "error", "user role does not have permission to access this end point")
			http.Error(w, "User role does not have access to this end point", http.StatusUnauthorized)
			return
		}

		// Read incoming data
		var raw map[string]any
		err = json.NewDecoder(r.Body).Decode(&raw)
		if err != nil {
			log.Error("REQUEST_ERROR", "error", err)
			http.Error(w, fmt.Sprintf("Could not decode body. Error: %v", err), http.StatusInternalServerError)
			return
		}
		if len(raw) == 0 {
			log.Error("REQUEST_ERROR", "error", "no valid json supplied")
			http.Error(w, "No valid json supplied", http.StatusBadRequest)
			return
		}

		log.Debug("Decoding and coercing raw data", "data", fmt.Sprint(raw))
		valid_resources, invalid_resources := tools.Validate_Map_AgainstConfig(cfg, raw, false, true)
		if len(valid_resources) < 1 {
			log.Warn("Request with no valid resources")
			response["successful_submission"] = false
			response["rows_received"] = 1
			response["rows_valid"] = len(valid_resources)
			response["rows_invalid"] = len(invalid_resources)
			response["invalid"] = invalid_resources
			w.WriteHeader(http.StatusBadRequest)

			json.NewEncoder(w).Encode(response)
			return
		}

		// Extract context and queue action
		ctx_preserve := context.WithoutCancel(middleware.StartTask(ctx, function, cfg))
		task_id, err := qm.QueueFunction(ctx_preserve, tools.SingleInsert(ctx_preserve, qm.Db, cfg, valid_resources[0]), note)

		if err != nil {
			log.Error("TASK_ERROR", "error", "could not create task", "resource", raw, "error", err)
			http.Error(w, fmt.Sprintf("Error creating create task\nError: %v", err), http.StatusInternalServerError)
			return
		}

		// Response
		response["task_id"] = task_id
		response["message"] = "successful submission of task"
		json.NewEncoder(w).Encode(response)
	}
}

// Add multiple resources via the default path
func addNewResources_Group(
	qm *tools.QueueManager,
	cfg *models.DataModel,
	svr_cfg *models.ServerConfig,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		task_type := "Add Bulk Resources"
		function := "create"
		user_key := middleware.Contextkey("user")
		req_ip := tools.GetIP(r)
		req_id, err := tools.Generate32CharString()
		req_username := r.Context().Value(user_key).(*models.User).Username
		note := r.Header.Get("X-User-Note")
		log := qm.Logger.With("user", req_username, "IP", req_ip, "function", function, "task_type", task_type, "end_point", *cfg.End_point, "table", *cfg.Table_name, "request_id", req_id)
		ctx := middleware.SetLogger(r.Context(), log)

		log.Info("REQUEST_RECEIVED")

		// Response intialization
		response := map[string]any{"task_type": task_type}
		w.Header().Set("Content-Type", "application/json")

		// Check that a user is allowed to inteface with this command
		if !middleware.CheckUserHasAllowedRole(ctx, cfg.End_points_allowed.POST_GROUP, svr_cfg) {
			log.Warn("REQUEST_UNAUTHORISED", "error", "user role does not have permission to access this end point")
			http.Error(w, "User role does not have access to this end point", http.StatusUnauthorized)
			return
		}

		var raw []map[string]any
		err = json.NewDecoder(r.Body).Decode(&raw)

		// Validation and errors
		if err != nil {
			log.Error("REQUEST_ERROR", "error", err)
			http.Error(w, fmt.Sprintf("Error decoding body: %v", err), http.StatusInternalServerError)
			return
		}

		log.Debug("Decoding and coercing raw data", "data", fmt.Sprint(raw))
		valid_resources, invalid_resources := tools.Validate_SliceOfMaps_AgainstConfig(cfg, raw, false, true)
		if len(valid_resources) < 1 {
			log.Warn("Request with no valid resources")
			response["successful_submission"] = false
			response["rows_received"] = len(raw)
			response["rows_valid"] = len(valid_resources)
			response["rows_invalid"] = len(invalid_resources)
			response["invalid"] = invalid_resources
			w.WriteHeader(http.StatusBadRequest)

			json.NewEncoder(w).Encode(response)
			return
		}

		ctx_preserve := context.WithoutCancel(middleware.StartTask(ctx, function, cfg))
		task_id, err := qm.QueueFunction(ctx_preserve, tools.RecursiveBatchInsert(ctx_preserve, qm.Db, cfg, valid_resources), note)
		if err != nil {
			qm.Logger.Error("TASK_ERROR", "error", err)
			http.Error(w, fmt.Sprintf("Error creating bulk create task\nError: %v", err), http.StatusInternalServerError)
			return
		}

		// Respond
		response["task_id"] = task_id
		response["successful_submission"] = err == nil
		response["rows_received"] = len(raw)
		response["rows_valid"] = len(valid_resources)
		response["rows_invalid"] = len(invalid_resources)
		response["invalid"] = invalid_resources

		json.NewEncoder(w).Encode(response)
	}
}

// Get resources via the standard api
func getResource(
	qm *tools.QueueManager,
	cfg *models.DataModel,
	svr_cfg *models.ServerConfig,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		task_type := "Get Resource"
		function := "get"
		user_key := middleware.Contextkey("user")
		req_ip := tools.GetIP(r)
		req_id, _ := tools.Generate32CharString()
		req_username := r.Context().Value(user_key).(*models.User).Username
		log := qm.Logger.With("user", req_username, "IP", req_ip, "function", function, "task_type", task_type, "end_point", *cfg.End_point, "table", *cfg.Table_name, "request_id", req_id)
		ctx := middleware.SetLogger(context.WithoutCancel(r.Context()), log)

		log.Info("REQUEST_RECEIVED")

		// Response intialization
		w.Header().Set("Content-Type", "application/json")
		response := map[string]any{"task_type": task_type}

		// Check that a user is allowed to inteface with this command
		if !middleware.CheckUserHasAllowedRole(ctx, cfg.End_points_allowed.GET, svr_cfg) {
			log.Warn("REQUEST_UNAUTHORISED", "error", "user role does not have permission to access this end point")
			http.Error(w, "User role does not have access to this end point", http.StatusUnauthorized)
			return
		}

		// Create new query builder and save it into the context of the request
		qb := tools.NewQueryBuilder(log)
	  qb.SetDefaults(cfg, r)

		if err := qb.ProcessURLParams(r, cfg); err != nil {
			log.Error("REQUEST_ERROR", "user", req_username, "IP", req_ip, "req_id", req_id, "function", task_type, "error", err)
			http.Error(w, fmt.Sprintf("Error in parsing where clauses: %v", err.Error()), http.StatusBadRequest)
			return
		}

		// Return the schema if this was requested
		if qb.IsSchemaBuilder() {
			json.NewEncoder(w).Encode(qb.BuildSchema(cfg))
			return 
		}

		var query string
		if qb.HasFields() {
			query = qb.BuildSelect(*cfg.Table_name, qb.GetFields())
		} else {
			query = qb.BuildSelect(*cfg.Table_name, tools.DynamicGetDatabaseColumns(cfg, false, false))
		}
		// Save the page data into the response
		response["page"] = qb.GetPage()
		response["page_size"] = qb.GetPageSize()

		// Run the queries and extract the data asyncronously
		var (
			data        []map[string]any
			total_count int
		)	
		g, queryCtx := errgroup.WithContext(ctx)

		g.Go(func() error {
			r, err := qm.Db.Query(queryCtx, query, qb.GetArgs()...)
			if err != nil {
				return err
			}
			defer r.Close()
			data, err = pgx.CollectRows(r, pgx.RowToMap)
			return err
		})

		g.Go(func() error {
			r, err := qm.Db.Query(queryCtx, qb.BuildCountWithWhere(*cfg.Table_name), qb.GetArgs()...)
			if err != nil {
				return err
			}
			defer r.Close()
			total_count, err = pgx.CollectOneRow(r, pgx.RowTo[int])
			return err
		})

		if err := g.Wait(); err != nil {
			log.Error("GET_ERROR", "error", err)
			http.Error(w, fmt.Sprintf("Error with the query:\n%v", err), http.StatusInternalServerError)
			return
		}	

		response["data"] = data
		response["total_count"] = total_count

		// Return
		json.NewEncoder(w).Encode(response)
	}
}


func getResourceHistory(
	qm *tools.QueueManager,
	cfg *models.DataModel,
	svr_cfg *models.ServerConfig,
) http.HandlerFunc {
	historyTable := fmt.Sprintf("%s_history", *cfg.Table_name)

	return func(w http.ResponseWriter, r *http.Request) {
		task_type := "Get Resource History"
		function := "get"
		user_key := middleware.Contextkey("user")
		req_ip := tools.GetIP(r)
		req_id, _ := tools.Generate32CharString()
		req_username := r.Context().Value(user_key).(*models.User).Username
		log := qm.Logger.With("user", req_username, "IP", req_ip, "function", function, "task_type", task_type, "end_point", *cfg.End_point, "table", historyTable, "request_id", req_id)
		ctx := middleware.SetLogger(r.Context(), log)

		log.Info("REQUEST_RECEIVED")

		// Response intialization
		w.Header().Set("Content-Type", "application/json")
		response := map[string]any{"task_type": task_type}

		// Check that a user is allowed to inteface with this command
		if !middleware.CheckUserHasAllowedRole(ctx, cfg.End_points_allowed.GET, svr_cfg) {
			log.Warn("REQUEST_UNAUTHORISED", "error", "user role does not have permission to access this end point")
			http.Error(w, "User role does not have access to this end point", http.StatusUnauthorized)
			return
		}

		// Path key — the asset identifier, matched against history.record (FK to the
		// model's track-history-field).
		key := r.PathValue("key")
		if key == "" {
			http.Error(w, "Missing record key in path", http.StatusBadRequest)
			return
		}

		qb := tools.NewQueryBuilder(log)
		qb.SetWhereAbsolute("record", key)

		if err := qb.ProcessHistoryURLParams(r); err != nil {
			log.Error("REQUEST_ERROR", "error", err)
			http.Error(w, fmt.Sprintf("Error parsing query parameters: %v", err.Error()), http.StatusBadRequest)
			return
		}

		// Column list: explicit selection if provided, otherwise the full history schema.
		cols := tools.HistoryColumns
		if qb.HasFields() {
			cols = qb.GetFields()
		}

		query := qb.BuildSelect(historyTable, cols)
		count_query := qb.BuildCountWithWhere(historyTable)

		// Save the page data into the response
		response["page"] = qb.GetPage()
		response["page_size"] = qb.GetPageSize()

		// Get the rows and count in parallel — both share qb.args because the count uses
		// the same WHERE clauses.
		var (
			data        []map[string]any
			total_count int
		)	
		g, queryCtx := errgroup.WithContext(ctx)

		// Run the queries
		g.Go(func() error {
			r, err := qm.Db.Query(queryCtx, query, qb.GetArgs()...)
			if err != nil {
				return err
			}
			defer r.Close()
			data, err = pgx.CollectRows(r, pgx.RowToMap)
			return err
		})

		g.Go(func() error {
			r, err := qm.Db.Query(queryCtx, count_query, qb.GetArgs()...)
			if err != nil {
				return err
			}
			defer r.Close()
			total_count, err = pgx.CollectOneRow(r, pgx.RowTo[int])
			return err
		})

		if err := g.Wait(); err != nil {
			log.Error("GET_ERROR", "error", err)
			http.Error(w, fmt.Sprintf("Error with the query:\n%v", err), http.StatusInternalServerError)
			return
		}	

		// pgx returns jsonb columns as []byte; decode them so they render as nested JSON.
		decodeHistoryJSONB(data)

		response["data"] = data
		response["total_count"] = total_count

		json.NewEncoder(w).Encode(response)
	}
}

// decodeHistoryJSONB converts the raw []byte values returned by pgx for jsonb columns
// (old_values, new_values) into decoded maps so they serialise as nested JSON rather
// than base64 blobs.
func decodeHistoryJSONB(rows []map[string]any) {
	for _, row := range rows {
		for _, col := range []string{"old_values", "new_values"} {
			raw, ok := row[col]
			if !ok || raw == nil {
				continue
			}
			var decoded any
			switch v := raw.(type) {
			case []byte:
				if json.Unmarshal(v, &decoded) == nil {
					row[col] = decoded
				}
			case string:
				if json.Unmarshal([]byte(v), &decoded) == nil {
					row[col] = decoded
				}
			}
		}
	}
}

// Update resource via the standard api
func updateResource(
	qm *tools.QueueManager,
	cfg *models.DataModel,
	svr_cfg *models.ServerConfig,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		task_type := "Update Resource"
		function := "update"
		user_key := middleware.Contextkey("user")
		req_ip := tools.GetIP(r)
		req_id, err := tools.Generate32CharString()
		req_username := r.Context().Value(user_key).(*models.User).Username
		note := r.Header.Get("X-User-Note")
		log := qm.Logger.With("user", req_username, "IP", req_ip, "function", function, "task_type", task_type, "end_point", *cfg.End_point, "table", *cfg.Table_name, "request_id", req_id)
		ctx := middleware.SetLogger(r.Context(), log)

		log.Info("REQUEST_RECEIVED")

		// Response intialization
		response := map[string]any{"task_type": task_type}
		w.Header().Set("Content-Type", "application/json")

		// Check that a user is allowed to inteface with this command
		if !middleware.CheckUserHasAllowedRole(ctx, cfg.End_points_allowed.PUT, svr_cfg) {
			log.Warn("REQUEST_UNAUTHORISED", "error", "user role does not have permission to access this end point")
			http.Error(w, "User role does not have access to this end point", http.StatusUnauthorized)
			return
		}

		var raw map[string]any
		err = json.NewDecoder(r.Body).Decode(&raw)

		if err != nil {
			log.Error("REQUEST_ERROR", "error", err)
			http.Error(w, fmt.Sprintf("Error decoding body: %v", err), http.StatusInternalServerError)
			return
		}

		// Coerce data into map — enforce key presence (PK or unique key), not required-on-insert
		valid_resources, invalid_resources := tools.Validate_Map_AgainstConfig(cfg, raw, true, false)
		if len(invalid_resources) > 0 {
			log.Warn("Request with no valid resources")
			response["successful_submission"] = false
			response["rows_received"] = len(raw)
			response["rows_valid"] = len(valid_resources)
			response["rows_invalid"] = len(invalid_resources)
			response["invalid"] = invalid_resources
			w.WriteHeader(http.StatusBadRequest)

			json.NewEncoder(w).Encode(response)
			return
		}

		// Queue the query
		ctx_preserve := context.WithoutCancel(middleware.StartTask(ctx, function, cfg))
		task_id, err := qm.QueueFunction(ctx_preserve, tools.MultiUpdate(ctx_preserve, qm.Db, cfg, valid_resources), note)
		if err != nil {
			qm.Logger.Error("TASK_ERROR", "user", req_username, "IP", req_ip, "req_id", req_id, "function", task_type, "error", err)
			http.Error(w, fmt.Sprintf("Error creating update task\nError: %v", err), http.StatusInternalServerError)
			return
		}


		// Build and return
		response["task_id"] = task_id
		response["successful_submission"] = err == nil
		response["rows_received"] = 1
		response["rows_valid"] = len(valid_resources)
		response["rows_invalid"] = len(invalid_resources)
		response["invalid"] = invalid_resources

		json.NewEncoder(w).Encode(response)
	}
}

func updateResource_Group(
	qm *tools.QueueManager,
	cfg *models.DataModel,
	svr_cfg *models.ServerConfig,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		task_type := "Update Multiple Resources"
		function := "update"
		user_key := middleware.Contextkey("user")
		req_ip := tools.GetIP(r)
		req_id, err := tools.Generate32CharString()
		req_username := r.Context().Value(user_key).(*models.User).Username
		note := r.Header.Get("X-User-Note")
		log := qm.Logger.With("user", req_username, "IP", req_ip, "function", function, "task_type", task_type, "end_point", *cfg.End_point, "table", *cfg.Table_name, "request_id", req_id)
		ctx := middleware.SetLogger(r.Context(), log)

		log.Info("REQUEST_RECEIVED")

		// Response intialization
		response := map[string]any{"task_type": task_type}
		w.Header().Set("Content-Type", "application/json")

		// Check that a user is allowed to inteface with this command
		if !middleware.CheckUserHasAllowedRole(ctx, cfg.End_points_allowed.PUT_GROUP, svr_cfg) {
			log.Warn("REQUEST_UNAUTHORISED", "error", "user role does not have permission to access this end point")
			http.Error(w, "User role does not have access to this end point", http.StatusUnauthorized)
			return
		}

		var raw []map[string]any
		err = json.NewDecoder(r.Body).Decode(&raw)
		valid_resources, invalid_resources := tools.Validate_SliceOfMaps_AgainstConfig(cfg, raw, true, false)

		if len(valid_resources) < 1 {
			log.Warn("Request with no valid resources")
			response["successful_submission"] = false
			response["rows_received"] = len(raw)
			response["rows_valid"] = len(valid_resources)
			response["rows_invalid"] = len(invalid_resources)
			response["invalid"] = invalid_resources
			w.WriteHeader(http.StatusBadRequest)

			json.NewEncoder(w).Encode(response)
			return
		}

		if err != nil {
			log.Error("REQUEST_ERROR", "error", err)
			http.Error(w, fmt.Sprintf("Error decoding body: %v", err), http.StatusInternalServerError)
			return
		}

		// Queue the query
		ctx_preserve := context.WithoutCancel(middleware.StartTask(ctx, function, cfg))
		task_id, err := qm.QueueFunction(ctx_preserve, tools.MultiUpdate(ctx_preserve, qm.Db, cfg, valid_resources), note)
		if err != nil {
			log.Error("TASK_ERROR", "error", err)
			http.Error(w, fmt.Sprintf("Error creating update task\nError: %v", err), http.StatusInternalServerError)
			return
		}

		// Build and return
		response["task_id"] = task_id
		response["successful_submission"] = err == nil
		response["rows_received"] = len(raw)
		response["rows_valid"] = len(valid_resources)
		response["rows_invalid"] = len(invalid_resources)
		response["invalid"] = invalid_resources

		json.NewEncoder(w).Encode(response)
	}
}


// GET /{endpoint}/diff — list stored diffs for this table
func dynamicGetDiff(
	cfg *models.DataModel,
	qm *tools.QueueManager,
	svr_cfg *models.ServerConfig,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user_key := middleware.Contextkey("user")
		function := "get"
		req_ip := tools.GetIP(r)
		req_id, _ := tools.Generate32CharString()
		req_username := r.Context().Value(user_key).(*models.User).Username
		log := qm.Logger.With("user", req_username, "IP", req_ip, "function", function, "task_type", "Get Diff", "end_point", *cfg.End_point, "table", *cfg.Table_name, "request_id", req_id)
		ctx := middleware.SetLogger(r.Context(), log)

		log.Info("REQUEST_RECEIVED")
		w.Header().Set("Content-Type", "application/json")

		// Check that a user is allowed to inteface with this command
		if !middleware.CheckUserHasAllowedRole(ctx, cfg.End_points_allowed.GET, svr_cfg) {
			log.Warn("REQUEST_UNAUTHORISED", "error", "user role does not have permission to access this end point")
			http.Error(w, "User role does not have access to this end point", http.StatusUnauthorized)
			return
		}

		diffCols := []string{"diff_id", "diff_type", "task_id", "missing_from_supplied", "missing_from_stored", "diffs", "generated_by_user", "checksum", "created", "note", "batched", "batched_date"}
		qb := tools.NewQueryBuilder(log)
		qb.SetWhereAbsolute("diff_type", *cfg.Table_name)

		taskID := r.URL.Query().Get("task_id")
		if taskID != "" {
			qb.SetWhereAbsolute("task_id", taskID)
		}
		checksum := tools.GetChecksum(r)
		if checksum != "" {
			qb.SetWhereAbsolute("checksum", checksum)
		}

		query := qb.BuildSelect("diffs", diffCols)
		rows, err := qm.Db.Query(r.Context(), fmt.Sprintf("%s LIMIT 1;", strings.TrimRight(query, "; ")), qb.GetArgs()...)
		if err != nil {
			log.Error("GET_ERROR", "error", err)
			http.Error(w, fmt.Sprintf("Error querying diffs: %v", err), http.StatusInternalServerError)
			return
		}
		defer rows.Close()
		result, err := pgx.CollectRows(rows, pgx.RowToMap)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(result)
	}
}

// POST /{endpoint}/diff — create a diff between supplied data and stored data
func dynamicCreateDiff(
	cfg *models.DataModel,
	qm *tools.QueueManager,
	svr_cfg *models.ServerConfig,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		task_type := "Create Diff"
		function := "diff"
		user_key := middleware.Contextkey("user")
		req_ip := tools.GetIP(r)
		req_id, err := tools.Generate32CharString()
		req_username := r.Context().Value(user_key).(*models.User).Username
		note := r.Header.Get("X-User-Note")
		log := qm.Logger.With("user", req_username, "IP", req_ip, "function", function, "task_type", task_type, "table", *cfg.Table_name, "request_id", req_id)
		ctx := middleware.SetLogger(r.Context(), log)

		log.Info("REQUEST_RECEIVED")
		response := map[string]any{"task_type": task_type}
		w.Header().Set("Content-Type", "application/json")

		// Check that a user is allowed to inteface with this command
		if !middleware.CheckUserHasAllowedRole(ctx, cfg.End_points_allowed.POST, svr_cfg) {
			log.Warn("REQUEST_UNAUTHORISED", "error", "user role does not have permission to access this end point")
			http.Error(w, "User role does not have access to this end point", http.StatusUnauthorized)
			return
		}

		var raw []map[string]any
		err = json.NewDecoder(r.Body).Decode(&raw)
		if err != nil {
			log.Error("REQUEST_ERROR", "error", err)
			http.Error(w, fmt.Sprintf("Error decoding body: %v", err), http.StatusBadRequest)
			return
		}
		if len(raw) < 1 {
			http.Error(w, "No data supplied", http.StatusBadRequest)
			return
		}

		// Coerce all supplied rows against the config
		valid_resources, invalid_resources := tools.Validate_SliceOfMaps_AgainstConfig(cfg, raw, true, false)
		if len(valid_resources) < 1 {
			log.Warn("Request with no valid resources")
			response["successful_submission"] = false
			response["rows_received"] = len(raw)
			response["rows_valid"] = len(valid_resources)
			response["rows_invalid"] = len(invalid_resources)
			response["invalid"] = invalid_resources
			w.WriteHeader(http.StatusBadRequest)

			json.NewEncoder(w).Encode(response)
			return
		}

		ctx_preserve := context.WithoutCancel(middleware.StartTask(ctx, function, cfg))
		task_id, err := qm.QueueFunction(ctx_preserve, tools.CreateDiff(ctx_preserve, qm.Db, cfg, valid_resources, note), note)
		if err != nil {
			log.Error("TASK_ERROR", "error", err)
			http.Error(w, fmt.Sprintf("Error creating diff task: %v", err), http.StatusInternalServerError)
			return
		}

		response["task_id"] = task_id
		response["rows_received"] = len(raw)
		response["rows_valid"] = len(valid_resources)
		response["rows_invalid"] = len(invalid_resources)
		response["invalid_resources"] = invalid_resources
		json.NewEncoder(w).Encode(response)
	}
}

// PUT /{endpoint}/diff — action a stored diff (return sync instructions)
func dynamicActionDiff(
	cfg *models.DataModel,
	qm *tools.QueueManager,
	svr_cfg *models.ServerConfig,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		task_type := "Action Diff"
		function := "diff"
		user_key := middleware.Contextkey("user")
		req_ip := tools.GetIP(r)
		req_id, _ := tools.Generate32CharString()
		req_username := r.Context().Value(user_key).(*models.User).Username
		log := qm.Logger.With("user", req_username, "IP", req_ip, "function", function, "task_type", task_type, "end_point", *cfg.End_point, "table", *cfg.Table_name, "request_id", req_id)
		ctx := middleware.SetLogger(r.Context(), log)

		log.Info("REQUEST_RECEIVED")
		w.Header().Set("Content-Type", "application/json")

		// Check that a user is allowed to inteface with this command
		if !middleware.CheckUserHasAllowedRole(ctx, cfg.End_points_allowed.PUT, svr_cfg) {
			log.Warn("REQUEST_UNAUTHORISED", "error", "user role does not have permission to access this end point")
			http.Error(w, "User role does not have access to this end point", http.StatusUnauthorized)
			return
		}

		checksum := tools.GetChecksum(r)
		if checksum == "" {
			http.Error(w, "Checksum was not provided", http.StatusBadRequest)
			return
		}
		log.Debug("Checksum decoded", "checksum", checksum)

		// Read the diff row as a raw map so JSONB columns come back as []byte
		rows, err := qm.Db.Query(r.Context(),
		`SELECT * FROM diffs WHERE diff_type = $1 AND checksum = $2 LIMIT 1;`,
		*cfg.Table_name, checksum,
	)
	if err != nil {
		log.Error("DATA_READ_ERROR", "error", err)
		http.Error(w, "Error reading diff", http.StatusInternalServerError)
		return
	}
	rawRows, err := pgx.CollectRows(rows, pgx.RowToMap)
	if err != nil || len(rawRows) == 0 {
		http.Error(w, "Invalid checksum provided", http.StatusBadRequest)
		return
	}
	row := rawRows[0]

	// Helper to decode a JSONB column from []byte into a target
	decodeJSONB := func(col string, target any) {
		log.Debug("attempting to identify type", "col", col)
		switch v := row[col].(type) {
		case []byte:
			json.Unmarshal(v, target)
		case string:
			json.Unmarshal([]byte(v), target)
		default:
			b, err := json.Marshal(v)
			if err != nil {
				return
			}
			json.Unmarshal(b, target)
		}
	}

	var missingFromSupplied []map[string]any
	var missingFromStored []map[string]any
	var diffs []models.Item_Diff[map[string]any]
	decodeJSONB("missing_from_supplied", &missingFromSupplied)
	decodeJSONB("missing_from_stored", &missingFromStored)
	decodeJSONB("diffs", &diffs)

	// Generate batch code
	var batchCode string
	batchRow := qm.Db.QueryRow(r.Context(),
	`SELECT generate_batch_num($1, $2, $3)`,
	req_username, *cfg.Table_name, checksum)
	if err := batchRow.Scan(&batchCode); err != nil {
		log.Error("BATCH_CODE_ERROR", "error", err)
		http.Error(w, "Error generating batch code", http.StatusInternalServerError)
		return
	}

	// Build sync arrays from diffs
	syncStored := make([]map[string]any, 0)
	syncSupplied := make([]map[string]any, 0)
	for _, d := range diffs {
		if d.Supplied != nil {
			syncStored = append(syncStored, *d.Supplied)
		}
		if d.Stored != nil {
			syncSupplied = append(syncSupplied, *d.Stored)
		}
	}

	response := map[string]any{
		"batch_code":            batchCode,
		"missing_from_supplied": missingFromSupplied,
		"missing_from_stored":   missingFromStored,
		"sync_stored":           syncStored,
		"sync_supplied":         syncSupplied,
	}
	json.NewEncoder(w).Encode(response)
}
}

// Delete a single resource identified by its primary key in the request body
func deleteResource(
	qm *tools.QueueManager,
	cfg *models.DataModel,
	svr_cfg *models.ServerConfig,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		task_type := "Delete Resource"
		function := "delete"
		user_key := middleware.Contextkey("user")
		req_ip := tools.GetIP(r)
		req_id, err := tools.Generate32CharString()
		req_username := r.Context().Value(user_key).(*models.User).Username
		note := r.Header.Get("X-User-Note")
		log := qm.Logger.With("user", req_username, "IP", req_ip, "function", function, "task_type", task_type, "end_point", *cfg.End_point, "table", *cfg.Table_name, "request_id", req_id)
		ctx := middleware.SetLogger(r.Context(), log)

		log.Info("REQUEST_RECEIVED")

		response := map[string]any{"task_type": task_type}
		w.Header().Set("Content-Type", "application/json")

		// Check that a user is allowed to inteface with this command
		if !middleware.CheckUserHasAllowedRole(ctx, cfg.End_points_allowed.DELETE, svr_cfg) {
			log.Warn("REQUEST_UNAUTHORISED", "error", "user role does not have permission to access this end point")
			http.Error(w, "User role does not have access to this end point", http.StatusUnauthorized)
			return
		}

		var raw map[string]any
		err = json.NewDecoder(r.Body).Decode(&raw)
		log.Debug("Decoded", "read data", fmt.Sprint(raw))
		if err != nil {
			log.Error("REQUEST_ERROR", "error", err)
			http.Error(w, fmt.Sprintf("Error decoding body: %v", err), http.StatusBadRequest)
			return
		}

		// Enforce that at least one key (PK or unique key) is present; other fields are ignored
		valid_resources, invalid_resources := tools.Validate_Map_AgainstConfig(cfg, raw, true, false)
		log.Debug("validated", "valid resources", fmt.Sprint(valid_resources))
		if len(valid_resources) < 1 {
			log.Warn("Request with no valid resources")
			response["successful_submission"] = false
			response["rows_received"] = len(raw)
			response["rows_valid"] = len(valid_resources)
			response["rows_invalid"] = len(invalid_resources)
			response["invalid"] = invalid_resources
			w.WriteHeader(http.StatusBadRequest)

			json.NewEncoder(w).Encode(response)
			return
		}

		ctx_preserve := context.WithoutCancel(middleware.StartTask(ctx, function, cfg))
		task_id, err := qm.QueueFunction(ctx_preserve, tools.MultiDelete(ctx_preserve, qm.Db, cfg, valid_resources), note)
		if err != nil {
			log.Error("TASK_ERROR", "error", err)
			http.Error(w, fmt.Sprintf("Error creating bulk delete task\nError: %v", err), http.StatusInternalServerError)
			return
		}

		response["task_id"] = task_id
		response["successful_submission"] = true
		response["rows_received"] = len(raw)
		response["rows_valid"] = len(valid_resources)
		response["rows_invalid"] = len(invalid_resources)
		response["invalid"] = invalid_resources

		json.NewEncoder(w).Encode(response)
	}
}

// Delete multiple resources, each identified by its primary key
func deleteResource_Group(
	qm *tools.QueueManager,
	cfg *models.DataModel,
	svr_cfg *models.ServerConfig,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		task_type := "Delete Multiple Resources"
		function := "delete"
		user_key := middleware.Contextkey("user")
		req_ip := tools.GetIP(r)
		req_id, err := tools.Generate32CharString()
		req_username := r.Context().Value(user_key).(*models.User).Username
		note := r.Header.Get("X-User-Note")
		log := qm.Logger.With("user", req_username, "IP", req_ip, "function", function, "task_type", task_type, "end_point", *cfg.End_point, "table", *cfg.Table_name, "request_id", req_id)
		ctx := middleware.SetLogger(r.Context(), log)

		log.Info("REQUEST_RECEIVED")

		response := map[string]any{"task_type": task_type}
		w.Header().Set("Content-Type", "application/json")

		// Check that a user is allowed to inteface with this command
		if !middleware.CheckUserHasAllowedRole(ctx, cfg.End_points_allowed.DELETE_GROUP, svr_cfg) {
			log.Warn("REQUEST_UNAUTHORISED", "error", "user role does not have permission to access this end point")
			http.Error(w, "User role does not have access to this end point", http.StatusUnauthorized)
			return
		}

		var raw []map[string]any
		err = json.NewDecoder(r.Body).Decode(&raw)
		if err != nil {
			log.Error("REQUEST_ERROR", "error", err)
			http.Error(w, fmt.Sprintf("Error decoding body: %v", err), http.StatusBadRequest)
			return
		}

		// Only enforce that each row has the PK; other fields are ignored for delete
		valid_resources, invalid_resources := tools.Validate_SliceOfMaps_AgainstConfig(cfg, raw, true, false)
		if len(valid_resources) < 1 {
			log.Warn("Request with no valid resources")
			response["successful_submission"] = false
			response["rows_received"] = len(raw)
			response["rows_valid"] = len(valid_resources)
			response["rows_invalid"] = len(invalid_resources)
			response["invalid"] = invalid_resources
			w.WriteHeader(http.StatusBadRequest)

			json.NewEncoder(w).Encode(response)
			return
		}

		ctx_preserve := context.WithoutCancel(middleware.StartTask(ctx, function, cfg))
		task_id, err := qm.QueueFunction(ctx_preserve, tools.MultiDelete(ctx_preserve, qm.Db, cfg, valid_resources), note)
		if err != nil {
			log.Error("TASK_ERROR", "error", err)
			http.Error(w, fmt.Sprintf("Error creating bulk delete task\nError: %v", err), http.StatusInternalServerError)
			return
		}

		response["task_id"] = task_id
		response["successful_submission"] = true
		response["rows_received"] = len(raw)
		response["rows_valid"] = len(valid_resources)
		response["rows_invalid"] = len(invalid_resources)
		response["invalid"] = invalid_resources

		json.NewEncoder(w).Encode(response)
	}
}

// notAllowed returns a 405 handler listing which methods are enabled for this endpoint.
func notAllowed(
	allowed *models.End_pointsAllowed,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tools.GetBasicLogger().Debug("debug", "method", r.Method)
		var allowed_methods []string
		if allowed != nil {
			if allowed.GET != nil           { allowed_methods = append(allowed_methods, "GET") }
			if allowed.POST != nil          { allowed_methods = append(allowed_methods, "POST") }
			if allowed.PUT != nil           { allowed_methods = append(allowed_methods, "PUT") }
			if allowed.DELETE != nil        { allowed_methods = append(allowed_methods, "DELETE") }
			if allowed.POST_GROUP != nil    { allowed_methods = append(allowed_methods, "POST-GROUP") }
			if allowed.PUT_GROUP != nil     { allowed_methods = append(allowed_methods, "PUT-GROUP") }
			if allowed.DELETE_GROUP != nil  { allowed_methods = append(allowed_methods, "DELETE-GROUP") }
		}
		if len(allowed_methods) > 0 {
			w.Header().Set("Allow", strings.Join(allowed_methods, ", "))
		} else {
			w.Header().Set("Allow", "-")
		}
		http.Error(w, "Not allowed", http.StatusMethodNotAllowed)
	}
}

func emptyResponse() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}
}

// Read a request and upgrade it to a new websocket
func websocketRegister(
	topic string,
	cfg *models.DataModel,
	svr_cfg *models.ServerConfig,
	logger *slog.Logger,
	evh *tools.EventManager,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		task_type := "Open Websocket"
		user_key := middleware.Contextkey("user")
		req_ip := tools.GetIP(r)
		req_username := r.Context().Value(user_key).(*models.User).Username
		log := logger.With("user", req_username, "IP", req_ip, "function", task_type, "topic", topic)
		ctx := middleware.SetLogger(r.Context(), log)

		log.Info("REQUEST_RECEIVED")

		// Check that a user is allowed to inteface with this command
		if !middleware.CheckUserHasAllowedRole(ctx, cfg.End_points_allowed.POST, svr_cfg) {
			log.Warn("REQUEST_UNAUTHORISED", "error", "user role does not have permission to access this end point")
			http.Error(w, "User role does not have access to this end point", http.StatusUnauthorized)
			return
		}

		// Read request
		if err := r.ParseForm(); err != nil {
			log.Error("Error parsing url form")
			http.Error(w, "Error in parsing url parameters", http.StatusBadRequest)
		}
		
		var request models.ClientMessage
		m := tools.UrlValuesToMap(r.Form, &request)
		tools.BuildStructFromMap(m, &request)

		// Upgrade connection
		var err error
		if len(request.Status) == 0 {
			err = evh.RegiterTopicOnly(w, r, topic)
		} else {
			err = evh.RegiterTopicStatus(w, r, topic, []string{request.Status})
		}
		if err != nil { 
			http.Error(w, fmt.Sprintf("Error: %s", err.Error()), http.StatusInternalServerError)
		}
	}
}
