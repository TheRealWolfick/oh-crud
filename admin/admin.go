// Package admin serves the admin API, /admin/v1: what the management platform shows
// and, later, what it changes. Every route needs a user holding the platform admin
// role (rbac.admin-role), calling directly or through a service listed in
// admin.services. A few per-service routes also accept that service's admin role,
// <service>:admin.
//
// Lists answer {"items": [...], "next_cursor": "..."}; pass the cursor back to get the
// next page. Errors are RFC 9457 problem details (application/problem+json).
package admin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"slices"
	"strconv"

	"lotusforge.au/api-server/audit"
	"lotusforge.au/api-server/auth"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/registry"
	"lotusforge.au/api-server/schematools"
)

// Deps are what the admin API reads from
type Deps struct {
	DB        models.DBTxBeginner
	Auth      *auth.Auth
	Config    *models.SwappableServerConfig
	Models    *registry.ModelRegistry
	Functions *registry.FunctionRegistry
	Gate      *schematools.PendingApprovalGate
	Logger    *slog.Logger
}

// API serves /admin/v1
type API struct{ d Deps }

func New(d Deps) *API { return &API{d: d} }

// Routes registers every admin route on mux
func (a *API) Routes(mux *http.ServeMux) {
	platform := func(h http.HandlerFunc) http.Handler { return a.d.Auth.Middleware(a.require(a.platformAdmin, h)) }
	ofService := func(h http.HandlerFunc) http.Handler { return a.d.Auth.Middleware(a.require(a.serviceAdmin("svc"), h)) }

	mux.Handle("GET /admin/v1/overview", platform(a.overview))
	mux.Handle("GET /admin/v1/services", platform(a.listServices))
	mux.Handle("GET /admin/v1/services/{id}", platform(a.getService))
	mux.Handle("GET /admin/v1/users", platform(a.listUsers))
	mux.Handle("GET /admin/v1/users/{id}", platform(a.getUser))
	mux.Handle("GET /admin/v1/audit", platform(a.listAudit))
	mux.Handle("GET /admin/v1/roles/{role}/access", platform(a.roleAccess))
	mux.Handle("GET /admin/v1/schema/pending", platform(a.pendingSchema))
	mux.Handle("GET /admin/v1/services/{svc}/schema/pending", ofService(a.pendingSchema))
	mux.Handle("GET /admin/v1/logs", platform(a.listLogs))

	// Changes: each commits with its audit entry; admin.step-up-minutes can require a
	// recent second factor for all of them
	change := func(ok check, h http.HandlerFunc) http.Handler { return a.d.Auth.Middleware(a.require(ok, a.changes(h))) }
	mux.Handle("POST /admin/v1/services", change(a.platformAdmin, a.createService))
	mux.Handle("PATCH /admin/v1/services/{id}", change(a.platformAdmin, a.updateService))
	mux.Handle("POST /admin/v1/services/{id}/keys", change(a.platformAdmin, a.addServiceKey))
	mux.Handle("POST /admin/v1/services/{id}/keys/{kid}/revoke", change(a.platformAdmin, a.revokeServiceKey))
	mux.Handle("POST /admin/v1/services/{id}/disable", change(a.platformAdmin, a.setServiceDisabled(true)))
	mux.Handle("POST /admin/v1/services/{id}/enable", change(a.platformAdmin, a.setServiceDisabled(false)))
	mux.Handle("POST /admin/v1/users", change(a.platformAdmin, a.createUser))
	mux.Handle("PUT /admin/v1/users/{id}/roles", change(a.anyAdmin, a.setUserRoles)) // each role checked by canGrant
	mux.Handle("POST /admin/v1/users/{id}/disable", change(a.platformAdmin, a.setUserDisabled(true)))
	mux.Handle("POST /admin/v1/users/{id}/enable", change(a.platformAdmin, a.setUserDisabled(false)))
	mux.Handle("POST /admin/v1/users/{id}/sessions/revoke", change(a.platformAdmin, a.revokeUserSessions))
	mux.Handle("POST /admin/v1/users/{id}/sessions/{session}/revoke", change(a.platformAdmin, a.revokeUserSession))
}

// check decides whether the user may use a route
type check func(r *http.Request, user *models.Claims) bool

func (a *API) platformAdmin(_ *http.Request, u *models.Claims) bool {
	role := a.d.Config.Get().AdminRole()
	return role != "" && slices.Contains(u.Roles, role)
}

// serviceAdmin also lets in the admin of the service named in the path
func (a *API) serviceAdmin(param string) check {
	return func(r *http.Request, u *models.Claims) bool {
		return a.platformAdmin(r, u) || slices.Contains(u.Roles, r.PathValue(param)+":admin")
	}
}

// require runs after auth.Middleware: a user must be present, any service relaying the
// call must be listed in admin.services, and the user must pass ok
func (a *API) require(ok check, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// One id for the request: the response header, its audit entries and its logs
		id := audit.RequestID(r)
		w.Header().Set("X-Request-Id", id)
		r = r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id))
		p := auth.PrincipalFrom(r.Context())
		switch {
		case p == nil:
			problem(w, r, http.StatusUnauthorized, "Not signed in", "")
			return
		case p.User == nil:
			problem(w, r, http.StatusForbidden, "A user is required", "The admin API acts for a person, not a service alone.")
			return
		case p.Service != nil && !slices.Contains(a.d.Config.Get().AdminServices(), auth.ServiceID(p.Service)):
			problem(w, r, http.StatusForbidden, "This service may not call the admin API", "Add it to admin.services in server.yaml.")
			return
		case !ok(r, p.User):
			problem(w, r, http.StatusForbidden, "Not an administrator", "")
			return
		}
		next(w, r)
	})
}

type requestIDKey struct{}

// requestID is the id require chose for the request
func requestID(r *http.Request) string {
	id, _ := r.Context().Value(requestIDKey{}).(string)
	return id
}

// problem writes an RFC 9457 problem detail
func problem(w http.ResponseWriter, r *http.Request, status int, title string, detail string) {
	body := map[string]any{"type": "about:blank", "title": title, "status": status, "instance": r.URL.Path,
		"request_id": w.Header().Get("X-Request-Id")}
	if detail != "" { body["detail"] = detail }
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

// serverError logs err with the request id and answers 500 without detail
func (a *API) serverError(w http.ResponseWriter, r *http.Request, what string, err error) {
	a.d.Logger.Error("admin API: "+what, "error", err, "path", r.URL.Path, "request_id", w.Header().Get("X-Request-Id"))
	problem(w, r, http.StatusInternalServerError, "Something went wrong", "")
}

func writeJSON(w http.ResponseWriter, v any) { writeJSONStatus(w, http.StatusOK, v) }

func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// page is a list response. next_cursor is empty on the last page.
type page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor"`
}

// trim cuts rows fetched with limit+1 back to limit, and makes the cursor from the
// last row kept when there are more
func trim[T any](rows []T, limit int, key func(T) string) page[T] {
	p := page[T]{Items: rows}
	if p.Items == nil { p.Items = []T{} }
	if len(rows) > limit {
		p.Items = rows[:limit]
		p.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(key(rows[limit-1])))
	}
	return p
}

// cursor reads the cursor parameter back to the key it was made from
func cursor(r *http.Request) (string, bool) {
	raw := r.URL.Query().Get("cursor")
	if raw == "" { return "", true }
	b, err := base64.RawURLEncoding.DecodeString(raw)
	return string(b), err == nil
}

// limit reads ?limit=, default 50, at most 200
func limit(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || n <= 0 { return 50 }
	return min(n, 200)
}
