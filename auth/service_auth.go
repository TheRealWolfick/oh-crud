package auth

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"lotusforge.au/api-server/httpx"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/store"
)

type Auth struct {
	Auth      store.Services
	svr_conf  *models.SwappableServerConfig
	Replay    models.ReplayCache
	lg        *slog.Logger
	ks        *KeyStore
}

type ReplayMemory struct {
	mu   sync.RWMutex
	seen map[string]time.Time
}

// Returns a new authhandler object for middleware authentication handling
func AuthHandler(db *pgxpool.Pool, svr_conf *models.SwappableServerConfig, lg *slog.Logger, ks *KeyStore) *Auth {
	return &Auth{
		Auth: *store.NewServices(db),
		svr_conf: svr_conf,   // server config with the values auth uses.
		Replay: ReplayService(),
		lg: lg,
		ks: ks,
	}
}

// Reads a service assertion and confirms that the service is who it states it is
func (a *Auth) AuthenticateService(r *http.Request) (*models.Service, error) {
	assertion_type := "urn:ietf:params:oauth:client-assertion-type:jwt-bearer"

	// Ensure that the assertion type is the supported method
	if r.PostFormValue("client_assertion_type") != assertion_type {
		return nil, errors.New("invalid client auth method")
	}

	// Init the service variable and generate a new jwt
	var svc *models.Service
	var kid string
	c := &jwt.RegisteredClaims{}

	// Parse the client assertion
	_, err := jwt.ParseWithClaims(r.PostFormValue("client_assertion"), c,
	func(t *jwt.Token) (any, error) {
		kid, _ = t.Header["kid"].(string)
		iss, _ := t.Claims.GetIssuer()

		// Use the kid and iss to get the service from the DB. Error if revoked or invalid.
		key, s, err := a.Auth.ServicePublicKey(r.Context(), iss, kid)
		if err != nil { return nil, err }

		svc = s
		return ed25519.PublicKey(key), nil
	},
	jwt.WithValidMethods([]string{"EdDSA"}),
	jwt.WithAudience(a.svr_conf.Get().Token_url),
	jwt.WithExpirationRequired(),
	jwt.WithIssuedAt(),
	jwt.WithLeeway(30*time.Second))

	// Return error if key and service wasn't extracted
	if err != nil { return nil, err }

	// Checks that the jwt is valid
	switch {
	case c.Issuer != c.Subject:
		return nil, errors.New("service issuer and subject was not the same")
	case c.IssuedAt == nil || c.ID == "":
		return nil, errors.New("iat and jti are both required")
	case c.ExpiresAt.Sub(c.IssuedAt.Time) > 5*time.Minute:
		return nil, errors.New("assertion lifetime is too long")
	case !a.Replay.Remember(c.ID, c.ExpiresAt.Time.Add(30*time.Second)):
		return nil, errors.New("jti already seen")
	}
	
	// Touch the key
	go a.Auth.TouchServiceKey(context.WithoutCancel(r.Context()), kid)

	// The token at this point is valid
	return svc, nil
}

// Read the client credentials from the database
func (a *Auth) ClientCredentials(w http.ResponseWriter, r *http.Request) {
	// Check it is a valid client
	svc, err := a.AuthenticateService(r)
	cfg := a.svr_conf.Get()

	// Handle an invalid client
	if err != nil { 
		a.lg.Warn("invalid client attempted to connect", "error", err)
		oathError(w, "invalid_client", http.StatusUnauthorized)
		return
	}

	// Issue a new token to the service
	signed, err := a.ks.Issue(models.Claims{
		TokenUse: "service",
		Roles: []string{svc.Role},
		RegisteredClaims: jwt.RegisteredClaims{Subject: "svc:"+svc.ID},
	}, cfg.Token_lifetime)

	// Handle a key that wasn't signed
	if err != nil {
		a.lg.Warn("client was valid but error occured signing the key", "error", err)
		oathError(w, "server_error", http.StatusInternalServerError)
		return
	}

	// Write the signed token back to the client
	httpx.WriteJSON_nostore(w, map[string]any{"access_token": signed, "token_type": "Bearer", "expires_in": int(cfg.Token_lifetime.Seconds())})
}


// A standin function to serve as an in memory redis database until an actual
// redis instance is implemented
func ReplayService() *ReplayMemory {
	return &ReplayMemory{ seen: map[string]time.Time{} }
}

// Remember a jti for x time
func (r *ReplayMemory) Remember(jti string, until time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Check if the key has already been seen
	now := time.Now()
	if exp, ok := r.seen[jti]; ok && now.Before(exp) { return false }

	// Remove old jti entries, capping growth to only those within the last valid timeframe
	for k, exp := range r.seen {
		if now.After(exp) { delete(r.seen, k) }
	}

	// Remember the current key
	r.seen[jti] = until
	return true
}


// The token endpoint that wraps the various pathways a request can proceed
func (a *Auth) TokenEndpoint() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Process token route
		switch r.PostFormValue("grant_type") {
		case "client_credentials":
			a.ClientCredentials(w, r)
		default:
			a.OathError(w, r)
		}
	}
}


// A wrapper for the standard OathError
func (a *Auth) OathError(w http.ResponseWriter, r *http.Request) {
	oathError(w, "unsupported_grant_type", http.StatusBadRequest)
}

func oathError(w http.ResponseWriter, code string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": code})
}
