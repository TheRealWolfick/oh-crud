package models

import "sync"

type CorsConfig struct {
	Allowed_Origins []string `yaml:"allowed-origins"`
	Allowed_Headers []string `yaml:"allowed-headers"`
	Allow_Credentials *bool  `yaml:"allow-credentials"`
}

type ServerConfig struct {
	CORS *CorsConfig `yaml:"cors"`
}

type SwappableServerConfig struct {
	current *ServerConfig
	mu sync.RWMutex
}


func (s *SwappableServerConfig) Swap(new *ServerConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = new
}

func (s *SwappableServerConfig) Get() *ServerConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current
}

func NewSwappableServerConfig(s *ServerConfig) *SwappableServerConfig {
	return &SwappableServerConfig{current: s}
}
