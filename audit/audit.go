// Package audit writes the append-only record of security and admin actions to
// ohc_audit_events: who did what to which target, through which service, how they had
// signed in, and the values before and after.
//
// Record takes the pool or a transaction. An admin change should record inside the
// same transaction as the change, so the two commit or roll back together.
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"regexp"
	"strings"

	"lotusforge.au/api-server/globals"
	"lotusforge.au/api-server/httpx"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/secrets"
)

// Event is one entry. Action, Target and Service are required; leave anything unknown empty.
type Event struct {
	Action     string   // what happened: one of the constants above
	Target     string   // what it happened to: a user id, svc:<id>, svc:<id>/<kid>, username:<name>
	Service    string   // the service that owns the target
	ActorUser  string   // users.username of who did it; empty for the system
	ViaService string   // the service the request came through
	AMR        []string // how the actor had signed in
	OrgID      string
	Before     any      // marshalled to JSON; nil is stored as NULL
	After      any
	IP         string   // see IP
	RequestID  string   // see RequestID
}

// Record writes e through db, which may be a transaction
func Record(ctx context.Context, db models.DBExecutor, e Event) error {
	if e.Action == "" || e.Target == "" || e.Service == "" {
		return errors.New("audit: action, target and service are required")
	}
	before, err := toJSON(e.Before)
	if err != nil { return err }
	after, err := toJSON(e.After)
	if err != nil { return err }
	amr := e.AMR
	if amr == nil { amr = []string{} }

	_, err = db.Exec(ctx, globals.SQLInsertAuditEvent, e.Action, e.Target, e.Service, null(e.ActorUser), null(e.ViaService),
		amr, null(e.OrgID), before, after, null(e.IP), null(e.RequestID))
	return err
}

// IP is the caller's address without a port: the first valid address in
// X-Forwarded-For if there is one, else the connection's. Empty if neither parses.
func IP(r *http.Request) string {
	raw := httpx.GetIP(r)
	if first, _, found := strings.Cut(raw, ","); found { raw = first }
	raw = strings.TrimSpace(raw)
	if host, _, err := net.SplitHostPort(raw); err == nil { raw = host }
	raw = strings.Trim(raw, "[]")
	if ip := net.ParseIP(raw); ip != nil { return ip.String() }
	return ""
}

// requestIDPattern is what a caller may send as X-Request-Id. Anything else is replaced,
// so a caller can't put arbitrary text in the log.
var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{8,64}$`)

// RequestID is the caller's X-Request-Id when it is a plain token, so one request can
// be followed across services, and otherwise a new random id
func RequestID(r *http.Request) string {
	if id := r.Header.Get("X-Request-Id"); requestIDPattern.MatchString(id) { return id }
	id, _ := secrets.RandomString(24)
	return id
}

func toJSON(v any) (any, error) {
	if v == nil { return nil, nil }
	b, err := json.Marshal(v)
	if err != nil { return nil, err }
	return string(b), nil
}

func null(s string) any {
	if s == "" { return nil }
	return s
}
