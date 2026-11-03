package admin

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"lotusforge.au/api-server/globals"
)

// Service is one row of the services list
type Service struct {
	ID            string     `json:"id" db:"id"`
	Role          string     `json:"role" db:"role"`
	EntraClientID *string    `json:"entra_client_id" db:"entra_client_id"`
	DisabledAt    *time.Time `json:"disabled_at" db:"disabled_at"`
	ActiveKeys    int        `json:"active_keys" db:"active_keys"`
	RevokedKeys   int        `json:"revoked_keys" db:"revoked_keys"`
	LastSigned    *time.Time `json:"last_signed_at" db:"last_signed_at"` // the latest any of its keys signed an assertion
	Status        string     `json:"status" db:"-"`                      // active, idle or disabled
	WritesAs      string     `json:"writes_recorded_as" db:"-"`
}

// status fills in what is worked out rather than stored
func (a *API) finishService(s *Service) {
	idle := time.Duration(a.d.Config.Get().ServiceIdleDays()) * 24 * time.Hour
	switch {
	case s.DisabledAt != nil:
		s.Status = "disabled"
	case s.LastSigned == nil || time.Since(*s.LastSigned) > idle:
		s.Status = "idle"
	default:
		s.Status = "active"
	}
	s.WritesAs = "svc:" + s.ID
}

// services lists services by id, after the cursor, matching q (id or role)
func (a *API) services(ctx context.Context, q string, after string, n int) ([]Service, error) {
	rows, err := a.d.DB.Query(ctx, globals.SQLListServices, q, after, n)
	if err != nil { return nil, err }
	list, err := pgx.CollectRows(rows, pgx.RowToStructByNameLax[Service])
	if err != nil { return nil, err }
	for i := range list { a.finishService(&list[i]) }
	return list, nil
}

// GET /admin/v1/services?q=&cursor=&limit=
func (a *API) listServices(w http.ResponseWriter, r *http.Request) {
	after, ok := cursor(r)
	if !ok { problem(w, r, http.StatusBadRequest, "Bad cursor", ""); return }
	n := limit(r)
	list, err := a.services(r.Context(), r.URL.Query().Get("q"), after, n+1)
	if err != nil { a.serverError(w, r, "list services", err); return }
	writeJSON(w, trim(list, n, func(s Service) string { return s.ID }))
}

// ServiceKey is one of a service's keys
type ServiceKey struct {
	KID           string     `json:"kid" db:"kid"`
	CreatedAt     time.Time  `json:"created_at" db:"created_at"`
	LastUsedAt    *time.Time `json:"last_signed_at" db:"last_used_at"`
	RevokedAt     *time.Time `json:"revoked_at" db:"revoked_at"`
	RevokedReason *string    `json:"revoked_reason" db:"revoked_reason"`
}

// serviceView is a service as GET /services/{id} shows it: the service, its keys, and
// what its role can reach
func (a *API) serviceView(ctx context.Context, id string) (map[string]any, error) {
	rows, err := a.d.DB.Query(ctx, globals.SQLGetService, id)
	if err != nil { return nil, err }
	s, err := pgx.CollectOneRow(rows, pgx.RowToStructByNameLax[Service])
	if errors.Is(err, pgx.ErrNoRows) { return nil, refuse(http.StatusNotFound, "No such service", "No service %q.", id) }
	if err != nil { return nil, err }
	a.finishService(&s)

	rows, err = a.d.DB.Query(ctx, globals.SQLServiceKeys, id)
	if err != nil { return nil, err }
	keys, err := pgx.CollectRows(rows, pgx.RowToStructByName[ServiceKey])
	if err != nil { return nil, err }
	if keys == nil { keys = []ServiceKey{} }
	return map[string]any{"service": s, "keys": keys, "reach": a.access(s.Role)}, nil
}

// GET /admin/v1/services/{id}
func (a *API) getService(w http.ResponseWriter, r *http.Request) {
	v, err := a.serviceView(r.Context(), r.PathValue("id"))
	if err != nil { a.fail(w, r, "get service", err); return }
	writeJSON(w, v)
}
