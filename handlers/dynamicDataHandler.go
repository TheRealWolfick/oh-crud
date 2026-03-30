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
	qm.Logger.Debug("Dynamic end point generating", "data-model", *cfg.Name, "end-point", *cfg.End_Point)
	mux.Handle(fmt.Sprintf("GET /%s", *cfg.End_Point), auth(handleGet(cfg, qm)))
	//mux.Handle(fmt.Sprintf("PUT /%s", *dh.End_Point), auth(dh.HandleUpdate()))
	//mux.Handle(fmt.Sprintf("PUT /%s/group", *dh.End_Point), auth(dh.HandleMultiUpdate()))
	mux.Handle(fmt.Sprintf("POST /%s", *cfg.End_Point), auth(handleAddNew(cfg, qm)))
	mux.Handle(fmt.Sprintf("POST /%s/group", *cfg.End_Point), auth(handleAddMulipleNew(cfg, qm)))
	//mux.Handle(fmt.Sprintf("DELETE /%s", *dh.End_Point), auth(dh.HandleDelete()))
}

func handleGet(cfg *models.DataModel, qm *tools.QueueManager) http.HandlerFunc {
	if cfg.Allow.Get {
		return dynamicGetResource(qm, cfg)
	}
	return dynamicNotAllowed(*cfg.Allow)
}

func handleAddNew(cfg *models.DataModel, qm *tools.QueueManager) http.HandlerFunc {
	if cfg.Allow.Get {
		return dynamicAddNewResource(qm, cfg)
	}
	return dynamicNotAllowed(*cfg.Allow)
}

func handleAddMulipleNew(cfg *models.DataModel, qm *tools.QueueManager) http.HandlerFunc {
	if cfg.Allow.Post_Group { return dynamicAddMultipleNewResources(qm, cfg) }
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
		if tools.StructIsEmpty(&raw) {
			log.Error("REQUEST_ERROR", "error", "no valid json supplied")
			http.Error(w, "No valid json supplied", http.StatusBadRequest)
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
			query = qb.BuildSelect(*cfg.Table_Name, tools.DynamicGetDatabaseColumns(cfg))
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
func dynamicUpdateResource[T any](
	qm *tools.QueueManager,
	tableName string,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		task_type := "Update Resource"
		user_key := middleware.Contextkey("user")
		req_ip := tools.GetIP(r)
		req_id, err := tools.Generate32CharString()
		req_username := r.Context().Value(user_key).(*models.User).Username
		note := r.Header.Get("X-User-Note")
		qm.Logger.Info("REQUEST_RECEIVED", "user", req_username, "IP", req_ip, "function", task_type, "table", tableName, "request_id", req_id)

		// Response intialization
		response := map[string]any{"task_type": "UPDATE"}
		w.Header().Set("Content-Type", "application/json")

		var updated T
		err = json.NewDecoder(r.Body).Decode(&updated)

		if err != nil {
			qm.Logger.Error("REQUEST_ERROR", "user", req_username, "IP", req_ip, "req_id", req_id, "function", task_type, "error", err)
			http.Error(w, fmt.Sprintf("Error decoding body: %v", err), http.StatusInternalServerError)
			return
		}

		// Check for valid values
		if tools.StructIsEmpty(&updated) {
			qm.Logger.Error("REQUEST_ERROR", "user", req_username, "IP", req_ip, "req_id", req_id, "function", task_type, "error", "no valid json supplied")
			http.Error(w, "No valid updates", http.StatusBadRequest)
			return
		}

		// Create new query builder
		qb := tools.NewQueryBuilder(qm.Logger.With("call", "POST"))

		// Set values from the struct
		tools.SetValueFromStruct(qb, updated)

		// Build the query
		query := qb.BuildUpdate(tableName, r, updated)

		// Queue the query
		ctx_preserve := context.WithoutCancel(middleware.StartTask(r.Context(), task_type))
		task_id, err := qm.QueueExec(ctx_preserve, query, note, qb.GetArgs()...)
		if err != nil {
			qm.Logger.Error("TASK_ERROR", "user", req_username, "IP", req_ip, "req_id", req_id, "function", task_type, "error", err)
			http.Error(w, fmt.Sprintf("Error creating update task\nError: %v", err), http.StatusInternalServerError)
			return
		}

		// Build and return
		response["task_id"] = task_id
		response["successful_submission"] = err == nil

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

