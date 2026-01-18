package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"lotusforge.au/api-server/middleware"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/tools"
)

// Add a new resource via the default path
func HandleCreateDiff[T any](
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
