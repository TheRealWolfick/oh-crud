package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"lotusforge.au/api-server/middleware"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/tools"
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
	// Get vars
	var req models.UserInfoResponse
	username := r.Header.Get("X-Username")
	userAgent := r.Header.Get("User-Agent")
	origin := r.Header.Get("Origin")
	api_key := r.Header.Get("X-API-Key")

	if h.log_level >= 3 {
		h.logger.Info("Extracted the following info at GetUserInfo:", "Username", username, "user agent", userAgent, "origin", origin, "X-API-Key", api_key)
	}

	// Query database
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

	// Build return value
	to_ret, err := json.Marshal(req)
	if err != nil {
		h.logger.Error("Error decoding req in GetUserInfo", "error", err)
		http.Error(w, "Something went wrong!", http.StatusInternalServerError)
	}

	// return
	w.Write([]byte(string(to_ret)))
}

func (h *userHandler) UpdateUserInfo(w http.ResponseWriter, r *http.Request) {
	// Get vars
	var user models.UserCreateUpdate
	err := json.NewDecoder(r.Body).Decode(&user)
	
	// Get auth data - and copy username across
	requester, _ := middleware.GetUser(r.Context())
	user.Username = &requester.Username

	// Error checking
	if err != nil {
		http.Error(w, "No JSON sent with request", http.StatusBadRequest)
		return
	}
	if tools.StructIsEmpty(&user) {
		http.Error(w, "No valid Json in request", http.StatusBadRequest)
		return 
	}

	// Get current user info
	var user_cur models.UserCreateUpdate
	err = h.db.QueryRow(r.Context(), "SELECT email, mobile FROM users WHERE username = $1;", user.Username).Scan(&user_cur.Email, &user_cur.Mobile)

	if err != nil {
		http.Error(w, "Error occured during checking for current user data", http.StatusInternalServerError)
	}
	
	// Create query builder and compare old an new info
	qb := tools.NewQueryBuilder("username", user.Username)
	middleware.CompareFirst(qb.Set, "email", user_cur.Email, user.Email)
	middleware.CompareFirst(qb.Set, "mobile", user_cur.Mobile, user.Mobile)

	if !qb.HasUpdates() {
		http.Error(w, "No updates to make!", http.StatusExpectationFailed)
	}
	w.Write([]byte(qb.BuildUpdate("users")))
	// Check query actually has updated
	//if qb.HasUpdates() {
	//	err = h.db.QueryRow(r.Context(), qb.BuildUpdate("users"), qb.GetArgs()...)
	//}


	// Query database
}
