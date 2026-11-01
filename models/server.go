package models

import (
	"sync"
	"time"
)

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
	CORS 						*CorsConfig    `yaml:"cors"`
	RBAC 						*RBACConfig    `yaml:"rbac"`
  Issuer          string 	   	   `yaml:"issuer"`
	Token_url       string 			   `yaml:"token-url"`
	Token_lifetime  time.Duration  `yaml:"token-lifetime"`
	Logging         *LoggingConfig `yaml:"logging"`
	Audit           *AuditConfig   `yaml:"audit"`
}

// LoggingConfig controls the application log kept in the database (ohc_logs)
type LoggingConfig struct {
	Retention_days int    `yaml:"retention-days"` // Days of logs kept. Older days are dropped whole. Default 14
	DB_level       string `yaml:"db-level"`       // Lowest level written to the database: debug, info, warn, error. Default info
}

// AuditConfig controls the audit log (ohc_audit_events)
type AuditConfig struct {
	Min_retention_days int `yaml:"min-retention-days"` // Entries newer than this can never be pruned. Default 365
}

// Defaults for the settings above when server.yaml leaves them out
const (
	DefaultLogRetentionDays   = 14
	DefaultAuditRetentionDays = 365
)

// LogRetentionDays is how many days of application logs to keep
func (c *ServerConfig) LogRetentionDays() int {
	if c == nil || c.Logging == nil || c.Logging.Retention_days <= 0 { return DefaultLogRetentionDays }
	return c.Logging.Retention_days
}

// LogDBLevel is the lowest level written to the database, as written in server.yaml
func (c *ServerConfig) LogDBLevel() string {
	if c == nil || c.Logging == nil { return "" }
	return c.Logging.DB_level
}

// AuditMinRetentionDays is the age below which audit entries can never be pruned
func (c *ServerConfig) AuditMinRetentionDays() int {
	if c == nil || c.Audit == nil || c.Audit.Min_retention_days <= 0 { return DefaultAuditRetentionDays }
	return c.Audit.Min_retention_days
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
