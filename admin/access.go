package admin

import (
	"net/http"
	"slices"
	"sort"
)

// Access is what one role can reach, read from every loaded model's end-points-allowed
// and admin-roles, and every function's roles-allowed. It answers the dashboard's
// "what this role can reach", and lets an admin see what they are granting.
type Access struct {
	Role          string           `json:"role"`
	PlatformAdmin bool             `json:"platform_admin"` // the platform admin role passes every check
	Models        []ModelAccess    `json:"models"`
	Functions     []FunctionAccess `json:"functions"`
}

type ModelAccess struct {
	Service string   `json:"service"`
	Path    string   `json:"path"`
	Model   string   `json:"model"`
	Methods []string `json:"methods"` // GET, POST, PUT, DELETE, POST-GROUP, PUT-GROUP, DELETE-GROUP, DIFF
	Admin   bool     `json:"admin"`   // in the model's admin-roles: schema approvals for it
}

type FunctionAccess struct {
	Service string `json:"service"`
	Path    string `json:"path"`
	Name    string `json:"name"`
}

func (a *API) access(role string) Access {
	out := Access{Role: role, Models: []ModelAccess{}, Functions: []FunctionAccess{}}
	out.PlatformAdmin = role != "" && role == a.d.Config.Get().AdminRole()

	getAllowed := map[string]bool{} // model path -> role may GET it, for functions that inherit it
	for _, m := range a.d.Models.All() {
		if m.Path() == "" || m.Name == nil { continue }
		ma := ModelAccess{Service: *m.Service, Path: "/" + m.Path(), Model: *m.Name, Methods: []string{}}
		if e := m.End_points_allowed; e != nil {
			for _, x := range []struct {
				method string
				roles  []string
			}{{"GET", e.GET}, {"POST", e.POST}, {"PUT", e.PUT}, {"DELETE", e.DELETE},
				{"POST-GROUP", e.POST_GROUP}, {"PUT-GROUP", e.PUT_GROUP}, {"DELETE-GROUP", e.DELETE_GROUP}, {"DIFF", e.DIFF}} {
				if slices.Contains(x.roles, role) { ma.Methods = append(ma.Methods, x.method) }
			}
			getAllowed[m.Path()] = slices.Contains(e.GET, role)
		}
		ma.Admin = slices.Contains(m.Admin_roles, role)
		if len(ma.Methods) > 0 || ma.Admin { out.Models = append(out.Models, ma) }
	}
	for _, f := range a.d.Functions.All() {
		if f.BoundPath() == "" || f.Name == nil { continue }
		allowed := slices.Contains(f.Roles_allowed, role) || (len(f.Roles_allowed) == 0 && getAllowed[f.BoundPath()])
		if allowed {
			out.Functions = append(out.Functions, FunctionAccess{Service: *f.Service, Path: "/" + f.BoundPath() + "/fn/" + *f.Name, Name: *f.Name})
		}
	}
	sort.Slice(out.Models, func(i, j int) bool { return out.Models[i].Path < out.Models[j].Path })
	sort.Slice(out.Functions, func(i, j int) bool { return out.Functions[i].Path < out.Functions[j].Path })
	return out
}

// GET /admin/v1/roles/{role}/access
func (a *API) roleAccess(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, a.access(r.PathValue("role")))
}
