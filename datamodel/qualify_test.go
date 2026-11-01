package datamodel

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"lotusforge.au/api-server/models"
)

func TestProcessQualifiesNames(t *testing.T) {
	m := basicModel()
	m.Foreign_keys = map[string]models.ForeignKey{
		"fk_building": {Fields: []string{"code"}, Target_table: ptr(" building "), Target_fields: []string{"code"}},
	}
	m.Indexes = map[string]models.Index{"idx_name": {Fields: []string{"name"}}}

	ProcessModelAdditionalFields(&m)

	if *m.Table_name != "assets_test_table" || m.Short_table_name == nil || *m.Short_table_name != "test_table" {
		t.Errorf("expected table assets_test_table from test_table, got %q from %v", *m.Table_name, m.Short_table_name)
	}
	if got := *m.Foreign_keys["fk_building"].Target_table; got != "assets_building" {
		t.Errorf("expected the foreign key to point at assets_building, got %q", got)
	}
	// Foreign key names are per table in PostgreSQL, so they keep their names
	if _, ok := m.Foreign_keys["fk_building"]; !ok {
		t.Errorf("expected the foreign key name to be unchanged")
	}
	// Index names are per schema, so they get the service prefix
	if _, ok := m.Unique_keys["assets_test_uq_code"]; !ok || len(m.Unique_keys) != 1 {
		t.Errorf("expected unique key assets_test_uq_code, got %v", reflect.ValueOf(m.Unique_keys).MapKeys())
	}
	if _, ok := m.Indexes["assets_idx_name"]; !ok || len(m.Indexes) != 1 {
		t.Errorf("expected index assets_idx_name, got %v", reflect.ValueOf(m.Indexes).MapKeys())
	}

	// Processing again must not prefix twice
	ProcessModelAdditionalFields(&m)
	if *m.Table_name != "assets_test_table" || *m.Foreign_keys["fk_building"].Target_table != "assets_building" {
		t.Errorf("expected a second pass to change nothing, got %q and %q", *m.Table_name, *m.Foreign_keys["fk_building"].Target_table)
	}
	if _, ok := m.Indexes["assets_idx_name"]; !ok {
		t.Errorf("expected a second pass to leave index names alone")
	}
}

// Every database name must fit PostgreSQL's 63 character limit once the service is added,
// or Postgres truncates it and Oh CRUD queries a name that doesn't exist
func TestValidateQualifiedNameLengths(t *testing.T) {
	svc := strings.Repeat("s", 32)

	m := basicModel()
	m.Service = ptr(svc)
	m.Table_name = ptr(strings.Repeat("t", 30)) // 32 + 1 + 30 = 63
	m.Unique_keys = nil
	if err := ValidateDataModel(m); err != nil {
		t.Errorf("expected a 63 character table to pass, got %v", err)
	}

	m.Table_name = ptr(strings.Repeat("t", 31))
	if err := ValidateDataModel(m); err == nil || !strings.Contains(err.Error(), "table") {
		t.Errorf("expected a 64 character table to be rejected, got %v", err)
	}

	m = basicModel()
	m.Service = ptr(svc)
	m.Unique_keys = nil
	m.Table_name = ptr(strings.Repeat("t", 25))
	m.Track_history = ptr(true)
	if err := ValidateDataModel(m); err == nil || !strings.Contains(err.Error(), "history table") {
		t.Errorf("expected the history table to be checked, got %v", err)
	}

	m = basicModel()
	m.Service = ptr(svc)
	m.Unique_keys = map[string]models.UniqueKey{strings.Repeat("u", 31): {Fields: []string{"code"}}}
	if err := ValidateDataModel(m); err == nil || !strings.Contains(err.Error(), "unique key") {
		t.Errorf("expected a long unique key name to be rejected, got %v", err)
	}
}

func TestYAMLFiles(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"a.yaml", "b.yml", "notes.txt", "inventory/asset.yaml", "inventory/deep/x.yaml", ".hidden/skip.yaml"} {
		p := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil { t.Fatal(err) }
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil { t.Fatal(err) }
	}
	rel := func(paths []string) []string {
		out := []string{}
		for _, p := range paths { r, _ := filepath.Rel(dir, p); out = append(out, filepath.ToSlash(r)) }
		return out
	}

	flat, err := YAMLFiles(dir, false)
	if err != nil { t.Fatal(err) }
	if got := rel(flat); !reflect.DeepEqual(got, []string{"a.yaml"}) {
		t.Errorf("flat: got %v", got)
	}

	all, err := YAMLFiles(dir, true)
	if err != nil { t.Fatal(err) }
	if got := rel(all); !reflect.DeepEqual(got, []string{"a.yaml", "inventory/asset.yaml", "inventory/deep/x.yaml"}) {
		t.Errorf("recursive: got %v", got)
	}

	if _, err := YAMLFiles(filepath.Join(dir, "missing"), true); err == nil {
		t.Errorf("expected an error for a missing folder")
	}
}
