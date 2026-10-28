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
	"slices"
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

	// Writes made by the service are recorded against its own user row
	if err := a.ensureServiceUser(r.Context(), svc.ID); err != nil {
		a.lg.Error("could not create the user row for a service", "service", svc.ID, "error", err)
		oauthError(w, "server_error", http.StatusInternalServerError)
		return
	}

	// Issue a new token to the service
	signed, err := a.ks.Issue(models.Claims{
		TokenUse: "service",
		Roles: []string{svc.Role},
		RegisteredClaims: jwt.RegisteredClaims{Subject: serviceUserPrefix+svc.ID},
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


// serviceUserPrefix starts the subject of service tokens, and the id and username of
// the user row each service writes as
const serviceUserPrefix = "svc:"

// ensureServiceUser gives a service a user row to record its writes against, since
// history, diffs and events reference users.username. The row has no password or
// roles, so it can never log in or be used to widen access.
func (a *Auth) ensureServiceUser(ctx context.Context, svcID string) error {
	sql := `INSERT INTO users (id, username, display_name, roles) VALUES ($1, $1, $2, '{}') ON CONFLICT DO NOTHING`
	_, err := a.ks.db.Exec(ctx, sql, serviceUserPrefix+svcID, svcID)
	return err
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
		case "password":
			a.PasswordLogin(w, r)
		case "refresh_token":
			a.RefreshGrant(w, r)
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
				"password",
				"refresh_token",
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

// Text columns are nullable, so coalesce them to fit the plain string fields
const userColumns = `
	id, COALESCE(username, '') AS username, COALESCE(display_name, '') AS display_name,
	COALESCE(password_hash, '') AS password_hash, totp_secret, created_at, disabled_at,
	COALESCE(roles, '{}') AS roles`

func (a *Auth) getUser(ctx context.Context, where string, arg string) (*models.User, error) {
	rows, err := a.ks.db.Query(ctx, `SELECT `+userColumns+` FROM users WHERE `+where+` = $1 LIMIT 1`, arg)
	if err != nil { return nil, err }

	return pgx.CollectOneRow(rows, pgx.RowToAddrOfStructByName[models.User])
}

func (a *Auth) getUserByUsername(ctx context.Context, username string) (*models.User, error) {
	return a.getUser(ctx, "username", username)
}

func (a *Auth) getUserByID(ctx context.Context, id string) (*models.User, error) {
	return a.getUser(ctx, "id", id)
}

// Swap the stored hash only if it hasn't changed since it was read, so a
// concurrent password change is never overwritten with the old password
func (a *Auth) updatePasswordHash(ctx context.Context, userID string, oldHash string, newHash string) error {
	sql := `UPDATE users SET password_hash = $1 WHERE id = $2 AND password_hash = $3`
	_, err := a.ks.db.Exec(ctx, sql, newHash, userID, oldHash)
	return err
}

// The password grant for a user login. The client must assert who it is.
func (a *Auth) PasswordLogin(w http.ResponseWriter, r *http.Request) {
	username := r.PostFormValue("username")
	password := r.PostFormValue("password")   // This should be a hash

	// Verify which client is asking
	svc, err := a.AuthenticateService(r)
	if err != nil {
		a.lg.Warn("invalid client attempted a password login", "error", err)
		oauthError(w, "invalid_client", http.StatusUnauthorized)
		return
	}
	clientID := svc.ID

	// Ensure the request can be processed
	if !a.Limiter.Allow(clientID+":"+username) {
		oauthError(w, "slow_down", http.StatusTooManyRequests)
		return
	}

	// Get the user
	usr, err := a.getUserByUsername(r.Context(), username)
	
	// Use a dummy hash if there is no user, or the user has no password (such as one
	// who signs in through Entra), so every failure takes as long as a real check
	hasPassword := err == nil && usr.PasswordHash != ""
	hash := secrets.DummyHash
	if hasPassword { hash = usr.PasswordHash }

	// Check if the hash matches the supplied password
	match, params, hashErr := argon2id.CheckHash(password, hash)

	// Handle false login. A match against the dummy never counts.
	// TODO: implement logging for failed login
	if !hasPassword || hashErr != nil || !match { unauthorized(w); return }

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
	a.issueUserTokens(w, r, usr, clientID, uuid.NewString(), amr, refreshExpiry(amr, nil))
}


const (
	// amrFederated marks a login made through an external IdP such as Entra
	amrFederated = "fed"

	// Password sessions slide: each rotation extends them by the full lifetime
	refreshLifetime = 14 * 24 * time.Hour
	// External IdP sessions are capped from login and never extended, so the user goes
	// back to the IdP daily and an account disabled there is noticed within a day
	federatedRefreshLifetime = 24 * time.Hour
)

// refreshExpiry returns when a session's next refresh token expires. current is the
// token being rotated, or nil for a new login.
func refreshExpiry(amr []string, current *models.RefreshToken) time.Time {
	if !slices.Contains(amr, amrFederated) { return time.Now().Add(refreshLifetime) }
	if current != nil { return current.ExpiresAt }
	return time.Now().Add(federatedRefreshLifetime)
}

// Issue a user token to the user
func (a *Auth) issueUserTokens(
	w http.ResponseWriter,
	r *http.Request,
	u *models.User, 
	clientID string, 
	family string, 
	amr []string,
	refreshExpires time.Time,
) {
	// Create the access token
	access, err := a.ks.Issue(models.Claims{
		TokenUse: "user",
		Roles: u.Roles,
		SID: family,
		AMR: amr,
		Username: u.Username,
		ClientID: clientID,
		RegisteredClaims: jwt.RegisteredClaims{Subject: u.ID},
	}, 15*time.Minute)

	// Handle the server failing to create the access issue
	if err != nil { http.Error(w, "server_error", http.StatusInternalServerError); return }

	// Create a new refresh token that the client can use
	refresh, err := a.newRefreshToken(r.Context(), u.ID, clientID, family, amr, refreshExpires)
	if errors.Is(err, ErrFamilyRevoked) { oauthError(w, "invalid_grant", http.StatusBadRequest); return }
	if err != nil { http.Error(w, "server_error", http.StatusInternalServerError); return }

	// Give the token back to the user
	httpx.WriteJSON_nostore(w, map[string]any{
		"access_token": access, "token_type": "Bearer", "expires_in": 900,
		"refresh_token": refresh,
	})
}

// Create and save a new refresh token
func (a *Auth) newRefreshToken(
	ctx context.Context,
	userID string,
	clientID string,
	family string,
	amr []string,
	expiresAt time.Time,
) (string, error) {
	// Create the key and randomize it
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil { return "", err }

	// Create the token and save it
	tok := "ohc_rt_"+base64.RawURLEncoding.EncodeToString(b)
	rt := models.RefreshToken{
		TokenHash: secrets.Sha256Hex(tok),
		UserID: userID,
		ClientID: clientID,
		FamilyID: family,
		AMR: amr,
		ExpiresAt: expiresAt,
	}

	// Save the token
	err := a.ks.SaveRefreshToken(ctx, &rt)

	// Return the unhashed token 
	return tok, err
}

// The refresh grant. Each refresh token is single use: redeeming it marks it
// used and issues a new one in the same family. A used token presented again
// means it was copied, so the whole family (the login session) is revoked.
func (a *Auth) RefreshGrant(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Verify which client is asking
	svc, err := a.AuthenticateService(r)
	if err != nil {
		a.lg.Warn("invalid client attempted a refresh", "error", err)
		oauthError(w, "invalid_client", http.StatusUnauthorized)
		return
	}

	// Find the token
	rt, err := a.ks.RefreshTokenbyHash(ctx, secrets.Sha256Hex(r.PostFormValue("refresh_token")))
	if errors.Is(err, pgx.ErrNoRows) { oauthError(w, "invalid_grant", http.StatusBadRequest); return }
	if err != nil {
		a.lg.Error("refresh token lookup failed", "error", err)
		oauthError(w, "server_error", http.StatusInternalServerError)
		return
	}

	// Token must be redeemed by the client it was issued to
	if rt.ClientID != svc.ID {
		a.lg.Warn("refresh token presented by the wrong client", "client", svc.ID, "family", rt.FamilyID)
		oauthError(w, "invalid_grant", http.StatusBadRequest)
		return
	}

	// Token was revoked or expired
	if rt.RevokedAt != nil || time.Now().After(rt.ExpiresAt) {
		oauthError(w, "invalid_grant", http.StatusBadRequest)
		return
	}

	// Claim the token. Losing the claim means someone else redeemed it, even if
	// they did so a moment ago in a concurrent request.
	claimed, err := a.ks.UseRefreshToken(ctx, rt.ID)
	if err != nil {
		a.lg.Error("refresh token claim failed", "error", err)
		oauthError(w, "server_error", http.StatusInternalServerError)
		return
	}
	if rt.UsedAt != nil || !claimed {
		a.lg.Warn("refresh token reuse, revoking family", "user", rt.UserID, "client", svc.ID, "family", rt.FamilyID)
		a.revokeFamily(ctx, rt.FamilyID)
		oauthError(w, "invalid_grant", http.StatusBadRequest)
		return
	}

	// Load the user fresh, so disabling an account or changing roles takes effect on refresh
	usr, err := a.getUserByID(ctx, rt.UserID)
	if err != nil || usr.DisabledAt != nil {
		a.revokeFamily(ctx, rt.FamilyID)
		oauthError(w, "invalid_grant", http.StatusBadRequest)
		return
	}

	// Rotate: new tokens in the same family, keeping how the user logged in
	a.issueUserTokens(w, r, usr, svc.ID, rt.FamilyID, rt.AMR, refreshExpiry(rt.AMR, rt))
}

// Revoke a session family, logging rather than failing the request if it errors
func (a *Auth) revokeFamily(ctx context.Context, family string) {
	if err := a.ks.RevokeRefreshFamily(ctx, family); err != nil {
		a.lg.Error("failed to revoke refresh token family", "family", family, "error", err)
	}
}
