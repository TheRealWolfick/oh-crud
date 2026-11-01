package datamodel

import (
	"testing"
)

func TestValidateDataModelEndpoint(t *testing.T) {
	// Every end-point sits under its service, so top-level names like health are fine
	valid := []string{"", "  ", "asset", "asset/data", "Asset_Data/v2", "a-b/c_d", "health", "oauth/token"}
	for _, ep := range valid {
		m := basicModel()
		m.End_point = ptr(ep)
		if err := ValidateDataModel(m); err != nil {
			t.Errorf("expected end-point %q to pass, got: %v", ep, err)
		}
	}

	// Segments every model uses for its own sub-routes are refused anywhere in the path,
	// or one model's routes could collide with another's (asset/group vs asset + /group)
	invalid := []string{"/asset", "asset/", "asset//data", "asset data", "asset/{key}", ".well-known",
		"ws", "ws/asset", "asset/ws", "asset/group", "diff", "a/fn/b", "x/history", "admin"}
	for _, ep := range invalid {
		m := basicModel()
		m.End_point = ptr(ep)
		if err := ValidateDataModel(m); err == nil {
			t.Errorf("expected end-point %q to be rejected", ep)
		}
	}

	// Omitted is fine too
	m := basicModel()
	m.End_point = nil
	if err := ValidateDataModel(m); err != nil {
		t.Errorf("expected an omitted end-point to pass, got: %v", err)
	}
}

func TestProcessModelAdditionalFieldsNormalisesEndpoint(t *testing.T) {
	m := basicModel()
	m.End_point = nil
	ProcessModelAdditionalFields(&m)
	if m.End_point == nil || *m.End_point != "" || m.HasEndpoint() {
		t.Errorf("expected an omitted end-point to become empty, got %v", m.End_point)
	}

	m = basicModel()
	m.End_point = ptr("  asset/data ")
	ProcessModelAdditionalFields(&m)
	if *m.End_point != "asset/data" || !m.HasEndpoint() {
		t.Errorf("expected the end-point to be trimmed, got %q", *m.End_point)
	}
}
