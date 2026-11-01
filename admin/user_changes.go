package admin

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"lotusforge.au/api-server/audit"
	"lotusforge.au/api-server/auth"
	"lotusforge.au/api-server/models"
)

// rolePattern is a role name: a plain role, or <service>:<role>
var rolePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$`)

// minPassword is the shortest password an admin may set
const minPassword = 12

// isAdminRole reports whether role makes its holder an admin: the platform admin role,
// or a service's <service>:admin
func (a *API) isAdminRole(role string) bool {
	return role == a.d.Config.Get().AdminRole() || (strings.HasSuffix(role, ":admin") && len(role) > len(":admin"))
}

// anyAdmin lets in platform admins and the admin of any service; the route then checks
// each role it touches (canGrant)
func (a *API) anyAdmin(_ *http.Request, u *models.Claims) bool { return hasAdminRole(u.Roles, a.d.Config.Get().AdminRole()) }

// canGrant reports whether the caller may give or take role: a platform admin any role,
// a service admin only that service's roles (appointing other admins of it included)
func (a *API) canGrant(r *http.Request, role string) bool {
	u := auth.PrincipalFrom(r.Context()).User
	if a.platformAdmin(r, u) { return true }
	svc, _, ok := strings.Cut(role, ":")
	return ok && svc != "" && slices.Contains(u.Roles, svc+":admin")
}

// lastHolder reports whether user is the only active holder of role
func lastHolder(ctx context.Context, tx pgx.Tx, role string, user string) (bool, error) {
	var others int
	err := tx.QueryRow(ctx, `SELECT count(*) FROM ohc_users WHERE $1 = ANY(roles) AND disabled_at IS NULL AND id <> $2`, role, user).Scan(&others)
	return others == 0, err
}

// lockUser reads a user for change, refusing service rows
func lockUser(ctx context.Context, tx pgx.Tx, id string) (roles []string, disabled bool, err error) {
	err = tx.QueryRow(ctx, `SELECT COALESCE(roles, '{}'), disabled_at IS NOT NULL FROM ohc_users WHERE id = $1 FOR UPDATE`, id).Scan(&roles, &disabled)
	if errors.Is(err, pgx.ErrNoRows) { return nil, false, refuse(http.StatusNotFound, "No such user", "No user %q.", id) }
	if err == nil && strings.HasPrefix(id, "svc:") {
		err = refuse(http.StatusConflict, "That is a service row", "Service rows record a service's own writes; manage the service instead.")
	}
	return roles, disabled, err
}

func cleanRoles(in []string) ([]string, error) {
	out := []string{}
	for _, r := range in {
		r = strings.TrimSpace(r)
		if !rolePattern.MatchString(r) { return nil, refuse(http.StatusBadRequest, "Bad role", "%q is not a role name.", r) }
		if !slices.Contains(out, r) { out = append(out, r) }
	}
	sort.Strings(out)
	return out, nil
}

// POST /admin/v1/users {"username", "display_name"?, "password"?, "roles"?}
// Without a password the user can't sign in with one (an Entra user, say).
func (a *API) createUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username    string   `json:"username"`
		DisplayName string   `json:"display_name"`
		Password    string   `json:"password"`
		Roles       []string `json:"roles"`
	}
	if err := decode(r, &in); err != nil { a.fail(w, r, "", err); return }
	roles, err := cleanRoles(in.Roles)
	if err != nil { a.fail(w, r, "", err); return }
	if in.Password != "" && len(in.Password) < minPassword {
		a.fail(w, r, "", refuse(http.StatusBadRequest, "Password too short", "Use at least %d characters.", minPassword))
		return
	}

	var id string
	err = a.inTx(r.Context(), func(tx pgx.Tx) error {
		u, err := a.d.Auth.CreateUserWith(r.Context(), tx, strings.TrimSpace(in.Username), in.DisplayName, in.Password, roles)
		if err != nil {
			if strings.Contains(err.Error(), "username") { return refuse(http.StatusBadRequest, "Bad username", "%v", err) }
			return err
		}
		id = u.ID
		e := a.event(r, audit.UserCreate, u.ID)
		e.After = map[string]any{"username": u.Username, "roles": roles, "password": in.Password != ""}
		return audit.Record(r.Context(), tx, e)
	})
	if err != nil { a.fail(w, r, "create user", err); return }
	a.writeUser(w, r, http.StatusCreated, id)
}

// PUT /admin/v1/users/{id}/roles {"roles": [...]}: the user's whole role list.
// Removing a role refuses the user's current access tokens, so it applies at once; their
// next refresh carries the new list without a new login.
func (a *API) setUserRoles(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct{ Roles []string `json:"roles"` }
	if err := decode(r, &in); err != nil { a.fail(w, r, "", err); return }
	want, err := cleanRoles(in.Roles)
	if err != nil { a.fail(w, r, "", err); return }

	var removed []string
	err = a.inTx(r.Context(), func(tx pgx.Tx) error {
		have, _, err := lockUser(r.Context(), tx, id)
		if err != nil { return err }
		sort.Strings(have)
		var added []string
		for _, role := range want { if !slices.Contains(have, role) { added = append(added, role) } }
		for _, role := range have { if !slices.Contains(want, role) { removed = append(removed, role) } }
		if len(added) == 0 && len(removed) == 0 { return nil }

		for _, role := range append(append([]string{}, added...), removed...) {
			if !a.canGrant(r, role) { return refuse(http.StatusForbidden, "You can't change that role", "You may not give or take %q.", role) }
		}
		for _, role := range removed {
			if !a.isAdminRole(role) { continue }
			if last, err := lastHolder(r.Context(), tx, role, id); err != nil {
				return err
			} else if last {
				return refuse(http.StatusConflict, "Last admin", "%q is the last active user with %q. Give it to someone else first.", id, role)
			}
		}

		if _, err := tx.Exec(r.Context(), `UPDATE ohc_users SET roles = $2 WHERE id = $1`, id, want); err != nil { return err }
		e := a.event(r, audit.UserRolesUpdate, id)
		e.Service = roleService(append(append([]string{}, added...), removed...))
		e.Before, e.After = map[string][]string{"roles": have}, map[string][]string{"roles": want}
		return audit.Record(r.Context(), tx, e)
	})
	if err != nil { a.fail(w, r, "set roles", err); return }
	if len(removed) > 0 {
		a.after(r, "refuse old access tokens", a.d.Auth.RevokeAccessTokens(r.Context(), id, "roles removed"))
	}
	a.writeUser(w, r, http.StatusOK, id)
}

// roleService is the service a role change belongs to, for the audit entry: the one
// service whose roles changed, so its admins see it; otherwise Oh CRUD's own
func roleService(roles []string) string {
	svc := ""
	for _, r := range roles {
		s, _, ok := strings.Cut(r, ":")
		if !ok || (svc != "" && s != svc) { return audit.System }
		svc = s
	}
	if svc == "" { return audit.System }
	return svc
}

// POST /admin/v1/users/{id}/disable and /enable. Disabling ends every session the user
// has and stops them signing in. Nobody can disable themselves, or the last admin.
func (a *API) setUserDisabled(disable bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		action := audit.UserEnable
		if disable { action = audit.UserDisable }
		changed := false
		err := a.inTx(r.Context(), func(tx pgx.Tx) error {
			roles, was, err := lockUser(r.Context(), tx, id)
			if err != nil { return err }
			if was == disable { return nil }
			if disable {
				if id == auth.PrincipalFrom(r.Context()).User.Subject {
					return refuse(http.StatusConflict, "That's you", "You can't disable your own account.")
				}
				for _, role := range roles {
					if !a.isAdminRole(role) { continue }
					if last, err := lastHolder(r.Context(), tx, role, id); err != nil {
						return err
					} else if last {
						return refuse(http.StatusConflict, "Last admin", "%q is the last active user with %q.", id, role)
					}
				}
			}
			sql := `UPDATE ohc_users SET disabled_at = NULL WHERE id = $1`
			if disable { sql = `UPDATE ohc_users SET disabled_at = now() WHERE id = $1` }
			if _, err := tx.Exec(r.Context(), sql, id); err != nil { return err }
			changed = true
			e := a.event(r, action, id)
			e.Before, e.After = map[string]bool{"disabled": was}, map[string]bool{"disabled": disable}
			return audit.Record(r.Context(), tx, e)
		})
		if err != nil { a.fail(w, r, action, err); return }
		if disable && changed {
			a.after(r, "end the user's sessions", a.d.Auth.RevokeUser(r.Context(), id, "user disabled"))
		}
		a.writeUser(w, r, http.StatusOK, id)
	}
}

// POST /admin/v1/users/{id}/sessions/revoke: end every session the user has
func (a *API) revokeUserSessions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := a.inTx(r.Context(), func(tx pgx.Tx) error {
		if _, _, err := lockUser(r.Context(), tx, id); err != nil { return err }
		return audit.Record(r.Context(), tx, a.event(r, audit.UserSessionsRevoke, id))
	})
	if err != nil { a.fail(w, r, "revoke sessions", err); return }
	a.after(r, "end the user's sessions", a.d.Auth.RevokeUser(r.Context(), id, "ended by an admin"))
	a.writeUser(w, r, http.StatusOK, id)
}

// POST /admin/v1/users/{id}/sessions/{session}/revoke: end one session
func (a *API) revokeUserSession(w http.ResponseWriter, r *http.Request) {
	id, session := r.PathValue("id"), r.PathValue("session")
	err := a.inTx(r.Context(), func(tx pgx.Tx) error {
		if _, _, err := lockUser(r.Context(), tx, id); err != nil { return err }
		var live bool
		err := tx.QueryRow(r.Context(), `SELECT bool_or(revoked_at IS NULL AND used_at IS NULL AND expires_at > now())
			FROM ohc_refresh_tokens WHERE user_id = $1 AND family_id = $2 HAVING count(*) > 0`, id, session).Scan(&live)
		if errors.Is(err, pgx.ErrNoRows) { return refuse(http.StatusNotFound, "No such session", "User %q has no session %q.", id, session) }
		if err != nil { return err }
		e := a.event(r, audit.UserSessionRevoke, id)
		e.Before = map[string]any{"session": session, "live": live}
		return audit.Record(r.Context(), tx, e)
	})
	if err != nil { a.fail(w, r, "revoke session", err); return }
	a.d.Auth.EndSession(r.Context(), session, "ended by an admin")
	a.writeUser(w, r, http.StatusOK, id)
}

