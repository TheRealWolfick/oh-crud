package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"lotusforge.au/api-server/models"
)

func validateAPIKey(ctx context.Context, db *pgxpool.Pool, username string, api_key string) (result *models.User, err error) {
	// Early validation
	if username == "" || api_key == "" {
		return nil, errors.New("Missing credentials")
	}

	// Memory location to scan username into
	user := &models.User{}
	
	// Query db
	err = db.QueryRow(ctx, "SELECT username, email, mobile, api_access, roles FROM users WHERE username = $1 and api_key = $2;", username, api_key).Scan(&user.Username, &user.Email, &user.Mobile, &user.Api_Access, &user.Roles)

	// validate
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, errors.New("Invalid credentials")
		}
		return nil, err
	}
	if user.Api_Access {
		return user, nil
	}
	return nil, errors.New("Unauthorized user")
}


func RequireAuth(db *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract info
			username := r.Header.Get("X-Username")
			api_key := r.Header.Get("X-API-Key")
			var user *models.User

			// Get the row
			user, err := validateAPIKey(r.Context(), db, username, api_key)			
			if err != nil {
				http.Error(w, err.Error(), http.StatusUnauthorized)
				return

			}

			// Add user context
			ctx := SetUser(SetRoles(r.Context(), strings.Split(user.Roles, ",")), user)

			// Call the next function
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
