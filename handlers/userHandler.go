package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"lotusforge.au/api-server/middleware"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/tools"
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
	// Get vars
	var req models.UserInfoResponse
	username := r.Header.Get("X-Username")
	userAgent := r.Header.Get("User-Agent")
	origin := r.Header.Get("Origin")
	api_key := r.Header.Get("X-API-Key")

	h.logger.Debug("Extracted the following info at GetUserInfo:", "Username", username, "user agent", userAgent, "origin", origin, "X-API-Key", api_key)

	// Query database
	err := h.db.QueryRow(r.Context(),"SELECT username, email, mobile FROM users WHERE username = $1;", username).Scan(&req.Username, &req.Email, &req.Mobile)
	if err != nil {
		if err == pgx.ErrNoRows {
			h.logger.Debug("No rows returned", "function", "getUserInfo", "user", username, "useragent", userAgent, "origin", origin)
			http.Error(w, "GetUserInfo: Something went wrong!", http.StatusInternalServerError)
		}
		h.logger.Debug("Server error occured", "function", "getUserInfo", "user", username, "useragent", userAgent,"origin", origin)
		http.Error(w, "GetUserInfo: Something went wrong!", http.StatusInternalServerError)
	}
	h.logger.Debug("Success", "function", "getUserInfo", "user", username, "useragent", userAgent, "origin", origin)

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
	
	// Error checking
	if err != nil {
		http.Error(w, "No JSON sent with request", http.StatusBadRequest)
		return
	}
	if tools.StructIsEmpty(&user) {
		http.Error(w, "No valid Json in request", http.StatusBadRequest)
		return 
	}

	user.Username = &requester.Username
	qb := tools.NewQueryBuilder()

	// Get current user info
	var user_cur models.UserCreateUpdate
	err = h.db.QueryRow(r.Context(), qb.BuildSelect("users", []string{"email", "mobile"}), user.Username).Scan(&user_cur.Email, &user_cur.Mobile)

	if err != nil {
		http.Error(w, "Error occured during checking for current user data", http.StatusInternalServerError)
	}
	
	// Create query builder and compare old an new info
	middleware.CompareFirst(qb.SetValue, "email", user_cur.Email, user.Email)
	middleware.CompareFirst(qb.SetValue, "mobile", user_cur.Mobile, user.Mobile)

	//Check query actually has updated
	if qb.HasUpdates() {
		var cmdTag pgconn.CommandTag
		cmdTag, err = h.db.Exec(r.Context(), qb.BuildUpdate("users", r, models.UserCreateUpdate{}), qb.GetArgs()...)
		if err != nil {
		  http.Error(w, "Something went wrong in User Update!", http.StatusInternalServerError)
		}
		if cmdTag.Update() && cmdTag.RowsAffected() == 1 {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Success"))
		} else {
			http.Error(w, "Something went wrong! User update status: " + cmdTag.String(), http.StatusInternalServerError)
		}
	} else {
		http.Error(w, "No updates to make!", http.StatusExpectationFailed)
	}
}
