package handlers

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"lotusforge.au/api-server/tools"
)

type genericDataHandler[T any] struct {
	BaseHandler
	TableName 	string
}

func NewGenericDataHandler[T any](logger *slog.Logger, log_level int, db *pgxpool.Pool, tableName string) *genericDataHandler[T] {
	return &genericDataHandler[T]{
		BaseHandler: BaseHandler{
			logger: logger,
			log_level: log_level,
			db: db,
		},
		TableName: tableName,
	}
}


// Add a new resource via the default path
func handleAddNewResource[T any](
	db *pgxpool.Pool,
	tableName string,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var resource T
		err := json.NewDecoder(r.Body).Decode(&resource)

		// Validation and errors
		if err != nil {
			http.Error(w, "Error decoding body", http.StatusBadRequest)
			return
		}
		if tools.StructIsEmpty(&resource) {
			http.Error(w, "No valid json supplied", http.StatusBadRequest)
			return
		}
		valid_resources, _ := tools.ValidateStruct(resource)
		if len(valid_resources) < 1 {
			http.Error(w, "No valid domains", http.StatusBadRequest)
			return
		}

		suc, _ := tools.SingleInsert(r.Context(), db, tableName, valid_resources)

		// Validate Response
		if !suc {
			http.Error(w, fmt.Sprintf("Failed to insert resource into '%s'", tableName), http.StatusInternalServerError)
			return
		}

		// Response
		response := map[string]interface{}{
			"action":        "INSERT",
			"rows_received": 1,
			"rows_affected": 1,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}
}


// Add multiple resources via the default path
func handleAddMultipleNewResources[T any](
	db *pgxpool.Pool,
	tableName string,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var resources []T
		err := json.NewDecoder(r.Body).Decode(&resources)

		// Validation and errors
		if err != nil {
			http.Error(w, "Error decoding body", http.StatusBadRequest)
			return
		}
		if tools.StructIsEmpty(&resources) {
			http.Error(w, "No valid json supplied", http.StatusBadRequest)
			return
		}

		valid_resources, invalid_resources := tools.ValidateMultiStruct(resources)
		if len(valid_resources) < 1 {
			http.Error(w, "No valid resources", http.StatusBadRequest)
			return
		}

		result := tools.RecursiveBatchInsert(r.Context(), db, tableName, tools.ToAnySlice(valid_resources))

		// Respond
		response := map[string]interface{}{
			"action":        "INSERT",
			"rows_received": len(resources),
			"rows_affected": result.SuccessCount,
			"invalid":       invalid_resources,
			"failed":        result.FailedItems,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}
}

// Get resources via the standard api
func handleGetResource[T interface{}](
	db *pgxpool.Pool,
	tableName string,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var res_type T

		// Create new query builder 
		qb := tools.NewBlankQueryBuilder()

		// Load where vals
		if err := tools.SetWhereFromURL(qb, r, res_type); err != nil {
			http.Error(w, "Error in parsing where clauses", http.StatusBadRequest)
		}

		// Build the query
		query := qb.BuildSelect(tableName, tools.GetDatabaseColumns(res_type))

		// Get the rows
		rows, err := db.Query(r.Context(), query, qb.GetArgs()...)

		if err != nil {
			http.Error(w, fmt.Sprintf("Error with the query:\n%v", err), http.StatusInternalServerError)
		}

		// Handle the rows
		defer rows.Close()
		resources, err := pgx.CollectRows(rows, pgx.RowToStructByName[T])

		// Return
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resources)
	}
}

// Update resource via the standard api
func handleUpdateResource[T any](
	db *pgxpool.Pool,
	tableName string,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var updated T
		err := json.NewDecoder(r.Body).Decode(&updated)

		if err != nil {
			http.Error(w, "Error reading request body", http.StatusBadRequest)
			return
		}

		// Check for valid values
		if tools.StructIsEmpty(&updated) {
			http.Error(w, "No valid updates", http.StatusBadRequest)
			return
		}

		// Create new query builder
		qb := tools.NewBlankQueryBuilder()

		// Set values from the struct
		tools.SetValueFromStruct(qb, updated)

		// Build the query
		query := qb.BuildUpdate(tableName, r, updated)

		// Execute the query
		cmdtag, err := db.Exec(r.Context(), query, qb.GetArgs()...)
		if err != nil {
			http.Error(w, "Error executing query", http.StatusInternalServerError)
			return
		}

		// Build and return
		response := map[string]interface{}{
			"action":        "UPDATE",
			"rows_affected": cmdtag.RowsAffected(),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}
}

func handleDeleteResource[T any](
	db *pgxpool.Pool,
	tableName string,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var resource_to_delete *T
		err := json.NewDecoder(r.Body).Decode(&resource_to_delete)

		// Error checking
		if err != nil {
			http.Error(w, "Error reading request body", http.StatusBadRequest)
			return
		}

		if tools.StructIsEmpty(resource_to_delete) {
			http.Error(w, "Error reading request body", http.StatusBadRequest)
			return
		}

		// Ensure the reosurce is valid for deletion
		valid_resources, _ := tools.ValidateStruct(resource_to_delete)
		if len(valid_resources) < 1 {
			http.Error(w, "Resource is invalid for deletion", http.StatusBadRequest)
			return
		}

		// Create query builder
		qb := tools.NewBlankQueryBuilder()
		query := qb.BuildDelete(tableName, resource_to_delete)

		// Execute the query
		cmdtag, err := db.Exec(r.Context(), query, qb.GetArgs()...)
		if err != nil {

			http.Error(w, "Error executing query", http.StatusInternalServerError)
			return
		}

		// Build and return
		response := map[string]interface{}{
			"action":        "DELETE",
			"rows_affected": cmdtag.RowsAffected(),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}
}


func (h *genericDataHandler[T]) HandleUpdate() http.HandlerFunc {
	return handleUpdateResource[T](h.db, h.TableName)
}

func (h *genericDataHandler[T]) HandleGet() http.HandlerFunc {
	return handleGetResource[T](h.db, h.TableName)
}

func (h *genericDataHandler[T]) HandleAddNew() http.HandlerFunc {
	return handleAddNewResource[T](h.db, h.TableName)
}

func (h *genericDataHandler[T]) HandleAddMultipleNew() http.HandlerFunc {
	return handleAddMultipleNewResources[T](h.db, h.TableName)
}

func (h *genericDataHandler[T]) HandleDelete() http.HandlerFunc {
	return handleDeleteResource[T](h.db, h.TableName)
}
