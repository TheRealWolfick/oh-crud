package handlers

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/tools"
)

type sitesHandler struct {
	BaseHandler
}

func NewSitesHandler(logger *slog.Logger, log_level int, db *pgxpool.Pool) *sitesHandler {
	return &sitesHandler{
		BaseHandler: BaseHandler{
			logger: logger,
			log_level: log_level,
			db: db,
		},
	}
}


func (h *sitesHandler) AddNewDomain(w http.ResponseWriter, r *http.Request) {
  var domain models.Domain
	err := json.NewDecoder(r.Body).Decode(&domain)

	// Validation and errors
	if err != nil {
		http.Error(w, "Error decoding body", http.StatusBadRequest)
		return
	}
	if tools.StructIsEmpty(&domain) {
		http.Error(w, "No domain supplied", http.StatusBadRequest)
		return
	}
	valid_domains, _ := tools.ValidateStruct(domain)
	if len(valid_domains) < 1 {
		http.Error(w, "No valid domains", http.StatusBadRequest)
		return
	}

  suc, _ := tools.SingleInsert(r.Context(), h.db, "domains", valid_domains)

	// Validate Response
	if !suc {
		http.Error(w, fmt.Sprintf("Failed to insert domain '%s'", *domain.Domain_code), http.StatusInternalServerError)
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


// Add multiple domains at once
func (h *sitesHandler) AddMultiNewDomain(w http.ResponseWriter, r *http.Request) {
	var domains []models.Domain
	err := json.NewDecoder(r.Body).Decode(&domains)

	// Validation and errors
	if err != nil {
		http.Error(w, "Error decoding body", http.StatusBadRequest)
		return
	}
	if tools.StructIsEmpty(&domains) {
		http.Error(w, "No domains supplied", http.StatusBadRequest)
		return
	}
	
	valid_domains, invalid_domains := tools.ValidateMultiStruct(domains)
	if len(valid_domains) < 1 {
		http.Error(w, "No valid domains", http.StatusBadRequest)
		return
	}

	result := tools.RecursiveBatchInsert(r.Context(), h.db, "domains", tools.ToAnySlice(valid_domains))

	// Respond
	response := map[string]interface{}{
		"action":        "INSERT",
		"rows_received": len(domains),
		"rows_affected": result.SuccessCount,
		"invalid":       invalid_domains,
		"failed":        result.FailedItems,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}


// Select domains from DB
func (h *sitesHandler) GetDomains(w http.ResponseWriter, r *http.Request) {
	var domainsRequest []models.GetDomain

	// Create new query builder 
	qb := tools.NewBlankQueryBuilder()
	
	// Load where vals
	if err := tools.SetWhereFromURL(qb, r, models.GetDomain{}); err != nil {
		http.Error(w, "Error in parsing where clauses", http.StatusBadRequest)
	}
	
	// Build the query
	query := qb.BuildSelect("domains", tools.GetDatabaseColumns(models.GetDomain{}))

	// Get the rows
	rows, err := h.db.Query(r.Context(), query, qb.GetArgs()...)
	
	if err != nil {
		http.Error(w, fmt.Sprintf("Error with the query:\n%v", domainsRequest), http.StatusInternalServerError)
	}
	
	defer rows.Close()
	
	for rows.Next() {
		var domain models.GetDomain
		rows.Scan(&domain.Domain_code, &domain.Description)
		domainsRequest = append(domainsRequest, domain)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(domainsRequest)
}


// Update domain
func(h *sitesHandler) UpdateDomain(w http.ResponseWriter, r *http.Request) {
	var updatedDomain models.GetDomain
	err := json.NewDecoder(r.Body).Decode(&updatedDomain)
	
	if err != nil {
		http.Error(w, "Error reading request body", http.StatusBadRequest)
		return
	}

	// Check for valid values
	if tools.StructIsEmpty(&updatedDomain) {
		http.Error(w, "No valid updates", http.StatusBadRequest)
		return
	}

	// Create new query builder
	qb := tools.NewBlankQueryBuilder()

	// Set values from the struct
	tools.SetValueFromStruct(qb, updatedDomain)

	// Build the query
	query := qb.BuildUpdate("domains", r, updatedDomain)

	// Execute the query
	cmdtag, err := h.db.Exec(r.Context(), query, qb.GetArgs()...)
	if err != nil {
		http.Error(w, "Error with executing query to update domain", http.StatusInternalServerError)
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
