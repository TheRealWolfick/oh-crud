package datamodel

import "testing"

func TestValidateDataModelService(t *testing.T) {
	valid := []string{"ohc", "assets", "a1", "permittracker", "abcdefghijklmnopqrstuvwxyz012345"}
	for _, svc := range valid {
		m := basicModel()
		m.Service = ptr(svc)
		if err := ValidateDataModel(m); err != nil {
			t.Errorf("expected service %q to pass, got: %v", svc, err)
		}
	}

	// Must be a single lower-case path segment that can't collide with the server's routes
	// '-' would break unquoted SQL table names, and '_' would make <service>_<table> ambiguous
	invalid := []string{"", "a", "Assets", "1assets", "-assets", "permit-tracker", "as/sets", "as sets", "as.sets", "as_sets",
		"abcdefghijklmnopqrstuvwxyz0123456", "admin", "mesh", "oauth", "ws", "fn", "health"}
	for _, svc := range invalid {
		m := basicModel()
		m.Service = ptr(svc)
		if err := ValidateDataModel(m); err == nil {
			t.Errorf("expected service %q to be rejected", svc)
		}
	}

	m := basicModel()
	m.Service = nil
	if err := ValidateDataModel(m); err == nil {
		t.Errorf("expected a missing service to be rejected")
	}
}

func TestValidateModelOwner(t *testing.T) {
	cases := []struct {
		name    string
		path    *string
		service string
		ok      bool
	}{
		{"system model as ohc", ptr("./config/default/users.yaml"), "ohc", true},
		{"system model, unclean path", ptr("config//default/./users.yaml"), "ohc", true},
		{"system model claiming another service", ptr("./config/default/users.yaml"), "assets", false},
		{"user model as its service", ptr("./config/base-models/asset.yaml"), "assets", true},
		{"user model claiming ohc", ptr("./config/base-models/asset.yaml"), "ohc", false},
		{"user model in a sub folder of default", ptr("./config/default/extra/asset.yaml"), "ohc", false},
		{"model with no file claiming ohc", nil, "ohc", false},
		{"model with no file", nil, "assets", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := basicModel()
			m.Filepath = tc.path
			m.Service = ptr(tc.service)
			if err := ValidateModelOwner(m); (err == nil) != tc.ok {
				t.Errorf("expected ok=%v, got %v", tc.ok, err)
			}
		})
	}
}
