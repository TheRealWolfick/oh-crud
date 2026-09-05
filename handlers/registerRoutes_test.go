package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/schematools"
	"lotusforge.au/api-server/tools"
)

func noopAuth(h http.Handler) http.Handler { return h }

// TestRegisterRoutes_NilDiffCfg_NoPanic covers the case where a resource model sets
// allow-diff: true but the diffs model failed to load (diff_cfg == nil) — e.g. because
// config/default/diffs.yaml is missing or invalid. RegisterRoutes must not panic, and
// must simply skip registering the /diff routes for that resource.
func TestRegisterRoutes_NilDiffCfg_NoPanic(t *testing.T) {
	db := &fakeDB{}
	qm := newTestQueueManager(db)
	cfg := diffResourceCfg() // Allow_diff: true, but we pass no diffs model below
	mux := http.NewServeMux()
	hr := tools.NewHandlerRegistry(mux)
	evh := tools.NewEventManager(0, 0, db)
	gate := schematools.NewPendingApprovalGate()
	modelRegistry := tools.NewModelRegistry()
	svr := models.NewSwappableServerConfig(&models.ServerConfig{})

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("RegisterRoutes panicked with nil diff_cfg: %v", r)
		}
	}()

	RegisterRoutes(cfg, nil, hr, noopAuth, qm, svr, evh, gate, modelRegistry)

	// The base resource route must still be registered...
	req := httptest.NewRequest(http.MethodGet, "/assets", nil)
	if _, pattern := mux.Handler(req); pattern == "" {
		t.Errorf("expected GET /assets to be registered even when diff_cfg is nil")
	}

	// ...but the /diff routes must not be, since there's no diffs model to serve them.
	diffReq := httptest.NewRequest(http.MethodGet, "/assets/diff", nil)
	if _, pattern := mux.Handler(diffReq); pattern != "" {
		t.Errorf("expected GET /assets/diff to be unregistered when diff_cfg is nil, got pattern %q", pattern)
	}
}

func TestRegisterRoutes_WithDiffCfg_RegistersDiffRoutes(t *testing.T) {
	db := &fakeDB{}
	qm := newTestQueueManager(db)
	cfg := diffResourceCfg()
	diffCfg := diffModelCfg()
	mux := http.NewServeMux()
	hr := tools.NewHandlerRegistry(mux)
	evh := tools.NewEventManager(0, 0, db)
	gate := schematools.NewPendingApprovalGate()
	modelRegistry := tools.NewModelRegistry()
	svr := models.NewSwappableServerConfig(&models.ServerConfig{})

	RegisterRoutes(cfg, diffCfg, hr, noopAuth, qm, svr, evh, gate, modelRegistry)

	for _, target := range []string{"/assets/diff"} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		if _, pattern := mux.Handler(req); pattern == "" {
			t.Errorf("expected GET %s to be registered when diff_cfg is provided", target)
		}
	}
}
