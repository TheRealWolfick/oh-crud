package middleware

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"lotusforge.au/api-server/models"
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
func NewKeyStore(issuer string, audience string, db models.DBExecQuery) *KeyStore {
	return &KeyStore{
		active: nil,
		all: map[string]*SigningKey{},
		issuer: issuer,
		audience: audience,
		db: db,
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
	if c.TokenUse != "service" || c.TokenUse != "user" { return nil, errors.New("missing token use") }

	// Everything all good, return the claims
	return c, nil
}


// Load all the keys in the database where the key is active, next, or retiring.
// Intended for a once off use during setup
func (ks *KeyStore) RefreshKeys() error {
	// Clear the current keys
	if len(ks.all) > 0 { for k := range ks.all { delete(ks.all, k) } }

	// Load keys from the database into register
	keys := []SigningKey{}
	qry := "SELECT KID, Public, Private FROM signing_keys WHERE "

	// Read the rows
	rows, err := ks.db.Query(context.Background(), qry)
	if err != nil { return err }

	// Scan the rows
	err = rows.Scan(keys)
	if err != nil { return err }

	// Add the rows into the register
	for _, k := range keys { 
		ks.all[k.KID] = &SigningKey{
			KID: k.KID,
		}  
	}

	return nil
}
