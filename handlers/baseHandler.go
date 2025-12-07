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
