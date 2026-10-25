package handlers

import (
	"encoding/json"
	"net/http"
)

// oauthError uses the standard OAuth error body. Keep messages generic:
// "invalid_grant" for every login failure, never "unknown user" vs "wrong password".
func oauthError(w http.ResponseWriter, code string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func unauthorized(w http.ResponseWriter) { http.Error(w, "unauthorized", http.StatusUnauthorized) }
func forbidden(w http.ResponseWriter)    { http.Error(w, "forbidden", http.StatusForbidden) }
