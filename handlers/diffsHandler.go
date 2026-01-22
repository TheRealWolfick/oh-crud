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
func handleCreateDiff[T any](
	qm *tools.QueueManager,
	tableName string,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user_key := middleware.Contextkey("user")
		req_ip := tools.GetIP(r)
		req_id, err := tools.Generate32CharString()
		req_username := r.Context().Value(user_key).(*models.User).Username
		note := r.Header.Get("X-User-Note")
		qm.Logger.Info("REQUEST_RECEIVED", "user", req_username, "IP", req_ip, "function", "Create Diff", "table", tableName, "request_id", req_id)

		// Response intialization
		response := map[string]any{"task_type": "CREATE"}
		w.Header().Set("Content-Type", "application/json")

		var resources []T
		err = json.NewDecoder(r.Body).Decode(&resources)

		// Validation and errors
		if err != nil {
			qm.Logger.Error("REQUEST_ERROR", "user", req_username, "IP", req_ip, "req_id", req_id, "function", "Create Diff", "error", err)
			http.Error(w, fmt.Sprintf("Could not decode body. Error: %v", err), http.StatusInternalServerError)
			return
		}
		if tools.StructIsEmpty(&resources) {
			http.Error(w, "No valid json supplied", http.StatusBadRequest)
			qm.Logger.Error("REQUEST_ERROR", "user", req_username, "IP", req_ip, "req_id", req_id, "function", "Create Diff", "error", "No valid json supplied")
			return
		}


		valid_resources, invalid_resources := tools.ValidateMultiStruct(resources)
		if len(valid_resources) < 1 {
			qm.Logger.Error("REQUEST_ERROR", "user", req_username, "IP", req_ip, "req_id", req_id, "function", "Create Diff", "error", "resource invalid", "resource", resources)
			http.Error(w, "No valid resources", http.StatusBadRequest)
			return
		}

		// Extract context and queue action
		ctx_preserve := context.WithoutCancel(middleware.StartTask(r.Context()))
		task_id, err := qm.QueueFunction(ctx_preserve, tools.CreateDiff(ctx_preserve, qm.Db, tableName, resources, note), note)

		if err != nil {
			qm.Logger.Error("TASK_ERROR", "user", req_username, "IP", req_ip, "req_id", req_id, "function", "Create Diff", "error", "could not create task", "resource", resources, "error", err)
			http.Error(w, fmt.Sprintf("Error creating create task\nError: %v", err), http.StatusInternalServerError)
			return
		}

		// Response
		response["task_id"] = task_id
		response["resources_count"] = len(resources)
		response["valid_resources"] = len(valid_resources)
		response["invalid_resources"] = len(invalid_resources)
		response["message"] = "successful submission of task"
		json.NewEncoder(w).Encode(response)
	}
}
