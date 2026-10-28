package auth

import (
	"context"
	"errors"
	"net/http"
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
		if err != nil {
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
				if err != nil || u.TokenUse != "user" {
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

		ctx := context.WithValue(r.Context(), principalKey{}, p)
		ctx = reqctx.SetCallerRoles(ctx, p.callerRoles())
		ctx = reqctx.SetUser(ctx, p.actor())
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

// authenticate accepts Oh CRUD access tokens
func (a *Auth) authenticate(raw string) (*models.Claims, error) {
	c, err := a.ks.Verify(raw)
	if err != nil { return nil, err }
	if c.Subject == "" { return nil, errors.New("token has no subject") }
	return c, nil
}

// serviceID is the services.id a service token was issued for
func serviceID(c *models.Claims) string { return strings.TrimPrefix(c.Subject, serviceUserPrefix) }

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
