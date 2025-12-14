package handlers

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

type BaseHandler struct {
	logger *slog.Logger
	log_level int
	db *pgxpool.Pool
}

func NewBaseHandler(logger *slog.Logger, log_level int, db *pgxpool.Pool) *BaseHandler {
	return &BaseHandler{
		logger: logger,
		log_level: log_level,
		db: db,
	}
}
