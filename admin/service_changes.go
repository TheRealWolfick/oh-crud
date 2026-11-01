package admin

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"lotusforge.au/api-server/audit"
	"lotusforge.au/api-server/datamodel"
)

// kidPattern is a key id: it is sent in every assertion header and stored as varchar(32)
var kidPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`)

// KeyInput is a public key being registered. The service keeps the private half.
type KeyInput struct {
	KID       string `json:"kid"`
	PublicKey string `json:"public_key"` // the 32 byte Ed25519 public key, base64 (standard or URL, padded or not)
}

func (k KeyInput) decode() ([]byte, error) {
	if !kidPattern.MatchString(k.KID) {
		return nil, refuse(http.StatusBadRequest, "Bad key id", "kid must be 1 to 32 letters, digits, '.', '_' or '-'.")
	}
	raw := strings.TrimSpace(k.PublicKey)
	for _, enc := range []*base64.Encoding{base64.RawURLEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.StdEncoding} {
		if b, err := enc.DecodeString(raw); err == nil && len(b) == ed25519.PublicKeySize { return b, nil }
	}
	return nil, refuse(http.StatusBadRequest, "Bad public key", "public_key must be a base64 Ed25519 public key (32 bytes).")
}

func (a *API) insertKey(ctx context.Context, tx pgx.Tx, service string, k KeyInput) error {
	pub, err := k.decode()
	if err != nil { return err }
	_, err = tx.Exec(ctx, `INSERT INTO ohc_service_keys (kid, service_id, public_key) VALUES ($1, $2, $3)`, k.KID, service, pub)
	return err
}

// serviceExists refuses with 404 unless the service exists, and returns its row
func (a *API) serviceRow(ctx context.Context, tx pgx.Tx, id string) (role string, disabled bool, err error) {
	err = tx.QueryRow(ctx, `SELECT role, disabled_at IS NOT NULL FROM ohc_services WHERE id = $1 FOR UPDATE`, id).Scan(&role, &disabled)
	if errors.Is(err, pgx.ErrNoRows) { err = refuse(http.StatusNotFound, "No such service", "No service %q.", id) }
	return role, disabled, err
}

// POST /admin/v1/services {"id", "role", "entra_client_id"?, "key"?: {"kid", "public_key"}}
func (a *API) createService(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID            string    `json:"id"`
		Role          string    `json:"role"`
		EntraClientID *string   `json:"entra_client_id"`
		Key           *KeyInput `json:"key"`
	}
	if err := decode(r, &in); err != nil { a.fail(w, r, "", err); return }
	if err := datamodel.ValidateService(in.ID); err != nil || in.ID == datamodel.SystemService {
		a.fail(w, r, "", refuse(http.StatusBadRequest, "Bad service id", "2 to 32 lower case letters or digits, starting with a letter, and not a reserved name."))
		return
	}
	if !rolePattern.MatchString(in.Role) { a.fail(w, r, "", refuse(http.StatusBadRequest, "Bad role", "role is required.")); return }

	err := a.inTx(r.Context(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `INSERT INTO ohc_services (id, role, entra_client_id) VALUES ($1, $2, $3)`, in.ID, in.Role, in.EntraClientID); err != nil { return err }
		after := map[string]any{"role": in.Role}
		if in.Key != nil {
			if err := a.insertKey(r.Context(), tx, in.ID, *in.Key); err != nil { return err }
			after["kid"] = in.Key.KID
		}
		e := a.event(r, audit.ServiceCreate, "svc:"+in.ID)
		e.After = after
		return audit.Record(r.Context(), tx, e)
	})
	if err != nil { a.fail(w, r, "create service", err); return }
	a.writeService(w, r, http.StatusCreated, in.ID)
}

// writeService answers with the service as GET /services/{id} shows it
func (a *API) writeService(w http.ResponseWriter, r *http.Request, status int, id string) {
	v, err := a.serviceView(r.Context(), id)
	if err != nil { a.fail(w, r, "read service back", err); return }
	writeJSONStatus(w, status, v)
}

// PATCH /admin/v1/services/{id} {"role"}
func (a *API) updateService(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct{ Role string `json:"role"` }
	if err := decode(r, &in); err != nil { a.fail(w, r, "", err); return }
	if !rolePattern.MatchString(in.Role) { a.fail(w, r, "", refuse(http.StatusBadRequest, "Bad role", "role is required.")); return }

	err := a.inTx(r.Context(), func(tx pgx.Tx) error {
		old, _, err := a.serviceRow(r.Context(), tx, id)
		if err != nil { return err }
		if _, err := tx.Exec(r.Context(), `UPDATE ohc_services SET role = $2 WHERE id = $1`, id, in.Role); err != nil { return err }
		e := a.event(r, audit.ServiceUpdate, "svc:"+id)
		e.Before, e.After = map[string]string{"role": old}, map[string]string{"role": in.Role}
		return audit.Record(r.Context(), tx, e)
	})
	if err != nil { a.fail(w, r, "update service", err); return }
	// The role is in the service's tokens: refuse the current ones so it applies now
	a.after(r, "revoke service tokens", a.d.Auth.RevokeService(r.Context(), id, "role changed"))
	a.writeService(w, r, http.StatusOK, id)
}

// POST /admin/v1/services/{id}/keys {"kid", "public_key"}
func (a *API) addServiceKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in KeyInput
	if err := decode(r, &in); err != nil { a.fail(w, r, "", err); return }
	err := a.inTx(r.Context(), func(tx pgx.Tx) error {
		if _, _, err := a.serviceRow(r.Context(), tx, id); err != nil { return err }
		if err := a.insertKey(r.Context(), tx, id, in); err != nil { return err }
		e := a.event(r, audit.ServiceKeyAdd, "svc:"+id+"/"+in.KID)
		e.After = map[string]string{"kid": in.KID}
		return audit.Record(r.Context(), tx, e)
	})
	if err != nil { a.fail(w, r, "add service key", err); return }
	a.writeService(w, r, http.StatusCreated, id)
}

// POST /admin/v1/services/{id}/keys/{kid}/revoke {"reason"?}
// The key can't sign again, and every token the service holds now is refused: a leaked
// key may have been used to get them. The service asserts again with another key.
func (a *API) revokeServiceKey(w http.ResponseWriter, r *http.Request) {
	id, kid := r.PathValue("id"), r.PathValue("kid")
	var in struct{ Reason string `json:"reason"` }
	if r.ContentLength != 0 {
		if err := decode(r, &in); err != nil { a.fail(w, r, "", err); return }
	}
	err := a.inTx(r.Context(), func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `UPDATE ohc_service_keys SET revoked_at = now(), revoked_reason = $3
			WHERE service_id = $1 AND kid = $2 AND revoked_at IS NULL`, id, kid, nullable(in.Reason))
		if err != nil { return err }
		if tag.RowsAffected() == 0 { return refuse(http.StatusNotFound, "No such key", "Service %q has no active key %q.", id, kid) }
		e := a.event(r, audit.ServiceKeyRevoke, "svc:"+id+"/"+kid)
		e.Before, e.After = map[string]string{"status": "active"}, map[string]string{"status": "revoked", "reason": in.Reason}
		return audit.Record(r.Context(), tx, e)
	})
	if err != nil { a.fail(w, r, "revoke service key", err); return }
	a.after(r, "revoke service tokens", a.d.Auth.RevokeService(r.Context(), id, "key "+kid+" revoked"))
	a.writeService(w, r, http.StatusOK, id)
}

// POST /admin/v1/services/{id}/disable and /enable. Disabling refuses the service's
// tokens and stops it getting new ones; its users' sessions through it go with them.
func (a *API) setServiceDisabled(disable bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		action := audit.ServiceEnable
		if disable { action = audit.ServiceDisable }
		err := a.inTx(r.Context(), func(tx pgx.Tx) error {
			_, was, err := a.serviceRow(r.Context(), tx, id)
			if err != nil { return err }
			if was == disable { return nil } // already so: nothing to record
			sql := `UPDATE ohc_services SET disabled_at = NULL WHERE id = $1`
			if disable { sql = `UPDATE ohc_services SET disabled_at = now() WHERE id = $1` }
			if _, err := tx.Exec(r.Context(), sql, id); err != nil { return err }
			e := a.event(r, action, "svc:"+id)
			e.Before, e.After = map[string]bool{"disabled": was}, map[string]bool{"disabled": disable}
			return audit.Record(r.Context(), tx, e)
		})
		if err != nil { a.fail(w, r, action, err); return }
		if disable { a.after(r, "revoke service tokens", a.d.Auth.RevokeService(r.Context(), id, "service disabled")) }
		a.writeService(w, r, http.StatusOK, id)
	}
}

func nullable(s string) any {
	if s == "" { return nil }
	return s
}
