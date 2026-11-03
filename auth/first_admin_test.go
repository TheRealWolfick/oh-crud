package auth

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/alexedwards/argon2id"
	"github.com/jackc/pgx/v5/pgconn"
	"lotusforge.au/api-server/globals"
	"lotusforge.au/api-server/models/modelstest"
)

// firstAdminDB is a database holding people users (service rows aside)
func firstAdminDB(people int) *modelstest.FakeDB {
	db := &modelstest.FakeDB{Row: []any{people}}
	db.ExecFunc = func(sql string, args []any) (pgconn.CommandTag, error) {
		switch {
		case sql == globals.SQLFirstAdminLock:
			return pgconn.NewCommandTag("SELECT 1"), nil
		case strings.Contains(sql, "INSERT INTO ohc_users"), strings.Contains(sql, "INSERT INTO "+globals.AuditTable):
			return pgconn.NewCommandTag("INSERT 0 1"), nil
		}
		return pgconn.CommandTag{}, nil
	}
	return db
}

func firstAdminAuth(db *modelstest.FakeDB) *Auth {
	return &Auth{ks: NewKeyStore("iss", "aud", db, testMaster)}
}

// calls returns the statements run whose SQL contains part, in order
func calls(db *modelstest.FakeDB, part string) []modelstest.Call {
	var out []modelstest.Call
	for _, c := range db.Calls {
		if strings.Contains(c.SQL, part) { out = append(out, c) }
	}
	return out
}

func TestFirstAdminCreated(t *testing.T) {
	db := firstAdminDB(0)
	const password = "the operator's choice"
	created, generated, err := firstAdminAuth(db).EnsureFirstAdmin(context.Background(), "administrator", password)
	if err != nil || !created || generated != "" {
		t.Fatalf("expected the admin created with the given password: created=%v generated=%q err=%v", created, generated, err)
	}

	// The lock comes before the count, so instances starting together can't both create one
	if len(db.Calls) < 2 || db.Calls[0].SQL != globals.SQLFirstAdminLock || db.Calls[1].SQL != globals.SQLUserCount {
		t.Fatalf("expected the lock, then the count; got %+v", db.Calls)
	}

	// SQLInsertUser: id, username, display_name, password_hash, roles, needs_new_password
	inserts := calls(db, "INSERT INTO ohc_users")
	if len(inserts) != 1 {
		t.Fatalf("expected one user inserted, got %d", len(inserts))
	}
	args := inserts[0].Args
	if args[1] != globals.FirstAdminUsername || !equalStrings(args[4].([]string), []string{"administrator"}) || args[5] != true {
		t.Errorf("expected %s with the admin role, due a new password; got %v", globals.FirstAdminUsername, args)
	}
	if ok, err := argon2id.ComparePasswordAndHash(password, *args[3].(*string)); err != nil || !ok {
		t.Errorf("expected a hash of the given password (%v)", err)
	}

	// Audited with no actor (the system), in the same transaction, without the password
	audits := calls(db, "INSERT INTO "+globals.AuditTable)
	if len(audits) != 1 {
		t.Fatalf("expected one audit entry, got %d", len(audits))
	}
	a := audits[0].Args // action, target, service, actor_user, via_service, amr, org_id, before, after, ...
	if a[0] != globals.ActionUserCreate || a[1] != args[0] || a[2] != globals.AuditSystem || a[3] != nil {
		t.Errorf("unexpected audit entry %v", a)
	}
	var after map[string]any
	if err := json.Unmarshal([]byte(a[8].(string)), &after); err != nil || after["reason"] != "bootstrap" || after["username"] != globals.FirstAdminUsername {
		t.Errorf("expected after to name the bootstrap and username, got %v (%v)", a[8], err)
	}
	if strings.Contains(a[8].(string), password) {
		t.Errorf("the audit entry holds the password")
	}
	if db.Tx == nil || !db.Tx.Committed {
		t.Errorf("expected the user and its audit entry committed together")
	}
}

func TestFirstAdminGeneratesAPassword(t *testing.T) {
	db := firstAdminDB(0)
	created, generated, err := firstAdminAuth(db).EnsureFirstAdmin(context.Background(), "administrator", "")
	if err != nil || !created {
		t.Fatalf("expected the admin created: %v", err)
	}
	if len(generated) != globals.FirstAdminPasswordLength {
		t.Fatalf("expected a %d character password, got %q", globals.FirstAdminPasswordLength, generated)
	}
	hash := *calls(db, "INSERT INTO ohc_users")[0].Args[3].(*string)
	if ok, _ := argon2id.ComparePasswordAndHash(generated, hash); !ok {
		t.Errorf("the stored hash must be of the password returned")
	}

	// Two starts never make the same password
	_, again, _ := firstAdminAuth(firstAdminDB(0)).EnsureFirstAdmin(context.Background(), "administrator", "")
	if again == generated {
		t.Errorf("expected a new random password each time")
	}
}

// Once anyone can sign in, nothing happens, and nothing else is checked: a password
// left in the environment, or no admin role, doesn't stop the instance starting
func TestFirstAdminNotNeeded(t *testing.T) {
	for name, tc := range map[string]struct{ role, password string }{
		"normal":                  {"administrator", "the operator's choice"},
		"short password left set": {"administrator", "short"},
		"no admin role":           {"", ""},
	} {
		t.Run(name, func(t *testing.T) {
			db := firstAdminDB(1)
			created, generated, err := firstAdminAuth(db).EnsureFirstAdmin(context.Background(), tc.role, tc.password)
			if err != nil || created || generated != "" {
				t.Errorf("expected nothing done: created=%v generated=%q err=%v", created, generated, err)
			}
			if len(calls(db, "INSERT")) != 0 {
				t.Errorf("nothing may be inserted")
			}
		})
	}
}

func TestFirstAdminRefused(t *testing.T) {
	for name, tc := range map[string]struct{ role, password, want string }{
		"no admin role":  {"", "the operator's choice", "rbac.admin-role"},
		"short password": {"administrator", "short", globals.FirstAdminPasswordEnv},
	} {
		t.Run(name, func(t *testing.T) {
			db := firstAdminDB(0)
			created, _, err := firstAdminAuth(db).EnsureFirstAdmin(context.Background(), tc.role, tc.password)
			if err == nil || created || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("expected an error naming %s, got created=%v err=%v", tc.want, created, err)
			}
			if len(calls(db, "INSERT")) != 0 || db.Tx.Committed {
				t.Errorf("a refusal must change nothing")
			}
		})
	}
}

// Service rows are not people: an instance only services have used still gets an admin
func TestFirstAdminIgnoresServiceRows(t *testing.T) {
	if !strings.Contains(globals.SQLUserCount, "NOT LIKE 'svc:%'") {
		t.Errorf("the user count must leave out service rows: %s", globals.SQLUserCount)
	}
}
