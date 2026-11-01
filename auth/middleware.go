package auth

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/reqctx"
)

// UserTokenHeader carries the user's access token when a service calls on their behalf
const UserTokenHeader = "X-OhCrud-User"

// Principal is who is making a request: the service calling Oh CRUD, and the user it acts for
type Principal struct {
	Service *models.Claims // nil only for direct user calls
	User    *models.Claims // nil for background tasks
}

type principalKey struct{}

// ContextWithPrincipal records p as the request's principal, as Middleware does
func ContextWithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

func PrincipalFrom(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalKey{}).(*Principal)
	return p
}

// Middleware authenticates the bearer token, and the user token if a service sends one,
// then records the principal for the handlers. Role checks happen per operation in the
// handlers, and ForModel adds each model's access rule.
func (a *Auth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			unauthorized(w)
			return
		}
		first, err := a.authenticate(raw)
		if err != nil || a.revoked(first) {
			unauthorized(w)
			return
		}

		p := &Principal{}
		ut := r.Header.Get(UserTokenHeader)
		switch first.TokenUse {
		case "service":
			p.Service = first
			if ut != "" {
				u, err := a.authenticate(ut)
				if err != nil || u.TokenUse != "user" || a.revoked(u) {
					unauthorized(w)
					return
				}
				// The user token must have been issued to this service, so one service
				// cannot replay a user token it received from another
				if u.ClientID != serviceID(first) {
					unauthorized(w)
					return
				}
				p.User = u
			}
		case "user":
			// Direct call: no service involved. A user token cannot vouch for another.
			if ut != "" {
				unauthorized(w)
				return
			}
			p.User = first
		default:
			// Verify only accepts the two above, but never rely on that here
			unauthorized(w)
			return
		}

		ctx := ContextWithPrincipal(r.Context(), p)
		ctx = reqctx.SetActor(ctx, p.actor(), p.callerRoles())
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ForModel returns the middleware for a model's routes: authentication, then the
// model's access rule. A nil model, such as a function bound to a model that has
// not loaded, gets the default rule.
func (a *Auth) ForModel(cfg *models.DataModel) func(http.Handler) http.Handler {
	var rule models.Access
	if cfg != nil { rule = cfg.Access }

	return func(next http.Handler) http.Handler {
		return a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := PrincipalFrom(r.Context())
			switch {
			case p == nil:
				unauthorized(w)
			case p.Service == nil && !rule.AllowDirect:
				forbidden(w)
			case rule.RequireUser && p.User == nil:
				forbidden(w)
			default:
				next.ServeHTTP(w, r)
			}
		}))
	}
}

// This function checks a user object against a datamodel to check if they are authorized.
// A nil user or model, such as a function bound to a model that has not loaded, is denied.
func (a *Auth) CheckUserAgainstConfig(usr *models.User, cfg *models.DataModel, end_point string) bool {
	if usr == nil || cfg == nil { return false }

	// Check the admin roles across every role first, so role order does not matter
	for _, role := range usr.Roles {
		if slices.Contains(cfg.Admin_roles, role) { return true }
	}

	// Check against the end point
	if cfg.End_points_allowed == nil { return false }
	var allowed []string
	switch strings.ToLower(end_point) {
	case "get":    allowed = cfg.End_points_allowed.GET
	case "create": allowed = cfg.End_points_allowed.POST
	case "update": allowed = cfg.End_points_allowed.PUT
	case "delete": allowed = cfg.End_points_allowed.DELETE
	case "diff":   allowed = cfg.End_points_allowed.DIFF
	}
	for _, role := range usr.Roles {
		if slices.Contains(allowed, role) { return true }
	}
	return false
}

// authenticate accepts Oh CRUD access tokens
func (a *Auth) authenticate(raw string) (*models.Claims, error) {
	c, err := a.ks.Verify(raw)
	if err != nil { return nil, err }
	if c.Subject == "" { return nil, errors.New("token has no subject") }
	return c, nil
}

// revoked reports whether the token was issued before a revocation of its user, its
// session or its service
func (a *Auth) revoked(c *models.Claims) bool {
	if a.Blocks == nil { return false }
	// Every token Oh CRUD issues has iat; one without can't be checked, so it is refused
	if c.IssuedAt == nil { return true }
	return a.Blocks.Blocked(c.IssuedAt.Time, TokenKeys(c)...)
}

// serviceID is the services.id a service token was issued for
func serviceID(c *models.Claims) string { return strings.TrimPrefix(c.Subject, serviceUserPrefix) }

// ServiceID is the services.id a service token was issued for
func ServiceID(c *models.Claims) string { return serviceID(c) }

func (p *Principal) callerRoles() reqctx.CallerRoles {
	var c reqctx.CallerRoles
	if p.Service != nil { c.Service, c.HasService = p.Service.Roles, true }
	if p.User != nil { c.User, c.HasUser = p.User.Roles, true }
	return c
}

// actor is who writes are recorded against: the user when there is one, otherwise the
// service's own user row (see ensureServiceUser)
func (p *Principal) actor() *models.User {
	if p.User != nil {
		return &models.User{ID: p.User.Subject, Username: p.User.Username, Roles: p.User.Roles}
	}
	return &models.User{ID: p.Service.Subject, Username: p.Service.Subject, Roles: p.Service.Roles}
}
