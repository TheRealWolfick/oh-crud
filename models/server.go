package models

import "sync"

type CorsConfig struct {
	Allowed_Origins []string `yaml:"allowed-origins"`
	Allowed_Headers []string `yaml:"allowed-headers"`
	Allow_Credentials *bool  `yaml:"allow-credentials"`
}

type RBACConfig struct {
	Admin_role         string    `yaml:"admin-role"`
	Default_user_role  string    `yaml:"default-user-role"`
	Custom_roles       []string  `yaml:"custom-roles"`
}

type ServerConfig struct {
	CORS *CorsConfig `yaml:"cors"`
	RBAC *RBACConfig `yaml:"rbac"`
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
