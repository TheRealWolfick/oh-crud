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
	
	res, _ := json.Marshal(domain)
	w.Write(res)
}
