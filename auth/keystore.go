package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/secrets"
)

type SigningKey struct {
	KID      string
	Private  ed25519.PrivateKey
	Public   ed25519.PublicKey
}

// This stores all keys
type KeyStore struct {
	mu       sync.RWMutex
	active   *SigningKey
	all      map[string]*SigningKey
	issuer   string
	audience string
	db       models.DBTxBeginner
	master   []byte
}


// Generate and return a new signing key with KID, public and private.
func NewSigningKey() (*SigningKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil { return nil, err }

	mod, _ := secrets.RandomString(6)

	return &SigningKey{
		KID: "ohc-" + time.Now().UTC().Format("20060102") + "-" + mod,
		Private: priv,
		Public: pub,
	}, nil
}

func ValidateSigningKey(k *SigningKey) bool {
	// Ensure all fields present
	if k.KID == "" || k.Public == nil || k.Private == nil { return false }

	// Ensure key pairs match
	if !k.Private.Public().(ed25519.PublicKey).Equal(k.Public) { return false }

	// Ensure length is proper
	if len(k.Public) != ed25519.PublicKeySize || len(k.Private) != ed25519.PrivateKeySize { return false }

	return true
} 

// Create a new keystore
func NewKeyStore(issuer string, audience string, db models.DBTxBeginner, master []byte) *KeyStore {
	return &KeyStore{
		active: nil,
		all: map[string]*SigningKey{},
		issuer: issuer,
		audience: audience,
		db: db,
		master: master,
	}
}

// Return the active signing key
func (ks *KeyStore) Active() *SigningKey {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	return ks.active
}

// Lookup a key, if it is within the JWKS, it will return (*key, true), else (nil, false)
func (ks *KeyStore) Lookup(kid string) (*SigningKey, bool) {
	// Early termination of lookup
	if kid == "" { return nil, false }

	ks.mu.RLock()
	defer ks.mu.RUnlock()

	key, found := ks.all[kid]
	return key, found
}


// insertKey validates, encrypts and saves a key to the database with the given status.
// It only touches the database, so callers decide when the key goes into memory.
func (ks *KeyStore) insertKey(ctx context.Context, k *SigningKey, status string) error {
	// Ensure signing key is valid
	if !ValidateSigningKey(k) { return errors.New("invalid signing key") }

	// Encrypt and add the key to the database
	priv_sealed, err := secrets.Encrypt(ks.master, k.Private, []byte(k.KID))
	if err != nil { return fmt.Errorf("encrypt %s: %w", k.KID, err) }

	// activated_at is only stamped when the key goes straight in as active
	qry := `INSERT INTO signing_keys(kid, public_key, private_key, status, activated_at)
	VALUES ( $1, $2, $3, $4::varchar, CASE WHEN $4::varchar = 'active' THEN now() END )`

	cmd, err := ks.db.Exec(ctx, qry, k.KID, k.Public, priv_sealed, status)
	if err != nil { return fmt.Errorf("save %s: %w", k.KID, err) }
	if cmd.RowsAffected() != 1 { return fmt.Errorf("save %s: row not inserted", k.KID) }
	return nil
}

// Publish saves a key to the database as 'next' and adds it to the JWKS without signing with it.
// The JWKS is only changed once the insert succeeds.
func (ks *KeyStore) Publish(ctx context.Context, k *SigningKey) error {
	if err := ks.insertKey(ctx, k, "next"); err != nil { return fmt.Errorf("publish: %w", err) }

	ks.mu.Lock()
	defer ks.mu.Unlock()

	ks.all[k.KID] = k
	return nil
}

// Activate moves a published key into the active state and retires the current one.
// Both status changes run in one transaction, so the database is never left without an active key.
// The retiring key stays in the JWKS so tokens it already signed still verify.
func (ks *KeyStore) Activate(ctx context.Context, kid string) error {
	ks.mu.Lock()
	defer ks.mu.Unlock()

	k, ok := ks.all[kid]
	if !ok { return fmt.Errorf("activate: unknown kid %s", kid) }

	tx, err := ks.db.Begin(ctx)
	if err != nil { return fmt.Errorf("activate: begin: %w", err) }
	// Does nothing once Commit has succeeded, so it's safe to always defer
	defer tx.Rollback(ctx)

	// Retire the current key, if there is one
	if ks.active != nil {
		cmd, err := tx.Exec(ctx, `UPDATE signing_keys SET status = 'retiring', retired_at = now() WHERE kid = $1`, ks.active.KID)
		if err != nil { return fmt.Errorf("activate: retire %s: %w", ks.active.KID, err) }
		if cmd.RowsAffected() != 1 { return fmt.Errorf("activate: retire %s: row not found", ks.active.KID) }
	}

	// Activate the new key
	cmd, err := tx.Exec(ctx, `UPDATE signing_keys SET status = 'active', activated_at = now() WHERE kid = $1`, k.KID)
	if err != nil { return fmt.Errorf("activate: %s: %w", k.KID, err) }
	if cmd.RowsAffected() != 1 { return fmt.Errorf("activate: %s: row not found", k.KID) }

	if err := tx.Commit(ctx); err != nil { return fmt.Errorf("activate: commit: %w", err) }

	// Change memory only after the commit worked
	ks.active = k
	return nil
}

// Delete removes a key from the JWKS and prevents it from
// activating anything
func (ks *KeyStore) Delete(kid string) {
	ks.mu.Lock()
	defer ks.mu.Unlock()

	delete(ks.all, kid)
}


// This returns all the keys in the format for the .well-known endpoint
func (ks *KeyStore) JWKS() []models.Jwk {
	ks.mu.RLock()
	defer ks.mu.RUnlock()

	// Make the jwks structure
	keys := make([]models.Jwk, 0, len(ks.all))

	// Append all the keys
	for _, k := range ks.all {
		keys = append(keys, models.Jwk{
			Kty: "OKP",
			Crv: "Ed25519",
			X: base64.RawURLEncoding.EncodeToString(k.Public),
			Kid: k.KID,
			Alg: "EdDSA",
			Use: "sig",
		})
	}

	return keys
}


// Fill in the claim with the standard claims then return it signed with the active key
func (ks *KeyStore) Issue(c models.Claims, ttl time.Duration) (string, error) {
	k := ks.Active()
	if k == nil || k.Private == nil { return "", errors.New("No active signing key") }

	now := time.Now()
	c.Issuer = ks.issuer
	c.Audience = jwt.ClaimStrings{ks.audience}
	c.IssuedAt = jwt.NewNumericDate(now)
	c.NotBefore = jwt.NewNumericDate(now)
	c.ExpiresAt = jwt.NewNumericDate(now.Add(ttl))
	c.ID        = uuid.NewString()

	t := jwt.NewWithClaims(jwt.SigningMethodEdDSA, c)
	t.Header["kid"] = k.KID
	t.Header["typ"] = "at+jwt"

	// Sign the jwt with the private key. The recipient will verify it with the public key
	return t.SignedString(k.Private)
}


// Check the signature, algorithm, issuer, audience, and time (in that order)
func (ks *KeyStore) Verify(raw string) (*models.Claims, error) {
	// Create a new claims object
	c := &models.Claims{}

	// Process the raw jwt
	_, err := jwt.ParseWithClaims(raw, c, func(t *jwt.Token) (any, error) {
		// Check token type
		if typ, _ := t.Header["typ"].(string); typ != "at+jwt" { return nil, errors.New("not an access token") }

		// Check key ID
		kid, _ := t.Header["kid"].(string)
		key, ok := ks.Lookup(kid)
		if !ok { return nil, errors.New("unknown sighning key") }
		return key.Public, nil},
		// Additional parse option restrictions
		jwt.WithValidMethods([]string{"EdDSA"}),
		jwt.WithIssuer(ks.issuer),
		jwt.WithAudience(ks.audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(50*time.Second),
	)

	if err != nil { return nil, err }
	if c.TokenUse != "service" && c.TokenUse != "user" { return nil, errors.New("missing token use") }

	// Everything all good, return the claims
	return c, nil
}


// RefreshKeys loads every usable key from the database and swaps them in.
// Safe to call at startup and on a timer. On any error the current keys are kept.
func (ks *KeyStore) RefreshKeys(ctx context.Context) error {
	rows, err := ks.db.Query(ctx, `
	SELECT kid, public_key, private_key, status
	FROM signing_keys
	WHERE status IN ('next', 'active', 'retiring')`)
	if err != nil {
		return fmt.Errorf("query signing keys: %w", err)
	}
	defer rows.Close()

	all := make(map[string]*SigningKey)
	var active *SigningKey

	// For each row
	for rows.Next() {
		// Declare variables
		var kid, status string
		var pub, encrypted []byte

		// Scan the row into the variables
		if err := rows.Scan(&kid, &pub, &encrypted, &status); err != nil {
			return fmt.Errorf("scan signing key: %w", err)
		}

		// Decrypt the private key
		priv, err := secrets.Decrypt(ks.master, encrypted, []byte(kid))
		if err != nil { return fmt.Errorf("decrypt signing key %s (wrong master key or tampered row): %w", kid, err) }
		if len(pub) != ed25519.PublicKeySize || len(priv) != ed25519.PrivateKeySize {
			return fmt.Errorf("signing key %s has the wrong size", kid)
		}

		// Create the decrypted key construct.
		k := &SigningKey{KID: kid, Public: ed25519.PublicKey(pub), Private: ed25519.PrivateKey(priv)}
		if !k.Private.Public().(ed25519.PublicKey).Equal(k.Public) {
			return fmt.Errorf("signing key %s: private and public halves don't match", kid)
		}

		// Add the signing key to the JWKS keystore staging
		all[kid] = k

		// Action the active key (assign active, throw error if active already has an assignment)
		if status == "active" {
			if active != nil { return errors.New("more than one active signing key") }
			active = k
		}
	}

	// Row reading error`
	if err := rows.Err(); err != nil { return fmt.Errorf("read signing keys: %w", err) }

	// Nothing in the key store, so create a key straight into the active state (there is nothing to retire).
	// Only the database is written here; memory changes at the swap below, so an error keeps the current keys.
	if len(all) == 0 {
		// Keys already loaded means the table was emptied under us (wipe, restore, wrong database).
		// Keep serving with the current keys rather than replacing them.
		if ks.Active() != nil { return errors.New("signing_keys is empty but keys are loaded; keeping current keys") }

		key, err := NewSigningKey()
		if err != nil { return fmt.Errorf("bootstrap signing key: %w", err) }
		if err := ks.insertKey(ctx, key, "active"); err != nil { return fmt.Errorf("bootstrap signing key: %w", err) }
		all[key.KID], active = key, key
	}

	// No active error
	if active == nil { return errors.New("no active key") }

	// Move staging into the JWKS
	ks.mu.Lock()
	ks.all, ks.active = all, active
	ks.mu.Unlock()
	return nil
}


// Save a new refresh token
func (ks *KeyStore) SaveRefreshToken(ctx context.Context, rt *models.RefreshToken) error {
	sql := `INSERT INTO refresh_tokens (token_hash, user_id, client_id, family_id, expires_at) VALUES ($1, $2, $3, $4, $5)`

	// Add the token to the database
	cmd, err := ks.db.Exec(ctx, sql, rt.TokenHash, rt.UserID, rt.ClientID, rt.FamilyID, rt.ExpiresAt)
	if err != nil || cmd.RowsAffected() == 0 { return err }

	return nil
}

// Read a refresh token
func (ks *KeyStore) RefreshTokenbyHash(ctx context.Context, hash string) (*models.RefreshToken, error) {
	sql := `SELECT id, token_hash, user_id, client_id, family_id, expires_at, used_at, revoked_at
	FROM refresh_tokens WHERE token_hash = $1 LIMIT 1`

	// Get the refresh token
	rows, err := ks.db.Query(ctx, sql, hash)
	if err != nil { return nil, err }

	return pgx.CollectOneRow(rows, pgx.RowToAddrOfStructByName[models.RefreshToken])
}
