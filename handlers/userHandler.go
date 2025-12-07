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

func NewUserHandler(logger *slog.Logger, db *pgxpool.Pool) *userHandler {
	return &userHandler{
		BaseHandler: BaseHandler{
			logger: logger,
			db: db,
		},
	}
}

func (h *userHandler) GetUserInfo(w http.ResponseWriter, r *http.Request) {
	var req models.UserInfoResponse
	username := r.Header.Get("X-Username")

	err := h.db.QueryRow(r.Context(),"SELECT username, email, mobile FROM users WHERE username = $1;", username).Scan(&req.Username, &req.Email, &req.Mobile)
	if err != nil {
		if err == pgx.ErrNoRows {
			h.logger.Error("No rows returned", "function", "getUserInfo", "user", username, "useragent", r.Header.Get("User-Agent"), "origin", r.Header.Get("Origin"))
			http.Error(w, "GetUserInfo: Something went wrong!", http.StatusInternalServerError)
		}
		h.logger.Error("Server error occured", "function", "getUserInfo", "user", username, "useragent", r.Header.Get("User-Agent"), "origin", r.Header.Get("Origin"))
		http.Error(w, "GetUserInfo: Something went wrong!", http.StatusInternalServerError)
	}
	h.logger.Info("Success", "function", "getUserInfo", "user", username, "useragent", r.Header.Get("User-Agent"), "origin", r.Header.Get("Origin"))
}

