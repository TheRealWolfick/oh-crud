package models

import (
	"time"
)


type Service struct {
	ID               string `yaml:"id"`
	Role             string `yaml:"role"`
	Entra_client_id  *string `yaml:"entra_client_id,omitempty"`
	Disabled_at      *time.Time `yaml:"disabled_at,omitempty"`
}

type ServiceKey struct {
	KID            string      `yaml:"kid"`
	Service_id     string      `yaml:"service_id"`
	Public_key     []byte      `yaml:"public_key"`
	Last_used_at   *time.Time  `yaml:"last_used_at,omitempty"`
  Revoked_at     *time.Time  `yaml:"revoked_at,omitempty"`
	Revoked_reason *string      `yaml:"revoked_reason,omitempty"`
}

type ReplayCache interface {
	// Return false if jti was already seen.
	Remember(jti string, until time.Time) bool
}

