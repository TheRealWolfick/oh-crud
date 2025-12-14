package handlers

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
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
  
}
