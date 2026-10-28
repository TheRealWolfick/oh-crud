package auth

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexedwards/argon2id"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pquerna/otp/totp"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/models/modelstest"
	"lotusforge.au/api-server/secrets"
)

const (
	testUserID   = "user-1"
	testUsername = "alice"
	testPassword = "correct horse battery staple"
	otherService = "svc-b"
)

// authTables is an in-memory users and refresh_tokens table behind a FakeDB.
// It understands only the statements the user grants run.
type authTables struct {
	mu     sync.Mutex
	users  []*models.User
	tokens []*models.RefreshToken
	nextID int

	// tokenReads, when set, holds each refresh token lookup until all of them
	// have read, forcing concurrent requests to see the same unused row
	tokenReads *sync.WaitGroup
}

var userCols = []string{"id", "username", "display_name", "password_hash", "totp_secret", "created_at", "disabled_at", "roles"}
var tokenCols = []string{"id", "token_hash", "user_id", "client_id", "family_id", "amr", "expires_at", "used_at", "revoked_at"}

func (tb *authTables) fakeDB() *modelstest.FakeDB {
	db := &modelstest.FakeDB{}
	db.QueryFunc = tb.query
	db.ExecFunc = tb.exec
	return db
}

func (tb *authTables) query(sql string, args []any) (pgx.Rows, error) {
	rows, err := tb.read(sql, args)
	if tb.tokenReads != nil && strings.Contains(sql, "FROM refresh_tokens") {
		tb.tokenReads.Done()
		tb.tokenReads.Wait()
	}
	return rows, err
}

func (tb *authTables) read(sql string, args []any) (pgx.Rows, error) {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	switch {
	case strings.Contains(sql, "FROM users WHERE username"), strings.Contains(sql, "FROM users WHERE id"):
		byID := strings.Contains(sql, "WHERE id")
		for _, u := range tb.users {
			if (byID && u.ID == args[0]) || (!byID && u.Username == args[0]) {
				return &modelstest.FakeRows{Cols: userCols, Data: [][]any{{
					u.ID, u.Username, u.Displayname, u.PasswordHash, u.TotpSecret, u.CreatedAt, u.DisabledAt, u.Roles,
				}}}, nil
			}
		}
	case strings.Contains(sql, "FROM refresh_tokens WHERE token_hash"):
		for _, rt := range tb.tokens {
			if rt.TokenHash == args[0] {
				return &modelstest.FakeRows{Cols: tokenCols, Data: [][]any{{
					rt.ID, rt.TokenHash, rt.UserID, rt.ClientID, rt.FamilyID, rt.AMR, rt.ExpiresAt, rt.UsedAt, rt.RevokedAt,
				}}}, nil
			}
		}
	default:
		return nil, errors.New("authTables: unexpected query: " + sql)
	}
	return &modelstest.FakeRows{Cols: userCols}, nil // no rows
}

func (tb *authTables) exec(sql string, args []any) (pgconn.CommandTag, error) {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	now := time.Now()

	switch {
	case strings.Contains(sql, "INSERT INTO refresh_tokens"):
		for _, rt := range tb.tokens {
			if rt.FamilyID == args[3] && rt.RevokedAt != nil {
				return pgconn.NewCommandTag("INSERT 0 0"), nil
			}
		}
		tb.nextID++
		tb.tokens = append(tb.tokens, &models.RefreshToken{
			ID: tb.nextID, TokenHash: args[0].(string), UserID: args[1].(string), ClientID: args[2].(string),
			FamilyID: args[3].(string), AMR: args[4].([]string), ExpiresAt: args[5].(time.Time),
		})
		return pgconn.NewCommandTag("INSERT 0 1"), nil

	case strings.Contains(sql, "SET used_at"):
		for _, rt := range tb.tokens {
			if rt.ID == args[0] && rt.UsedAt == nil && rt.RevokedAt == nil && rt.ExpiresAt.After(now) {
				rt.UsedAt = &now
				return pgconn.NewCommandTag("UPDATE 1"), nil
			}
		}
		return pgconn.NewCommandTag("UPDATE 0"), nil

	case strings.Contains(sql, "SET revoked_at"):
		n := 0
		for _, rt := range tb.tokens {
			if rt.FamilyID == args[0] && rt.RevokedAt == nil {
				rt.RevokedAt = &now
				n++
			}
		}
		return pgconn.NewCommandTag(fmt.Sprintf("UPDATE %d", n)), nil

	case strings.Contains(sql, "UPDATE users SET password_hash"):
		for _, u := range tb.users {
			if u.ID == args[1] && u.PasswordHash == args[2] {
				u.PasswordHash = args[0].(string)
				return pgconn.NewCommandTag("UPDATE 1"), nil
			}
		}
		return pgconn.NewCommandTag("UPDATE 0"), nil
	}
	return pgconn.CommandTag{}, errors.New("authTables: unexpected exec: " + sql)
}

// token returns the stored row for a raw refresh token
func (tb *authTables) token(raw string) *models.RefreshToken {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	for _, rt := range tb.tokens {
		if rt.TokenHash == secrets.Sha256Hex(raw) {
			return rt
		}
	}
	return nil
}

func (tb *authTables) family(family string) []*models.RefreshToken {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	var out []*models.RefreshToken
	for _, rt := range tb.tokens {
		if rt.FamilyID == family {
			out = append(out, rt)
		}
	}
	return out
}

// grantFixture is an Auth wired to a service key database and the in-memory user tables
type grantFixture struct {
	a      *Auth
	svcDB  *modelstest.FakeDB
	priv   ed25519.PrivateKey
	tables *authTables
	user   *models.User
}

func newGrantFixture(t *testing.T) *grantFixture {
	t.Helper()
	a, svcDB, priv := newTestServiceAuth(t)
	a.lg = slog.New(slog.DiscardHandler)
	a.Limiter = newLimiter(10, 15*time.Minute)

	// The signing keys are cached once loaded, so the keystore can then be pointed at the user tables
	a.ks = newActiveKeyStore(t)
	tables := &authTables{}
	a.ks.db = tables.fakeDB()

	hash, err := secrets.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	user := &models.User{ID: testUserID, Username: testUsername, PasswordHash: hash, CreatedAt: time.Now(), Roles: []string{"editor"}}
	tables.users = append(tables.users, user)

	return &grantFixture{a: a, svcDB: svcDB, priv: priv, tables: tables, user: user}
}

// asService makes the service key lookup return svcID, as if that service's key signed the assertion
func (f *grantFixture) asService(svcID string) {
	pub := f.priv.Public().(ed25519.PublicKey)
	f.svcDB.Row = []any{[]byte(pub), svcID, "reader", nil, nil}
}

// enableTOTP stores an encrypted TOTP secret on the user and returns the plain secret
func (f *grantFixture) enableTOTP(t *testing.T) string {
	t.Helper()
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "oh-crud", AccountName: testUsername})
	if err != nil {
		t.Fatalf("totp.Generate: %v", err)
	}
	sealed, err := secrets.Encrypt(testMaster, []byte(key.Secret()), []byte(testUserID))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	f.user.TotpSecret = sealed
	return key.Secret()
}

// post sends a grant to the token endpoint. A non-empty svcID adds a client assertion signed as that service.
func (f *grantFixture) post(t *testing.T, svcID string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	if svcID != "" {
		c := validAssertionClaims()
		c.Issuer, c.Subject = svcID, svcID
		form.Set("client_assertion_type", testAssertionType)
		form.Set("client_assertion", signAssertion(t, f.priv, testKID, c))
	}
	r := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return tokenRequest(f.a, r)
}

func (f *grantFixture) login(t *testing.T, svcID, username, password string) *httptest.ResponseRecorder {
	t.Helper()
	return f.post(t, svcID, url.Values{"grant_type": {"password"}, "username": {username}, "password": {password}})
}

func (f *grantFixture) refresh(t *testing.T, svcID, token string) *httptest.ResponseRecorder {
	t.Helper()
	return f.post(t, svcID, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {token}})
}

type userTokens struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
}

// mustTokens expects a 200 with both tokens, and returns them with the verified access claims
func mustTokens(t *testing.T, w *httptest.ResponseRecorder, ks *KeyStore) (userTokens, *models.Claims) {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Token responses must be no-store")
	}
	var body userTokens
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("Decode body: %v", err)
	}
	if body.AccessToken == "" || body.RefreshToken == "" || body.TokenType != "Bearer" {
		t.Fatalf("Expected an access and refresh token, got %+v", body)
	}
	claims, err := ks.Verify(body.AccessToken)
	if err != nil {
		t.Fatalf("Issued access token does not verify: %v", err)
	}
	return body, claims
}

// mustFail expects a status and OAuth error code, and that no token was handed out
func mustFail(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if w.Code != status {
		t.Errorf("Expected %d, got %d %s", status, w.Code, w.Body.String())
	}
	if code != "" {
		var body map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if body["error"] != code {
			t.Errorf("Expected error %q, got %s", code, w.Body.String())
		}
	}
	if strings.Contains(w.Body.String(), "token\"") {
		t.Errorf("No token may be returned on failure: %s", w.Body.String())
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ── Password grant ────────────────────────────────────────────────────────────

func TestPasswordGrant(t *testing.T) {
	f := newGrantFixture(t)

	body, claims := mustTokens(t, f.login(t, testServiceID, testUsername, testPassword), f.a.ks)

	if claims.TokenUse != "user" || claims.Subject != testUserID {
		t.Errorf("Expected a user token for %s, got %s for %s", testUserID, claims.TokenUse, claims.Subject)
	}
	if !equalStrings(claims.Roles, []string{"editor"}) || !equalStrings(claims.AMR, []string{"pwd"}) {
		t.Errorf("Unexpected roles %v or amr %v", claims.Roles, claims.AMR)
	}
	if claims.SID == "" {
		t.Errorf("Expected the access token to carry the session family")
	}

	// Only the hash of the refresh token is stored, bound to the client and session
	rt := f.tables.token(body.RefreshToken)
	if rt == nil {
		t.Fatalf("Refresh token was not stored")
	}
	if rt.TokenHash == body.RefreshToken {
		t.Errorf("The raw refresh token must never be stored")
	}
	if rt.UserID != testUserID || rt.ClientID != testServiceID || rt.FamilyID != claims.SID {
		t.Errorf("Refresh token bound to the wrong user, client or family: %+v", rt)
	}
	if !equalStrings(rt.AMR, []string{"pwd"}) {
		t.Errorf("Expected the login method to be stored, got %v", rt.AMR)
	}
}

func TestPasswordGrantRejects(t *testing.T) {
	cases := []struct {
		name     string
		svcID    string
		username string
		password string
		setup    func(f *grantFixture)
		status   int
		code     string
	}{
		{name: "no client assertion", username: testUsername, password: testPassword, status: http.StatusUnauthorized, code: "invalid_client"},
		{name: "unknown client key", svcID: testServiceID, username: testUsername, password: testPassword,
			setup: func(f *grantFixture) { f.svcDB.Row = nil }, status: http.StatusUnauthorized, code: "invalid_client"},
		{name: "wrong password", svcID: testServiceID, username: testUsername, password: "wrong", status: http.StatusUnauthorized},
		{name: "unknown user", svcID: testServiceID, username: "mallory", password: testPassword, status: http.StatusUnauthorized},
		{name: "user without a password", svcID: testServiceID, username: testUsername, password: "",
			setup: func(f *grantFixture) { f.user.PasswordHash = "" }, status: http.StatusUnauthorized},
		{name: "user without a password, guessing the dummy", svcID: testServiceID, username: testUsername, password: "fake-password",
			setup: func(f *grantFixture) { f.user.PasswordHash = "" }, status: http.StatusUnauthorized},
		{name: "disabled user", svcID: testServiceID, username: testUsername, password: testPassword,
			setup: func(f *grantFixture) { now := time.Now(); f.user.DisabledAt = &now }, status: http.StatusUnauthorized},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newGrantFixture(t)
			if tc.setup != nil {
				tc.setup(f)
			}
			mustFail(t, f.login(t, tc.svcID, tc.username, tc.password), tc.status, tc.code)
			if len(f.tables.tokens) != 0 {
				t.Errorf("A failed login must not store a refresh token")
			}
		})
	}
}

// Passwordless users (such as Entra users) are checked against the dummy hash for timing.
// Even a password that matches the dummy must never log them in.
func TestPasswordGrantPasswordlessUserNeverMatchesDummy(t *testing.T) {
	f := newGrantFixture(t)
	f.user.PasswordHash = ""

	known, err := argon2id.CreateHash("known-dummy", secrets.Argon2Params)
	if err != nil {
		t.Fatalf("CreateHash: %v", err)
	}
	original := secrets.DummyHash
	secrets.DummyHash = known
	t.Cleanup(func() { secrets.DummyHash = original })

	mustFail(t, f.login(t, testServiceID, testUsername, "known-dummy"), http.StatusUnauthorized, "")
	mustFail(t, f.login(t, testServiceID, "mallory", "known-dummy"), http.StatusUnauthorized, "")
}

// An unknown user and a wrong password must look identical, or usernames can be enumerated
func TestPasswordGrantUnknownUserMatchesWrongPassword(t *testing.T) {
	f := newGrantFixture(t)
	wrong := f.login(t, testServiceID, testUsername, "wrong")
	unknown := f.login(t, testServiceID, "mallory", "wrong")

	if wrong.Code != unknown.Code || wrong.Body.String() != unknown.Body.String() {
		t.Errorf("Responses differ: wrong password %d %q, unknown user %d %q",
			wrong.Code, wrong.Body.String(), unknown.Code, unknown.Body.String())
	}
}

func TestPasswordGrantTOTP(t *testing.T) {
	f := newGrantFixture(t)
	secret := f.enableTOTP(t)

	// The password alone is not enough
	mustFail(t, f.login(t, testServiceID, testUsername, testPassword), http.StatusUnauthorized, "invalid_grant")

	wrong := url.Values{"grant_type": {"password"}, "username": {testUsername}, "password": {testPassword}, "otp": {"000000"}}
	if code, _ := totp.GenerateCode(secret, time.Now()); code == "000000" {
		t.Skip("The current code happens to be 000000")
	}
	mustFail(t, f.post(t, testServiceID, wrong), http.StatusUnauthorized, "invalid_grant")

	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	form := url.Values{"grant_type": {"password"}, "username": {testUsername}, "password": {testPassword}, "otp": {code}}
	body, claims := mustTokens(t, f.post(t, testServiceID, form), f.a.ks)

	if !equalStrings(claims.AMR, []string{"pwd", "otp"}) {
		t.Errorf("Expected amr [pwd otp], got %v", claims.AMR)
	}
	if rt := f.tables.token(body.RefreshToken); rt == nil || !equalStrings(rt.AMR, []string{"pwd", "otp"}) {
		t.Errorf("Expected the refresh token to remember the OTP login, got %+v", rt)
	}
}

func TestPasswordGrantRehashesWeakHash(t *testing.T) {
	f := newGrantFixture(t)
	weak, err := argon2id.CreateHash(testPassword, &argon2id.Params{Memory: 16 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32})
	if err != nil {
		t.Fatalf("CreateHash: %v", err)
	}
	f.user.PasswordHash = weak

	mustTokens(t, f.login(t, testServiceID, testUsername, testPassword), f.a.ks)

	if f.user.PasswordHash == weak {
		t.Fatalf("Expected the weak hash to be replaced")
	}
	match, params, err := argon2id.CheckHash(testPassword, f.user.PasswordHash)
	if err != nil || !match {
		t.Fatalf("The new hash must still match the password: match=%v err=%v", match, err)
	}
	if secrets.NeedsRehash(params) {
		t.Errorf("The new hash still uses weak params: %+v", params)
	}

	// The upgraded password keeps working
	mustTokens(t, f.login(t, testServiceID, testUsername, testPassword), f.a.ks)
}

func TestPasswordGrantKeepsCurrentHash(t *testing.T) {
	f := newGrantFixture(t)
	before := f.user.PasswordHash

	mustTokens(t, f.login(t, testServiceID, testUsername, testPassword), f.a.ks)

	if f.user.PasswordHash != before {
		t.Errorf("A hash with current params must not be rewritten")
	}
	for _, call := range f.a.ks.db.(*modelstest.FakeDB).Calls {
		if strings.Contains(call.SQL, "UPDATE users") {
			t.Errorf("Unexpected password update: %s", call.SQL)
		}
	}
}

func TestPasswordGrantRateLimited(t *testing.T) {
	f := newGrantFixture(t)
	f.a.Limiter = newLimiter(2, time.Minute)

	f.login(t, testServiceID, testUsername, "wrong")
	f.login(t, testServiceID, testUsername, "wrong")

	// Even the right password is refused once the limit is hit
	mustFail(t, f.login(t, testServiceID, testUsername, testPassword), http.StatusTooManyRequests, "slow_down")

	// The limit is per client and username, so another user is unaffected
	if w := f.login(t, testServiceID, "bob", "wrong"); w.Code == http.StatusTooManyRequests {
		t.Errorf("Another username must have its own limit")
	}
}

// ── Refresh grant ─────────────────────────────────────────────────────────────

// loginTokens logs the fixture user in through svc-a and returns the tokens and claims
func loginTokens(t *testing.T, f *grantFixture) (userTokens, *models.Claims) {
	t.Helper()
	return mustTokens(t, f.login(t, testServiceID, testUsername, testPassword), f.a.ks)
}

func TestRefreshGrantRotates(t *testing.T) {
	f := newGrantFixture(t)
	first, loginClaims := loginTokens(t, f)

	second, claims := mustTokens(t, f.refresh(t, testServiceID, first.RefreshToken), f.a.ks)

	if second.RefreshToken == first.RefreshToken {
		t.Fatalf("Expected a new refresh token on rotation")
	}
	if claims.Subject != testUserID || claims.SID != loginClaims.SID {
		t.Errorf("Expected the same user and session, got %s in %s", claims.Subject, claims.SID)
	}
	if !equalStrings(claims.AMR, loginClaims.AMR) {
		t.Errorf("Expected amr %v to carry over, got %v", loginClaims.AMR, claims.AMR)
	}

	old, next := f.tables.token(first.RefreshToken), f.tables.token(second.RefreshToken)
	if old.UsedAt == nil {
		t.Errorf("The redeemed token must be marked used")
	}
	if next == nil || next.FamilyID != old.FamilyID || next.ClientID != testServiceID || next.UsedAt != nil {
		t.Errorf("Expected a fresh token in the same family for the same client, got %+v", next)
	}

	// The rotated token works in turn
	mustTokens(t, f.refresh(t, testServiceID, second.RefreshToken), f.a.ks)
}

// federatedLogin starts a session as the token exchange grant will, since that grant does not exist yet
func federatedLogin(t *testing.T, f *grantFixture) userTokens {
	t.Helper()
	amr := []string{amrFederated, "mfa"}
	r := httptest.NewRequest(http.MethodPost, "/oauth/token", nil)
	w := httptest.NewRecorder()
	f.a.issueUserTokens(w, r, f.user, testServiceID, uuid.NewString(), amr, refreshExpiry(amr, nil))
	body, _ := mustTokens(t, w, f.a.ks)
	return body
}

func within(got, want time.Time) bool { return got.Sub(want).Abs() < time.Minute }

func TestRefreshLifetimePasswordSessionSlides(t *testing.T) {
	f := newGrantFixture(t)
	first, _ := loginTokens(t, f)

	old := f.tables.token(first.RefreshToken)
	if !within(old.ExpiresAt, time.Now().Add(14*24*time.Hour)) {
		t.Fatalf("Expected a password login to last %v, got expiry %v", refreshLifetime, old.ExpiresAt)
	}

	// Pretend the token is near its end, then rotate: the session is extended in full
	old.ExpiresAt = time.Now().Add(time.Hour)
	second, _ := mustTokens(t, f.refresh(t, testServiceID, first.RefreshToken), f.a.ks)

	if next := f.tables.token(second.RefreshToken); !within(next.ExpiresAt, time.Now().Add(14*24*time.Hour)) {
		t.Errorf("Expected rotation to extend a password session to %v, got expiry %v", refreshLifetime, next.ExpiresAt)
	}
}

func TestRefreshLifetimeFederatedSessionIsCapped(t *testing.T) {
	f := newGrantFixture(t)
	f.user.PasswordHash = "" // an Entra user has no password
	first := federatedLogin(t, f)

	old := f.tables.token(first.RefreshToken)
	if !within(old.ExpiresAt, time.Now().Add(24*time.Hour)) {
		t.Fatalf("Expected a federated login to last %v, got expiry %v", federatedRefreshLifetime, old.ExpiresAt)
	}

	// Rotation keeps the expiry from login rather than extending it
	second, claims := mustTokens(t, f.refresh(t, testServiceID, first.RefreshToken), f.a.ks)
	next := f.tables.token(second.RefreshToken)
	if !next.ExpiresAt.Equal(old.ExpiresAt) {
		t.Errorf("Expected rotation to keep expiry %v, got %v", old.ExpiresAt, next.ExpiresAt)
	}
	if !equalStrings(claims.AMR, []string{amrFederated, "mfa"}) || !equalStrings(next.AMR, claims.AMR) {
		t.Errorf("Expected the federated amr to carry over, got %v and %v", claims.AMR, next.AMR)
	}

	// Once the day is up the session ends, however recently it was refreshed
	next.ExpiresAt = time.Now().Add(-time.Second)
	mustFail(t, f.refresh(t, testServiceID, second.RefreshToken), http.StatusBadRequest, "invalid_grant")
}

func TestRefreshGrantPicksUpUserChanges(t *testing.T) {
	f := newGrantFixture(t)
	first, _ := loginTokens(t, f)

	f.user.Roles = []string{"admin"}
	_, claims := mustTokens(t, f.refresh(t, testServiceID, first.RefreshToken), f.a.ks)

	if !equalStrings(claims.Roles, []string{"admin"}) {
		t.Errorf("Expected refreshed roles [admin], got %v", claims.Roles)
	}
}

// Redeeming a token twice means it was copied, so the whole session ends
func TestRefreshGrantReuseRevokesFamily(t *testing.T) {
	f := newGrantFixture(t)
	first, claims := loginTokens(t, f)
	second, _ := mustTokens(t, f.refresh(t, testServiceID, first.RefreshToken), f.a.ks)

	mustFail(t, f.refresh(t, testServiceID, first.RefreshToken), http.StatusBadRequest, "invalid_grant")

	for _, rt := range f.tables.family(claims.SID) {
		if rt.RevokedAt == nil {
			t.Errorf("Token %d in the family was not revoked", rt.ID)
		}
	}
	// Including the token the legitimate client was given
	mustFail(t, f.refresh(t, testServiceID, second.RefreshToken), http.StatusBadRequest, "invalid_grant")
}

func TestRefreshGrantOtherSessionsSurviveReuse(t *testing.T) {
	f := newGrantFixture(t)
	stolen, _ := loginTokens(t, f)
	other, _ := loginTokens(t, f)

	mustTokens(t, f.refresh(t, testServiceID, stolen.RefreshToken), f.a.ks)
	mustFail(t, f.refresh(t, testServiceID, stolen.RefreshToken), http.StatusBadRequest, "invalid_grant")

	// A separate login is a separate family and is unaffected
	mustTokens(t, f.refresh(t, testServiceID, other.RefreshToken), f.a.ks)
}

// Two requests racing on one token. Whichever order the claim, revoke and rotation
// land in, at most one may succeed and the session must end with no live token.
func TestRefreshGrantConcurrentRedeem(t *testing.T) {
	f := newGrantFixture(t)
	first, claims := loginTokens(t, f)

	// Sign both assertions up front, since t.Fatalf cannot be called from these goroutines
	forms := make([]url.Values, 2)
	for i := range forms {
		c := validAssertionClaims()
		forms[i] = url.Values{
			"grant_type":            {"refresh_token"},
			"refresh_token":         {first.RefreshToken},
			"client_assertion_type": {testAssertionType},
			"client_assertion":      {signAssertion(t, f.priv, testKID, c)},
		}
	}

	f.tables.tokenReads = &sync.WaitGroup{}
	f.tables.tokenReads.Add(len(forms))

	codes := make([]int, 2)
	var wg sync.WaitGroup
	for i := range forms {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(forms[i].Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			codes[i] = tokenRequest(f.a, r).Code
		}(i)
	}
	wg.Wait()

	ok := 0
	for _, c := range codes {
		if c == http.StatusOK {
			ok++
		}
	}
	if ok > 1 {
		t.Fatalf("Expected at most one success, got status codes %v", codes)
	}
	// Both read the token as unused, so the loser only finds out when its claim
	// fails. That is treated as reuse.
	for _, rt := range f.tables.family(claims.SID) {
		if rt.RevokedAt == nil {
			t.Errorf("Expected the family to be revoked after a contested redeem")
		}
	}
}

// A rotation that lands after its family was revoked must not leave a live token
func TestSaveRefreshTokenRevokedFamily(t *testing.T) {
	f := newGrantFixture(t)
	_, claims := loginTokens(t, f)

	if err := f.a.ks.RevokeRefreshFamily(t.Context(), claims.SID); err != nil {
		t.Fatalf("RevokeRefreshFamily: %v", err)
	}
	if _, err := f.a.newRefreshToken(t.Context(), testUserID, testServiceID, claims.SID, []string{"pwd"}, time.Now().Add(time.Hour)); !errors.Is(err, ErrFamilyRevoked) {
		t.Fatalf("Expected ErrFamilyRevoked, got %v", err)
	}
	for _, rt := range f.tables.family(claims.SID) {
		if rt.RevokedAt == nil {
			t.Errorf("A token was added to a revoked family")
		}
	}
}

func TestRefreshGrantWrongClient(t *testing.T) {
	f := newGrantFixture(t)
	first, _ := loginTokens(t, f)

	f.asService(otherService)
	mustFail(t, f.refresh(t, otherService, first.RefreshToken), http.StatusBadRequest, "invalid_grant")

	// The attempt must not burn the token for the client that owns it
	if rt := f.tables.token(first.RefreshToken); rt.UsedAt != nil || rt.RevokedAt != nil {
		t.Errorf("A wrong client must not consume or revoke the token: %+v", rt)
	}
	f.asService(testServiceID)
	mustTokens(t, f.refresh(t, testServiceID, first.RefreshToken), f.a.ks)
}

func TestRefreshGrantRejects(t *testing.T) {
	cases := []struct {
		name   string
		svcID  string
		token  func(raw string) string
		setup  func(f *grantFixture, rt *models.RefreshToken)
		status int
		code   string
		// burns is whether the token may be consumed by the failed attempt
		burns bool
	}{
		{name: "no client assertion", status: http.StatusUnauthorized, code: "invalid_client"},
		{name: "unknown client key", svcID: testServiceID, status: http.StatusUnauthorized, code: "invalid_client",
			setup: func(f *grantFixture, rt *models.RefreshToken) { f.svcDB.Row = nil }},
		{name: "unknown token", svcID: testServiceID, status: http.StatusBadRequest, code: "invalid_grant",
			token: func(string) string { return "ohc_rt_not-a-real-token" }},
		{name: "empty token", svcID: testServiceID, status: http.StatusBadRequest, code: "invalid_grant",
			token: func(string) string { return "" }},
		{name: "expired token", svcID: testServiceID, status: http.StatusBadRequest, code: "invalid_grant",
			setup: func(f *grantFixture, rt *models.RefreshToken) { rt.ExpiresAt = time.Now().Add(-time.Minute) }},
		{name: "revoked token", svcID: testServiceID, status: http.StatusBadRequest, code: "invalid_grant", burns: true,
			setup: func(f *grantFixture, rt *models.RefreshToken) { now := time.Now(); rt.RevokedAt = &now }},
		{name: "user disabled since login", svcID: testServiceID, status: http.StatusBadRequest, code: "invalid_grant", burns: true,
			setup: func(f *grantFixture, rt *models.RefreshToken) { now := time.Now(); f.user.DisabledAt = &now }},
		{name: "user deleted since login", svcID: testServiceID, status: http.StatusBadRequest, code: "invalid_grant", burns: true,
			setup: func(f *grantFixture, rt *models.RefreshToken) { f.tables.users = nil }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newGrantFixture(t)
			first, _ := loginTokens(t, f)
			rt := f.tables.token(first.RefreshToken)
			if tc.setup != nil {
				tc.setup(f, rt)
			}
			token := first.RefreshToken
			if tc.token != nil {
				token = tc.token(token)
			}

			mustFail(t, f.refresh(t, tc.svcID, token), tc.status, tc.code)

			if !tc.burns && rt.UsedAt != nil {
				t.Errorf("A rejected request must not consume the token")
			}
		})
	}
}

func TestRefreshGrantDisabledUserRevokesFamily(t *testing.T) {
	f := newGrantFixture(t)
	first, claims := loginTokens(t, f)

	now := time.Now()
	f.user.DisabledAt = &now
	mustFail(t, f.refresh(t, testServiceID, first.RefreshToken), http.StatusBadRequest, "invalid_grant")

	for _, rt := range f.tables.family(claims.SID) {
		if rt.RevokedAt == nil {
			t.Errorf("Expected a disabled user's session to be revoked")
		}
	}
}

func TestRefreshGrantDatabaseError(t *testing.T) {
	f := newGrantFixture(t)
	first, _ := loginTokens(t, f)

	f.a.ks.db.(*modelstest.FakeDB).QueryFunc = func(string, []any) (pgx.Rows, error) {
		return nil, errors.New("connection reset")
	}
	// An outage is a server error, not a bad token, so clients retry rather than log the user out
	mustFail(t, f.refresh(t, testServiceID, first.RefreshToken), http.StatusInternalServerError, "server_error")
}
