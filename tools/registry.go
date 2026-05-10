package tools

import (
	"net/http"
	"sync"

	"lotusforge.au/api-server/models"
)

// ── Handler registry ──────────────────────────────────────────────────────────

// SwappableHandler wraps an http.Handler and allows it to be hot-swapped without
// recreating the route registration.
type SwappableHandler struct {
	current http.Handler
	mu      sync.RWMutex
	version string
}

// HandlerRegistry maps routes to SwappableHandlers, allowing live handler replacement.
type HandlerRegistry struct {
	mu       sync.RWMutex
	Handlers map[string]*SwappableHandler
	mux      *http.ServeMux
}

func NewHandlerRegistry(mux *http.ServeMux) *HandlerRegistry {
	return &HandlerRegistry{
		mux:      mux,
		Handlers: map[string]*SwappableHandler{},
	}
}

// Register adds or updates the handler for route. If the route already exists, the handler
// is only swapped if the new version is greater than the current one.
func (r *HandlerRegistry) Register(route string, handler http.Handler, version string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	cur, exists := r.Handlers[route]
	if exists {
		updated, err := CheckVersionIncrease(cur.version, version)
		if updated {
			cur.Swap(handler)
			cur.version = version
		}
		if err != nil {
			return err
		}
		return nil
	}

	sh := &SwappableHandler{current: handler, version: version}
	r.Handlers[route] = sh
	r.mux.Handle(route, sh)
	return nil
}

// Swap thread-safely replaces the current handler.
func (h *SwappableHandler) Swap(handler http.Handler) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.current = handler
}

func (s *SwappableHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	h := s.current
	s.mu.Unlock()
	h.ServeHTTP(w, r)
}

// ── Model registry ────────────────────────────────────────────────────────────

// ModelRegistry is a thread-safe store of all loaded DataModels, keyed by table name.
type ModelRegistry struct {
	mu     sync.RWMutex
	models map[string]*models.DataModel
}

func NewModelRegistry() *ModelRegistry {
	return &ModelRegistry{models: make(map[string]*models.DataModel)}
}

// Register adds or replaces the model for its table. Safe to call concurrently.
func (r *ModelRegistry) Register(m *models.DataModel) {
	if m.Table_name == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.models[*m.Table_name] = m
}

// All returns a snapshot of all currently registered models.
func (r *ModelRegistry) All() []models.DataModel {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]models.DataModel, 0, len(r.models))
	for _, m := range r.models {
		out = append(out, *m)
	}
	return out
}

// ByEndpoint returns the model whose End_point matches the given path, or
// (nil, false) if no model claims that endpoint.
func (r *ModelRegistry) ByEndpoint(end_point string) (*models.DataModel, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, m := range r.models {
		if m.End_point != nil && *m.End_point == end_point {
			return m, true
		}
	}
	return nil, false
}
