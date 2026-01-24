package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
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
		task_type := "Create Diff"
		user_key := middleware.Contextkey("user")
		req_ip := tools.GetIP(r)
		req_id, err := tools.Generate32CharString()
		req_username := r.Context().Value(user_key).(*models.User).Username
		note := r.Header.Get("X-User-Note")
		qm.Logger.Info("REQUEST_RECEIVED", "user", req_username, "IP", req_ip, "function", task_type, "table", tableName, "request_id", req_id)

		// Response intialization
		response := map[string]any{"task_type": "CREATE"}
		w.Header().Set("Content-Type", "application/json")

		var resources []T
		err = json.NewDecoder(r.Body).Decode(&resources)

		// Validation and errors
		if err != nil {
			qm.Logger.Error("REQUEST_ERROR", "user", req_username, "IP", req_ip, "req_id", req_id, "function", task_type, "error", err)
			http.Error(w, fmt.Sprintf("Could not decode body. Error: %v", err), http.StatusInternalServerError)
			return
		}
		if tools.StructIsEmpty(&resources) {
			http.Error(w, "No valid json supplied", http.StatusBadRequest)
			qm.Logger.Error("REQUEST_ERROR", "user", req_username, "IP", req_ip, "req_id", req_id, "function", task_type, "error", "No valid json supplied")
			return
		}


		valid_resources, invalid_resources := tools.ValidateMultiStruct(resources)
		if len(valid_resources) < 1 {
			qm.Logger.Error("REQUEST_ERROR", "user", req_username, "IP", req_ip, "req_id", req_id, "function", task_type, "error", "resource invalid", "resource", resources)
			http.Error(w, "No valid resources", http.StatusBadRequest)
			return
		}

		// Extract context and queue action
		ctx_preserve := context.WithoutCancel(middleware.StartTask(r.Context(), task_type))
		task_id, err := qm.QueueFunction(ctx_preserve, tools.CreateDiff(ctx_preserve, qm.Db, tableName, resources, note), note)

		if err != nil {
			qm.Logger.Error("TASK_ERROR", "user", req_username, "IP", req_ip, "req_id", req_id, "function", task_type, "error", "could not create task", "resource", resources, "error", err)
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

func handleActionDiff[T any](
	qm *tools.QueueManager,
	tableName string,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Variables
		task_type := "Create Diff"
		user_key := middleware.Contextkey("user")
		req_ip := tools.GetIP(r)
		req_id, _ := tools.Generate32CharString()
		req_username := r.Context().Value(user_key).(*models.User).Username

		qm.Logger.Info("REQUEST_RECEIVED", "user", req_username, "IP", req_ip, "function", task_type, "table", "diffs", "type", tableName, "request_id", req_id)

		checksum := tools.GetChecksum(r)
		if checksum == "" {
			qm.Logger.Info("BAD_REQUEST", "user", req_username, "IP", req_ip, "function", task_type, "table", "diffs", "type", tableName, "request_id", req_id, "error", "Checksum was not provided")
			http.Error(w, "Checksum was not provided", http.StatusBadRequest)
			return
		}

		var supplied_add []T
		var stored_add []T
		var sync_to_stored []T
		var sync_to_supplied []T
		var diff models.Diff[T]
		var batch_code models.Read_Batch_Code

		// Get item from diffs table
		row, err := qm.Db.Query(r.Context(), `SELECT * FROM diffs WHERE diff_type = $1 AND checksum = $2;`, tableName, checksum)
		diff, err = pgx.CollectOneRow(row, pgx.RowToStructByName[models.Diff[T]])
		 
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				http.Error(w, "Invalid checksum provided", http.StatusBadRequest)
				qm.Logger.Info("BAD_CHECKSUM", "user", req_username, "IP", req_ip, "function", task_type, "table", "diffs", "type", tableName, "request_id", req_id, "error", "Checksum was invalid")
				return
			}
			http.Error(w, "Error with reading the diff", http.StatusInternalServerError)
			qm.Logger.Error("DATA_READ_ERROR", "user", req_username, "IP", req_ip, "function", task_type, "table", "diffs", "type", tableName, "request_id", req_id, "error", err)
			return
		}

		// Create batch number and get batch code
		row2 := qm.Db.QueryRow(r.Context(), `SELECT generate_batch_num($1, $2, $3)`, req_username, tableName, checksum)
		err = row2.Scan(&batch_code.BatchCode)
		if err != nil {
			http.Error(w, "Error generating batch code", http.StatusInternalServerError)
			qm.Logger.Error("BATCH_CODE_ERROR", "user", req_username, "IP", req_ip, "function", task_type, "table", "diffs", "type", tableName, "request_id", req_id, "error", err)
			return
		}

		// Copy diff segments from diff to individual arrays to work with
		stored_add = diff.MissingFromStored      // Items that exist in supplied but not in stored
		supplied_add = diff.MissingFromSupplied  // Items that exist in stored but not in supplied

		// Process diffs into sync arrays
		// sync_to_stored: updates that would sync stored to match supplied
		// sync_to_supplied: updates that would sync supplied to match stored
		for _, item_diff := range diff.Diffs {
			if item_diff.Supplied != nil {
				sync_to_stored = append(sync_to_stored, *item_diff.Supplied)
			}
			if item_diff.Stored != nil {
				sync_to_supplied = append(sync_to_supplied, *item_diff.Stored)
			}
		}

		// Prepare response structure
		response := map[string]any{
			"batch_code": batch_code.BatchCode,
			"missing_from_supplied": supplied_add,
			"missing_from_stored": stored_add,
			"sync_to_stored":   sync_to_stored,    // Updates to make stored match supplied
			"sync_to_supplied": sync_to_supplied,  // Updates to make supplied match stored
		}

		// Set response headers
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		// Encode and send response
		if err := json.NewEncoder(w).Encode(response); err != nil {
			qm.Logger.Error("RESPONSE_ENCODE_ERROR", "user", req_username, "IP", req_ip, "function", task_type, "table", "diffs", "type", tableName, "request_id", req_id, "error", err)
			return
		}
	}
}

