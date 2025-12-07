package handlers

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

type BaseHandler struct {
	logger *slog.Logger
	db *pgxpool.Pool
}
