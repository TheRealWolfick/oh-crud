package admin

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// AuditEntry is one row of ohc_audit_events
type AuditEntry struct {
	ID         int64           `json:"id" db:"id"`
	At         time.Time       `json:"at" db:"at"`
	Action     string          `json:"action" db:"action"`
	Target     string          `json:"target" db:"target"`
	Service    string          `json:"service" db:"service"`
	ActorUser  *string         `json:"actor_user" db:"actor_user"`
	ViaService *string         `json:"via_service" db:"via_service"`
	AMR        []string        `json:"amr" db:"amr"`
	OrgID      *string         `json:"org_id" db:"org_id"`
	Before     json.RawMessage `json:"before" db:"before"`
	After      json.RawMessage `json:"after" db:"after"`
	IP         *string         `json:"ip" db:"ip"`
	RequestID  *string         `json:"request_id" db:"request_id"`
}

type auditFilter struct {
	actor, action, target, service, requestID string
	subject                                   string // the user as target or actor
	from, to                                  time.Time
}

// csvLimit caps an export; narrow the filters for more
const csvLimit = 10000

// audit lists entries newest first, after the cursor (an id), matching f
func (a *API) audit(ctx context.Context, f auditFilter, after string, n int) ([]AuditEntry, error) {
	var where []string
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "?", "$"+strconv.Itoa(len(args))))
	}
	if f.actor != "" { add("actor_user = ?", f.actor) }
	if f.target != "" { add("target = ?", f.target) }
	if f.service != "" { add("service = ?", f.service) }
	if f.requestID != "" { add("request_id = ?", f.requestID) }
	if f.subject != "" { add("(target = ? OR actor_user = ?)", f.subject) } // both ? become the same $n
	if f.action != "" {
		if prefix, ok := strings.CutSuffix(f.action, "*"); ok {
			add(`action LIKE ? ESCAPE '\'`, likeEscape(prefix)+"%")
		} else {
			add("action = ?", f.action)
		}
	}
	if !f.from.IsZero() { add("at >= ?", f.from) }
	if !f.to.IsZero() { add("at < ?", f.to) }
	if after != "" {
		id, err := strconv.ParseInt(after, 10, 64)
		if err != nil { return nil, fmt.Errorf("bad cursor") }
		add("id < ?", id)
	}
	sql := `SELECT id, at, action, target, service, actor_user, via_service, amr, org_id, before, after, ip, request_id
	          FROM ohc_audit_events`
	if len(where) > 0 { sql += " WHERE " + strings.Join(where, " AND ") }
	args = append(args, n)
	sql += " ORDER BY id DESC LIMIT $" + strconv.Itoa(len(args))

	rows, err := a.d.DB.Query(ctx, sql, args...)
	if err != nil { return nil, err }
	list, err := pgx.CollectRows(rows, pgx.RowToStructByName[AuditEntry])
	if list == nil { list = []AuditEntry{} }
	return list, err
}

// likeEscape makes s match literally in a LIKE pattern: action names hold '_'
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// parseFilter reads the audit query parameters
func parseFilter(r *http.Request) (auditFilter, error) {
	q := r.URL.Query()
	f := auditFilter{actor: q.Get("actor"), action: q.Get("action"), target: q.Get("target"),
		service: q.Get("service"), requestID: q.Get("request_id")}
	var err error
	if f.from, err = parseWhen(q.Get("from"), false); err != nil { return f, fmt.Errorf("from: %w", err) }
	if f.to, err = parseWhen(q.Get("to"), true); err != nil { return f, fmt.Errorf("to: %w", err) }
	return f, nil
}

// parseWhen reads an RFC 3339 time or a date. A date as an upper bound means the end
// of that day, so to=2026-10-28 includes the 28th.
func parseWhen(s string, upper bool) (time.Time, error) {
	if s == "" { return time.Time{}, nil }
	if t, err := time.Parse(time.RFC3339, s); err == nil { return t, nil }
	d, err := time.Parse("2006-01-02", s)
	if err != nil { return time.Time{}, fmt.Errorf("use RFC 3339 or YYYY-MM-DD, not %q", s) }
	if upper { d = d.AddDate(0, 0, 1) }
	return d, nil
}

// GET /admin/v1/audit?actor=&action=service_key.*&target=&service=&request_id=&from=&to=&cursor=&limit=
// Add format=csv to download up to 10,000 matching entries instead.
func (a *API) listAudit(w http.ResponseWriter, r *http.Request) {
	f, err := parseFilter(r)
	if err != nil { problem(w, r, http.StatusBadRequest, "Bad filter", err.Error()); return }

	if r.URL.Query().Get("format") == "csv" {
		list, err := a.audit(r.Context(), f, "", csvLimit)
		if err != nil { a.serverError(w, r, "export audit", err); return }
		writeAuditCSV(w, list)
		return
	}

	after, ok := cursor(r)
	if !ok { problem(w, r, http.StatusBadRequest, "Bad cursor", ""); return }
	n := limit(r)
	list, err := a.audit(r.Context(), f, after, n+1)
	if err != nil { a.serverError(w, r, "list audit", err); return }
	writeJSON(w, trim(list, n, func(e AuditEntry) string { return strconv.FormatInt(e.ID, 10) }))
}

func writeAuditCSV(w http.ResponseWriter, list []AuditEntry) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="audit.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	c := csv.NewWriter(w)
	c.Write([]string{"id", "at", "action", "target", "service", "actor_user", "via_service", "amr", "org_id", "before", "after", "ip", "request_id"})
	str := func(p *string) string { if p == nil { return "" }; return *p }
	for _, e := range list {
		c.Write([]string{strconv.FormatInt(e.ID, 10), e.At.UTC().Format(time.RFC3339Nano), e.Action, e.Target, e.Service,
			str(e.ActorUser), str(e.ViaService), strings.Join(e.AMR, " "), str(e.OrgID),
			string(e.Before), string(e.After), str(e.IP), str(e.RequestID)})
	}
	c.Flush()
}
