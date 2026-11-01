package admin

import (
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
)

// Task is one entry in the overview's "needs you" list. Kind and ID say what to open.
type Task struct {
	Kind  string `json:"kind"` // user, service or schema
	ID    string `json:"id"`
	Title string `json:"title"`
}

// KeyHealth is when each service last signed
type KeyHealth struct {
	Service    string     `json:"service"`
	LastSigned *time.Time `json:"last_signed_at"`
	Status     string     `json:"status"`
}

// The audit actions that are logins and logouts, not changes an admin made
const notAdminChanges = `('user.login', 'user.login_failed', 'user.logout', 'user.logout_all')`

// GET /admin/v1/overview: the dashboard's first screen. "Today" is the UTC day.
func (a *API) overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cfg := a.d.Config.Get()

	// Every service, for the tile and key health. There are few enough to read whole.
	services, err := a.services(ctx, "", "", 1000)
	if err != nil { a.serverError(w, r, "overview services", err); return }
	idle, disabled := 0, 0
	health := []KeyHealth{}
	var tasks []Task
	for _, s := range services {
		health = append(health, KeyHealth{Service: s.ID, LastSigned: s.LastSigned, Status: s.Status})
		switch s.Status {
		case "disabled":
			disabled++
		case "idle":
			idle++
			since := "never signed"
			if s.LastSigned != nil { since = fmt.Sprintf("quiet for %d days", int(time.Since(*s.LastSigned).Hours()/24)) }
			tasks = append(tasks, Task{Kind: "service", ID: s.ID, Title: fmt.Sprintf("%s %s", s.ID, since)})
		}
	}

	var people, activeToday, endedToday, reuseToday, adminChanges, adminChangesOTP int
	err = a.d.DB.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM ohc_users WHERE id NOT LIKE 'svc:%'),
		       (SELECT count(DISTINCT target) FROM ohc_audit_events WHERE action = 'user.login' AND at >= date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
		       (SELECT count(*) FROM ohc_audit_events WHERE action IN ('session.revoke', 'refresh_token.reuse') AND at >= date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
		       (SELECT count(*) FROM ohc_audit_events WHERE action = 'refresh_token.reuse' AND at >= date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
		       (SELECT count(*) FROM ohc_audit_events WHERE actor_user IS NOT NULL AND action NOT IN `+notAdminChanges+` AND at >= date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
		       (SELECT count(*) FROM ohc_audit_events WHERE actor_user IS NOT NULL AND action NOT IN `+notAdminChanges+` AND 'otp' = ANY(amr) AND at >= date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')`).
		Scan(&people, &activeToday, &endedToday, &reuseToday, &adminChanges, &adminChangesOTP)
	if err != nil { a.serverError(w, r, "overview counts", err); return }

	// Admins with no second factor, and anyone whose sessions were ended by token reuse
	rows, err := a.d.DB.Query(ctx, `
		SELECT id, username, COALESCE(roles, '{}') AS roles, totp_secret IS NOT NULL AS has_otp,
		       EXISTS (SELECT 1 FROM ohc_audit_events a WHERE a.target = ohc_users.id AND a.action = 'refresh_token.reuse'
		                  AND a.at > now() - interval '1 day') AS reused
		  FROM ohc_users WHERE id NOT LIKE 'svc:%' AND disabled_at IS NULL ORDER BY username`)
	if err != nil { a.serverError(w, r, "overview users", err); return }
	type flagged struct {
		ID       string   `db:"id"`
		Username string   `db:"username"`
		Roles    []string `db:"roles"`
		HasOTP   bool     `db:"has_otp"`
		Reused   bool     `db:"reused"`
	}
	users, err := pgx.CollectRows(rows, pgx.RowToStructByName[flagged])
	if err != nil { a.serverError(w, r, "overview users", err); return }
	for _, u := range users {
		if !u.HasOTP && hasAdminRole(u.Roles, cfg.AdminRole()) {
			tasks = append(tasks, Task{Kind: "user", ID: u.ID, Title: u.Username + " is an admin with no second factor"})
		}
		if u.Reused {
			tasks = append(tasks, Task{Kind: "user", ID: u.ID, Title: u.Username + "'s sessions were ended after a refresh token was reused"})
		}
	}
	for _, p := range a.pending("") {
		tasks = append(tasks, Task{Kind: "schema", ID: p.Table, Title: "Schema change waiting on " + p.Table})
	}
	if tasks == nil { tasks = []Task{} }

	recent, err := a.audit(ctx, auditFilter{}, "", 5)
	if err != nil { a.serverError(w, r, "overview recent", err); return }

	writeJSON(w, map[string]any{
		"tiles": map[string]any{
			"services": map[string]int{"total": len(services), "idle": idle, "disabled": disabled},
			"people":   map[string]int{"total": people, "signed_in_today": activeToday},
			"sessions_ended_today": map[string]int{"total": endedToday, "token_reuse": reuseToday},
			"admin_changes_today":  map[string]int{"total": adminChanges, "with_otp": adminChangesOTP},
		},
		"recent_changes": recent,
		"needs_you":      tasks,
		"key_health":     health,
	})
}
