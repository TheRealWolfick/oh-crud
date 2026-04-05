package models

import "sync"

// ModelRegistry is a thread-safe store of all loaded DataModels, keyed by table name.
// It is populated at startup and updated by the live-reload monitor when a config
// file changes. The schema sync path reads All() so it always has the full model set.
type ModelRegistry struct {
	mu     sync.RWMutex
	models map[string]*DataModel
}

func NewModelRegistry() *ModelRegistry {
	return &ModelRegistry{models: make(map[string]*DataModel)}
}

// Register adds or replaces the model for its table. Safe to call concurrently.
func (r *ModelRegistry) Register(m *DataModel) {
	if m.Table_name == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.models[*m.Table_name] = m
}

// All returns a snapshot of all currently registered models.
func (r *ModelRegistry) All() []DataModel {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]DataModel, 0, len(r.models))
	for _, m := range r.models {
		out = append(out, *m)
	}
	return out
}
