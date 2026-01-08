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

type DataHandler[T any] struct {
	Qm                *tools.QueueManager
	TableName 	      string
	EndPoint          string
	DefaultWhere    	map[string]any
	SelectOverwrite   []string
	Allowed 					map[string]bool
}

type GetOnlyDataHandler[T any] struct {
	DataHandler[T]
}

type DataHandlerInterface interface {
	RegisterRoutes(mux *http.ServeMux, auth func(http.Handler) http.Handler, qm *tools.QueueManager)
	HandleUpdate() http.HandlerFunc
	HandleGet() http.HandlerFunc 
	HandleAddNew() http.HandlerFunc 
	HandleAddMultipleNew() http.HandlerFunc 
	HandleDelete() http.HandlerFunc 
}


// Create a new data handler which allows all API requests
func NewGenericDataHandler[T any](qm *tools.QueueManager, tableName string, endPoint string, allows map[string]bool, defaultWhere map[string]any, overwriteSelect []string) *DataHandler[T] {
	return &DataHandler[T]{
		Qm: qm,
		TableName: tableName,
		EndPoint: endPoint,
		DefaultWhere: defaultWhere,
		SelectOverwrite: overwriteSelect,
		Allowed: allows,
	}
}

// Create a new data handler which allows only GET requests

func (dh *DataHandler[T]) RegisterRoutes(mux *http.ServeMux, auth func(http.Handler) http.Handler, qm *tools.QueueManager) {
	mux.Handle(fmt.Sprintf("GET /%s", dh.EndPoint), auth(dh.HandleGet()))
	mux.Handle(fmt.Sprintf("PUT /%s", dh.EndPoint), auth(dh.HandleUpdate()))
	mux.Handle(fmt.Sprintf("POST /%s", dh.EndPoint), auth(dh.HandleAddNew()))
	mux.Handle(fmt.Sprintf("POST /%s-group", dh.EndPoint), auth(dh.HandleAddMultipleNew()))
	mux.Handle(fmt.Sprintf("DELETE /%s", dh.EndPoint), auth(dh.HandleDelete()))
}


// Add a new resource via the default path
func handleAddNewResource[T any](
	qm *tools.QueueManager,
	tableName string,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user_key := middleware.Contextkey("user")
		req_ip := tools.GetIP(r)
		req_id, err := tools.Generate32CharString()
		req_username := r.Context().Value(user_key).(*models.User).Username
		qm.Logger.Info("REQUEST_RECEIVED", "user", req_username, "IP", req_ip, "function", "Add New Resource", "table", tableName, "request_id", req_id)

		// Response intialization
		response := map[string]any{"task_type": "CREATE"}
		w.Header().Set("Content-Type", "application/json")

		var resource T
		err = json.NewDecoder(r.Body).Decode(&resource)

		// Validation and errors
		if err != nil {
			qm.Logger.Error("REQUEST_ERROR", "user", req_username, "IP", req_ip, "req_id", req_id, "function", "Add New Resource", "error", err)
			http.Error(w, fmt.Sprintf("Could not decode body. Error: %v", err), http.StatusInternalServerError)
			return
		}
		if tools.StructIsEmpty(&resource) {
			http.Error(w, "No valid json supplied", http.StatusBadRequest)
			qm.Logger.Error("REQUEST_ERROR", "user", req_username, "IP", req_ip, "req_id", req_id, "function", "Add New Resource", "error", "No valid json supplied")
			return
		}
		valid_resources, _ := tools.ValidateStruct(resource)
		if len(valid_resources) < 1 {
			qm.Logger.Error("REQUEST_ERROR", "user", req_username, "IP", req_ip, "req_id", req_id, "function", "Add New Resource", "error", "resource invalid", "resource", resource)
			http.Error(w, "No valid domains", http.StatusBadRequest)
			return
		}

		// Extract context and queue action
		ctx_preserve := context.WithoutCancel(r.Context())
		task_id, err := qm.QueueFunction(ctx_preserve, tools.SingleInsert(ctx_preserve, qm.Db, tableName, resource))

		if err != nil {
			qm.Logger.Error("TASK_ERROR", "user", req_username, "IP", req_ip, "req_id", req_id, "function", "Add New Resource", "error", "could not create task", "resource", resource, "error", err)
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
func handleAddMultipleNewResources[T any](
	qm *tools.QueueManager,
	tableName string,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user_key := middleware.Contextkey("user")
		req_ip := tools.GetIP(r)
		req_id, err := tools.Generate32CharString()
		req_username := r.Context().Value(user_key).(*models.User).Username
		qm.Logger.Info("REQUEST_RECEIVED", "user", req_username, "IP", req_ip, "function", "Add New Bulk Resource", "table", tableName, "request_id", req_id)

		// Response intialization
		response := map[string]any{"task_type": "CREATE_BULK"}
		w.Header().Set("Content-Type", "application/json")

		var resources []T
		err = json.NewDecoder(r.Body).Decode(&resources)

		// Validation and errors
		if err != nil {
			qm.Logger.Error("REQUEST_ERROR", "user", req_username, "IP", req_ip, "req_id", req_id, "function", "Add New Bulk Resource", "error", err)
			http.Error(w, fmt.Sprintf("Error decoding body: %v", err), http.StatusInternalServerError)
			return
		}
		if tools.StructIsEmpty(&resources) {
			qm.Logger.Error("REQUEST_ERROR", "user", req_username, "IP", req_ip, "req_id", req_id, "function", "Add New Bulk Resource", "error", "no valid json supplied")
			http.Error(w, "No valid json supplied", http.StatusBadRequest)
			return
		}

		valid_resources, invalid_resources := tools.ValidateMultiStruct(resources)
		if len(valid_resources) < 1 {
			qm.Logger.Error("REQUEST_ERROR", "user", req_username, "IP", req_ip, "req_id", req_id, "function", "Add New Bulk Resource", "error", "no valid resources", "valid_resources", valid_resources)
			http.Error(w, "No valid resources", http.StatusBadRequest)
			return
		}

		ctx_preserve := context.WithoutCancel(r.Context())
		task_id, err := qm.QueueFunction(ctx_preserve, tools.RecursiveBatchInsert(ctx_preserve, qm.Db, tableName, tools.ToAnySlice(valid_resources)))
		if err != nil {
			qm.Logger.Error("TASK_ERROR", "user", req_username, "IP", req_ip, "req_id", req_id, "function", "Add New Bulk Resource", "error", err)
			http.Error(w, fmt.Sprintf("Error creating bulk create task\nError: %v", err), http.StatusInternalServerError)
			return
		}

		// Respond
		response["task_id"] = task_id
		response["successful_submission"] = err == nil
		response["rows_received"] = len(resources)
		response["rows_valid"] = len(valid_resources)
		response["rows_invalid"] = len(invalid_resources)
		response["invalid"] = invalid_resources

		json.NewEncoder(w).Encode(response)
	}
}

// Get resources via the standard api
func handleGetResource[T interface{}](
	qm *tools.QueueManager,
	tableName string,
	defaultSelect []string,
	defaultWhere map[string]any,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user_key := middleware.Contextkey("user")
		req_ip := tools.GetIP(r)
		req_id, err := tools.Generate32CharString()
		req_username := r.Context().Value(user_key).(*models.User).Username
		qm.Logger.Info("REQUEST_RECEIVED", "user", req_username, "IP", req_ip, "function", "Get Resource", "table", tableName, "request_id", req_id)

		// Response intialization
		w.Header().Set("Content-Type", "application/json")

		var res_type T
		var query string

		// Create new query builder 
		qb := tools.NewQueryBuilder()

		// Load where vals
		for _, key := range defaultWhere {
			qb.SetWhereAbsolute(key.(string), defaultWhere[key.(string)])
		}
		if err := tools.SetWhereFromURL(qb, r, res_type); err != nil {
			qm.Logger.Error("REQUEST_ERROR", "user", req_username, "IP", req_ip, "req_id", req_id, "function", "Get Resource", "error", err)
			http.Error(w, "Error in parsing where clauses", http.StatusBadRequest)
		}

		// Build the query
		if defaultSelect == nil {
			query = qb.BuildSelect(tableName, tools.GetDatabaseColumns(res_type))
		} else {
			query = qb.BuildSelect(tableName, defaultSelect)
		}

		// Get the rows
		rows, err := qm.Db.Query(r.Context(), query, qb.GetArgs()...)

		if err != nil {
			qm.Logger.Error("GET_ERROR", "user", req_username, "IP", req_ip, "req_id", req_id, "function", "Get Resource", "error", err)
			http.Error(w, fmt.Sprintf("Error with the query:\n%v", err), http.StatusInternalServerError)
		}

		// Handle the rows
		defer rows.Close()
		response, err := pgx.CollectRows(rows, pgx.RowToStructByName[T])

		// Return
		json.NewEncoder(w).Encode(response)
	}
}

// Update resource via the standard api
func handleUpdateResource[T any](
	qm *tools.QueueManager,
	tableName string,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user_key := middleware.Contextkey("user")
		req_ip := tools.GetIP(r)
		req_id, err := tools.Generate32CharString()
		req_username := r.Context().Value(user_key).(*models.User).Username
		qm.Logger.Info("REQUEST_RECEIVED", "user", req_username, "IP", req_ip, "function", "Update Resource", "table", tableName, "request_id", req_id)

		// Response intialization
		response := map[string]any{"task_type": "UPDATE"}
		w.Header().Set("Content-Type", "application/json")

		var updated T
		err = json.NewDecoder(r.Body).Decode(&updated)

		if err != nil {
			qm.Logger.Error("REQUEST_ERROR", "user", req_username, "IP", req_ip, "req_id", req_id, "function", "Update Resource", "error", err)
			http.Error(w, fmt.Sprintf("Error decoding body: %v", err), http.StatusInternalServerError)
			return
		}

		// Check for valid values
		if tools.StructIsEmpty(&updated) {
			qm.Logger.Error("REQUEST_ERROR", "user", req_username, "IP", req_ip, "req_id", req_id, "function", "Update Resource", "error", "no valid json supplied")
			http.Error(w, "No valid updates", http.StatusBadRequest)
			return
		}

		// Create new query builder
		qb := tools.NewQueryBuilder()

		// Set values from the struct
		tools.SetValueFromStruct(qb, updated)

		// Build the query
		query := qb.BuildUpdate(tableName, r, updated)

		// Queue the query
		ctx_preserve := context.WithoutCancel(r.Context())
		task_id, err := qm.QueueExec(ctx_preserve, query, qb.GetArgs()...)
		if err != nil {
			qm.Logger.Error("TASK_ERROR", "user", req_username, "IP", req_ip, "req_id", req_id, "function", "Add New Bulk Resource", "error", err)
			http.Error(w, fmt.Sprintf("Error creating update task\nError: %v", err), http.StatusInternalServerError)
			return
		}

		// Build and return
		response["task_id"] = task_id
		response["successful_submission"] = err == nil

		json.NewEncoder(w).Encode(response)
	}
}

func handleDeleteResource[T any](
	qm *tools.QueueManager,
	tableName string,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user_key := middleware.Contextkey("user")
		req_ip := tools.GetIP(r)
		req_id, err := tools.Generate32CharString()
		req_username := r.Context().Value(user_key).(*models.User).Username
		qm.Logger.Info("REQUEST_RECEIVED", "user", req_username, "IP", req_ip, "function", "Delete Resource", "table", tableName, "request_id", req_id)

		// Response intialization
		response := map[string]any{"task_type": "DELETE"}
		w.Header().Set("Content-Type", "application/json")

		var resource_to_delete *T
		err = json.NewDecoder(r.Body).Decode(&resource_to_delete)

		// Error checking
		if err != nil {
			qm.Logger.Error("REQUEST_ERROR", "user", req_username, "IP", req_ip, "req_id", req_id, "function", "Delete Resource", "error", err)
			http.Error(w, "Error reading request body", http.StatusBadRequest)
			return
		}

		if tools.StructIsEmpty(resource_to_delete) {
			qm.Logger.Error("REQUEST_ERROR", "user", req_username, "IP", req_ip, "req_id", req_id, "function", "Delete Resource", "error", "no valid json supplied")
			http.Error(w, "No valid json in request body", http.StatusBadRequest)
			return
		}

		// Ensure the reosurce is valid for deletion
		valid_resources, _ := tools.ValidateStruct(resource_to_delete)
		if len(valid_resources) < 1 {
			qm.Logger.Error("REQUEST_ERROR", "user", req_username, "IP", req_ip, "req_id", req_id, "function", "Delete Resource", "error", "no valid resource to delete")
			http.Error(w, "Resource is invalid for deletion", http.StatusBadRequest)
			return
		}

		// Create query builder
		qb := tools.NewQueryBuilder()
		query := qb.BuildDelete(tableName, resource_to_delete)

		// Execute the query
		ctx_preserve := context.WithoutCancel(r.Context())
		task_id, err := qm.QueueExec(ctx_preserve, query, qb.GetArgs()...)
		if err != nil {
			qm.Logger.Error("TASK_ERROR", "user", req_username, "IP", req_ip, "req_id", req_id, "function", "Delete Resource", "error", err)
			http.Error(w, fmt.Sprintf("Error creating delete task\nError: %v", err), http.StatusInternalServerError)
			return
		}

		// Build and return
		response["task_id"] = task_id
		response["successful_submission"] = err == nil

		json.NewEncoder(w).Encode(response)
	}
}


// Handle a disallowed endPoint
func handleNotAllowed[T any](
	allowed map[string]bool,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var allowed_methods []string
		for key, allow := range allowed {
			if allow {
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


func (h *DataHandler[T]) HandleUpdate() http.HandlerFunc {
	if h.Allowed["ALL"] || h.Allowed["PUT"] {
		return handleUpdateResource[T](h.Qm, h.TableName)
	}
	return handleNotAllowed[T](h.Allowed)
}

func (h *DataHandler[T]) HandleGet() http.HandlerFunc {
	if h.Allowed["ALL"] || h.Allowed["GET"] {
		return handleGetResource[T](h.Qm, h.TableName, h.SelectOverwrite, h.DefaultWhere)
	}
	return handleNotAllowed[T](h.Allowed)
}

func (h *DataHandler[T]) HandleAddNew() http.HandlerFunc {
	if h.Allowed["ALL"] || h.Allowed["POST"] {
		return handleAddNewResource[T](h.Qm, h.TableName)
	}
	return handleNotAllowed[T](h.Allowed)
}

func (h *DataHandler[T]) HandleAddMultipleNew() http.HandlerFunc {
	if h.Allowed["ALL"] || h.Allowed["POST"] {
		return handleAddMultipleNewResources[T](h.Qm, h.TableName)
	}
	return handleNotAllowed[T](h.Allowed)
}

func (h *DataHandler[T]) HandleDelete() http.HandlerFunc {
	if h.Allowed["ALL"] || h.Allowed["DELETE"] {
		return handleDeleteResource[T](h.Qm, h.TableName)
	}
	return handleNotAllowed[T](h.Allowed)
}
