package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"lotusforge.au/api-server/audit"
	"lotusforge.au/api-server/auth"
)

// Changes made through the admin API are written in one transaction with their audit
// entry, so a change is never recorded without happening or happen without a record.
// Revocations that follow a change (ending sessions, refusing tokens) run after it
// commits.

// httpError is a refusal that becomes a problem response
type httpError struct {
	status int
	title  string
	detail string
}

func (e *httpError) Error() string { return e.title + ": " + e.detail }

func refuse(status int, title string, detail string, args ...any) error {
	return &httpError{status: status, title: title, detail: fmt.Sprintf(detail, args...)}
}

// fail answers err: a refusal as its problem, a unique violation as a 409, anything else
// as a logged 500
func (a *API) fail(w http.ResponseWriter, r *http.Request, what string, err error) {
	var he *httpError
	var pe *pgconn.PgError
	switch {
	case errors.As(err, &he):
		problem(w, r, he.status, he.title, he.detail)
	case errors.As(err, &pe) && pe.Code == "23505":
		problem(w, r, http.StatusConflict, "Already exists", "Something with that name or id already exists.")
	default:
		a.serverError(w, r, what, err)
	}
}

// decode reads a JSON body into v, refusing unknown fields and bodies over 64 KB
func decode(r *http.Request, v any) error {
	d := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return refuse(http.StatusBadRequest, "Bad request body", "%v", err)
	}
	return nil
}

// changes wraps a route that changes something: the optional step-up check
// (admin.step-up-minutes), then h
func (a *API) changes(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mins := a.d.Config.Get().StepUpMinutes()
		if mins > 0 {
			u := auth.PrincipalFrom(r.Context()).User
			fresh := u.AuthTime != nil && time.Since(u.AuthTime.Time) <= time.Duration(mins)*time.Minute
			if !slices.Contains(u.AMR, "otp") || !fresh {
				// RFC 9470: the client signs the user in again with a second factor, then retries
				w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer error="insufficient_user_authentication", max_age=%d`, mins*60))
				problem(w, r, http.StatusUnauthorized, "A recent second factor is required",
					fmt.Sprintf("Sign in again with a one-time code; changes need one from the last %d minutes.", mins))
				return
			}
		}
		h(w, r)
	}
}

// event starts an audit entry for this request's actor
func (a *API) event(r *http.Request, action string, target string) audit.Event {
	p := auth.PrincipalFrom(r.Context())
	e := audit.Event{Action: action, Target: target, Service: audit.System, ActorUser: p.User.Subject,
		AMR: p.User.AMR, IP: audit.IP(r), RequestID: requestID(r)}
	if p.Service != nil { e.ViaService = auth.ServiceID(p.Service) }
	return e
}

// inTx runs fn in a transaction and commits it
func (a *API) inTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := a.d.DB.Begin(ctx)
	if err != nil { return err }
	defer tx.Rollback(ctx)
	if err := fn(tx); err != nil { return err }
	return tx.Commit(ctx)
}

// after runs follow-up work once a change has committed. A failure is logged: the
// change itself stands.
func (a *API) after(r *http.Request, what string, err error) {
	if err != nil {
		a.d.Logger.Error("admin API: "+what+" after a committed change", "error", err, "request_id", requestID(r))
	}
}
