package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/argon2id"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pquerna/otp/totp"
	"lotusforge.au/api-server/globals"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/models/modelstest"
)

const newTestPassword = "a brand new passphrase"

// changeForm is a password grant that also carries new_password (and otp, when set)
func changeForm(password, newPassword, otp string) url.Values {
	v := url.Values{"grant_type": {"password"}, "username": {testUsername}, "password": {password}}
	if newPassword != "" { v.Set("new_password", newPassword) }
	if otp != "" { v.Set("otp", otp) }
	return v
}

// mustDescribe expects a 400 with the given error code and some error_description
func mustDescribe(t *testing.T, body string, code string) {
	t.Helper()
	var m map[string]string
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("expected a JSON error body, got %q", body)
	}
	if m["error"] != code || m["error_description"] == "" {
		t.Errorf("expected error %q with a description, got %s", code, body)
	}
}

// passwordMatches reports whether the stored hash is the hash of password
func passwordMatches(t *testing.T, f *grantFixture, password string) bool {
	t.Helper()
	ok, err := argon2id.ComparePasswordAndHash(password, f.user.PasswordHash)
	if err != nil {
		t.Fatalf("compare hash: %v", err)
	}
	return ok
}

func TestPasswordChangeRequired(t *testing.T) {
	f := newGrantFixture(t)
	f.user.NeedsNewPassword = true

	w := f.login(t, testServiceID, testUsername, testPassword)
	mustFail(t, w, http.StatusBadRequest, "password_change_required")
	mustDescribe(t, w.Body.String(), "password_change_required")

	row := f.tables.lastAudit(t)
	if row.Action != globals.ActionUserLoginFailed || row.Target != testUserID || afterField(t, row, "reason") != "password_change_required" {
		t.Errorf("expected a failed login with reason password_change_required, got %+v", row)
	}
	if !passwordMatches(t, f, testPassword) || !f.user.NeedsNewPassword {
		t.Errorf("nothing may change until a new password is given")
	}
}

// The distinct answer only comes once the password is right, so it can't confirm a guess
func TestPasswordChangeRequiredOnlyAfterTheRightPassword(t *testing.T) {
	f := newGrantFixture(t)
	f.user.NeedsNewPassword = true

	wrong := f.post(t, testServiceID, changeForm("wrong", "", ""))
	unknown := f.post(t, testServiceID, url.Values{"grant_type": {"password"}, "username": {"mallory"}, "password": {"wrong"}})
	mustFail(t, wrong, http.StatusBadRequest, "invalid_grant")
	if wrong.Body.String() != unknown.Body.String() {
		t.Errorf("a wrong password must look like an unknown user: %q vs %q", wrong.Body.String(), unknown.Body.String())
	}

	// The same holds with TOTP: a right password and no code must not reveal the reset
	f.enableTOTP(t)
	mustFail(t, f.login(t, testServiceID, testUsername, testPassword), http.StatusBadRequest, "invalid_grant")
}

func TestPasswordChangeRejectsBadNewPasswords(t *testing.T) {
	for name, tc := range map[string]struct {
		newPassword string
		required    bool
	}{
		"required, too short":  {strings.Repeat("x", globals.MinPassword-1), true},
		"required, unchanged":  {testPassword, true},
		"voluntary, too short": {strings.Repeat("x", globals.MinPassword-1), false},
		"voluntary, unchanged": {testPassword, false},
	} {
		t.Run(name, func(t *testing.T) {
			f := newGrantFixture(t)
			f.user.NeedsNewPassword = tc.required
			newPassword := tc.newPassword
			loginsBefore := len(f.tables.tokens)

			w := f.post(t, testServiceID, changeForm(testPassword, newPassword, ""))
			mustFail(t, w, http.StatusBadRequest, "invalid_new_password")
			mustDescribe(t, w.Body.String(), "invalid_new_password")
			if strings.Contains(w.Body.String(), newPassword) {
				t.Errorf("the response must not echo the password: %s", w.Body.String())
			}
			if !passwordMatches(t, f, testPassword) || f.user.NeedsNewPassword != tc.required || len(f.tables.tokens) != loginsBefore {
				t.Errorf("a refused new password must change nothing and issue no tokens")
			}
		})
	}
}

func TestPasswordChange(t *testing.T) {
	f := newGrantFixture(t)
	f.user.NeedsNewPassword = true

	_, claims := mustTokens(t, f.post(t, testServiceID, changeForm(testPassword, newTestPassword, "")), f.a.ks)
	if claims.Subject != testUserID {
		t.Errorf("expected tokens for %s, got %s", testUserID, claims.Subject)
	}
	if !passwordMatches(t, f, newTestPassword) || f.user.NeedsNewPassword {
		t.Fatalf("expected the new password stored and the flag cleared")
	}

	// The change is audited before the login, and neither entry holds a password
	n := len(f.tables.audits)
	change, login := f.tables.audits[n-2], f.tables.audits[n-1]
	if change.Action != globals.ActionUserPasswordChange || change.Target != testUserID || change.ActorUser != testUsername ||
		afterField(t, change, "reason") != "required" {
		t.Errorf("unexpected password change entry %+v", change)
	}
	if login.Action != globals.ActionUserLogin {
		t.Errorf("expected the login after the change, got %+v", login)
	}
	for _, row := range f.tables.audits {
		if s := strings.ToLower(strings.Join([]string{row.Action, row.Target, afterOrEmpty(row)}, " ")); strings.Contains(s, newTestPassword) {
			t.Errorf("audit entry holds the new password: %+v", row)
		}
	}
	assertNoSecrets(t, f)

	// The temporary password is gone, and the new one works without asking again
	mustFail(t, f.login(t, testServiceID, testUsername, testPassword), http.StatusBadRequest, "invalid_grant")
	mustTokens(t, f.login(t, testServiceID, testUsername, newTestPassword), f.a.ks)
}

// Any user may replace their password at login. Their other sessions end at their next
// refresh, while this login's own tokens keep working.
func TestVoluntaryPasswordChange(t *testing.T) {
	f := newGrantFixture(t)
	withBlocks(f, time.Now()) // a block-list entry now would also catch this login's tokens
	other, _ := loginTokens(t, f)

	body, claims := mustTokens(t, f.post(t, testServiceID, changeForm(testPassword, newTestPassword, "")), f.a.ks)
	if !passwordMatches(t, f, newTestPassword) || f.user.NeedsNewPassword {
		t.Fatalf("expected the new password stored, with no flag set")
	}

	n := len(f.tables.audits)
	if change := f.tables.audits[n-2]; change.Action != globals.ActionUserPasswordChange || afterField(t, change, "reason") != "voluntary" {
		t.Errorf("expected a voluntary password change entry, got %+v", change)
	}

	// This login's tokens work: the access token at once, the refresh token to rotate
	mw := f.a.ForModel(modelWith(models.Access{}))
	svc := serviceToken(t, f.a, testServiceID, "reader")
	if w, _ := call(mw, svc, body.AccessToken); w.Code != http.StatusOK {
		t.Errorf("expected the new access token to work, got %d %s", w.Code, w.Body.String())
	}
	if f.tables.token(body.RefreshToken).FamilyID != claims.SID {
		t.Fatalf("expected the refresh token in this login's session")
	}
	mustTokens(t, f.refresh(t, testServiceID, body.RefreshToken), f.a.ks)

	// The session from before the change ends at its next refresh
	mustFail(t, f.refresh(t, testServiceID, other.RefreshToken), http.StatusBadRequest, "invalid_grant")

	// The old password is gone
	mustFail(t, f.login(t, testServiceID, testUsername, testPassword), http.StatusBadRequest, "invalid_grant")
}

// A change replaces the hash outright, so the rehash of the old password must not run
func TestPasswordChangeSkipsTheRehash(t *testing.T) {
	for name, required := range map[string]bool{"required": true, "voluntary": false} {
		t.Run(name, func(t *testing.T) {
			f := newGrantFixture(t)
			f.user.NeedsNewPassword = required
			weak, err := argon2id.CreateHash(testPassword, &argon2id.Params{Memory: 16 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32})
			if err != nil {
				t.Fatalf("CreateHash: %v", err)
			}
			f.user.PasswordHash = weak
			db := f.a.ks.db.(*modelstest.FakeDB)

			mustTokens(t, f.post(t, testServiceID, changeForm(testPassword, newTestPassword, "")), f.a.ks)
			if !passwordMatches(t, f, newTestPassword) {
				t.Errorf("expected the new password stored")
			}
			// The voluntary change's statement has the same text as the rehash's, so look
			// at what each password update stored: never a hash of the old password
			for _, c := range db.Calls {
				if !strings.HasPrefix(c.SQL, "UPDATE ohc_users SET password_hash") { continue }
				if ok, _ := argon2id.ComparePasswordAndHash(testPassword, c.Args[0].(string)); ok {
					t.Errorf("the old password must not be rehashed after a change")
				}
			}
		})
	}
}

// Being asked for a new password must not spend the OTP code, but the login that uses
// it must
func TestPasswordChangeWithTOTP(t *testing.T) {
	f := newGrantFixture(t)
	secret := f.enableTOTP(t)
	f.user.NeedsNewPassword = true

	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	mustFail(t, f.post(t, testServiceID, changeForm(testPassword, "", code)), http.StatusBadRequest, "password_change_required")
	mustFail(t, f.post(t, testServiceID, changeForm(testPassword, "short", code)), http.StatusBadRequest, "invalid_new_password")

	_, claims := mustTokens(t, f.post(t, testServiceID, changeForm(testPassword, newTestPassword, code)), f.a.ks)
	if !equalStrings(claims.AMR, []string{"pwd", "otp"}) {
		t.Errorf("expected amr [pwd otp], got %v", claims.AMR)
	}

	// Now the code is spent
	mustFail(t, f.post(t, testServiceID, changeForm(newTestPassword, "", code)), http.StatusBadRequest, "invalid_grant")
	if row := f.tables.lastAudit(t); afterField(t, row, "reason") != "otp_reused" {
		t.Errorf("expected otp_reused, got %+v", row)
	}
}

// If the password changes between the login reading it and saving the new one (another
// request won, or an admin reset it again), the login fails and the other change stands
func TestPasswordChangeLosesToAConcurrentChange(t *testing.T) {
	f := newGrantFixture(t)
	f.user.NeedsNewPassword = true
	db := f.a.ks.db.(*modelstest.FakeDB)
	exec := db.ExecFunc
	db.ExecFunc = func(sql string, args []any) (pgconn.CommandTag, error) {
		if sql == globals.SQLUpdatePasswordHash {
			f.tables.mu.Lock()
			f.user.PasswordHash = "changed elsewhere"
			f.tables.mu.Unlock()
		}
		return exec(sql, args)
	}

	mustFail(t, f.post(t, testServiceID, changeForm(testPassword, newTestPassword, "")), http.StatusBadRequest, "invalid_grant")
	if f.user.PasswordHash != "changed elsewhere" || !f.user.NeedsNewPassword {
		t.Errorf("the other change must stand")
	}
	if row := f.tables.lastAudit(t); afterField(t, row, "reason") != "password_changed_concurrently" {
		t.Errorf("expected password_changed_concurrently, got %+v", row)
	}
	if len(f.tables.tokens) != 0 {
		t.Errorf("no tokens may be issued")
	}
}

func TestPasswordChangeDatabaseError(t *testing.T) {
	f := newGrantFixture(t)
	f.user.NeedsNewPassword = true
	db := f.a.ks.db.(*modelstest.FakeDB)
	exec := db.ExecFunc
	db.ExecFunc = func(sql string, args []any) (pgconn.CommandTag, error) {
		if sql == globals.SQLUpdatePasswordHash { return pgconn.CommandTag{}, errors.New("database down") }
		return exec(sql, args)
	}

	mustFail(t, f.post(t, testServiceID, changeForm(testPassword, newTestPassword, "")), http.StatusInternalServerError, "server_error")
	if len(f.tables.tokens) != 0 || !f.user.NeedsNewPassword {
		t.Errorf("a failed change must issue no tokens and keep the flag")
	}
}

// afterOrEmpty is an entry's after field as text, or "" when there is none
func afterOrEmpty(row auditRow) string {
	s, _ := row.After.(string)
	return s
}
