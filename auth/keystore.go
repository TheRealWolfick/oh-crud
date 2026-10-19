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
	db       models.DBExecQuery
	master   []byte
}


// Generate and return a new signing key with KID, public and private.
func NewSigningKey() (*SigningKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil { return nil, err }

	return &SigningKey{
		KID: "ohc-" + time.Now().UTC().Format("20060102"),
		Private: priv,
		Public: pub,
	}, nil
}

// Create a new keystore
func NewKeyStore(issuer string, audience string, db models.DBExecQuery, master []byte) *KeyStore {
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


// Publish adds a key to the JWKS without signing with it
// Does this need an error / prevention of overriding a key?
func (ks *KeyStore) Publish(k *SigningKey) {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	ks.all[k.KID] = k
}

// Activate moves a published key into an active state
// It returns the success or failure
func (ks *KeyStore) Activate(kid string) bool {
	ks.mu.RLock()
	defer ks.mu.RUnlock()

	k, ok := ks.all[kid]
	if !ok { return false }

	ks.active = k
	return true
}

// Delete removes a key from the JWKS and prevents it from
// activating anything
func (ks *KeyStore) Delete(kid string) {
	ks.mu.RLock()
	defer ks.mu.RUnlock()

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
	k := ks.active
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

	for rows.Next() {
		var kid, status string
		var pub, encrypted []byte
		if err := rows.Scan(&kid, &pub, &encrypted, &status); err != nil {
			return fmt.Errorf("scan signing key: %w", err)
		}

		priv, err := secrets.Decrypt(ks.master, encrypted, []byte(kid))
		if err != nil {
			return fmt.Errorf("decrypt signing key %s (wrong master key or tampered row): %w", kid, err)
		}
		if len(pub) != ed25519.PublicKeySize || len(priv) != ed25519.PrivateKeySize {
			return fmt.Errorf("signing key %s has the wrong size", kid)
		}
		k := &SigningKey{KID: kid, Public: ed25519.PublicKey(pub), Private: ed25519.PrivateKey(priv)}
		if !k.Private.Public().(ed25519.PublicKey).Equal(k.Public) {
			return fmt.Errorf("signing key %s: private and public halves don't match", kid)
		}

		all[kid] = k
		if status == "active" {
			if active != nil {
				return errors.New("more than one active signing key")
			}
			active = k
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read signing keys: %w", err)
	}
	if active == nil {
		return errors.New("no active key")
	}

	ks.mu.Lock()
	ks.all, ks.active = all, active
	ks.mu.Unlock()
	return nil
}
