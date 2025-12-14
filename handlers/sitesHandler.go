package handlers

import (
	"encoding/json"
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

	// Build the query
	qb := tools.NewQueryBuilder("domain_code", domain.Domain_code)
	qb.Set("domain_code", domain.Domain_code)
	qb.Set("wrong", nil)
	test := qb.BuildInsert("domains", domain)
	
	res, _ := json.Marshal(test)
	w.Write(res)
}

func (h *sitesHandler) AddMultiNewDomain(w http.ResponseWriter, r *http.Request) {
	var domains []models.Domain

	err := json.NewDecoder(r.Body).Decode(&domains)

	// Validation and errors
	if err != nil {
		http.Error(w, "Error decoding body", http.StatusBadRequest)
		return
	}
	if tools.StructIsEmpty(&domains) {
		http.Error(w, "No domain supplied", http.StatusBadRequest)
		return
	}

	// Build the query
	vals, vals_success := tools.ExtractValueFromMultiStruct("Domain_code", domains)
	if !vals_success {
		http.Error(w, "No proper values supplied in array", http.StatusBadRequest)
		return
	}
	qb := tools.NewQueryBuilder("domain_code", vals)
	test := qb.BuildMultiInsert("domains", tools.ToAnySlice(domains))

	// Insert into database
	

	
	// Response
	res, _ := json.Marshal(test)
	w.Write(res)
}
