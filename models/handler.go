package models

import (
	"net/http"
	"sync"
)

type SwappableHandler struct {
	current http.Handler
	mu sync.RWMutex
	version string
}

type HandlerRegistry struct {
	mu  sync.RWMutex
	Handlers map[string]*SwappableHandler
	mux *http.ServeMux
}

func NewHandlerRegistry(mux *http.ServeMux) *HandlerRegistry {
	return &HandlerRegistry{
		mux: mux,
		Handlers: map[string]*SwappableHandler{},
	}
}

func (s *HandlerRegistry) Register(route string, handler http.Handler, version string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cur, exists := s.Handlers[route]; if exists {
		updated, err := CheckVersionIncrease(cur.version, version) 
		if updated {
			cur.Swap(handler) 
			cur.version = version
		}
		if err != nil { return err }
		return nil
	}

	sh := &SwappableHandler{current: handler, version: version}
	s.Handlers[route] = sh
	s.mux.Handle(route, sh)
	return nil
}

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
