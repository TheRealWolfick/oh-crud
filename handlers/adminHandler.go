package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"lotusforge.au/api-server/middleware"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/schematools"
	"lotusforge.au/api-server/tools"
)

// adminAuthorised runs the shared admin preamble: builds a request logger, checks the
// caller holds one of the model's admin roles, and returns the request context. The
// bool is false when the request has already been rejected (caller should return).
func adminAuthorised(
	w http.ResponseWriter,
	r *http.Request,
	qm *tools.QueueManager,
	cfg *models.DataModel,
	svr_cfg *models.SwappableServerConfig,
	task_type string,
	function string,
) (context.Context, bool) {
	user_key := middleware.Contextkey("user")
	req_ip := tools.GetIP(r)
	req_id, _ := tools.Generate32CharString()
	req_username := r.Context().Value(user_key).(*models.User).Username
	log := qm.Logger.With("user", req_username, "IP", req_ip, "function", function, "task_type", task_type, "end_point", *cfg.End_point, "table", *cfg.Table_name, "request_id", req_id)
	ctx := middleware.SetLogger(r.Context(), log)

	log.Info("REQUEST_RECEIVED")
	w.Header().Set("Content-Type", "application/json")

	if !middleware.CheckUserHasAllowedRole(ctx, cfg.Admin_roles, svr_cfg.Get()) {
		log.Warn("REQUEST_UNAUTHORISED", "error", "user role does not have permission to administrate this end point")
		http.Error(w, "User role does not have access to administrate this end point", http.StatusUnauthorized)
		return ctx, false
	}
	return ctx, true
}

// handlePendingChanges lists the pending destructive change for this table (if any),
// plus the set of all tables currently awaiting approval.
func handlePendingChanges(
	gate *schematools.PendingApprovalGate,
	qm *tools.QueueManager,
	cfg *models.DataModel,
	svr_cfg *models.SwappableServerConfig,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, ok := adminAuthorised(w, r, qm, cfg, svr_cfg, "Review Pending Table Changes", "get")
		if !ok {
			return
		}

		pending, has_changes := gate.Pending(*cfg.Table_name)

		response := map[string]any{
			"task_type":           "Review Pending Table Changes",
			"table":               *cfg.Table_name,
			"has_changes_pending": has_changes,
			"all_pending_tables":  gate.PendingTables(),
		}
		if has_changes {
			response["pending"] = map[string]any{
				"table":    pending.Table,
				"changes":  pending.Changes,
				"approved": pending.Approved,
			}
		}
		json.NewEncoder(w).Encode(response)
	}
}

// handleApproveChange approves or denies the pending change for this table.
//
//	{"approve": true}  → mark approved and attempt to commit every approved change in
//	                     one combined apply. If an approved change depends on a still-
//	                     pending one, the apply fails and the change stays queued.
//	{"approve": false} → discard the change and revert the config file to last-good.
func handleApproveChange(
	gate *schematools.PendingApprovalGate,
	qm *tools.QueueManager,
	cfg *models.DataModel,
	svr_cfg *models.SwappableServerConfig,
	modelRegistry *tools.ModelRegistry,
	onApplied func(*models.DataModel),
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, ok := adminAuthorised(w, r, qm, cfg, svr_cfg, "Approve Pending Table Changes", "post")
		if !ok {
			return
		}
		log := qm.Logger
		table := *cfg.Table_name

		var raw map[string]any
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			http.Error(w, fmt.Sprintf("Could not decode body: %v", err), http.StatusBadRequest)
			return
		}
		approveRaw, present := raw["approve"]
		if !present {
			http.Error(w, fmt.Sprintf("Missing 'approve' key. Use {\"approve\": true|false}. Body received: {%s}", raw), http.StatusBadRequest)
			return
		}

		if _, has := gate.Pending(table); !has {
			http.Error(w, fmt.Sprintf("No pending change for table %q", table), http.StatusNotFound)
			return
		}

		response := map[string]any{"task_type": "Approve Pending Table Changes", "table": table}

		if tools.BoolDeref(approveRaw) {
			// Approve, then attempt to commit the whole approved set.
			gate.SetApproved(table)
			pool, okPool := qm.Db.(*pgxpool.Pool)
			if !okPool {
				http.Error(w, "Database pool unavailable", http.StatusInternalServerError)
				return
			}
			if err := schematools.ApplyApproved(ctx, pool, modelRegistry.All(), log, gate, onApplied); err != nil {
				// Kept approved and queued — likely waiting on an interdependent change.
				log.Warn("Approved change not yet applied", "table", table, "error", err)
				response["approved"] = true
				response["applied"] = false
				response["message"] = err.Error()
				w.WriteHeader(http.StatusAccepted)
				json.NewEncoder(w).Encode(response)
				return
			}
			response["approved"] = true
			response["applied"] = true
			response["message"] = "approved changes applied"
		} else {
			// Deny → revert the config file to its last-good snapshot.
			if err := schematools.RevertChange(gate, table, log); err != nil {
				http.Error(w, fmt.Sprintf("Failed to revert change: %v", err), http.StatusInternalServerError)
				return
			}
			response["approved"] = false
			response["applied"] = false
			response["message"] = "change denied and config reverted to last-good"
		}

		json.NewEncoder(w).Encode(response)
	}
}
