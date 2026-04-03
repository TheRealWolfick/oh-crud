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


func RegisterRoutes(cfg *models.DataModel, mux *http.ServeMux, auth func(http.Handler) http.Handler, qm *tools.QueueManager) {
	qm.Logger.Debug("Dynamic end point generating", "data-model", *cfg.Name)
	mux.Handle(fmt.Sprintf("GET /%s", *cfg.End_Point), auth(handleGet(cfg, qm)))
	mux.Handle(fmt.Sprintf("PUT /%s", *cfg.End_Point), auth(handleUpdate(cfg, qm)))
	mux.Handle(fmt.Sprintf("PUT /%s/group", *cfg.End_Point), auth(handleUpdate_Group(cfg, qm)))
	mux.Handle(fmt.Sprintf("POST /%s", *cfg.End_Point), auth(handleAddNew(cfg, qm)))
	mux.Handle(fmt.Sprintf("POST /%s/group", *cfg.End_Point), auth(handleAddNew_Group(cfg, qm)))
	mux.Handle(fmt.Sprintf("DELETE /%s", *cfg.End_Point), auth(handleDelete(cfg, qm)))
	mux.Handle(fmt.Sprintf("DELETE /%s/group", *cfg.End_Point), auth(handleDelete_Group(cfg, qm)))

	if cfg.Allow_Diff != nil && *cfg.Allow_Diff {
		mux.Handle(fmt.Sprintf("GET /%s/diff", *cfg.End_Point), auth(dynamicGetDiff(cfg, qm)))
		mux.Handle(fmt.Sprintf("POST /%s/diff", *cfg.End_Point), auth(dynamicCreateDiff(cfg, qm)))
		mux.Handle(fmt.Sprintf("PUT /%s/diff", *cfg.End_Point), auth(dynamicActionDiff(cfg, qm)))
	}
}

func handleGet(cfg *models.DataModel, qm *tools.QueueManager) http.HandlerFunc {
	if cfg.Allow.Get { return getResource(qm, cfg) }
	return notAllowed(*cfg.Allow)
}

func handleAddNew(cfg *models.DataModel, qm *tools.QueueManager) http.HandlerFunc {
	if cfg.Allow.Post { return addNewResource(qm, cfg) }
	return notAllowed(*cfg.Allow)
}

func handleAddNew_Group(cfg *models.DataModel, qm *tools.QueueManager) http.HandlerFunc {
	if cfg.Allow.Post_Group { return addNewResources_Group(qm, cfg) }
	return notAllowed(*cfg.Allow)
}

func handleUpdate(cfg *models.DataModel, qm *tools.QueueManager) http.HandlerFunc {
	if cfg.Allow.Put { return updateResource(qm, cfg) }
	return notAllowed(*cfg.Allow)
}

func handleUpdate_Group(cfg *models.DataModel, qm *tools.QueueManager) http.HandlerFunc {
	if cfg.Allow.Put_Group { return updateResource_Group(qm, cfg) }
	return notAllowed(*cfg.Allow)
}

func handleDelete(cfg *models.DataModel, qm *tools.QueueManager) http.HandlerFunc {
	if cfg.Allow.Delete { return deleteResource(qm, cfg) }
	return notAllowed(*cfg.Allow)
}

func handleDelete_Group(cfg *models.DataModel, qm *tools.QueueManager) http.HandlerFunc {
	if cfg.Allow.Delete_Group { return deleteResource_Group(qm, cfg) }
	return notAllowed(*cfg.Allow)
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
		log := qm.Logger.With("user", req_username, "IP", req_ip, "function", task_type, "end_point", *cfg.End_Point, "table", *cfg.Table_Name, "request_id", req_id)
		ctx := middleware.SetLogger(r.Context(), log)

		log.Info("REQUEST_RECEIVED")

		// Response intialization
		response := map[string]any{"task_type": "CREATE"}
		w.Header().Set("Content-Type", "application/json")

		// Read and coerce incoming data
		var raw map[string]any
		err = json.NewDecoder(r.Body).Decode(&raw)
		log.Debug("Decoding and coercing raw data", "data", fmt.Sprint(raw))
		resource, err := models.DecodeAndCoerce(raw, cfg, true, true)

		// Validation and errors
		if err != nil {
			log.Error("REQUEST_ERROR", "error", err)
			http.Error(w, fmt.Sprintf("Could not decode body. Error: %v", err), http.StatusInternalServerError)
			return
		}
		if len(resource) == 0 {
			http.Error(w, "No valid json supplied", http.StatusBadRequest)
			log.Error("REQUEST_ERROR", "error", "no valid json supplied")		
			return
		}
		valid_resources, _ := tools.Validate_Map_AgainstConfig(cfg, resource, false, true)
		if len(valid_resources) < 1 {
			log.Error("REQUEST_ERROR", "error", "resource invalid", "resource", raw)
			http.Error(w, "No valid domains", http.StatusBadRequest)
			return
		}


		// Extract context and queue action
		ctx_preserve := context.WithoutCancel(middleware.StartTask(ctx, task_type))
		task_id, err := qm.QueueFunction(ctx_preserve, tools.SingleInsert_Dynamic(ctx_preserve, qm.Db, cfg, valid_resources[0]), note)

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
		log := qm.Logger.With("user", req_username, "IP", req_ip, "function", task_type, "end_point", *cfg.End_Point, "table", *cfg.Table_Name, "request_id", req_id)
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
		valid_resources, invalid_resources := tools.Validate_SliceOfMaps_AgainstConfig(cfg, raw, true, true)
		if len(valid_resources) < 1 {
			log.Error("REQUEST_ERROR", "error", "no valid resources")
			http.Error(w, "No valid resources", http.StatusBadRequest)
			return
		}

		ctx_preserve := context.WithoutCancel(middleware.StartTask(ctx, task_type))
		task_id, err := qm.QueueFunction(ctx_preserve, tools.RecursiveBatchInsert_Dynamic(ctx_preserve, qm.Db, cfg, valid_resources), note)
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
		log := qm.Logger.With("user", req_username, "IP", req_ip, "function", task_type, "end_point", *cfg.End_Point, "table", *cfg.Table_Name, "request_id", req_id)
		ctx := middleware.SetLogger(r.Context(), log)
		
		log.Info("REQUEST_RECEIVED")

		// Response intialization
		w.Header().Set("Content-Type", "application/json")

		var query string

		// Create new query builder and save it into the context of the request
		qb := tools.NewQueryBuilder(log)

		// Load where vals
		log.Debug("Read default where in dynamicgetresource", "value", cfg.Default_Where)
		if cfg.Default_Where != nil {
			for key, _ := range cfg.Default_Where {
				qb.SetWhereAbsolute(key, cfg.Default_Where[key])
			}
		}
		if err := tools.DynamicSetWhereFromURL(qb, r, cfg); err != nil {
			log.Error("REQUEST_ERROR", "user", req_username, "IP", req_ip, "req_id", req_id, "function", task_type, "error", err)
			http.Error(w, "Error in parsing where clauses", http.StatusBadRequest)
			return
		}

		// Build the query
		log.Debug("Read overwrite select in dynamicgetresource", "value", cfg.Overwrite_Select)
		if cfg.Overwrite_Select == nil {
			query = qb.BuildSelect(*cfg.Table_Name, tools.DynamicGetDatabaseColumns(cfg, false, false))
		} else {
			query = qb.BuildSelect(*cfg.Table_Name, cfg.Overwrite_Select)
		}

		log.Debug("Loading custom with in dynamicgetresource", "pointer", cfg.Custom_With)
		if cfg.Custom_With != nil {
			query = fmt.Sprintf("%s %s", *cfg.Custom_With, query)
		}

		// Get the rows
		rows, err := qm.Db.Query(ctx, query, qb.GetArgs()...)

		if err != nil {
			log.Error("GET_ERROR", "error", err)
			http.Error(w, fmt.Sprintf("Error with the query:\n%v", err), http.StatusInternalServerError)
			return
		}

		// Handle the rows
		defer rows.Close()
		response, err := pgx.CollectRows(rows, pgx.RowToMap)

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
		log := qm.Logger.With("user", req_username, "IP", req_ip, "function", task_type, "end_point", *cfg.End_Point, "table", *cfg.Table_Name, "request_id", req_id)
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

		// Coerce data into map
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

		// Create new query builder
		qb := tools.NewQueryBuilder(log)

		// Set values from the struct
		prim_keys := tools.DynamicGetDatabaseColumns(cfg, true, false)
		if len(prim_keys) < 1 {
			log.Error("Error reading the primary key from config")
			http.Error(w, "Error reading primary key from config", http.StatusBadRequest)
		}
		tools.SetValueAndWhereFromMap(qb, valid_resources[0], prim_keys[0])

		// Build the query
		query := qb.BuildUpdate_Dynamic(cfg)

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
		log := qm.Logger.With("user", req_username, "IP", req_ip, "function", task_type, "end_point", *cfg.End_Point, "table", *cfg.Table_Name, "request_id", req_id)
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
		task_id, err := qm.QueueFunction(ctx_preserve, tools.MultiUpdate_Dynamic(ctx_preserve, qm.Db, cfg, valid_resources), note)
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
		log := qm.Logger.With("user", req_username, "IP", req_ip, "function", "Get Diff", "end_point", *cfg.End_Point, "table", *cfg.Table_Name, "request_id", req_id)

		log.Info("REQUEST_RECEIVED")
		w.Header().Set("Content-Type", "application/json")

		diffCols := []string{"diff_id", "diff_type", "task_id", "missing_from_supplied", "missing_from_stored", "diffs", "generated_by_user", "checksum", "created", "note", "batched", "batched_date"}
		qb := tools.NewQueryBuilder(log)
		qb.SetWhereAbsolute("diff_type", *cfg.Table_Name)

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
		log := qm.Logger.With("user", req_username, "IP", req_ip, "function", task_type, "table", *cfg.Table_Name, "request_id", req_id)
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
		task_id, err := qm.QueueFunction(ctx_preserve, tools.CreateDiff_Dynamic(ctx_preserve, qm.Db, cfg, valid_resources, note), note)
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
		log := qm.Logger.With("user", req_username, "IP", req_ip, "function", task_type, "end_point", *cfg.End_Point, "table", *cfg.Table_Name, "request_id", req_id)

		log.Info("REQUEST_RECEIVED")
		w.Header().Set("Content-Type", "application/json")

		checksum := tools.GetChecksum(r)
		if checksum == "" {
			http.Error(w, "Checksum was not provided", http.StatusBadRequest)
			return
		}
		log.Debug("Cecksum decoded", "checksum", checksum)

		// Read the diff row as a raw map so JSONB columns come back as []byte
		rows, err := qm.Db.Query(r.Context(),
		`SELECT * FROM diffs WHERE diff_type = $1 AND checksum = $2 LIMIT 1;`,
		*cfg.Table_Name, checksum,
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
		log.Debug("attenpting to identify type", "col", col)
		switch v := row[col].(type) {
		case []byte:
			json.Unmarshal(v, target)
		case string:
			json.Unmarshal([]byte(v), target)
		default:
			b, err := json.Marshal(v)
			if err != nil { return }
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
	req_username, *cfg.Table_Name, checksum)
	if err := batchRow.Scan(&batchCode); err != nil {
		log.Error("BATCH_CODE_ERROR", "error", err)
		http.Error(w, "Error generating batch code", http.StatusInternalServerError)
		return
	}

	// Build sync arrays from diffs
	syncStored := make([]map[string]any, 0)
	syncSupplied := make([]map[string]any, 0)
	for _, d := range diffs {
		if d.Supplied != nil { syncStored = append(syncStored, *d.Supplied) }
		if d.Stored != nil   { syncSupplied = append(syncSupplied, *d.Stored) }
	}

	response := map[string]any{
		"batch_code":            "testing",
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
		log := qm.Logger.With("user", req_username, "IP", req_ip, "function", task_type, "end_point", *cfg.End_Point, "table", *cfg.Table_Name, "request_id", req_id)
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

		// Only enforce that the PK is present; other fields are ignored for delete
		valid_resources, _ := tools.Validate_Map_AgainstConfig(cfg, raw, true, false)
		log.Debug("validated", "valid resources", fmt.Sprint(valid_resources))
		if len(valid_resources) < 1 {
			log.Error("REQUEST_ERROR", "error", "missing primary key")
			http.Error(w, "Primary key missing or invalid", http.StatusBadRequest)
			return
		}

		prim_keys := tools.DynamicGetDatabaseColumns(cfg, true, false)
		if len(prim_keys) < 1 {
			log.Error("Error reading the primary key from config")
			http.Error(w, "Error reading primary key from config", http.StatusInternalServerError)
			return
		}

		qb := tools.NewQueryBuilder(log)
		for _, k := range prim_keys {
			if _, ok := valid_resources[0][k]; !ok {
				log.Error("Not all primary keys were supplied in data!", "missing key", k)
				http.Error(w, "Not all primary keys were supplied in data!", http.StatusBadRequest)
				return
			} 
		}
		for _, k := range prim_keys {
			qb.SetWhereAbsolute(k, valid_resources[0][k])
		}

		query := qb.BuildDelete_Dynamic(cfg)

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
		log := qm.Logger.With("user", req_username, "IP", req_ip, "function", task_type, "end_point", *cfg.End_Point, "table", *cfg.Table_Name, "request_id", req_id)
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
		task_id, err := qm.QueueFunction(ctx_preserve, tools.MultiDelete_Dynamic(ctx_preserve, qm.Db, cfg, valid_resources), note)
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

// Handle a disallowed endPoint
func notAllowed(
	allowed models.DataModelAllow,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var allowed_methods []string
		for key, allow := range tools.GetStructAsDict(allowed) {
			if allow == true {
				allowed_methods = append(allowed_methods, key)
			}
		}
		if len(allowed_methods) > 0 {
			w.Header().Set("Allow", strings.Join(allowed_methods, ", "))
		} else {
			w.Header().Set("Allow", "-")
		}
		http.Error(w, "Not allowed", http.StatusMethodNotAllowed)
	}
}

