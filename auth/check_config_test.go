package auth

import (
	"testing"

	"lotusforge.au/api-server/models"
)

func TestCheckUserAgainstConfig(t *testing.T) {
	a := &Auth{}
	cfg := &models.DataModel{
		Admin_roles:        []string{"admin"},
		End_points_allowed: &models.End_pointsAllowed{GET: []string{"viewer"}},
	}
	noGet := &models.DataModel{
		Admin_roles:        []string{"admin"},
		End_points_allowed: &models.End_pointsAllowed{},
	}

	cases := []struct {
		name string
		usr  *models.User
		cfg  *models.DataModel
		want bool
	}{
		{"allowed role", &models.User{Roles: []string{"viewer"}}, cfg, true},
		{"other role", &models.User{Roles: []string{"writer"}}, cfg, false},
		{"allowed role second", &models.User{Roles: []string{"writer", "viewer"}}, cfg, true},
		{"admin after a non-matching role, GET disabled", &models.User{Roles: []string{"viewer", "admin"}}, noGet, true},
		{"GET disabled", &models.User{Roles: []string{"viewer"}}, noGet, false},
		{"nil user", nil, cfg, false},
		{"nil model", &models.User{Roles: []string{"admin"}}, nil, false},
		{"nil end points", &models.User{Roles: []string{"viewer"}}, &models.DataModel{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := a.CheckUserAgainstConfig(tc.usr, tc.cfg, "get"); got != tc.want {
				t.Errorf("CheckUserAgainstConfig = %v, expected %v", got, tc.want)
			}
		})
	}
}

// Diff topics are gated on the DIFF role, not GET
func TestCheckUserAgainstConfigDiff(t *testing.T) {
	a := &Auth{}
	cfg := &models.DataModel{
		End_points_allowed: &models.End_pointsAllowed{GET: []string{"viewer"}, DIFF: []string{"auditor"}},
	}
	if a.CheckUserAgainstConfig(&models.User{Roles: []string{"viewer"}}, cfg, "diff") {
		t.Error("Expected a GET role to be refused diffs")
	}
	if !a.CheckUserAgainstConfig(&models.User{Roles: []string{"auditor"}}, cfg, "diff") {
		t.Error("Expected the DIFF role to be allowed diffs")
	}
}
