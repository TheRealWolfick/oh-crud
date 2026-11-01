package admin

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"lotusforge.au/api-server/auth"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/models/modelstest"
	"lotusforge.au/api-server/registry"
	"lotusforge.au/api-server/schematools"
)

func p[T any](v T) *T { return &v }

func testAPI(db *modelstest.FakeDB) *API {
	cfg := &models.ServerConfig{RBAC: &models.RBACConfig{Admin_role: "administrator"}, Admin: &models.AdminConfig{Services: []string{"management"}}}
	return New(Deps{DB: db, Config: models.NewSwappableServerConfig(cfg), Models: registry.NewModelRegistry(),
		Functions: registry.NewFunctionRegistry(), Gate: schematools.NewPendingApprovalGate(), Logger: slog.New(slog.DiscardHandler)})
}

func user(roles ...string) *models.Claims {
	return &models.Claims{TokenUse: "user", Roles: roles, RegisteredClaims: jwt.RegisteredClaims{Subject: "usr_1"}}
}

func svc(id string) *models.Claims {
	return &models.Claims{TokenUse: "service", RegisteredClaims: jwt.RegisteredClaims{Subject: "svc:" + id}}
}

// serve runs h behind require, as if the middleware had found principal
func serve(a *API, ok check, principal *auth.Principal, target string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	if principal != nil { r = r.WithContext(auth.ContextWithPrincipal(r.Context(), principal)) }
	w := httptest.NewRecorder()
	mux := http.NewServeMux()
	mux.Handle("GET /admin/v1/services/{svc}/schema/pending", a.require(ok, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	mux.Handle("GET /x", a.require(ok, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	mux.ServeHTTP(w, r)
	return w
}

func TestRequire(t *testing.T) {
	a := testAPI(&modelstest.FakeDB{})
	cases := []struct {
		name   string
		ok     check
		p      *auth.Principal
		target string
		status int
		title  string
	}{
		{"no principal", a.platformAdmin, nil, "/x", 401, "Not signed in"},
		{"service alone", a.platformAdmin, &auth.Principal{Service: svc("management")}, "/x", 403, "A user is required"},
		{"admin, direct", a.platformAdmin, &auth.Principal{User: user("administrator")}, "/x", 200, ""},
		{"admin through the management service", a.platformAdmin, &auth.Principal{Service: svc("management"), User: user("administrator")}, "/x", 200, ""},
		{"admin through another service", a.platformAdmin, &auth.Principal{Service: svc("web"), User: user("administrator")}, "/x", 403, "This service may not call the admin API"},
		{"not an admin", a.platformAdmin, &auth.Principal{User: user("user")}, "/x", 403, "Not an administrator"},
		{"service admin, own service", a.serviceAdmin("svc"), &auth.Principal{User: user("inventory:admin")}, "/admin/v1/services/inventory/schema/pending", 200, ""},
		{"service admin, other service", a.serviceAdmin("svc"), &auth.Principal{User: user("inventory:admin")}, "/admin/v1/services/permits/schema/pending", 403, "Not an administrator"},
		{"service admin on a platform route", a.platformAdmin, &auth.Principal{User: user("inventory:admin")}, "/x", 403, "Not an administrator"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := serve(a, tc.ok, tc.p, tc.target)
			if w.Code != tc.status {
				t.Fatalf("got %d, want %d: %s", w.Code, tc.status, w.Body.String())
			}
			if w.Header().Get("X-Request-Id") == "" {
				t.Errorf("expected a request id on every answer")
			}
			if tc.status == 200 { return }
			var body map[string]any
			json.Unmarshal(w.Body.Bytes(), &body)
			if w.Header().Get("Content-Type") != "application/problem+json" || body["title"] != tc.title || body["request_id"] == "" {
				t.Errorf("expected a problem titled %q, got %s %v", tc.title, w.Header().Get("Content-Type"), body)
			}
		})
	}
}

func TestAccess(t *testing.T) {
	a := testAPI(&modelstest.FakeDB{})
	a.d.Models.Register(&models.DataModel{Name: p("Asset"), Service: p("inventory"), Table_name: p("inventory_asset"), End_point: p("asset"),
		End_points_allowed: &models.End_pointsAllowed{GET: []string{"reader", "editor"}, POST: []string{"editor"}, DIFF: []string{"editor"}},
		Admin_roles: []string{"inventory:admin"}})
	a.d.Models.Register(&models.DataModel{Name: p("Permit"), Service: p("permits"), Table_name: p("permits_permit"), End_point: p("permit"),
		End_points_allowed: &models.End_pointsAllowed{GET: []string{"editor"}}})
	a.d.Models.Register(&models.DataModel{Name: p("Internal"), Service: p("ohc"), Table_name: p("ohc_internal"), End_point: p("")})
	a.d.Functions.Register(&models.FunctionDef{Name: p("by-building"), Service: p("inventory"), Bound_to: p("asset")})                                  // inherits GET
	a.d.Functions.Register(&models.FunctionDef{Name: p("report"), Service: p("inventory"), Bound_to: p("asset"), Roles_allowed: []string{"auditor"}}) // its own roles

	got := a.access("editor")
	if len(got.Models) != 2 || got.Models[0].Path != "/inventory/asset" || !reflect.DeepEqual(got.Models[0].Methods, []string{"GET", "POST", "DIFF"}) {
		t.Errorf("editor models: %+v", got.Models)
	}
	if len(got.Functions) != 1 || got.Functions[0].Path != "/inventory/asset/fn/by-building" {
		t.Errorf("editor functions: %+v", got.Functions)
	}

	got = a.access("auditor")
	if len(got.Models) != 0 || len(got.Functions) != 1 || got.Functions[0].Name != "report" {
		t.Errorf("auditor: %+v", got)
	}

	got = a.access("inventory:admin")
	if len(got.Models) != 1 || !got.Models[0].Admin || len(got.Models[0].Methods) != 0 {
		t.Errorf("service admin: %+v", got.Models)
	}
	if !a.access("administrator").PlatformAdmin || a.access("editor").PlatformAdmin {
		t.Errorf("only the configured admin role is the platform admin")
	}
}

func TestAuditQuery(t *testing.T) {
	db := &modelstest.FakeDB{Rows: modelstest.FakeRows{Cols: []string{"id"}}}
	a := testAPI(db)
	from := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	a.audit(t.Context(), auditFilter{action: "service_key.*", subject: "usr_1", service: "ohc", from: from}, "120", 51)

	c := db.LastCall()
	for _, want := range []string{`service = $1`, `(target = $2 OR actor_user = $2)`, `action LIKE $3 ESCAPE '\'`, `at >= $4`, `id < $5`, `LIMIT $6`} {
		if !strings.Contains(c.SQL, want) {
			t.Errorf("expected %q in\n%s", want, c.SQL)
		}
	}
	// '_' is a LIKE wildcard, so it is escaped: service_key.* must not match servicexkey.x
	want := []any{"ohc", "usr_1", `service\_key.%`, from, int64(120), 51}
	if !reflect.DeepEqual(c.Args, want) {
		t.Errorf("args %#v, want %#v", c.Args, want)
	}

	if _, err := a.audit(t.Context(), auditFilter{}, "not-a-number", 10); err == nil {
		t.Errorf("expected a bad cursor refused")
	}
}

func TestParseWhen(t *testing.T) {
	if d, _ := parseWhen("2026-10-28", true); !d.Equal(time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("a date as an upper bound includes the whole day, got %v", d)
	}
	if d, _ := parseWhen("2026-10-28", false); !d.Equal(time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("got %v", d)
	}
	if d, _ := parseWhen("2026-10-28T10:00:00+11:00", true); !d.Equal(time.Date(2026, 10, 27, 23, 0, 0, 0, time.UTC)) {
		t.Errorf("got %v", d)
	}
	if _, err := parseWhen("yesterday", false); err == nil {
		t.Errorf("expected an error")
	}
}

func TestTrimAndCursor(t *testing.T) {
	rows := []string{"a", "b", "c"}
	pg := trim(rows, 2, func(s string) string { return s })
	if len(pg.Items) != 2 || pg.NextCursor == "" {
		t.Fatalf("expected two items and a cursor, got %+v", pg)
	}
	r := httptest.NewRequest("GET", "/?cursor="+pg.NextCursor, nil)
	if got, ok := cursor(r); !ok || got != "b" {
		t.Errorf("expected the cursor to read back as b, got %q", got)
	}
	if pg := trim(rows, 3, func(s string) string { return s }); pg.NextCursor != "" {
		t.Errorf("no cursor on the last page")
	}
	if pg := trim([]string(nil), 3, func(s string) string { return s }); pg.Items == nil {
		t.Errorf("an empty page has items: [], not null")
	}
	if _, ok := cursor(httptest.NewRequest("GET", "/?cursor=not!base64", nil)); ok {
		t.Errorf("expected a bad cursor refused")
	}
}

func TestLimit(t *testing.T) {
	for q, want := range map[string]int{"": 50, "10": 10, "0": 50, "-1": 50, "x": 50, "1000": 200} {
		if got := limit(httptest.NewRequest("GET", "/?limit="+q, nil)); got != want {
			t.Errorf("limit=%q: got %d, want %d", q, got, want)
		}
	}
}

func TestHasAdminRole(t *testing.T) {
	for roles, want := range map[string]bool{"administrator": true, "inventory:admin": true, "user,report": false, "admin": false, ":admin": false} {
		if got := hasAdminRole(strings.Split(roles, ","), "administrator"); got != want {
			t.Errorf("%q: got %v", roles, got)
		}
	}
}
