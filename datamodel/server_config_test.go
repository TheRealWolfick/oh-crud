package datamodel

import (
	"strings"
	"testing"
	"time"

	"lotusforge.au/api-server/models"
)

// Loads the real server.yaml so a yaml tag that doesn't match its key fails here
// instead of silently loading as a zero value
func TestServerConfigAuthFields(t *testing.T) {
	cfg, err := LoadYAMLIntoModel[models.ServerConfig]("../config/server/server.yaml")
	if err != nil {
		t.Fatalf("Load server.yaml: %v", err)
	}

	if !strings.HasPrefix(cfg.Issuer, "https://") {
		t.Errorf("Expected issuer to be an https URL, got %q", cfg.Issuer)
	}
	if !strings.HasPrefix(cfg.Token_url, "https://") || !strings.HasSuffix(cfg.Token_url, "/oauth/token") {
		t.Errorf("Expected token-url to be the https /oauth/token URL, got %q", cfg.Token_url)
	}
	if cfg.Token_lifetime < time.Minute || cfg.Token_lifetime > time.Hour {
		t.Errorf("Expected token-lifetime between 1m and 1h, got %v", cfg.Token_lifetime)
	}
}
