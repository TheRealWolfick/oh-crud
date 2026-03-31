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


func NewRegisterRoutes(cfg *models.DataModel, mux *http.ServeMux, auth func(http.Handler) http.Handler, qm *tools.QueueManager) {
	qm.Logger = qm.Logger.With("end_point", *cfg.End_Point)
	qm.Logger.Debug("Dynamic end point generating", "data-model", *cfg.Name)
	mux.Handle(fmt.Sprintf("GET /%s", *cfg.End_Point), auth(handleGet(cfg, qm)))
	mux.Handle(fmt.Sprintf("PUT /%s", *cfg.End_Point), auth(handleUpdate(cfg, qm)))
	//mux.Handle(fmt.Sprintf("PUT /%s/group", *dh.End_Point), auth(dh.HandleMultiUpdate()))
	mux.Handle(fmt.Sprintf("POST /%s", *cfg.End_Point), auth(handleAddNew(cfg, qm)))
	mux.Handle(fmt.Sprintf("POST /%s/group", *cfg.End_Point), auth(handleAddMulipleNew(cfg, qm)))
	//mux.Handle(fmt.Sprintf("DELETE /%s", *dh.End_Point), auth(dh.HandleDelete()))
}

func handleGet(cfg *models.DataModel, qm *tools.QueueManager) http.HandlerFunc {
	if cfg.Allow.Get { return dynamicGetResource(qm, cfg) }
	return dynamicNotAllowed(*cfg.Allow)
}

func handleAddNew(cfg *models.DataModel, qm *tools.QueueManager) http.HandlerFunc {
	if cfg.Allow.Get { return dynamicAddNewResource(qm, cfg) }
	return dynamicNotAllowed(*cfg.Allow)
}

func handleAddMulipleNew(cfg *models.DataModel, qm *tools.QueueManager) http.HandlerFunc {
	if cfg.Allow.Post_Group { return dynamicAddMultipleNewResources(qm, cfg) }
	return dynamicNotAllowed(*cfg.Allow)
}

func handleUpdate(cfg *models.DataModel, qm *tools.QueueManager) http.HandlerFunc {
	if cfg.Allow.Put { return dynamicUpdateResource(qm, cfg) }
	return dynamicNotAllowed(*cfg.Allow)
}


func dynamicAddNewResource(
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
		log := qm.Logger.With("user", req_username, "IP", req_ip, "function", task_type, "table", *cfg.Table_Name, "request_id", req_id)
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
func dynamicAddMultipleNewResources(
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
		log := qm.Logger.With("user", req_username, "IP", req_ip, "function", task_type, "table", *cfg.Table_Name, "request_id", req_id)
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
func dynamicGetResource(
	qm *tools.QueueManager,
	cfg *models.DataModel,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		task_type := "Get Resource"
		user_key := middleware.Contextkey("user")
		req_ip := tools.GetIP(r)
		req_id, err := tools.Generate32CharString()
		req_username := r.Context().Value(user_key).(*models.User).Username
		log := qm.Logger.With("user", req_username, "IP", req_ip, "function", task_type, "table", *cfg.Table_Name, "request_id", req_id)
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
func dynamicUpdateResource(
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
		log := qm.Logger.With("user", req_username, "IP", req_ip, "function", task_type, "table", *cfg.Table_Name, "request_id", req_id)
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

func dynamicMultiUpdateResource(
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
		log := qm.Logger.With("user", req_username, "IP", req_ip, "function", task_type, "table", *cfg.Table_Name, "request_id", req_id)
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


// Handle a disallowed endPoint
func dynamicNotAllowed(
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

