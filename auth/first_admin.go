package auth

import (
	"context"
	"errors"
	"fmt"

	"lotusforge.au/api-server/audit"
	"lotusforge.au/api-server/globals"
	"lotusforge.au/api-server/secrets"
)

// EnsureFirstAdmin gives a new instance someone to sign in as. When no people can sign
// in yet (service rows don't count), it creates globals.FirstAdminUsername holding
// adminRole, who must choose a new password at their first login. password is the
// temporary one; when empty, a random one is made and returned as generated, for the
// caller to show once. It is never logged or recorded.
//
// The check and the insert run under an advisory lock, so of several instances starting
// on an empty database only one creates the admin. created is false when there were
// already people, and then nothing else is checked: a password left in the environment
// after the first start does no harm.
func (a *Auth) EnsureFirstAdmin(ctx context.Context, adminRole string, password string) (created bool, generated string, err error) {
	tx, err := a.ks.db.Begin(ctx)
	if err != nil { return false, "", err }
	defer tx.Rollback(ctx) // does nothing once committed

	if _, err := tx.Exec(ctx, globals.SQLFirstAdminLock); err != nil { return false, "", fmt.Errorf("lock: %w", err) }
	var people int
	if err := tx.QueryRow(ctx, globals.SQLUserCount).Scan(&people); err != nil { return false, "", fmt.Errorf("count users: %w", err) }
	if people > 0 { return false, "", nil }

	// Only now do these matter: there is an admin to make
	if adminRole == "" { return false, "", errors.New("rbac.admin-role is not set, so the first admin would hold no admin role") }
	if password == "" {
		if password, err = secrets.RandomString(globals.FirstAdminPasswordLength); err != nil { return false, "", err }
		generated = password
	} else if len(password) < globals.MinPassword {
		return false, "", fmt.Errorf("%s must be at least %d characters", globals.FirstAdminPasswordEnv, globals.MinPassword)
	}

	// A user created with a password must replace it at their first login
	roles := []string{adminRole}
	u, err := a.CreateUserWith(ctx, tx, globals.FirstAdminUsername, globals.FirstAdminUsername, password, roles)
	if err != nil { return false, "", fmt.Errorf("create user: %w", err) }

	// No actor: the system made it. Recorded in the same transaction, so never unrecorded.
	e := audit.Event{Action: globals.ActionUserCreate, Target: u.ID, Service: globals.AuditSystem,
		After: map[string]any{"username": u.Username, "roles": roles, "password": true, "reason": "bootstrap"}}
	if err := audit.Record(ctx, tx, e); err != nil { return false, "", fmt.Errorf("audit: %w", err) }

	if err := tx.Commit(ctx); err != nil { return false, "", err }
	return true, generated, nil
}
