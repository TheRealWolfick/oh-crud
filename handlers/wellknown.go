package handlers

import (
	"encoding/json"
	"net/http"

	"lotusforge.au/api-server/auth"
)

type KeystoreHandler struct {
	keystore *auth.KeyStore
}

func NewKeystoreHandler(ks *auth.KeyStore) *KeystoreHandler {
	return &KeystoreHandler{ keystore: ks }
}

func (ks *KeystoreHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ks.keystore.JWKS())
}
