package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"lotusforge.au/api-server/middleware"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/tools"
)

func RegisterRoutes(
	cfg *models.DataModel, 
	handlerRegistry *tools.HandlerRegistry, 
	auth func(http.Handler) http.Handler, 
	qm *tools.QueueManager, 
	server_conf *models.SwappableServerConfig,
) {
	var err error
	qm.Logger.Debug("Dynamic end point generating", "data-model", *cfg.Name)

	// Perform error check on first handler and soft cancel on error
	err = handlerRegistry.Register(fmt.Sprintf("GET /%s", *cfg.End_point), middleware.Cors(cfg, server_conf)(auth(handleGet(cfg, qm))), *cfg.Version)
	if err != nil {
		qm.Logger.Error("Failed to load model", "error", err)
		return
	}

	handlerRegistry.Register(fmt.Sprintf("PUT /%s", *cfg.End_point), middleware.Cors(cfg, server_conf)(auth(handleUpdate(cfg, qm))), *cfg.Version)
	handlerRegistry.Register(fmt.Sprintf("PUT /%s/group", *cfg.End_point), middleware.Cors(cfg, server_conf)(auth(handleUpdate_Group(cfg, qm))), *cfg.Version)
	handlerRegistry.Register(fmt.Sprintf("POST /%s", *cfg.End_point), middleware.Cors(cfg, server_conf)(auth(handleAddNew(cfg, qm))), *cfg.Version)
	handlerRegistry.Register(fmt.Sprintf("POST /%s/group", *cfg.End_point), middleware.Cors(cfg, server_conf)(auth(handleAddNew_Group(cfg, qm))), *cfg.Version)
	handlerRegistry.Register(fmt.Sprintf("DELETE /%s", *cfg.End_point), middleware.Cors(cfg, server_conf)(auth(handleDelete(cfg, qm))), *cfg.Version)
	handlerRegistry.Register(fmt.Sprintf("DELETE /%s/group", *cfg.End_point), middleware.Cors(cfg, server_conf)(auth(handleDelete_Group(cfg, qm))), *cfg.Version)

	if cfg.Allow_diff != nil && *cfg.Allow_diff {
		handlerRegistry.Register(fmt.Sprintf("GET /%s/diff", *cfg.End_point), auth(dynamicGetDiff(cfg, qm)), *cfg.Version)
		handlerRegistry.Register(fmt.Sprintf("POST /%s/diff", *cfg.End_point), auth(dynamicCreateDiff(cfg, qm)), *cfg.Version)
		handlerRegistry.Register(fmt.Sprintf("PUT /%s/diff", *cfg.End_point), auth(dynamicActionDiff(cfg, qm)), *cfg.Version)
	}
}

func handleGet(cfg *models.DataModel, qm *tools.QueueManager) http.HandlerFunc {
	if cfg.End_points_allowed != nil && cfg.End_points_allowed.GET != nil && *cfg.End_points_allowed.GET {
		return getResource(qm, cfg)
	}
	return notAllowed(cfg.End_points_allowed)
}

func handleAddNew(cfg *models.DataModel, qm *tools.QueueManager) http.HandlerFunc {
	if cfg.End_points_allowed != nil && cfg.End_points_allowed.POST != nil && *cfg.End_points_allowed.POST {
		return addNewResource(qm, cfg)
	}
	return notAllowed(cfg.End_points_allowed)
}

func handleAddNew_Group(cfg *models.DataModel, qm *tools.QueueManager) http.HandlerFunc {
	if cfg.End_points_allowed != nil && cfg.End_points_allowed.POST_GROUP != nil && *cfg.End_points_allowed.POST_GROUP {
		return addNewResources_Group(qm, cfg)
	}
	return notAllowed(cfg.End_points_allowed)
}

func handleUpdate(cfg *models.DataModel, qm *tools.QueueManager) http.HandlerFunc {
	if cfg.End_points_allowed != nil && cfg.End_points_allowed.PUT != nil && *cfg.End_points_allowed.PUT {
		return updateResource(qm, cfg)
	}
	return notAllowed(cfg.End_points_allowed)
}

func handleUpdate_Group(cfg *models.DataModel, qm *tools.QueueManager) http.HandlerFunc {
	if cfg.End_points_allowed != nil && cfg.End_points_allowed.PUT_GROUP != nil && *cfg.End_points_allowed.PUT_GROUP {
		return updateResource_Group(qm, cfg)
	}
	return notAllowed(cfg.End_points_allowed)
}

func handleDelete(cfg *models.DataModel, qm *tools.QueueManager) http.HandlerFunc {
	if cfg.End_points_allowed != nil && cfg.End_points_allowed.DELETE != nil && *cfg.End_points_allowed.DELETE {
		return deleteResource(qm, cfg)
	}
	return notAllowed(cfg.End_points_allowed)
}

func handleDelete_Group(cfg *models.DataModel, qm *tools.QueueManager) http.HandlerFunc {
	if cfg.End_points_allowed != nil && cfg.End_points_allowed.DELETE_GROUP != nil && *cfg.End_points_allowed.DELETE_GROUP {
		return deleteResource_Group(qm, cfg)
	}
	return notAllowed(cfg.End_points_allowed)
}


func addNewResource(
	qm *tools.QueueManager,
	cfg *models.DataModel,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		task_type := "Add Resource"
		user_key := middleware.Contextkey("user")
		req_ip := tools.GetIP(r)
		req_id, err := tools.Generate32CharString()
		req_username := r.Context().Value(user_key).(*models.User).Username
		note := r.Header.Get("X-User-Note")
		log := qm.Logger.With("user", req_username, "IP", req_ip, "function", task_type, "end_point", *cfg.End_point, "table", *cfg.Table_name, "request_id", req_id)
		ctx := middleware.SetLogger(r.Context(), log)

		log.Info("REQUEST_RECEIVED")

		// Response intialization
		response := map[string]any{"task_type": "CREATE"}
		w.Header().Set("Content-Type", "application/json")

		// Read incoming data
		var raw map[string]any
		err = json.NewDecoder(r.Body).Decode(&raw)
		if err != nil {
			log.Error("REQUEST_ERROR", "error", err)
			http.Error(w, fmt.Sprintf("Could not decode body. Error: %v", err), http.StatusInternalServerError)
			return
		}
		if len(raw) == 0 {
			http.Error(w, "No valid json supplied", http.StatusBadRequest)
			log.Error("REQUEST_ERROR", "error", "no valid json supplied")
			return
		}

		log.Debug("Decoding and coercing raw data", "data", fmt.Sprint(raw))
		valid_resources, _ := tools.Validate_Map_AgainstConfig(cfg, raw, false, true)
		if len(valid_resources) < 1 {
			log.Error("REQUEST_ERROR", "error", "resource invalid", "resource", raw)
			http.Error(w, "No valid resources", http.StatusBadRequest)
			return
		}

		// Extract context and queue action
		ctx_preserve := context.WithoutCancel(middleware.StartTask(ctx, task_type))
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
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		task_type := "Add Bulk Resources"
		user_key := middleware.Contextkey("user")
		req_ip := tools.GetIP(r)
		req_id, err := tools.Generate32CharString()
		req_username := r.Context().Value(user_key).(*models.User).Username
		note := r.Header.Get("X-User-Note")
		log := qm.Logger.With("user", req_username, "IP", req_ip, "function", task_type, "end_point", *cfg.End_point, "table", *cfg.Table_name, "request_id", req_id)
		ctx := middleware.SetLogger(r.Context(), log)

		log.Info("REQUEST_RECEIVED")

		// Response intialization
		response := map[string]any{"task_type": "CREATE_BULK"}
		w.Header().Set("Content-Type", "application/json")

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
			log.Error("REQUEST_ERROR", "error", "no valid resources")
			http.Error(w, "No valid resources", http.StatusBadRequest)
			return
		}

		ctx_preserve := context.WithoutCancel(middleware.StartTask(ctx, task_type))
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
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		task_type := "Get Resource"
		user_key := middleware.Contextkey("user")
		req_ip := tools.GetIP(r)
		req_id, err := tools.Generate32CharString()
		req_username := r.Context().Value(user_key).(*models.User).Username
		log := qm.Logger.With("user", req_username, "IP", req_ip, "function", task_type, "end_point", *cfg.End_point, "table", *cfg.Table_name, "request_id", req_id)
		ctx := middleware.SetLogger(r.Context(), log)

		log.Info("REQUEST_RECEIVED")

		// Response intialization
		w.Header().Set("Content-Type", "application/json")
		response := map[string]any{"task_type": "GET"}

		// Create new query builder and save it into the context of the request
		qb := tools.NewQueryBuilder(log)

		if err := qb.ProcessURLParams(r, cfg); err != nil {
			log.Error("REQUEST_ERROR", "user", req_username, "IP", req_ip, "req_id", req_id, "function", task_type, "error", err)
			http.Error(w, "Error in parsing where clauses", http.StatusBadRequest)
			return
		}

		query := qb.BuildSelect(*cfg.Table_name, tools.DynamicGetDatabaseColumns(cfg, false, false))
		// Save the page data into the response
		response["page"] = qb.GetPage()
		response["page_size"] = qb.GetPageSize()

		// Get the rows
		rows, err := qm.Db.Query(ctx, query, qb.GetArgs()...)
		count, err := qm.Db.Query(ctx, qb.BuildCount(*cfg.Table_name))

		if err != nil {
			log.Error("GET_ERROR", "error", err)
			http.Error(w, fmt.Sprintf("Error with the query:\n%v", err), http.StatusInternalServerError)
			return
		}

		// Handle the rows
		defer rows.Close()
		defer count.Close()
		response["data"], err = pgx.CollectRows(rows, pgx.RowToMap)
		response["total_count"], err = pgx.CollectOneRow(count, pgx.RowTo[int])

		// Handle error
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Return
		json.NewEncoder(w).Encode(response)
	}
}

// Update resource via the standard api
func updateResource(
	qm *tools.QueueManager,
	cfg *models.DataModel,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		task_type := "Update Resource"
		user_key := middleware.Contextkey("user")
		req_ip := tools.GetIP(r)
		req_id, err := tools.Generate32CharString()
		req_username := r.Context().Value(user_key).(*models.User).Username
		note := r.Header.Get("X-User-Note")
		log := qm.Logger.With("user", req_username, "IP", req_ip, "function", task_type, "end_point", *cfg.End_point, "table", *cfg.Table_name, "request_id", req_id)
		ctx := middleware.SetLogger(r.Context(), log)

		log.Info("REQUEST_RECEIVED")

		// Response intialization
		response := map[string]any{"task_type": "UPDATE"}
		w.Header().Set("Content-Type", "application/json")

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
			log.Warn("Request with no valid resources for update")
			response["successful_submission"] = false
			response["rows_received"] = len(raw)
			response["rows_valid"] = len(valid_resources)
			response["rows_invalid"] = len(invalid_resources)
			response["invalid"] = invalid_resources

			json.NewEncoder(w).Encode(response)
			return
		}

		// Determine which key fields (PK or unique key) are present — they become the WHERE clause
		where_fields, ok := tools.FindRowKeyFields(valid_resources[0], cfg)
		if !ok {
			log.Error("No key fields found for update")
			http.Error(w, "No identifying key (primary key or unique key) supplied for update", http.StatusBadRequest)
			return
		}

		// Create new query builder
		qb := tools.NewQueryBuilder(log)
		tools.SetValueAndWhereFromMap(qb, valid_resources[0], where_fields)

		// Build the query
		query := qb.BuildUpdate(cfg)

		// Queue the query
		ctx_preserve := context.WithoutCancel(middleware.StartTask(ctx, task_type))
		task_id, err := qm.QueueExec(ctx_preserve, query, note, qb.GetArgs()...)
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
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		task_type := "Update Multiple Resources"
		user_key := middleware.Contextkey("user")
		req_ip := tools.GetIP(r)
		req_id, err := tools.Generate32CharString()
		req_username := r.Context().Value(user_key).(*models.User).Username
		note := r.Header.Get("X-User-Note")
		log := qm.Logger.With("user", req_username, "IP", req_ip, "function", task_type, "end_point", *cfg.End_point, "table", *cfg.Table_name, "request_id", req_id)
		ctx := middleware.SetLogger(r.Context(), log)

		log.Info("REQUEST_RECEIVED")

		// Response intialization
		response := map[string]any{"task_type": "BULK_UPDATE"}
		w.Header().Set("Content-Type", "application/json")

		var raw []map[string]any
		err = json.NewDecoder(r.Body).Decode(&raw)
		valid_resources, invalid_resources := tools.Validate_SliceOfMaps_AgainstConfig(cfg, raw, true, false)

		if err != nil {
			log.Error("REQUEST_ERROR", "error", err)
			http.Error(w, fmt.Sprintf("Error decoding body: %v", err), http.StatusInternalServerError)
			return
		}

		// Queue the query
		ctx_preserve := context.WithoutCancel(middleware.StartTask(ctx, task_type))
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
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user_key := middleware.Contextkey("user")
		req_ip := tools.GetIP(r)
		req_id, _ := tools.Generate32CharString()
		req_username := r.Context().Value(user_key).(*models.User).Username
		log := qm.Logger.With("user", req_username, "IP", req_ip, "function", "Get Diff", "end_point", *cfg.End_point, "table", *cfg.Table_name, "request_id", req_id)

		log.Info("REQUEST_RECEIVED")
		w.Header().Set("Content-Type", "application/json")

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
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		task_type := "Create Diff"
		user_key := middleware.Contextkey("user")
		req_ip := tools.GetIP(r)
		req_id, err := tools.Generate32CharString()
		req_username := r.Context().Value(user_key).(*models.User).Username
		note := r.Header.Get("X-User-Note")
		log := qm.Logger.With("user", req_username, "IP", req_ip, "function", task_type, "table", *cfg.Table_name, "request_id", req_id)
		ctx := middleware.SetLogger(r.Context(), log)

		log.Info("REQUEST_RECEIVED")
		response := map[string]any{"task_type": "CREATE_DIFF"}
		w.Header().Set("Content-Type", "application/json")

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
			log.Error("REQUEST_ERROR", "error", "no valid resources")
			http.Error(w, "No valid resources supplied", http.StatusBadRequest)
			return
		}

		ctx_preserve := context.WithoutCancel(middleware.StartTask(ctx, task_type))
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
		json.NewEncoder(w).Encode(response)
	}
}

// PUT /{endpoint}/diff — action a stored diff (return sync instructions)
func dynamicActionDiff(
	cfg *models.DataModel,
	qm *tools.QueueManager,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		task_type := "Action Diff"
		user_key := middleware.Contextkey("user")
		req_ip := tools.GetIP(r)
		req_id, _ := tools.Generate32CharString()
		req_username := r.Context().Value(user_key).(*models.User).Username
		log := qm.Logger.With("user", req_username, "IP", req_ip, "function", task_type, "end_point", *cfg.End_point, "table", *cfg.Table_name, "request_id", req_id)

		log.Info("REQUEST_RECEIVED")
		w.Header().Set("Content-Type", "application/json")

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
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		task_type := "Delete Resource"
		user_key := middleware.Contextkey("user")
		req_ip := tools.GetIP(r)
		req_id, err := tools.Generate32CharString()
		req_username := r.Context().Value(user_key).(*models.User).Username
		note := r.Header.Get("X-User-Note")
		log := qm.Logger.With("user", req_username, "IP", req_ip, "function", task_type, "end_point", *cfg.End_point, "table", *cfg.Table_name, "request_id", req_id)
		ctx := middleware.SetLogger(r.Context(), log)

		log.Info("REQUEST_RECEIVED")

		response := map[string]any{"task_type": "DELETE"}
		w.Header().Set("Content-Type", "application/json")

		var raw map[string]any
		err = json.NewDecoder(r.Body).Decode(&raw)
		log.Debug("Decoded", "read data", fmt.Sprint(raw))
		if err != nil {
			log.Error("REQUEST_ERROR", "error", err)
			http.Error(w, fmt.Sprintf("Error decoding body: %v", err), http.StatusBadRequest)
			return
		}

		// Enforce that at least one key (PK or unique key) is present; other fields are ignored
		valid_resources, _ := tools.Validate_Map_AgainstConfig(cfg, raw, true, false)
		log.Debug("validated", "valid resources", fmt.Sprint(valid_resources))
		if len(valid_resources) < 1 {
			log.Error("REQUEST_ERROR", "error", "no key field found for delete")
			http.Error(w, "No identifying key (primary key or unique key) supplied for delete", http.StatusBadRequest)
			return
		}

		where_fields, ok := tools.FindRowKeyFields(valid_resources[0], cfg)
		if !ok {
			log.Error("No key fields found for delete")
			http.Error(w, "No identifying key (primary key or unique key) supplied for delete", http.StatusBadRequest)
			return
		}

		qb := tools.NewQueryBuilder(log)
		for _, k := range where_fields {
			qb.SetWhereAbsolute(k, valid_resources[0][k])
		}

		query := qb.BuildDelete(cfg)

		ctx_preserve := context.WithoutCancel(middleware.StartTask(ctx, task_type))
		task_id, err := qm.QueueExec(ctx_preserve, query, note, qb.GetArgs()...)
		if err != nil {
			log.Error("TASK_ERROR", "error", err)
			http.Error(w, fmt.Sprintf("Error creating delete task\nError: %v", err), http.StatusInternalServerError)
			return
		}

		response["task_id"] = task_id
		response["successful_submission"] = true
		json.NewEncoder(w).Encode(response)
	}
}

// Delete multiple resources, each identified by its primary key
func deleteResource_Group(
	qm *tools.QueueManager,
	cfg *models.DataModel,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		task_type := "Delete Multiple Resources"
		user_key := middleware.Contextkey("user")
		req_ip := tools.GetIP(r)
		req_id, err := tools.Generate32CharString()
		req_username := r.Context().Value(user_key).(*models.User).Username
		note := r.Header.Get("X-User-Note")
		log := qm.Logger.With("user", req_username, "IP", req_ip, "function", task_type, "end_point", *cfg.End_point, "table", *cfg.Table_name, "request_id", req_id)
		ctx := middleware.SetLogger(r.Context(), log)

		log.Info("REQUEST_RECEIVED")

		response := map[string]any{"task_type": "BULK_DELETE"}
		w.Header().Set("Content-Type", "application/json")

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
			log.Error("REQUEST_ERROR", "error", "no valid resources with primary key")
			http.Error(w, "No valid resources with primary key supplied", http.StatusBadRequest)
			return
		}

		ctx_preserve := context.WithoutCancel(middleware.StartTask(ctx, task_type))
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
		var allowed_methods []string
		if allowed != nil {
			if allowed.GET != nil && *allowed.GET           { allowed_methods = append(allowed_methods, "GET") }
			if allowed.POST != nil && *allowed.POST         { allowed_methods = append(allowed_methods, "POST") }
			if allowed.PUT != nil && *allowed.PUT           { allowed_methods = append(allowed_methods, "PUT") }
			if allowed.DELETE != nil && *allowed.DELETE     { allowed_methods = append(allowed_methods, "DELETE") }
			if allowed.POST_GROUP != nil && *allowed.POST_GROUP     { allowed_methods = append(allowed_methods, "POST-GROUP") }
			if allowed.PUT_GROUP != nil && *allowed.PUT_GROUP       { allowed_methods = append(allowed_methods, "PUT-GROUP") }
			if allowed.DELETE_GROUP != nil && *allowed.DELETE_GROUP { allowed_methods = append(allowed_methods, "DELETE-GROUP") }
		}
		if len(allowed_methods) > 0 {
			w.Header().Set("Allow", strings.Join(allowed_methods, ", "))
		} else {
			w.Header().Set("Allow", "-")
		}
		http.Error(w, "Not allowed", http.StatusMethodNotAllowed)
	}
}
