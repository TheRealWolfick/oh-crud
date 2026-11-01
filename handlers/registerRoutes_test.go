package handlers

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"lotusforge.au/api-server/auth"
	"lotusforge.au/api-server/events"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/models/modelstest"
	"lotusforge.au/api-server/registry"
	"lotusforge.au/api-server/schematools"
)

func noopAuth(*models.DataModel) func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler { return h }
}

// TestRegisterRoutes_NilDiffCfg_NoPanic covers the case where a resource model sets
// allow-diff: true but the diffs model failed to load (diff_cfg == nil) — e.g. because
// config/default/diffs.yaml is missing or invalid. RegisterRoutes must not panic, and
// must simply skip registering the /diff routes for that resource.
func TestRegisterRoutes_NilDiffCfg_NoPanic(t *testing.T) {
	db := &modelstest.FakeDB{}
	qm := newTestQueueManager(db)
	cfg := diffResourceCfg() // Allow_diff: true, but we pass no diffs model below
	mux := http.NewServeMux()
	hr := registry.NewHandlerRegistry(mux)
	svr := models.NewSwappableServerConfig(&models.ServerConfig{})
	oauth := auth.AuthHandler(db, svr, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	evh := events.NewEventManager(0, 0, db, oauth)
	gate := schematools.NewPendingApprovalGate()
	modelRegistry := registry.NewModelRegistry()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("RegisterRoutes panicked with nil diff_cfg: %v", r)
		}
	}()

	RegisterRoutes(cfg, nil, hr, noopAuth, qm, svr, evh, gate, modelRegistry)

	// The base resource route must still be registered...
	req := httptest.NewRequest(http.MethodGet, "/inventory/assets", nil)
	if _, pattern := mux.Handler(req); pattern == "" {
		t.Errorf("expected GET /inventory/assets to be registered even when diff_cfg is nil")
	}

	// ...but the /diff routes must not be, since there's no diffs model to serve them.
	diffReq := httptest.NewRequest(http.MethodGet, "/inventory/assets/diff", nil)
	if _, pattern := mux.Handler(diffReq); pattern != "" {
		t.Errorf("expected GET /inventory/assets/diff to be unregistered when diff_cfg is nil, got pattern %q", pattern)
	}
}

func TestRegisterRoutes_WithDiffCfg_RegistersDiffRoutes(t *testing.T) {
	db := &modelstest.FakeDB{}
	qm := newTestQueueManager(db)
	cfg := diffResourceCfg()
	diffCfg := diffModelCfg()
	mux := http.NewServeMux()
	hr := registry.NewHandlerRegistry(mux)
	svr := models.NewSwappableServerConfig(&models.ServerConfig{})
	oauth := auth.AuthHandler(db, svr, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	evh := events.NewEventManager(0, 0, db, oauth)
	gate := schematools.NewPendingApprovalGate()
	modelRegistry := registry.NewModelRegistry()

	RegisterRoutes(cfg, diffCfg, hr, noopAuth, qm, svr, evh, gate, modelRegistry)

	for _, target := range []string{"/inventory/assets/diff"} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		if _, pattern := mux.Handler(req); pattern == "" {
			t.Errorf("expected GET %s to be registered when diff_cfg is provided", target)
		}
	}
}

// newRouteFixture builds what RegisterRoutes needs, returning the mux it registers on and
// a register func that loads a model the way startup does
func newRouteFixture(t *testing.T) (*http.ServeMux, func(cfg *models.DataModel) error) {
	t.Helper()
	db := &modelstest.FakeDB{}
	qm := newTestQueueManager(db)
	mux := http.NewServeMux()
	hr := registry.NewHandlerRegistry(mux)
	svr := models.NewSwappableServerConfig(&models.ServerConfig{})
	oauth := auth.AuthHandler(db, svr, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	evh := events.NewEventManager(0, 0, db, oauth)
	gate := schematools.NewPendingApprovalGate()
	modelRegistry := registry.NewModelRegistry()
	register := func(cfg *models.DataModel) error {
		if err := modelRegistry.Register(cfg); err != nil { return err }
		RegisterRoutes(cfg, nil, hr, noopAuth, qm, svr, evh, gate, modelRegistry)
		return nil
	}
	return mux, register
}

func routeRegistered(mux *http.ServeMux, method string, target string) bool {
	_, pattern := mux.Handler(httptest.NewRequest(method, target, nil))
	return pattern != ""
}

// A blank or missing end-point must not register routes at the server root
func TestRegisterRoutes_NoEndpoint_RegistersNothing(t *testing.T) {
	for name, ep := range map[string]*string{"blank": ptr(""), "missing": nil} {
		t.Run(name, func(t *testing.T) {
			mux, register := newRouteFixture(t)
			cfg := diffResourceCfg()
			cfg.End_point = ep
			if err := register(cfg); err != nil {
				t.Fatalf("expected a model with no end-point to load, got %v", err)
			}
			if routeRegistered(mux, http.MethodGet, "/") || routeRegistered(mux, http.MethodOptions, "/") {
				t.Error("expected no routes at the root for a model without an end-point")
			}
		})
	}
}

// A second model on the same end-point is refused, and does not stop the first reloading
func TestRegisterRoutes_DuplicateEndpoint_Refused(t *testing.T) {
	mux, register := newRouteFixture(t)
	first := diffResourceCfg()
	if err := register(first); err != nil {
		t.Fatalf("register first: %v", err)
	}

	second := diffResourceCfg()
	second.Name = ptr("other")
	second.Table_name = ptr("other_assets")
	if err := register(second); err == nil {
		t.Error("expected a second model on the same end-point to be refused")
	}

	// A hot reload of the owner is still allowed
	first.Version = ptr("1.0.1")
	if err := register(first); err != nil {
		t.Errorf("expected the owner to reload, got %v", err)
	}
	if !routeRegistered(mux, http.MethodPost, "/inventory/assets") {
		t.Error("expected the first model's routes to be registered")
	}
}

// Every route a model gets sits under /{service}/{end-point}, and nothing is left at the
// old unprefixed paths
func TestRegisterRoutes_AllRoutesUnderServicePath(t *testing.T) {
	mux, register := newRouteFixture(t)
	cfg := diffResourceCfg()
	cfg.Track_history = ptr(true)
	if err := register(cfg); err != nil {
		t.Fatalf("register: %v", err)
	}

	want := []struct{ method, target string }{
		{http.MethodGet, "/inventory/assets"},
		{http.MethodOptions, "/inventory/assets"},
		{http.MethodPut, "/inventory/assets/group"},
		{http.MethodGet, "/inventory/assets/fn/aggregate"},
		{http.MethodGet, "/inventory/assets/admin/pending"},
		{http.MethodPost, "/inventory/assets/admin/approve"},
		{http.MethodGet, "/inventory/assets/history/42"},
		{http.MethodGet, "/inventory/assets/ws"},
	}
	for _, w := range want {
		if !routeRegistered(mux, w.method, w.target) {
			t.Errorf("expected %s %s to be registered", w.method, w.target)
		}
	}

	gone := []struct{ method, target string }{
		{http.MethodGet, "/assets"},
		{http.MethodOptions, "/assets"},
		{http.MethodGet, "/assets/admin/pending"},
		{http.MethodGet, "/assets/history/42"},
		{http.MethodGet, "/ws/assets"},
	}
	for _, g := range gone {
		if routeRegistered(mux, g.method, g.target) {
			t.Errorf("expected nothing at the old path %s %s", g.method, g.target)
		}
	}
}

// Two services may each have an end-point of the same name
func TestRegisterRoutes_SameEndpointInTwoServices(t *testing.T) {
	mux, register := newRouteFixture(t)
	first := diffResourceCfg()
	if err := register(first); err != nil {
		t.Fatalf("register first: %v", err)
	}

	second := diffResourceCfg()
	second.Name = ptr("permit-assets")
	second.Table_name = ptr("permit_assets")
	second.Service = ptr("permits")
	if err := register(second); err != nil {
		t.Fatalf("expected the same end-point in another service to load, got %v", err)
	}

	for _, target := range []string{"/inventory/assets", "/permits/assets"} {
		if !routeRegistered(mux, http.MethodGet, target) {
			t.Errorf("expected GET %s to be registered", target)
		}
	}
}

// A model with an end-point but no service must not register routes or panic
func TestRegisterRoutes_NoService_RegistersNothing(t *testing.T) {
	mux, register := newRouteFixture(t)
	cfg := diffResourceCfg()
	cfg.Service = nil
	if err := register(cfg); err != nil {
		t.Fatalf("register: %v", err)
	}
	for _, target := range []string{"/assets", "//assets"} {
		if routeRegistered(mux, http.MethodGet, target) {
			t.Errorf("expected no route at %s for a model without a service", target)
		}
	}
}
