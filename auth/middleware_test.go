package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/models/modelstest"
	"lotusforge.au/api-server/reqctx"
)

// seen is what a handler behind the middleware observed
type seen struct {
	called    bool
	principal *Principal
	actor     *models.User
	ctx       *http.Request
}

func (s *seen) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.called = true
		s.principal = PrincipalFrom(r.Context())
		s.actor, _ = reqctx.GetUser(r.Context())
		s.ctx = r
		w.WriteHeader(http.StatusOK)
	})
}

func issue(t *testing.T, a *Auth, c models.Claims) string {
	t.Helper()
	raw, err := a.ks.Issue(c, 5*time.Minute)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	return raw
}

func serviceToken(t *testing.T, a *Auth, svcID string, roles ...string) string {
	return issue(t, a, models.Claims{TokenUse: "service", Roles: roles,
		RegisteredClaims: jwt.RegisteredClaims{Subject: serviceUserPrefix + svcID}})
}

func userToken(t *testing.T, a *Auth, clientID string, roles ...string) string {
	return issue(t, a, models.Claims{TokenUse: "user", Roles: roles, Username: testUsername, ClientID: clientID,
		SID: "family-1", RegisteredClaims: jwt.RegisteredClaims{Subject: testUserID}})
}

// call sends a request with the given bearer and user tokens through mw
func call(mw func(http.Handler) http.Handler, bearer, user string) (*httptest.ResponseRecorder, *seen) {
	s := &seen{}
	r := httptest.NewRequest(http.MethodGet, "/asset", nil)
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	if user != "" {
		r.Header.Set(UserTokenHeader, user)
	}
	w := httptest.NewRecorder()
	mw(s.handler()).ServeHTTP(w, r)
	return w, s
}

func modelWith(access models.Access) *models.DataModel { return &models.DataModel{Access: access} }

func TestMiddlewareServiceOnly(t *testing.T) {
	f := newGrantFixture(t)
	w, s := call(f.a.ForModel(nil), serviceToken(t, f.a, testServiceID, "reader"), "")

	if w.Code != http.StatusOK || !s.called {
		t.Fatalf("Expected a service call to pass, got %d", w.Code)
	}
	if s.principal.Service == nil || s.principal.User != nil {
		t.Fatalf("Expected a service-only principal, got %+v", s.principal)
	}
	// Background writes are recorded against the service's own user row
	if s.actor.Username != "svc:"+testServiceID || s.actor.ID != "svc:"+testServiceID {
		t.Errorf("Expected writes to be recorded as svc:%s, got %+v", testServiceID, s.actor)
	}
}

func TestMiddlewareServiceForUser(t *testing.T) {
	f := newGrantFixture(t)
	w, s := call(f.a.ForModel(nil), serviceToken(t, f.a, testServiceID, "reader"), userToken(t, f.a, testServiceID, "editor"))

	if w.Code != http.StatusOK {
		t.Fatalf("Expected a service acting for a user to pass, got %d", w.Code)
	}
	if s.principal.Service == nil || s.principal.User == nil || s.principal.User.Subject != testUserID {
		t.Fatalf("Expected both parties in the principal, got %+v", s.principal)
	}
	// Writes are recorded against the user, not the service
	if s.actor.Username != testUsername || s.actor.ID != testUserID {
		t.Errorf("Expected writes to be recorded as %s, got %+v", testUsername, s.actor)
	}
}

func TestMiddlewareRejects(t *testing.T) {
	f := newGrantFixture(t)
	svc := serviceToken(t, f.a, testServiceID, "reader")
	usr := userToken(t, f.a, testServiceID, "editor")

	// A token signed by a key Oh CRUD does not hold
	other := newActiveKeyStore(t)
	foreign, err := other.Issue(models.Claims{TokenUse: "service", RegisteredClaims: jwt.RegisteredClaims{Subject: "svc:x"}}, time.Minute)
	if err != nil {
		t.Fatalf("Issue foreign: %v", err)
	}

	cases := []struct {
		name         string
		bearer, user string
		header       string // raw Authorization header, when set
	}{
		{name: "no Authorization header"},
		{name: "not a bearer token", header: "Basic dXNlcjpwYXNz"},
		{name: "garbage token", bearer: "not-a-jwt"},
		{name: "refresh token as bearer", bearer: "ohc_rt_abcdef"},
		{name: "token from another issuer key", bearer: foreign},
		{name: "unknown token_use", bearer: issue(t, f.a, models.Claims{TokenUse: "admin", RegisteredClaims: jwt.RegisteredClaims{Subject: "x"}})},
		{name: "missing token_use", bearer: issue(t, f.a, models.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "x"}})},
		{name: "missing subject", bearer: issue(t, f.a, models.Claims{TokenUse: "service"})},
		{name: "invalid user token", bearer: svc, user: "not-a-jwt"},
		{name: "service token as the user", bearer: svc, user: serviceToken(t, f.a, otherService)},
		{name: "user token issued to another service", bearer: svc, user: userToken(t, f.a, otherService, "editor")},
		// Bound to this service, so only the token_use check can catch it
		{name: "service token bound to this service as the user", bearer: svc, user: issue(t, f.a, models.Claims{TokenUse: "service",
			ClientID: testServiceID, Roles: []string{"administrator"}, RegisteredClaims: jwt.RegisteredClaims{Subject: "svc:" + testServiceID}})},
		{name: "user token vouching for another user", bearer: usr, user: userToken(t, f.a, testServiceID)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &seen{}
			r := httptest.NewRequest(http.MethodGet, "/asset", nil)
			switch {
			case tc.header != "":
				r.Header.Set("Authorization", tc.header)
			case tc.bearer != "":
				r.Header.Set("Authorization", "Bearer "+tc.bearer)
			}
			if tc.user != "" {
				r.Header.Set(UserTokenHeader, tc.user)
			}
			w := httptest.NewRecorder()
			// allow-direct, so the failure can only come from authentication
			f.a.ForModel(modelWith(models.Access{AllowDirect: true}))(s.handler()).ServeHTTP(w, r)

			if w.Code != http.StatusUnauthorized || s.called {
				t.Errorf("Expected 401 without reaching the handler, got %d (handler called: %v)", w.Code, s.called)
			}
		})
	}
}

func TestMiddlewareExpiredToken(t *testing.T) {
	f := newGrantFixture(t)
	expired, err := f.a.ks.Issue(models.Claims{TokenUse: "service", RegisteredClaims: jwt.RegisteredClaims{Subject: "svc:a"}}, -2*time.Minute)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if w, s := call(f.a.ForModel(nil), expired, ""); w.Code != http.StatusUnauthorized || s.called {
		t.Errorf("Expected an expired token to be refused, got %d", w.Code)
	}
}

func TestForModelAccessRules(t *testing.T) {
	f := newGrantFixture(t)
	svc := serviceToken(t, f.a, testServiceID, "reader")
	usr := userToken(t, f.a, testServiceID, "editor")

	cases := []struct {
		name         string
		access       models.Access
		bearer, user string
		want         int
	}{
		{"default: service only", models.Access{}, svc, "", http.StatusOK},
		{"default: service for a user", models.Access{}, svc, usr, http.StatusOK},
		{"default: direct user call refused", models.Access{}, usr, "", http.StatusForbidden},
		{"require-user: service only refused", models.Access{RequireUser: true}, svc, "", http.StatusForbidden},
		{"require-user: service for a user", models.Access{RequireUser: true}, svc, usr, http.StatusOK},
		{"allow-direct: direct user call", models.Access{AllowDirect: true}, usr, "", http.StatusOK},
		{"allow-direct: service only still fine", models.Access{AllowDirect: true}, svc, "", http.StatusOK},
		{"allow-direct and require-user: direct user", models.Access{AllowDirect: true, RequireUser: true}, usr, "", http.StatusOK},
		{"allow-direct and require-user: service only", models.Access{AllowDirect: true, RequireUser: true}, svc, "", http.StatusForbidden},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, s := call(f.a.ForModel(modelWith(tc.access)), tc.bearer, tc.user)
			if w.Code != tc.want {
				t.Errorf("Expected %d, got %d", tc.want, w.Code)
			}
			if s.called != (tc.want == http.StatusOK) {
				t.Errorf("Handler called = %v, expected %v", s.called, tc.want == http.StatusOK)
			}
		})
	}
}

// The handlers' role checks see both parties, so a service cannot reach past its own roles
// by acting for a more privileged user, and the reverse
func TestMiddlewareRolesReachHandlers(t *testing.T) {
	f := newGrantFixture(t)
	svr := &models.ServerConfig{RBAC: &models.RBACConfig{Admin_role: "administrator"}}

	cases := []struct {
		name     string
		svcRoles []string
		usrRoles []string // nil for a service-only call
		allowed  []string
		want     bool
	}{
		{"service only, allowed", []string{"reader"}, nil, []string{"reader"}, true},
		{"service only, not allowed", []string{"reader"}, nil, []string{"editor"}, false},
		{"both allowed", []string{"reader"}, []string{"editor"}, []string{"reader", "editor"}, true},
		{"service allowed, user not", []string{"reader"}, []string{"viewer"}, []string{"reader"}, false},
		{"user allowed, service not", []string{"reader"}, []string{"editor"}, []string{"editor"}, false},
		{"admin user still needs an allowed service", []string{"reader"}, []string{"administrator"}, []string{"editor"}, false},
		{"admin user with an allowed service", []string{"reader"}, []string{"administrator"}, []string{"reader"}, true},
		{"admin service and admin user", []string{"administrator"}, []string{"administrator"}, []string{}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			user := ""
			if tc.usrRoles != nil {
				user = userToken(t, f.a, testServiceID, tc.usrRoles...)
			}
			w, s := call(f.a.ForModel(nil), serviceToken(t, f.a, testServiceID, tc.svcRoles...), user)
			if w.Code != http.StatusOK {
				t.Fatalf("Expected the request through, got %d", w.Code)
			}
			if got := reqctx.CheckUserHasAllowedRole(s.ctx.Context(), tc.allowed, svr); got != tc.want {
				t.Errorf("CheckUserHasAllowedRole = %v, expected %v", got, tc.want)
			}
		})
	}
}

// The token a user logs in with works through the middleware, bound to the client that logged in
func TestMiddlewareWithLoginTokens(t *testing.T) {
	f := newGrantFixture(t)
	login, claims := loginTokens(t, f)

	if claims.Username != testUsername || claims.ClientID != testServiceID {
		t.Fatalf("Expected the user token to carry the username and client, got %q and %q", claims.Username, claims.ClientID)
	}

	svc := serviceToken(t, f.a, testServiceID, "reader")
	if w, s := call(f.a.ForModel(nil), svc, login.AccessToken); w.Code != http.StatusOK || s.actor.Username != testUsername {
		t.Errorf("Expected the login token to pass for the service that logged in, got %d", w.Code)
	}
	if w, _ := call(f.a.ForModel(nil), serviceToken(t, f.a, otherService, "reader"), login.AccessToken); w.Code != http.StatusUnauthorized {
		t.Errorf("Expected another service to be refused the token, got %d", w.Code)
	}
	if w, _ := call(f.a.ForModel(nil), login.RefreshToken, ""); w.Code != http.StatusUnauthorized {
		t.Errorf("Expected a refresh token to be refused as a bearer, got %d", w.Code)
	}
}

func TestClientCredentialsCreatesServiceUser(t *testing.T) {
	f := newGrantFixture(t)
	w := tokenRequest(f.a, assertionRequest(testAssertionType, signAssertion(t, f.priv, testKID, validAssertionClaims())))
	if w.Code != http.StatusOK {
		t.Fatalf("Expected a token, got %d %s", w.Code, w.Body.String())
	}

	var insert *modelstest.Call
	for _, c := range f.a.ks.db.(*modelstest.FakeDB).Calls {
		if strings.Contains(c.SQL, "INSERT INTO users") {
			insert = &c
		}
	}
	if insert == nil {
		t.Fatalf("Expected the service's user row to be created")
	}
	if !strings.Contains(insert.SQL, "ON CONFLICT DO NOTHING") {
		t.Errorf("Creating the row must be safe to repeat: %s", insert.SQL)
	}
	if insert.Args[0] != "svc:"+testServiceID || insert.Args[1] != testServiceID {
		t.Errorf("Expected a row for svc:%s, got args %v", testServiceID, insert.Args)
	}
	if strings.Contains(insert.SQL, "password_hash") {
		t.Errorf("A service user row must never get a password")
	}
}
