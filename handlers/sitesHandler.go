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
	}
	if tools.StructIsEmpty(&domain) {
		http.Error(w, "No domain supplied", http.StatusBadRequest)
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
