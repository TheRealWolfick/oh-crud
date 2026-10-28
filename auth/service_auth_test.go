package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/models/modelstest"
	"lotusforge.au/api-server/store"
)

const (
	testAssertionType = "urn:ietf:params:oauth:client-assertion-type:jwt-bearer"
	testTokenURL      = "https://ohcrud.internal/oauth/token"
	testServiceID     = "svc-a"
	testKID           = "svc-a-key-1"
	testTokenLifetime = 10 * time.Minute
)

// newTestServiceAuth returns an Auth whose database knows one service key, and that key's private half
func newTestServiceAuth(t *testing.T) (*Auth, *modelstest.FakeDB, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	fake_db := &modelstest.FakeDB{
		Row:     []any{[]byte(pub), testServiceID, "reader", nil, nil},
		ExecTag: pgconn.NewCommandTag("UPDATE 1"),
	}
	a := &Auth{
		Auth: *store.NewServices(fake_db),
		svr_conf: models.NewSwappableServerConfig(&models.ServerConfig{
			Token_url:      testTokenURL,
			Token_lifetime: testTokenLifetime,
		}),
		Replay: ReplayService(),
	}
	return a, fake_db, priv
}

// validAssertionClaims are the claims a well behaved service sends
func validAssertionClaims() jwt.RegisteredClaims {
	now := time.Now()
	return jwt.RegisteredClaims{
		Issuer:    testServiceID,
		Subject:   testServiceID,
		Audience:  jwt.ClaimStrings{testTokenURL},
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(2 * time.Minute)),
		ID:        uuid.NewString(),
	}
}

// signAssertion signs claims with an Ed25519 key the way a service would
func signAssertion(t *testing.T, priv ed25519.PrivateKey, kid string, c jwt.RegisteredClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, c)
	tok.Header["kid"] = kid
	raw, err := tok.SignedString(priv)
	if err != nil {
		t.Fatalf("Sign assertion: %v", err)
	}
	return raw
}

// assertionRequest builds a token endpoint POST carrying a client assertion
func assertionRequest(testAssertionType, assertion string) *http.Request {
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_assertion_type", testAssertionType)
	form.Set("client_assertion", assertion)
	r := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}

// waitForExec waits for the async TouchServiceKey and returns the Exec call, if any
func waitForExec(fake_db *modelstest.FakeDB) (modelstest.Call, bool) {
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if call := fake_db.LastCall(); call.Method == "Exec" {
			return call, true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return modelstest.Call{}, false
}

func TestAuthenticateService(t *testing.T) {
	a, fake_db, priv := newTestServiceAuth(t)
	raw := signAssertion(t, priv, testKID, validAssertionClaims())

	svc, err := a.AuthenticateService(assertionRequest(testAssertionType, raw))
	if err != nil {
		t.Fatalf("AuthenticateService: %v", err)
	}
	if svc == nil || svc.ID != testServiceID || svc.Role != "reader" {
		t.Fatalf("Unexpected service: %+v", svc)
	}

	// The key lookup uses the kid header and the iss claim
	lookup := fake_db.Calls[0]
	if lookup.Method != "QueryRow" || lookup.Args[0] != testKID || lookup.Args[1] != testServiceID {
		t.Errorf("Expected a lookup of kid %s for %s, got %+v", testKID, testServiceID, lookup)
	}

	// last_used_at is touched for the key that signed the assertion
	touch, ok := waitForExec(fake_db)
	if !ok {
		t.Fatalf("Expected TouchServiceKey to run")
	}
	if len(touch.Args) != 1 || touch.Args[0] != testKID {
		t.Errorf("Expected last_used_at to be touched for %s, got %v", testKID, touch.Args)
	}
}

func TestAuthenticateServiceRejects(t *testing.T) {
	_, other, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	cases := []struct {
		name string
		// build returns the assertion type and assertion for the request
		build func(t *testing.T, priv ed25519.PrivateKey) (string, string)
		// unknown makes the database find no key for the service
		unknown bool
	}{
		{name: "wrong assertion type", build: func(t *testing.T, priv ed25519.PrivateKey) (string, string) {
			return "urn:ietf:params:oauth:client-assertion-type:saml2-bearer", signAssertion(t, priv, testKID, validAssertionClaims())
		}},
		{name: "missing assertion", build: func(t *testing.T, priv ed25519.PrivateKey) (string, string) {
			return testAssertionType, ""
		}},
		{name: "unknown or revoked key", unknown: true, build: func(t *testing.T, priv ed25519.PrivateKey) (string, string) {
			return testAssertionType, signAssertion(t, priv, testKID, validAssertionClaims())
		}},
		{name: "signed by a different key", build: func(t *testing.T, priv ed25519.PrivateKey) (string, string) {
			return testAssertionType, signAssertion(t, other, testKID, validAssertionClaims())
		}},
		{name: "HMAC instead of EdDSA", build: func(t *testing.T, priv ed25519.PrivateKey) (string, string) {
			tok := jwt.NewWithClaims(jwt.SigningMethodHS256, validAssertionClaims())
			tok.Header["kid"] = testKID
			raw, err := tok.SignedString([]byte("shared secret"))
			if err != nil {
				t.Fatalf("Sign HS256: %v", err)
			}
			return testAssertionType, raw
		}},
		{name: "iss and sub differ", build: func(t *testing.T, priv ed25519.PrivateKey) (string, string) {
			c := validAssertionClaims()
			c.Subject = "svc-b"
			return testAssertionType, signAssertion(t, priv, testKID, c)
		}},
		{name: "wrong audience", build: func(t *testing.T, priv ed25519.PrivateKey) (string, string) {
			c := validAssertionClaims()
			c.Audience = jwt.ClaimStrings{"https://elsewhere.example/token"}
			return testAssertionType, signAssertion(t, priv, testKID, c)
		}},
		{name: "expired", build: func(t *testing.T, priv ed25519.PrivateKey) (string, string) {
			c := validAssertionClaims()
			c.IssuedAt = jwt.NewNumericDate(time.Now().Add(-3 * time.Minute))
			c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-2 * time.Minute)) // past the 30s leeway
			return testAssertionType, signAssertion(t, priv, testKID, c)
		}},
		{name: "missing exp", build: func(t *testing.T, priv ed25519.PrivateKey) (string, string) {
			c := validAssertionClaims()
			c.ExpiresAt = nil
			return testAssertionType, signAssertion(t, priv, testKID, c)
		}},
		{name: "missing iat", build: func(t *testing.T, priv ed25519.PrivateKey) (string, string) {
			c := validAssertionClaims()
			c.IssuedAt = nil
			return testAssertionType, signAssertion(t, priv, testKID, c)
		}},
		{name: "missing jti", build: func(t *testing.T, priv ed25519.PrivateKey) (string, string) {
			c := validAssertionClaims()
			c.ID = ""
			return testAssertionType, signAssertion(t, priv, testKID, c)
		}},
		{name: "lifetime over 5 minutes", build: func(t *testing.T, priv ed25519.PrivateKey) (string, string) {
			c := validAssertionClaims()
			c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(10 * time.Minute))
			return testAssertionType, signAssertion(t, priv, testKID, c)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, fake_db, priv := newTestServiceAuth(t)
			if tc.unknown {
				fake_db.Row = nil // scans as pgx.ErrNoRows
			}
			typ, raw := tc.build(t, priv)

			svc, err := a.AuthenticateService(assertionRequest(typ, raw))
			if err == nil || svc != nil {
				t.Fatalf("Expected the assertion to be rejected, got service %+v", svc)
			}
			if _, touched := waitForExecBriefly(fake_db); touched {
				t.Errorf("A rejected assertion must not touch last_used_at")
			}
		})
	}
}

// waitForExecBriefly gives a wrongly started TouchServiceKey goroutine a moment to show up
func waitForExecBriefly(fake_db *modelstest.FakeDB) (modelstest.Call, bool) {
	time.Sleep(20 * time.Millisecond)
	call := fake_db.LastCall()
	return call, call.Method == "Exec"
}

func TestAuthenticateServiceReplay(t *testing.T) {
	a, _, priv := newTestServiceAuth(t)
	raw := signAssertion(t, priv, testKID, validAssertionClaims())

	if _, err := a.AuthenticateService(assertionRequest(testAssertionType, raw)); err != nil {
		t.Fatalf("First use: %v", err)
	}
	if _, err := a.AuthenticateService(assertionRequest(testAssertionType, raw)); err == nil {
		t.Fatalf("Expected a replayed assertion to be rejected")
	}

	// A fresh assertion from the same service still works
	if _, err := a.AuthenticateService(assertionRequest(testAssertionType, signAssertion(t, priv, testKID, validAssertionClaims()))); err != nil {
		t.Errorf("Fresh assertion after a replay: %v", err)
	}
}

func TestAuthenticateServiceReplayWithinLeeway(t *testing.T) {
	a, _, priv := newTestServiceAuth(t)

	// Expired 10s ago, which the 30s leeway still accepts
	c := validAssertionClaims()
	c.IssuedAt = jwt.NewNumericDate(time.Now().Add(-time.Minute))
	c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-10 * time.Second))
	raw := signAssertion(t, priv, testKID, c)

	if _, err := a.AuthenticateService(assertionRequest(testAssertionType, raw)); err != nil {
		t.Fatalf("First use inside the leeway: %v", err)
	}
	if _, err := a.AuthenticateService(assertionRequest(testAssertionType, raw)); err == nil {
		t.Fatalf("Expected a replay inside the leeway to be rejected")
	}
}

// tokenRequest sends a form to the token endpoint and returns the recorded response
func tokenRequest(a *Auth, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	a.TokenEndpoint().ServeHTTP(w, r)
	return w
}

func newTestTokenAuth(t *testing.T) (*Auth, ed25519.PrivateKey) {
	t.Helper()
	a, _, priv := newTestServiceAuth(t)
	a.lg = slog.New(slog.DiscardHandler)
	a.ks = newActiveKeyStore(t)
	return a, priv
}

func TestTokenEndpointClientCredentials(t *testing.T) {
	a, priv := newTestTokenAuth(t)
	w := tokenRequest(a, assertionRequest(testAssertionType, signAssertion(t, priv, testKID, validAssertionClaims())))

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Token responses must be Cache-Control: no-store")
	}

	var body struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("Decode response: %v", err)
	}
	if body.TokenType != "Bearer" {
		t.Errorf("Unexpected token type: %+v", body)
	}
	// expires_in is in seconds, not a raw time.Duration
	if body.ExpiresIn != int(testTokenLifetime.Seconds()) {
		t.Errorf("Expected expires_in %d seconds, got %d", int(testTokenLifetime.Seconds()), body.ExpiresIn)
	}

	// The access token is ours and describes the service
	claims, err := a.ks.Verify(body.AccessToken)
	if err != nil {
		t.Fatalf("Verify issued access token: %v", err)
	}
	if claims.TokenUse != "service" || claims.Subject != "svc:"+testServiceID || len(claims.Roles) != 1 || claims.Roles[0] != "reader" {
		t.Errorf("Unexpected access token claims: %+v", claims)
	}
	if exp := time.Until(claims.ExpiresAt.Time); exp < testTokenLifetime-5*time.Second || exp > testTokenLifetime {
		t.Errorf("Expected the token to expire in about %v, got %v", testTokenLifetime, exp)
	}
}

func TestTokenEndpointUsesSwappedConfig(t *testing.T) {
	a, priv := newTestTokenAuth(t)
	newURL := "https://moved.example/oauth/token"
	a.svr_conf.Swap(&models.ServerConfig{Token_url: newURL, Token_lifetime: 5 * time.Minute})

	// An assertion addressed to the old token URL is now the wrong audience
	old := tokenRequest(a, assertionRequest(testAssertionType, signAssertion(t, priv, testKID, validAssertionClaims())))
	if old.Code != http.StatusUnauthorized {
		t.Errorf("Expected the old audience to be rejected after a swap, got %d", old.Code)
	}

	c := validAssertionClaims()
	c.Audience = jwt.ClaimStrings{newURL}
	w := tokenRequest(a, assertionRequest(testAssertionType, signAssertion(t, priv, testKID, c)))
	if w.Code != http.StatusOK {
		t.Fatalf("Expected the new audience to be accepted, got %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		ExpiresIn int `json:"expires_in"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("Decode response: %v", err)
	}
	if body.ExpiresIn != 300 {
		t.Errorf("Expected the swapped lifetime of 300 seconds, got %d", body.ExpiresIn)
	}
}

func TestTokenEndpointErrors(t *testing.T) {
	a, priv := newTestTokenAuth(t)

	badClient := assertionRequest(testAssertionType, signAssertion(t, priv, "unknown-kid", validAssertionClaims()))
	a.Auth = *store.NewServices(&modelstest.FakeDB{}) // no keys at all

	form := url.Values{"grant_type": {"implicit"}}
	badGrant := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	badGrant.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	cases := []struct {
		name   string
		r      *http.Request
		status int
		code   string
	}{
		{"unknown client", badClient, http.StatusUnauthorized, "invalid_client"},
		{"unsupported grant", badGrant, http.StatusBadRequest, "unsupported_grant_type"},
	}
	for _, tc := range cases {
		w := tokenRequest(a, tc.r)
		var body map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if w.Code != tc.status || body["error"] != tc.code {
			t.Errorf("%s: expected %d %s, got %d %s", tc.name, tc.status, tc.code, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "access_token") {
			t.Errorf("%s: no token may be returned", tc.name)
		}
	}
}

func TestReplayMemoryRemember(t *testing.T) {
	r := ReplayService()
	until := time.Now().Add(time.Minute)

	if !r.Remember("a", until) {
		t.Fatalf("Expected a new jti to be accepted")
	}
	if r.Remember("a", until) {
		t.Errorf("Expected a repeated jti to be rejected")
	}
	if !r.Remember("b", until) {
		t.Errorf("Expected a different jti to be accepted")
	}
}

func TestReplayMemoryForgetsExpired(t *testing.T) {
	r := ReplayService()
	r.seen["old"] = time.Now().Add(-time.Second)

	if !r.Remember("old", time.Now().Add(time.Minute)) {
		t.Errorf("Expected an expired jti to be accepted again")
	}

	r.seen["stale"] = time.Now().Add(-time.Second)
	r.Remember("new", time.Now().Add(time.Minute))
	if _, kept := r.seen["stale"]; kept {
		t.Errorf("Expected expired entries to be removed")
	}
	if len(r.seen) != 2 {
		t.Errorf("Expected only the live entries old and new, got %v", r.seen)
	}
}

func TestReplayMemoryConcurrent(t *testing.T) {
	r := ReplayService()
	until := time.Now().Add(time.Minute)

	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted := 0
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r.Remember("same", until) {
				mu.Lock()
				accepted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if accepted != 1 {
		t.Errorf("Expected exactly one of 50 concurrent uses to be accepted, got %d", accepted)
	}
}
