package auth

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pquerna/otp/totp"
	"lotusforge.au/api-server/audit"
	"lotusforge.au/api-server/models/modelstest"
)

func TestLoginIsAudited(t *testing.T) {
	f := newGrantFixture(t)
	_, claims := loginTokens(t, f)

	row := f.tables.lastAudit(t)
	if row.Action != audit.UserLogin || row.Target != testUserID || row.Service != audit.System {
		t.Errorf("unexpected entry %+v", row)
	}
	if row.ActorUser != testUserID || row.ViaService != testServiceID {
		t.Errorf("expected actor %s via %s, got %v via %v", testUserID, testServiceID, row.ActorUser, row.ViaService)
	}
	if !equalStrings(row.AMR, []string{"pwd"}) {
		t.Errorf("expected amr [pwd], got %v", row.AMR)
	}
	if got := afterField(t, row, "session"); got != claims.SID {
		t.Errorf("expected the entry to name session %s, got %q", claims.SID, got)
	}
	// httptest requests come from 192.0.2.1:1234; the port is dropped
	if row.IP != "192.0.2.1" || row.RequestID == nil {
		t.Errorf("expected ip 192.0.2.1 and a request id, got %v and %v", row.IP, row.RequestID)
	}
}

// Each failed login records why, for admins, while the caller still sees one response
func TestFailedLoginsAreAuditedWithReason(t *testing.T) {
	cases := []struct {
		name     string
		setup    func(f *grantFixture) url.Values
		target   string
		reason   string
	}{
		{"unknown user", func(f *grantFixture) url.Values { return form("mallory", testPassword, "") }, "username:mallory", "unknown_user"},
		{"wrong password", func(f *grantFixture) url.Values { return form(testUsername, "wrong", "") }, testUserID, "bad_password"},
		{"no password", func(f *grantFixture) url.Values { f.user.PasswordHash = ""; return form(testUsername, testPassword, "") }, testUserID, "no_password"},
		{"disabled", func(f *grantFixture) url.Values { now := time.Now(); f.user.DisabledAt = &now; return form(testUsername, testPassword, "") }, testUserID, "disabled"},
		{"otp missing", func(f *grantFixture) url.Values { f.enableTOTP(t); return form(testUsername, testPassword, "") }, testUserID, "otp_missing"},
		{"otp wrong", func(f *grantFixture) url.Values {
			secret := f.enableTOTP(t)
			bad := "000000"
			if c, _ := totp.GenerateCode(secret, time.Now()); c == bad { bad = "111111" }
			return form(testUsername, testPassword, bad)
		}, testUserID, "bad_otp"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newGrantFixture(t)
			mustFail(t, f.post(t, testServiceID, tc.setup(f)), http.StatusBadRequest, "invalid_grant")

			row := f.tables.lastAudit(t)
			if row.Action != audit.UserLoginFailed || row.Target != tc.target || row.ViaService != testServiceID {
				t.Errorf("unexpected entry %+v", row)
			}
			if row.ActorUser != nil {
				t.Errorf("a failed login identifies no actor, got %v", row.ActorUser)
			}
			if got := afterField(t, row, "reason"); got != tc.reason {
				t.Errorf("expected reason %q, got %q", tc.reason, got)
			}
			assertNoSecrets(t, f)
		})
	}
}

func TestReusedOTPIsAudited(t *testing.T) {
	f := newGrantFixture(t)
	secret := f.enableTOTP(t)
	code, _ := totp.GenerateCode(secret, time.Now())

	mustTokens(t, f.post(t, testServiceID, form(testUsername, testPassword, code)), f.a.ks)
	if row := f.tables.lastAudit(t); !equalStrings(row.AMR, []string{"pwd", "otp"}) {
		t.Errorf("expected the login's amr [pwd otp], got %v", row.AMR)
	}

	mustFail(t, f.post(t, testServiceID, form(testUsername, testPassword, code)), http.StatusBadRequest, "invalid_grant")
	if got := afterField(t, f.tables.lastAudit(t), "reason"); got != "otp_reused" {
		t.Errorf("expected reason otp_reused, got %q", got)
	}
	assertNoSecrets(t, f)
}

func TestRateLimitedLoginIsAudited(t *testing.T) {
	f := newGrantFixture(t)
	f.a.Limiter = newLimiter(1, time.Minute)
	f.login(t, testServiceID, testUsername, "wrong")

	mustFail(t, f.login(t, testServiceID, testUsername, testPassword), http.StatusTooManyRequests, "slow_down")
	row := f.tables.lastAudit(t)
	if row.Action != audit.UserLoginFailed || row.Target != "username:"+testUsername || afterField(t, row, "reason") != "rate_limited" {
		t.Errorf("unexpected entry %+v", row)
	}
}

// A request that fails the client assertion is not a login attempt for any user
func TestInvalidClientIsNotAudited(t *testing.T) {
	f := newGrantFixture(t)
	f.login(t, "", testUsername, testPassword)
	if len(f.tables.audits) != 0 {
		t.Errorf("expected no audit entry, got %+v", f.tables.audits)
	}
}

func TestRefreshTokenReuseIsAudited(t *testing.T) {
	f := newGrantFixture(t)
	first, claims := loginTokens(t, f)
	mustTokens(t, f.refresh(t, testServiceID, first.RefreshToken), f.a.ks)

	mustFail(t, f.refresh(t, testServiceID, first.RefreshToken), http.StatusBadRequest, "invalid_grant")
	row := f.tables.lastAudit(t)
	if row.Action != audit.RefreshTokenReuse || row.Target != testUserID || row.ActorUser != nil {
		t.Errorf("unexpected entry %+v", row)
	}
	if afterField(t, row, "session") != claims.SID || afterField(t, row, "state") != "revoked" {
		t.Errorf("expected the session %s to be recorded as revoked, got %v", claims.SID, row.After)
	}
}

func TestRefreshByWrongClientIsAudited(t *testing.T) {
	f := newGrantFixture(t)
	first, _ := loginTokens(t, f)
	f.asService(otherService)

	mustFail(t, f.refresh(t, otherService, first.RefreshToken), http.StatusBadRequest, "invalid_grant")
	row := f.tables.lastAudit(t)
	if row.Action != audit.RefreshTokenMisused || row.ViaService != otherService || afterField(t, row, "issued_to") != testServiceID {
		t.Errorf("unexpected entry %+v", row)
	}
}

func TestRefreshForDisabledUserIsAudited(t *testing.T) {
	f := newGrantFixture(t)
	first, _ := loginTokens(t, f)
	now := time.Now()
	f.user.DisabledAt = &now

	mustFail(t, f.refresh(t, testServiceID, first.RefreshToken), http.StatusBadRequest, "invalid_grant")
	row := f.tables.lastAudit(t)
	if row.Action != audit.SessionRevoke || afterField(t, row, "reason") != "user_disabled" {
		t.Errorf("unexpected entry %+v", row)
	}
}

// A failed audit write is logged, but never turns a good login into a failure
func TestLoginSucceedsWhenAuditWriteFails(t *testing.T) {
	f := newGrantFixture(t)
	db := f.a.ks.db.(*modelstest.FakeDB)
	exec := db.ExecFunc
	db.ExecFunc = func(sql string, args []any) (pgconn.CommandTag, error) {
		if strings.Contains(sql, audit.Table) { return pgconn.CommandTag{}, fmt.Errorf("audit table down") }
		return exec(sql, args)
	}
	loginTokens(t, f)
}

func form(username, password, otp string) url.Values {
	v := url.Values{"grant_type": {"password"}, "username": {username}, "password": {password}}
	if otp != "" { v.Set("otp", otp) }
	return v
}

// assertNoSecrets checks no audit entry holds the password or an OTP code
func assertNoSecrets(t *testing.T, f *grantFixture) {
	t.Helper()
	for _, row := range f.tables.audits {
		s := fmt.Sprintf("%+v", row)
		if strings.Contains(s, testPassword) || strings.Contains(s, "otp\":") {
			t.Errorf("audit entry holds a secret: %s", s)
		}
	}
}
