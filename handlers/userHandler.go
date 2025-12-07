package handlers

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"lotusforge.au/api-server/models"
)

type userHandler struct {
	BaseHandler
}

func NewUserHandler(logger *slog.Logger, log_level int, db *pgxpool.Pool) *userHandler {
	return &userHandler{
		BaseHandler: BaseHandler{
			logger: logger,
			log_level: log_level,
			db: db,
		},
	}
}

func (h *userHandler) GetUserInfo(w http.ResponseWriter, r *http.Request) {
	var req models.UserInfoResponse
	username := r.Header.Get("X-Username")
	userAgent := r.Header.Get("User-Agent")
	origin := r.Header.Get("Origin")
	api_key := r.Header.Get("X-API-Key")

	if h.log_level >= 3 {
		h.logger.Info("Extracted the following info at GetUserInfo:", "Username", username, "user agent", userAgent, "origin", origin, "X-API-Key", api_key)
	}

	err := h.db.QueryRow(r.Context(),"SELECT username, email, mobile FROM users WHERE username = $1;", username).Scan(&req.Username, &req.Email, &req.Mobile)
	if err != nil {
		if err == pgx.ErrNoRows {
			if h.log_level >= 1 { h.logger.Error("No rows returned", "function", "getUserInfo", "user", username, "useragent", userAgent, "origin", origin) }
			http.Error(w, "GetUserInfo: Something went wrong!", http.StatusInternalServerError)
		}
		if h.log_level >= 1 { h.logger.Error("Server error occured", "function", "getUserInfo", "user", username, "useragent", userAgent,"origin", origin) }
		http.Error(w, "GetUserInfo: Something went wrong!", http.StatusInternalServerError)
	}
	if h.log_level >= 1 { h.logger.Info("Success", "function", "getUserInfo", "user", username, "useragent", userAgent, "origin", origin) }
}

