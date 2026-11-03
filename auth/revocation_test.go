package auth

import (
	"errors"
	"net/http"
	"reflect"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"lotusforge.au/api-server/globals"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/models/modelstest"
)

// withBlocks gives the fixture a blocklist whose revocations take effect at revokedAt.
// Tests pass a moment after every token they will issue: the fake database returns this
// fixed time, and a real revocation is always later than the tokens it is meant to end.
func withBlocks(f *grantFixture, revokedAt time.Time) *modelstest.FakeDB {
	db := &modelstest.FakeDB{Row: []any{revokedAt}}
	f.a.Blocks = NewBlockList(db, func() time.Duration { return 10 * time.Minute })
	return db
}

func TestBlockedComparesIssueTime(t *testing.T) {
	b := NewBlockList(&modelstest.FakeDB{}, func() time.Duration { return 0 })
	at := time.Date(2026, 11, 1, 10, 0, 0, 500_000_000, time.UTC)
	b.revoked["user:u1"] = at

	cases := []struct {
		name string
		iat  time.Time
		keys []string
		want bool
	}{
		{"issued before", at.Add(-time.Minute), []string{"user:u1"}, true},
		// JWT times are whole seconds, so the same second can't be told apart: refused
		{"same second, after", time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC), []string{"user:u1"}, true},
		{"next second", at.Add(time.Second).Truncate(time.Second), []string{"user:u1"}, false},
		{"other key", at.Add(-time.Minute), []string{"user:u2", "sid:x"}, false},
		{"any of several keys", at.Add(-time.Minute), []string{"sid:x", "user:u1"}, true},
	}
	for _, tc := range cases {
		if got := b.Blocked(tc.iat, tc.keys...); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
	var none *BlockList
	if none.Blocked(at, "user:u1") {
		t.Errorf("a nil blocklist blocks nothing")
	}
}

func TestRevokeRecordsAndBlocksAtOnce(t *testing.T) {
	at := time.Now()
	db := &modelstest.FakeDB{Row: []any{at}}
	b := NewBlockList(db, func() time.Duration { return 0 })

	got, err := b.Revoke(t.Context(), "sid:fam1", "logout")
	if err != nil || !got.Equal(at) {
		t.Fatalf("Revoke: %v %v", got, err)
	}
	c := db.LastCall()
	if !strings.Contains(c.SQL, "INSERT INTO ohc_revocations") || c.Args[0] != "sid:fam1" || c.Args[1] != "logout" {
		t.Errorf("unexpected insert %s %v", c.SQL, c.Args)
	}
	if !b.Blocked(at.Add(-time.Second), "sid:fam1") {
		t.Errorf("expected the key blocked on this instance without waiting for a reload")
	}
}

func TestLoad(t *testing.T) {
	at := time.Now()
	db := &modelstest.FakeDB{Rows: modelstest.FakeRows{Cols: []string{"key", "max"}, Data: [][]any{{"svc:billing", at}}}}
	// Service tokens live 30 minutes here, longer than user tokens, so they set the window
	b := NewBlockList(db, func() time.Duration { return 30 * time.Minute })

	if err := b.Load(t.Context()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if secs := db.LastCall().Args[0]; secs != (35 * time.Minute).Seconds() {
		t.Errorf("expected a 35 minute window, got %v seconds", secs)
	}
	if !b.Blocked(at.Add(-time.Second), "svc:billing") {
		t.Errorf("expected the loaded revocation blocked, got %v", b.revoked)
	}

	db.QueryErr = errors.New("db down")
	if err := b.Load(t.Context()); err == nil {
		t.Fatalf("expected the error")
	}
}

func TestMiddlewareRefusesRevokedTokens(t *testing.T) {
	f := newGrantFixture(t)
	withBlocks(f, time.Now().Add(time.Minute))
	svc := serviceToken(t, f.a, testServiceID, "reader")
	usr := userToken(t, f.a, testServiceID, "editor") // session family-1
	mw := f.a.ForModel(modelWith(models.Access{AllowDirect: true}))

	if w, _ := call(mw, svc, usr); w.Code != http.StatusOK {
		t.Fatalf("expected the tokens to pass before any revocation, got %d", w.Code)
	}

	cases := map[string]struct {
		key            string
		bearer, user   string
	}{
		"session, sent by a service": {"sid:family-1", svc, usr},
		"user, sent by a service":    {"user:" + testUserID, svc, usr},
		"user, direct call":          {"user:" + testUserID, usr, ""},
		"service":                    {"svc:" + testServiceID, svc, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f.a.Blocks.revoked = map[string]time.Time{tc.key: time.Now()}
			if w, s := call(mw, tc.bearer, tc.user); w.Code != http.StatusUnauthorized || s.called {
				t.Errorf("expected 401, got %d", w.Code)
			}
		})
	}

	// A token issued after the revocation works: a fresh login isn't locked out
	f.a.Blocks.revoked = map[string]time.Time{"user:" + testUserID: time.Now().Add(-2 * time.Second)}
	if w, _ := call(mw, svc, usr); w.Code != http.StatusOK {
		t.Errorf("expected a token issued after the revocation to pass, got %d", w.Code)
	}
}

// logout sends a logout through the real handler
func logout(f *grantFixture, bearer, user, query string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/oauth/logout"+query, nil)
	if bearer != "" { r.Header.Set("Authorization", "Bearer "+bearer) }
	if user != "" { r.Header.Set(globals.UserTokenHeader, user) }
	w := httptest.NewRecorder()
	f.a.LogoutHandler().ServeHTTP(w, r)
	return w
}

func TestLogoutEndsTheSession(t *testing.T) {
	f := newGrantFixture(t)
	withBlocks(f, time.Now().Add(time.Minute))
	first, claims := loginTokens(t, f)
	other, _ := loginTokens(t, f) // a second session, which must survive
	svc := serviceToken(t, f.a, testServiceID, "reader")

	if w := logout(f, svc, first.AccessToken, ""); w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d %s", w.Code, w.Body.String())
	}

	// The access token stops working, and the refresh token can't make a new one
	mw := f.a.ForModel(modelWith(models.Access{}))
	if w, _ := call(mw, svc, first.AccessToken); w.Code != http.StatusUnauthorized {
		t.Errorf("expected the logged out access token refused, got %d", w.Code)
	}
	mustFail(t, f.refresh(t, testServiceID, first.RefreshToken), http.StatusBadRequest, "invalid_grant")

	// Only that session ends
	if w, _ := call(mw, svc, other.AccessToken); w.Code != http.StatusOK {
		t.Errorf("expected the user's other session to keep working, got %d", w.Code)
	}
	mustTokens(t, f.refresh(t, testServiceID, other.RefreshToken), f.a.ks)

	row := f.tables.audits[2] // two logins, then the logout
	if row.Action != globals.ActionUserLogout || row.ActorUser != testUsername || row.ViaService != testServiceID {
		t.Errorf("unexpected audit entry %+v", row)
	}
	if _, ok := f.a.Blocks.revoked["sid:"+claims.SID]; !ok {
		t.Errorf("expected the session blocked")
	}
}

func TestLogoutAllEndsEverySession(t *testing.T) {
	f := newGrantFixture(t)
	withBlocks(f, time.Now().Add(time.Minute))
	first, _ := loginTokens(t, f)
	second, _ := loginTokens(t, f)
	svc := serviceToken(t, f.a, testServiceID, "reader")

	if w := logout(f, svc, first.AccessToken, "?all=true"); w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
	mw := f.a.ForModel(modelWith(models.Access{}))
	for _, tok := range []userTokens{first, second} {
		if w, _ := call(mw, svc, tok.AccessToken); w.Code != http.StatusUnauthorized {
			t.Errorf("expected every access token refused, got %d", w.Code)
		}
		mustFail(t, f.refresh(t, testServiceID, tok.RefreshToken), http.StatusBadRequest, "invalid_grant")
	}
	if f.tables.lastAudit(t).Action != globals.ActionUserLogoutAll {
		t.Errorf("expected a user.logout_all entry")
	}
}

// A user can log themselves out with their own token, without a service
func TestLogoutDirect(t *testing.T) {
	f := newGrantFixture(t)
	withBlocks(f, time.Now().Add(time.Minute))
	tok, _ := loginTokens(t, f)
	if w := logout(f, tok.AccessToken, "", ""); w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
	if row := f.tables.lastAudit(t); row.ViaService != nil {
		t.Errorf("expected no service on a direct logout, got %v", row.ViaService)
	}
}

func TestLogoutRejects(t *testing.T) {
	f := newGrantFixture(t)
	withBlocks(f, time.Now().Add(time.Minute))
	svc := serviceToken(t, f.a, testServiceID, "reader")

	if w := logout(f, "", "", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("no token: expected 401, got %d", w.Code)
	}
	// A service alone has no user session to end
	if w := logout(f, svc, "", ""); w.Code != http.StatusBadRequest {
		t.Errorf("service only: expected 400, got %d", w.Code)
	}
	// A user token with no session can't be logged out by session
	noSID := issue(t, f.a, models.Claims{TokenUse: "user", ClientID: testServiceID, RegisteredClaims: jwt.RegisteredClaims{Subject: testUserID}})
	if w := logout(f, svc, noSID, ""); w.Code != http.StatusBadRequest {
		t.Errorf("no session: expected 400, got %d", w.Code)
	}
}

// Refresh token reuse now ends the session's access tokens too, not just its refresh tokens
func TestRefreshReuseBlocksTheSession(t *testing.T) {
	f := newGrantFixture(t)
	withBlocks(f, time.Now().Add(time.Minute))
	first, claims := loginTokens(t, f)
	mustTokens(t, f.refresh(t, testServiceID, first.RefreshToken), f.a.ks)
	mustFail(t, f.refresh(t, testServiceID, first.RefreshToken), http.StatusBadRequest, "invalid_grant")

	if _, ok := f.a.Blocks.revoked["sid:"+claims.SID]; !ok {
		t.Errorf("expected the session's access tokens blocked after reuse")
	}
}

// A disabled user loses every session, not only the one that tried to refresh
func TestRefreshForDisabledUserBlocksTheUser(t *testing.T) {
	f := newGrantFixture(t)
	withBlocks(f, time.Now().Add(time.Minute))
	first, _ := loginTokens(t, f)
	second, _ := loginTokens(t, f)
	now := time.Now()
	f.user.DisabledAt = &now

	mustFail(t, f.refresh(t, testServiceID, first.RefreshToken), http.StatusBadRequest, "invalid_grant")
	if _, ok := f.a.Blocks.revoked["user:"+testUserID]; !ok {
		t.Errorf("expected the user blocked")
	}
	if rt := f.tables.token(second.RefreshToken); rt.RevokedAt == nil {
		t.Errorf("expected the user's other session revoked too")
	}
}


func TestRevocationCovers(t *testing.T) {
	at := time.Date(2026, 11, 1, 10, 0, 0, 500_000_000, time.UTC)
	before := jwt.NewNumericDate(at.Add(-time.Minute))
	after := jwt.NewNumericDate(at.Add(time.Second))
	user := &models.Claims{TokenUse: "user", SID: "fam1", RegisteredClaims: jwt.RegisteredClaims{Subject: "u1", IssuedAt: before}}
	svc := &models.Claims{TokenUse: "service", RegisteredClaims: jwt.RegisteredClaims{Subject: "svc:web", IssuedAt: before}}

	cases := []struct {
		name string
		key  string
		c    *models.Claims
		want bool
	}{
		{"user by user", "user:u1", user, true},
		{"user by session", "sid:fam1", user, true},
		{"user by other session", "sid:fam2", user, false},
		{"user by its service's key", "svc:web", user, false},
		{"service by service", "svc:web", svc, true},
		{"service by a user key", "user:u1", svc, false},
		{"issued after", "user:u1", &models.Claims{TokenUse: "user", RegisteredClaims: jwt.RegisteredClaims{Subject: "u1", IssuedAt: after}}, false},
		{"no iat", "user:u1", &models.Claims{TokenUse: "user", RegisteredClaims: jwt.RegisteredClaims{Subject: "u1"}}, true},
		{"nil token", "user:u1", nil, false},
	}
	for _, tc := range cases {
		if got := RevocationCovers(tc.key, at, tc.c); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A revocation made here is applied, announced to local listeners and shared with peers;
// one received from a peer is applied and announced but never shared back
func TestRevocationsShareAndReceive(t *testing.T) {
	at := time.Now()
	db := &modelstest.FakeDB{Row: []any{at}}
	b := NewBlockList(db, func() time.Duration { return 0 })
	var heard []string
	var shared []Revocation
	b.OnRevoke(func(key string, when time.Time) { heard = append(heard, key) })
	b.ShareWith(func(r Revocation) { shared = append(shared, r) })

	b.Revoke(t.Context(), "sid:local", "logout")
	if len(shared) != 1 || shared[0].Key != "sid:local" || !shared[0].At.Equal(at) {
		t.Errorf("expected the revocation shared with peers, got %+v", shared)
	}

	remote := at.Add(time.Second)
	msg := []byte(`{"key":"user:remote","at":"` + remote.Format(time.RFC3339Nano) + `"}`)
	if err := b.Receive(msg); err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if !b.Blocked(remote.Add(-time.Minute), "user:remote") {
		t.Errorf("expected the peer's revocation applied")
	}
	// Delivered twice (a retry): nothing happens the second time
	b.Receive(msg)

	if !reflect.DeepEqual(heard, []string{"sid:local", "user:remote"}) {
		t.Errorf("expected each revocation announced once, got %v", heard)
	}
	if len(shared) != 1 {
		t.Errorf("a received revocation must not be sent back out, got %+v", shared)
	}
	// The database is written only for the revocation made here
	if n := len(db.Calls); n != 1 {
		t.Errorf("expected one database call, got %d", n)
	}
}

func TestReceiveRefusesMalformed(t *testing.T) {
	b := NewBlockList(&modelstest.FakeDB{}, func() time.Duration { return 0 })
	for _, msg := range []string{`not json`, `{"key":"user:u1"}`, `{"key":"admin","at":"2026-11-01T00:00:00Z"}`} {
		if err := b.Receive([]byte(msg)); err == nil {
			t.Errorf("expected %s refused", msg)
		}
	}
	if len(b.revoked) != 0 {
		t.Errorf("nothing should have been applied, got %v", b.revoked)
	}
}
