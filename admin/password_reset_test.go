package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alexedwards/argon2id"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"lotusforge.au/api-server/auth"
	"lotusforge.au/api-server/globals"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/models/modelstest"
)

const tempPassword = "temporary passphrase"

// resetFixture is the admin API over a fake database holding one password user
type resetFixture struct {
	a         *API
	db        *modelstest.FakeDB
	resetRows int64 // what the reset UPDATE reports: 0 for a user without a password
}

func newResetFixture(t *testing.T) *resetFixture {
	t.Helper()
	f := &resetFixture{resetRows: 1}
	f.db = &modelstest.FakeDB{Row: []any{[]string{"editor"}, false}} // lockUser: roles, disabled
	f.db.ExecFunc = func(sql string, args []any) (pgconn.CommandTag, error) {
		switch sql {
		case globals.SQLResetPassword:
			return pgconn.NewCommandTag(fmt.Sprintf("UPDATE %d", f.resetRows)), nil
		case globals.SQLInsertAuditEvent:
			return pgconn.NewCommandTag("INSERT 0 1"), nil
		case globals.SQLRevokeUserRefreshTokens:
			return pgconn.NewCommandTag("UPDATE 2"), nil
		}
		return pgconn.CommandTag{}, errors.New("unexpected exec: " + sql)
	}
	f.db.QueryFunc = func(sql string, args []any) (pgx.Rows, error) {
		if sql == globals.SQLGetUserView {
			return &modelstest.FakeRows{
				Cols: []string{"id", "username", "has_password", "needs_new_password"},
				Data: [][]any{{args[0], "alice", true, true}},
			}, nil
		}
		return &modelstest.FakeRows{}, nil // sessions and recent activity: none
	}

	f.a = testAPI(f.db)
	au := auth.AuthHandler(f.db, f.a.d.Config, slog.New(slog.DiscardHandler), auth.NewKeyStore("iss", "aud", f.db, make([]byte, 32)))
	au.Blocks = nil // only the refresh token revocation is checked here
	f.a.d.Auth = au
	return f
}

// reset posts body to the reset route for user id, as a platform admin named root
func (f *resetFixture) reset(id string, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.Handle("POST /admin/v1/users/{id}/resetpw", f.a.changes(f.a.resetUserPassword))
	admin := &models.Claims{TokenUse: "user", Username: "root", Roles: []string{"administrator"}, AMR: []string{"pwd", "otp"},
		RegisteredClaims: jwt.RegisteredClaims{Subject: "usr_admin"}}
	r := asUser(httptest.NewRequest(http.MethodPost, "/admin/v1/users/"+id+"/resetpw", strings.NewReader(body)), admin)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

// calls returns the statements run with sql, in order
func (f *resetFixture) calls(sql string) []modelstest.Call {
	var out []modelstest.Call
	for _, c := range f.db.Calls {
		if c.SQL == sql { out = append(out, c) }
	}
	return out
}

func TestResetPassword(t *testing.T) {
	f := newResetFixture(t)
	w := f.reset("usr_2", `{"password": "`+tempPassword+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", w.Code, w.Body.String())
	}

	// The temporary password is stored hashed, against the user in the path
	resets := f.calls(globals.SQLResetPassword)
	if len(resets) != 1 || resets[0].Args[1] != "usr_2" {
		t.Fatalf("expected one reset of usr_2, got %+v", resets)
	}
	hash, _ := resets[0].Args[0].(string)
	if ok, err := argon2id.ComparePasswordAndHash(tempPassword, hash); err != nil || !ok {
		t.Errorf("expected an argon2id hash of the temporary password, got %q (%v)", hash, err)
	}

	// Audited in the same transaction, by the admin's username, without the password
	audits := f.calls(globals.SQLInsertAuditEvent)
	if len(audits) != 1 {
		t.Fatalf("expected one audit entry, got %d", len(audits))
	}
	if args := audits[0].Args; args[0] != globals.ActionUserPasswordReset || args[1] != "usr_2" || args[3] != "root" {
		t.Errorf("unexpected audit entry %v", args)
	}
	for _, c := range f.db.Calls {
		if strings.Contains(fmt.Sprint(c.Args...), tempPassword) {
			t.Errorf("the plain password reached the database: %s %v", c.SQL, c.Args)
		}
	}
	if f.db.Tx == nil || !f.db.Tx.Committed {
		t.Errorf("expected the reset and its audit entry committed together")
	}

	// The user's sessions end once it has committed
	if revokes := f.calls(globals.SQLRevokeUserRefreshTokens); len(revokes) != 1 || revokes[0].Args[0] != "usr_2" {
		t.Errorf("expected usr_2's sessions revoked, got %+v", revokes)
	}

	// The user comes back, now due a new password
	var body struct {
		User User `json:"user"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.User.ID != "usr_2" || !body.User.NeedsNewPass {
		t.Errorf("expected usr_2 back needing a new password, got %s (%v)", w.Body.String(), err)
	}
	if strings.Contains(w.Body.String(), tempPassword) {
		t.Errorf("the response must not echo the password")
	}
}

func TestResetPasswordRefused(t *testing.T) {
	for _, tc := range []struct {
		name   string
		id     string
		body   string
		setup  func(f *resetFixture)
		status int
	}{
		{name: "too short", id: "usr_2", body: `{"password": "short"}`, status: http.StatusBadRequest},
		{name: "no password", id: "usr_2", body: `{}`, status: http.StatusBadRequest},
		{name: "username in the body", id: "usr_2", body: `{"username": "alice", "password": "` + tempPassword + `"}`, status: http.StatusBadRequest},
		{name: "unknown user", id: "usr_9", body: `{"password": "` + tempPassword + `"}`,
			setup: func(f *resetFixture) { f.db.Row = nil }, status: http.StatusNotFound},
		{name: "service row", id: "svc:inventory", body: `{"password": "` + tempPassword + `"}`, status: http.StatusConflict},
		{name: "user without a password", id: "usr_2", body: `{"password": "` + tempPassword + `"}`,
			setup: func(f *resetFixture) { f.resetRows = 0 }, status: http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newResetFixture(t)
			if tc.setup != nil { tc.setup(f) }
			w := f.reset(tc.id, tc.body)
			if w.Code != tc.status {
				t.Errorf("expected %d, got %d %s", tc.status, w.Code, w.Body.String())
			}
			if len(f.calls(globals.SQLInsertAuditEvent)) != 0 || len(f.calls(globals.SQLRevokeUserRefreshTokens)) != 0 {
				t.Errorf("a refused reset must not be audited or end sessions")
			}
			if f.db.Tx != nil && f.db.Tx.Committed {
				t.Errorf("a refused reset must not commit")
			}
		})
	}
}

func TestResetPasswordRoute(t *testing.T) {
	f := newResetFixture(t)
	mux := http.NewServeMux()
	f.a.Routes(mux)
	for target, want := range map[string]string{
		"/admin/v1/users/usr_2/resetpw":   "POST /admin/v1/users/{id}/resetpw",
		"/admin/v1/users/usr_2/passreset": "",
	} {
		if _, pattern := mux.Handler(httptest.NewRequest(http.MethodPost, target, nil)); pattern != want {
			t.Errorf("POST %s: expected pattern %q, got %q", target, want, pattern)
		}
	}
}
