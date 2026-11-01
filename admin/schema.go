package admin

import (
	"net/http"
	"sort"
)

// PendingSchema is a destructive schema change waiting for approval
type PendingSchema struct {
	Table    string `json:"table"`
	Service  string `json:"service"`
	Model    string `json:"model"`
	Version  string `json:"version"`
	Changes  string `json:"changes"`
	Approved bool   `json:"approved"`
}

func (a *API) pending(service string) []PendingSchema {
	out := []PendingSchema{}
	for _, p := range a.d.Gate.PendingAll() {
		ps := PendingSchema{Table: p.Table, Changes: p.Changes, Approved: p.Approved}
		if m := p.Model; m != nil {
			if m.Service != nil { ps.Service = *m.Service }
			if m.Name != nil { ps.Model = *m.Name }
			if m.Version != nil { ps.Version = *m.Version }
		}
		if service != "" && ps.Service != service { continue }
		out = append(out, ps)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Table < out[j].Table })
	return out
}

// GET /admin/v1/schema/pending, and GET /admin/v1/services/{svc}/schema/pending for
// one service's tables (open to that service's admins too)
func (a *API) pendingSchema(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"items": a.pending(r.PathValue("svc"))})
}
