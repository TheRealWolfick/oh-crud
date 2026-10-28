package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/alexedwards/argon2id"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pquerna/otp/totp"
	"lotusforge.au/api-server/httpx"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/secrets"
	"lotusforge.au/api-server/store"
)

type Auth struct {
	Auth      store.Services
	Limiter   *Limiter
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
		Limiter: newLimiter(10, 15*time.Minute),
		svr_conf: svr_conf,   // server config with the values auth uses.
		Replay: ReplayService(),
		lg: lg,
		ks: ks,
	}
}

// oauthError uses the standard OAuth error body. Keep messages generic:
// "invalid_grant" for every login failure, never "unknown user" vs "wrong password".
func oauthError(w http.ResponseWriter, code string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func unauthorized(w http.ResponseWriter) { http.Error(w, "unauthorized", http.StatusUnauthorized) }
func forbidden(w http.ResponseWriter)    { http.Error(w, "forbidden", http.StatusForbidden) }

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
		oauthError(w, "invalid_client", http.StatusUnauthorized)
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
		oauthError(w, "server_error", http.StatusInternalServerError)
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
			oauthError(w, "unsupported_grant_type", http.StatusBadRequest)
		}
	}
}


// Returns the openid-configuration for Services
func (a *Auth) OpenIDConfig() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := a.svr_conf.Get()
		res := map[string]any{
			"issuer": cfg.Issuer,
			"jwks_uri": cfg.Issuer+"/.well-known/jwks.json",
			"token_endpoint": cfg.Token_url,
			"authorization_endpoint": cfg.Issuer+"/authorize",
			"end_session_endpoint": cfg.Issuer+"/logout",
			"grant_types_supported": []string{
				"client_credentials", 
				//	"refresh_token", 
				//	"authorization_code",
				//	"urn:ietf:params:oauth:grant-type:token-exchange"
			},
			"token_endpoint_auth_methods_supported": []string{"private_key_jwt"},
			"id_token_signing_alg_values_supported": []string{"EdDSA"},
			"code_challenge_methods_supported": []string{"S256"},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(res)
	}
}

func (a *Auth) getUserByUsername(ctx context.Context,  username string) (*models.User, error) {
	// Text columns are nullable, so coalesce them to fit the plain string fields
	sql := `
	SELECT id, COALESCE(username, '') AS username, COALESCE(display_name, '') AS display_name,
		COALESCE(password_hash, '') AS password_hash, totp_secret, created_at, disabled_at,
		COALESCE(roles, '{}') AS roles
	FROM users WHERE username = $1 LIMIT 1;
	`

	rows, err := a.ks.db.Query(ctx, sql, username)
	if err != nil { return nil, err }

	return pgx.CollectOneRow(rows, pgx.RowToAddrOfStructByName[models.User])
}

// Swap the stored hash only if it hasn't changed since it was read, so a
// concurrent password change is never overwritten with the old password
func (a *Auth) updatePasswordHash(ctx context.Context, userID string, oldHash string, newHash string) error {
	sql := `UPDATE users SET password_hash = $1 WHERE id = $2 AND password_hash = $3`
	_, err := a.ks.db.Exec(ctx, sql, newHash, userID, oldHash)
	return err
}

// An auth handler for a password login
func (a *Auth) PasswordLogin() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := r.PostFormValue("username")
		password := r.PostFormValue("password")   // This should be a hash

		// Verify who is asking
		clientID := "cli"
		if raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
			svc, err := a.ks.Verify(raw)
			if err != nil || svc.TokenUse != "service" {
				unauthorized(w)
				return
			}
			clientID = strings.TrimPrefix(svc.Subject, "svc:")
		}

		// Ensure the request can be processed
		if !a.Limiter.Allow(clientID+":"+username) {
			oauthError(w, "slow_down", http.StatusTooManyRequests)
			return
		}

		// Get the user
		usr, err := a.getUserByUsername(r.Context(), username)
		
		// Use a dummy hash if no user was returned
		hash := secrets.DummyHash
		if err == nil && usr.PasswordHash != "" { hash = usr.PasswordHash }

		// Check if the hash matches the supplied password
		match, params, hashErr := argon2id.CheckHash(password, hash)

		// Handle false login TODO: implement logging for failed login
		if err != nil || hashErr != nil || !match { unauthorized(w); return }

		// A disabled account fails the same way as a wrong password
		if usr.DisabledAt != nil { unauthorized(w); return }

		amr := []string{"pwd"}
		if len(usr.TotpSecret) > 0 {
			secret, err := secrets.Decrypt(a.ks.master, usr.TotpSecret, []byte(usr.ID))
			if err != nil || !totp.Validate(r.PostFormValue("otp"), string(secret)) {
				oauthError(w, "invalid_grant", http.StatusUnauthorized)
				return
			} else {
				amr = append(amr, "otp")
			}
		}

		// Rehash if the stored hash used weaker params than we now require.
		// The password is already verified, so a failure here must not fail the login
		if secrets.NeedsRehash(params) {
			if h, err := secrets.HashPassword(password); err == nil {
				if err := a.updatePasswordHash(r.Context(), usr.ID, usr.PasswordHash, h); err != nil {
					a.lg.Warn("password rehash failed", "user", usr.ID, "error", err)
				}
			}
		}

		// issue the user token
		a.issueUserTokens(w, *r, usr, clientID, uuid.NewString(), amr)
	}
}


// Issue a user token to the user
func (a *Auth) issueUserTokens(
	w http.ResponseWriter, 
	r http.Request, 
	u *models.User, 
	clientID string, 
	family string, 
	amr []string) {
		// Create the access token
		access, err := a.ks.Issue(models.Claims{
			TokenUse: "user",
			Roles: u.Roles,
			SID: family,
			AMR: amr,
			RegisteredClaims: jwt.RegisteredClaims{Subject: u.ID},
		}, 15*time.Minute)

		// Handle the server failing to create the access issue
		if err != nil { http.Error(w, "server_error", http.StatusInternalServerError); return }

		// Create a new refresh token that the client can use
		refresh, err := a.newRefreshToken(r.Context(), u.ID, clientID, family)
		if err != nil { http.Error(w, "server_error", http.StatusInternalServerError); return }

		// Give the token back to the user
		httpx.WriteJSON_nostore(w, map[string]any{
			"access_token": access, "token_type": "Bearer", "expires_in": 900,
			"refresh_token": refresh,
		})
	}


	func (a *Auth) newRefreshToken(
		ctx context.Context,
		userID string,
		clientID string,
		family string,
	) (string, error) {
		// Create the key and randomize it
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil { return "", err }

		// Create the token and save it
		tok := "ohc_rt_"+base64.RawStdEncoding.EncodeToString(b)
		rt := models.RefreshToken{
			TokenHash: secrets.Sha256Hex(tok),
			UserID: userID,
			ClientID: clientID,
			FamilyID: family,
			ExpiresAt: time.Now().Add(14 * 24 * time.Hour),
		}

		// Save the token
		err := a.ks.SaveRefreshToken(ctx, &rt)

		// Return the unhashed token 
		return tok, err
	}
