package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"

	"lotusforge.au/api-server/middleware"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/schematools"
	"lotusforge.au/api-server/tools"
)

func reviewTableChanges(
	gate *schematools.PendingApprovalGate,
	qm *tools.QueueManager,
	cfg *models.DataModel,
	svr_cfg *models.ServerConfig,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		task_type := "Review Pending Table Changes"
		function := "get"
		user_key := middleware.Contextkey("user")
		req_ip := tools.GetIP(r)
		req_id, _ := tools.Generate32CharString()
		req_username := r.Context().Value(user_key).(*models.User).Username
		log := qm.Logger.With("user", req_username, "IP", req_ip, "function", function, "task_type", task_type, "end_point", *cfg.End_point, "table", *cfg.Table_name, "request_id", req_id)
		ctx := middleware.SetLogger(r.Context(), log)

		log.Info("REQUEST_RECEIVED")

		// Response intialization
		response := map[string]any{"task_type": task_type}
		w.Header().Set("Content-Type", "application/json")

		// Check that a user is allowed to inteface with this command
		if !middleware.CheckUserHasAllowedRole(ctx, cfg.Admin_roles, svr_cfg) {
			log.Warn("REQUEST_UNAUTHORISED", "error", "user role does not have permission to administrate this end point")
			http.Error(w, "User role does not have access to administrate this end point", http.StatusUnauthorized)
			return
		}

		// Read the changes
		pending, has_changes := gate.Pending(*cfg.Table_name)
		response["table"] = *cfg.Table_name
		response["has_changes_pending"] = has_changes

		// Response
		if has_changes {
			response["pending"] = pending
		}

		json.NewEncoder(w).Encode(response)
	}
}


func approveTableChanges(
	qm *tools.QueueManager,
	cfg *models.DataModel,
	svr_cfg *models.ServerConfig,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		task_type := "Approve Pending Table Changes"
		function := "get"
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
		if !middleware.CheckUserHasAllowedRole(ctx, cfg.Admin_roles, svr_cfg) {
			log.Warn("REQUEST_UNAUTHORISED", "error", "user role does not have permission to administrate this end point")
			http.Error(w, "User role does not have access to administrate this end point", http.StatusUnauthorized)
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
			log.Error("REQUEST_ERROR", "error", "no approval key applied. Use {'approve': bool} in the request body")
			http.Error(w, "No valid json supplied", http.StatusBadRequest)
			return
		}

		approved, ok := raw["approve"]; if !ok {
			log.Error("REQUEST_ERROR", "error", "no approval key applied. Use {'approve': bool} in the request body")
			http.Error(w, "No valid json supplied", http.StatusBadRequest)
			return
		}

		// Response
		response["table"] = *cfg.Table_name
		response["approved"] = approved

		if (tools.BoolDeref(approved)) {
		} else {
		}
		response["message"] = "successful submission of task"
		json.NewEncoder(w).Encode(response)
	}
}
