package middleware

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"lotusforge.au/api-server/models"
)

func validateAPIKey(ctx context.Context, db *pgxpool.Pool, username string, api_key string) (result *models.User, err error) {
	// Memory location to scan username into
	var user *models.User

	// Early validation
	if username == "" || api_key == "" {
		return nil, errors.New("Missing credentials")
	}

	// Query db
	qry := db.QueryRow(ctx, "SELECT username, email, mobile FROM users WHERE username = $1 and api_key = $2;", username, api_key).Scan(&user.Username, &user.Email, &user.Mobile)

	// validate
	if qry != nil {
		if qry == pgx.ErrNoRows {
			return nil, errors.New("Invalid credentials")
		}
		return nil, err
	}
	if user.Api_Access {
		return user, err
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
			ctx := SetUser(r.Context(), user)

			// Call the next function
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
