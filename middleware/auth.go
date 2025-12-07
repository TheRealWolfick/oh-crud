package middleware

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type contextkey string

const userContextKey contextkey = "user"

func validateAPIKey(ctx context.Context, db *pgxpool.Pool, username string, api_key string) (result string, err error) {
	// Memory location to scan username into
	var dbUsername string
	var dbAuthorized bool

	// Query db
	qry := db.QueryRow(ctx, "SELECT username, api_access FROM users WHERE username = $1 and api_key = $2;", username, api_key).Scan(&dbUsername, &dbAuthorized)

	// validate
	if qry != nil {
		if qry == pgx.ErrNoRows {
			return "", errors.New("invalid credentials")
		}
		return "", err
	}
	if dbAuthorized {
		return dbUsername, err
	}
	return "", errors.New("Unauthorized user")

}

func RequireAuth(db *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract info
			username := r.Header.Get("X-Username")
			api_key := r.Header.Get("X-API-Key")

			// Get the row
			user, err := validateAPIKey(r.Context(), db, username, api_key)			
			if err != nil {
				http.Error(w, err.Error(), http.StatusUnauthorized)
				return
			}

			// Add user context
			ctx := context.WithValue(r.Context(), userContextKey, user)

			// Call the next function
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
