package handlers

import (
	"encoding/json"
	"net/http"

	"lotusforge.au/api-server/middleware"
)

func JWKSHandler(ks *middleware.KeyStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
    // Load the keys
		keys := ks.JWKS()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=300")
		json.NewEncoder(w).Encode(map[string]any{"keys": keys})
	}
}
