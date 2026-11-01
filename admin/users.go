package admin

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
)

// User is one row of the users list. Service rows (svc:<id>) are the users a service's
// own writes are recorded against; they can't sign in.
type User struct {
	ID            string     `json:"id" db:"id"`
	Username      string     `json:"username" db:"username"`
	DisplayName   *string    `json:"display_name" db:"display_name"`
	Roles         []string   `json:"roles" db:"roles"`
	CreatedAt     time.Time  `json:"created_at" db:"created_at"`
	DisabledAt    *time.Time `json:"disabled_at" db:"disabled_at"`
	HasPassword   bool       `json:"has_password" db:"has_password"`
	HasOTP        bool       `json:"has_otp" db:"has_otp"`
	IsService     bool       `json:"is_service" db:"is_service"`
	LastLogin     *time.Time `json:"last_login_at" db:"last_login"`
	LiveSessions  int        `json:"live_sessions" db:"live_sessions"`
	SessionsEnded bool       `json:"sessions_ended_recently" db:"sessions_ended"` // a session was ended by token reuse or a revocation in the last 7 days
	Status        string     `json:"status" db:"-"`                               // active, sessions_ended, disabled or service
}

// usersCTE works out each user's derived fields: last login from the audit log, live
// sessions from refresh tokens. A session is live while it holds an unused, unrevoked,
// unexpired refresh token.
const usersCTE = `
WITH u AS (
  SELECT id, username, display_name, COALESCE(roles, '{}') AS roles, created_at, disabled_at,
         COALESCE(password_hash, '') <> '' AS has_password,
         totp_secret IS NOT NULL           AS has_otp,
         id LIKE 'svc:%'                   AS is_service,
         (SELECT max(a.at) FROM ohc_audit_events a WHERE a.target = ohc_users.id AND a.action = 'user.login') AS last_login,
         (SELECT count(DISTINCT t.family_id) FROM ohc_refresh_tokens t
           WHERE t.user_id = ohc_users.id AND t.revoked_at IS NULL AND t.used_at IS NULL AND t.expires_at > now()) AS live_sessions,
         EXISTS (SELECT 1 FROM ohc_audit_events a WHERE a.target = ohc_users.id
                    AND a.action IN ('refresh_token.reuse', 'session.revoke') AND a.at > now() - interval '7 days') AS sessions_ended
    FROM ohc_users)`

// The user list's tabs, as SQL over u
var userStatus = map[string]string{
	"all":            `true`,
	"active":         `NOT is_service AND disabled_at IS NULL`,
	"sessions_ended": `NOT is_service AND disabled_at IS NULL AND sessions_ended`,
	"disabled":       `disabled_at IS NOT NULL`,
	"service":        `is_service`,
}

const userSearch = `($1 = '' OR username ILIKE '%' || $1 || '%' OR id ILIKE '%' || $1 || '%'
	OR display_name ILIKE '%' || $1 || '%' OR EXISTS (SELECT 1 FROM unnest(roles) r WHERE r ILIKE '%' || $1 || '%'))`

func finishUser(u *User) {
	switch {
	case u.IsService:
		u.Status = "service"
	case u.DisabledAt != nil:
		u.Status = "disabled"
	case u.SessionsEnded:
		u.Status = "sessions_ended"
	default:
		u.Status = "active"
	}
}

// GET /admin/v1/users?status=all|active|sessions_ended|disabled|service&q=&cursor=&limit=
// Sorted by username. counts gives each tab's total for the same search.
func (a *API) listUsers(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" { status = "all" }
	where, ok := userStatus[status]
	if !ok { problem(w, r, http.StatusBadRequest, "Unknown status", "Use all, active, sessions_ended, disabled or service."); return }
	after, ok := cursor(r)
	if !ok { problem(w, r, http.StatusBadRequest, "Bad cursor", ""); return }
	q, n := r.URL.Query().Get("q"), limit(r)

	rows, err := a.d.DB.Query(r.Context(), usersCTE+`
		SELECT * FROM u WHERE `+where+` AND `+userSearch+` AND username > $2 ORDER BY username LIMIT $3`, q, after, n+1)
	if err != nil { a.serverError(w, r, "list users", err); return }
	list, err := pgx.CollectRows(rows, pgx.RowToStructByNameLax[User])
	if err != nil { a.serverError(w, r, "list users", err); return }
	for i := range list { finishUser(&list[i]) }

	counts := map[string]int{}
	var all, active, ended, disabled, service int
	err = a.d.DB.QueryRow(r.Context(), usersCTE+`
		SELECT count(*),
		       count(*) FILTER (WHERE `+userStatus["active"]+`),
		       count(*) FILTER (WHERE `+userStatus["sessions_ended"]+`),
		       count(*) FILTER (WHERE `+userStatus["disabled"]+`),
		       count(*) FILTER (WHERE `+userStatus["service"]+`)
		  FROM u WHERE `+userSearch, q).Scan(&all, &active, &ended, &disabled, &service)
	if err != nil { a.serverError(w, r, "count users", err); return }
	counts["all"], counts["active"], counts["sessions_ended"], counts["disabled"], counts["service"] = all, active, ended, disabled, service

	p := trim(list, n, func(u User) string { return u.Username })
	writeJSON(w, map[string]any{"items": p.Items, "next_cursor": p.NextCursor, "counts": counts})
}

// Session is one login session: a refresh token family
type Session struct {
	ID        string     `json:"session" db:"family_id"`
	Service   string     `json:"service" db:"client_id"`
	AMR       []string   `json:"amr" db:"amr"`
	StartedAt *time.Time `json:"started_at" db:"started_at"` // the login; unknown for sessions from before auth_time was recorded
	ExpiresAt time.Time  `json:"expires_at" db:"expires_at"`
	Live      bool       `json:"-" db:"live"`
	Revoked   bool       `json:"-" db:"revoked"`
	State     string     `json:"state" db:"-"` // live, revoked or expired
}

// userView is a user as GET /users/{id} shows it: the user, their sessions, and their
// recent audit entries
func (a *API) userView(ctx context.Context, id string) (map[string]any, error) {
	rows, err := a.d.DB.Query(ctx, usersCTE+` SELECT * FROM u WHERE id = $1`, id)
	if err != nil { return nil, err }
	u, err := pgx.CollectOneRow(rows, pgx.RowToStructByNameLax[User])
	if errors.Is(err, pgx.ErrNoRows) { return nil, refuse(http.StatusNotFound, "No such user", "No user %q.", id) }
	if err != nil { return nil, err }
	finishUser(&u)

	rows, err = a.d.DB.Query(ctx, `
		SELECT family_id, client_id, COALESCE(min(amr), '{}') AS amr, -- one amr per session; min returns it as an array
		       min(auth_time) AS started_at, max(expires_at) AS expires_at,
		       bool_or(revoked_at IS NULL AND used_at IS NULL AND expires_at > now()) AS live,
		       bool_or(revoked_at IS NOT NULL) AS revoked
		  FROM ohc_refresh_tokens WHERE user_id = $1
		 GROUP BY family_id, client_id ORDER BY max(expires_at) DESC LIMIT 50`, id)
	if err != nil { return nil, err }
	sessions, err := pgx.CollectRows(rows, pgx.RowToStructByName[Session])
	if err != nil { return nil, err }
	for i := range sessions {
		s := &sessions[i]
		switch {
		case s.Live:
			s.State = "live"
		case s.Revoked:
			s.State = "revoked"
		default:
			s.State = "expired"
		}
	}
	if sessions == nil { sessions = []Session{} }

	recent, err := a.audit(ctx, auditFilter{subject: id}, "", 20)
	if err != nil { return nil, err }
	return map[string]any{"user": u, "sessions": sessions, "recent_activity": recent}, nil
}

// GET /admin/v1/users/{id}
func (a *API) getUser(w http.ResponseWriter, r *http.Request) {
	v, err := a.userView(r.Context(), r.PathValue("id"))
	if err != nil { a.fail(w, r, "get user", err); return }
	writeJSON(w, v)
}

// writeUser answers with the user as GET /users/{id} shows it
func (a *API) writeUser(w http.ResponseWriter, r *http.Request, status int, id string) {
	v, err := a.userView(r.Context(), id)
	if err != nil { a.fail(w, r, "read user back", err); return }
	writeJSONStatus(w, status, v)
}

// hasAdminRole reports whether roles hold the platform admin role or any service's admin role
func hasAdminRole(roles []string, platform string) bool {
	return slices.ContainsFunc(roles, func(r string) bool {
		return (platform != "" && r == platform) || (len(r) > 6 && r[len(r)-6:] == ":admin")
	})
}
