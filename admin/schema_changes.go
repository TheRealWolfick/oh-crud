package admin

import (
	"encoding/json"
	"net/http"
	"slices"

	"lotusforge.au/api-server/audit"
	"lotusforge.au/api-server/globals"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/schematools"
)

// Destructive schema changes wait in the gate until an admin approves or denies them.
// Approving applies every approved change together, so interdependent changes go in as
// one. The instance that decides tells its peers on the cluster bus, since each holds
// its own pending changes and config files.

type schemaMessage struct {
	Tables []string `json:"tables"`
}

// tableAdmin lets in platform admins, the admin of the service whose table it is, and
// any role in the model's own admin-roles
func (a *API) tableAdmin(r *http.Request, u *models.Claims) bool {
	if a.platformAdmin(r, u) { return true }
	p, ok := a.d.Gate.Pending(r.PathValue("table"))
	if !ok || p.Model == nil || p.Model.Service == nil { return false }
	if slices.Contains(u.Roles, *p.Model.Service+":admin") { return true }
	return slices.ContainsFunc(u.Roles, func(role string) bool { return slices.Contains(p.Model.Admin_roles, role) })
}

// SchemaDecision is the answer to an approval or denial
type SchemaDecision struct {
	Tables   []PendingSchema `json:"tables"`
	Approved bool            `json:"approved"`
	Applied  bool            `json:"applied"`
	Message  string          `json:"message"`
}

// POST /admin/v1/schema/{table}/approve
func (a *API) approveSchema(w http.ResponseWriter, r *http.Request) {
	table := r.PathValue("table")
	var pick []PendingSchema
	for _, p := range a.pending("") {
		if p.Table == table { pick = append(pick, p) }
	}
	if len(pick) == 0 { problem(w, r, http.StatusNotFound, "Nothing pending", "No schema change is waiting for "+table+"."); return }
	a.approve(w, r, pick)
}

// POST /admin/v1/services/{svc}/schema/approve: every change pending for the service
func (a *API) approveServiceSchema(w http.ResponseWriter, r *http.Request) {
	pick := a.pending(r.PathValue("svc"))
	if len(pick) == 0 { problem(w, r, http.StatusNotFound, "Nothing pending", "No schema change is waiting for this service."); return }
	a.approve(w, r, pick)
}

// approve marks the changes approved and applies everything approved. If that fails (an
// approved change needing one still pending, say) they stay approved and queued: 202.
func (a *API) approve(w http.ResponseWriter, r *http.Request, pick []PendingSchema) {
	a.schemaMu.Lock()
	defer a.schemaMu.Unlock()

	for _, p := range pick { a.d.Gate.SetApproved(p.Table) }
	err := schematools.ApplyApproved(r.Context(), a.d.Pool, a.d.Models.All(), a.d.Logger, a.d.Gate, a.d.OnSchemaApplied)

	out := SchemaDecision{Tables: pick, Approved: true, Applied: err == nil, Message: "approved and applied"}
	if err != nil { out.Message = err.Error() }

	// The schema change is made by Atlas in its own transaction, so the audit entry is
	// written straight after, saying whether it applied
	var tables []string
	for _, p := range pick {
		tables = append(tables, p.Table)
		e := a.event(r, globals.ActionSchemaApprove, p.Table)
		e.Service = p.Service
		e.Before = map[string]string{"version_pending": p.Version, "changes": p.Changes}
		e.After = map[string]any{"applied": out.Applied, "message": out.Message}
		a.after(r, "record the approval", audit.Record(r.Context(), a.d.DB, e))
	}

	if !out.Applied {
		a.d.Logger.Warn("Approved schema change not yet applied", "tables", tables, "error", err)
		writeJSONStatus(w, http.StatusAccepted, out)
		return
	}
	if a.d.Bus != nil { a.d.Bus.Publish(globals.SchemaAppliedChannel, schemaMessage{Tables: tables}) }
	writeJSON(w, out)
}

// POST /admin/v1/schema/{table}/deny {"reason"?}: drops the change and puts the model's
// file back to its last-good version
func (a *API) denySchema(w http.ResponseWriter, r *http.Request) {
	table := r.PathValue("table")
	var in struct{ Reason string `json:"reason"` }
	if r.ContentLength != 0 {
		if err := decode(r, &in); err != nil { a.fail(w, r, "", err); return }
	}
	a.schemaMu.Lock()
	defer a.schemaMu.Unlock()

	var p *PendingSchema
	for _, x := range a.pending("") {
		if x.Table == table { p = &x; break }
	}
	if p == nil { problem(w, r, http.StatusNotFound, "Nothing pending", "No schema change is waiting for "+table+"."); return }
	if err := schematools.RevertChange(a.d.Gate, table, a.d.Logger); err != nil { a.serverError(w, r, "revert schema change", err); return }

	e := a.event(r, globals.ActionSchemaDeny, table)
	e.Service = p.Service
	e.Before = map[string]string{"version_pending": p.Version, "changes": p.Changes}
	e.After = map[string]string{"reason": in.Reason}
	a.after(r, "record the denial", audit.Record(r.Context(), a.d.DB, e))

	if a.d.Bus != nil { a.d.Bus.Publish(globals.SchemaDeniedChannel, schemaMessage{Tables: []string{table}}) }
	writeJSON(w, SchemaDecision{Tables: []PendingSchema{*p}, Message: "denied; the model file is back to its last-good version"})
}

// listenToPeers handles schema decisions made on other instances: the change is already
// in the database (or dropped), so this instance only brings its own state in line
func (a *API) listenToPeers() {
	a.d.Bus.Handle(globals.SchemaAppliedChannel, func(data json.RawMessage) error {
		var m schemaMessage
		if err := json.Unmarshal(data, &m); err != nil { return err }
		a.schemaMu.Lock()
		defer a.schemaMu.Unlock()
		for _, t := range m.Tables {
			if model, ok := schematools.MarkApplied(a.d.Gate, t, a.d.Logger); ok && a.d.OnSchemaApplied != nil {
				a.d.OnSchemaApplied(model)
			}
		}
		return nil
	})
	a.d.Bus.Handle(globals.SchemaDeniedChannel, func(data json.RawMessage) error {
		var m schemaMessage
		if err := json.Unmarshal(data, &m); err != nil { return err }
		a.schemaMu.Lock()
		defer a.schemaMu.Unlock()
		for _, t := range m.Tables {
			if _, ok := a.d.Gate.Pending(t); ok {
				if err := schematools.RevertChange(a.d.Gate, t, a.d.Logger); err != nil { return err }
			}
		}
		return nil
	})
}
