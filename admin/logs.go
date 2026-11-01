package admin

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// LogEntry is one row of ohc_logs
type LogEntry struct {
	At        time.Time      `json:"at" db:"at"`
	Level     string         `json:"level" db:"level"`
	Msg       string         `json:"msg" db:"msg"`
	RequestID *string        `json:"request_id" db:"request_id"`
	Service   string         `json:"service" db:"service"`
	Attrs     map[string]any `json:"attrs" db:"attrs"`
}

// slog's numbers for the levels, as stored in ohc_logs.severity
var severity = map[string]int{"debug": -4, "info": 0, "warn": 4, "error": 8}

// GET /admin/v1/logs?level=warn&service=&request_id=&q=&since=&until=&cursor=&limit=
// Newest first. level is the lowest level shown. q searches the message.
func (a *API) listLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var where []string
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "?", "$"+strconv.Itoa(len(args))))
	}

	if l := strings.ToLower(q.Get("level")); l != "" {
		s, ok := severity[l]
		if !ok { problem(w, r, http.StatusBadRequest, "Unknown level", "Use debug, info, warn or error."); return }
		add("severity >= ?", s)
	}
	if v := q.Get("service"); v != "" { add("service = ?", v) }
	if v := q.Get("request_id"); v != "" { add("request_id = ?", v) }
	if v := q.Get("q"); v != "" { add(`msg ILIKE ? ESCAPE '\'`, "%"+likeEscape(v)+"%") }
	since, err := parseWhen(q.Get("since"), false)
	if err != nil { problem(w, r, http.StatusBadRequest, "Bad since", err.Error()); return }
	until, err := parseWhen(q.Get("until"), true)
	if err != nil { problem(w, r, http.StatusBadRequest, "Bad until", err.Error()); return }
	if !since.IsZero() { add("at >= ?", since) }
	if !until.IsZero() { add("at < ?", until) }

	// Logs have no id: the cursor is the time of the last row shown. Rows logged in the
	// same microsecond as a page boundary can be skipped, which is rare enough for logs.
	after, ok := cursor(r)
	if !ok { problem(w, r, http.StatusBadRequest, "Bad cursor", ""); return }
	if after != "" {
		t, err := time.Parse(time.RFC3339Nano, after)
		if err != nil { problem(w, r, http.StatusBadRequest, "Bad cursor", ""); return }
		add("at < ?", t)
	}

	n := limit(r)
	sql := `SELECT at, level, msg, request_id, service, attrs FROM ohc_logs`
	if len(where) > 0 { sql += " WHERE " + strings.Join(where, " AND ") }
	args = append(args, n+1)
	sql += " ORDER BY at DESC LIMIT $" + strconv.Itoa(len(args))

	rows, err := a.d.DB.Query(r.Context(), sql, args...)
	if err != nil { a.serverError(w, r, "list logs", err); return }
	list, err := pgx.CollectRows(rows, pgx.RowToStructByName[LogEntry])
	if err != nil { a.serverError(w, r, "list logs", err); return }
	writeJSON(w, trim(list, n, func(e LogEntry) string { return e.At.Format(time.RFC3339Nano) }))
}
