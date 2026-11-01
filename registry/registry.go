package registry

import (
	"fmt"
	"net/http"
	"sync"

	"lotusforge.au/api-server/datamodel"
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
		updated, err := datamodel.CheckVersionIncrease(cur.version, version)
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

// Register adds or replaces the model for its table. Safe to call concurrently. A model
// whose end-point another table already uses is refused, so it cannot take over that
// table's routes, nor block the owner from reloading.
func (r *ModelRegistry) Register(m *models.DataModel) error {
	if m.Table_name == nil {
		return fmt.Errorf("model has no table name")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if path := m.Path(); path != "" {
		for table, other := range r.models {
			if table != *m.Table_name && other.Path() == path {
				return fmt.Errorf("end-point %q is already used by table %q", path, table)
			}
		}
	}
	r.models[*m.Table_name] = m
	return nil
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

// ByPath returns the model served at path ("service/end-point", see DataModel.Path),
// or (nil, false) if no model is.
func (r *ModelRegistry) ByPath(path string) (*models.DataModel, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if path == "" { return nil, false }
	for _, m := range r.models {
		if m.Path() == path {
			return m, true
		}
	}
	return nil, false
}

// OtherByPath returns a model, other than the one for table_name, served at path
func (r *ModelRegistry) OtherByPath(path string, table_name string) (*models.DataModel, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if path == "" { return nil, false }
	for table, m := range r.models {
		if table != table_name && m.Path() == path {
			return m, true
		}
	}
	return nil, false
}

// ByTableName returns the model for a specific table as per the register
func (r *ModelRegistry) ByTableName(table_name string) (*models.DataModel, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	model, okay := r.models[table_name]
	return model, okay
}
