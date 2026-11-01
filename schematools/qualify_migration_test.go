package schematools

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"lotusforge.au/api-server/datamodel"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/models/modelstest"
)

func p[T any](v T) *T { return &v }

// loaded builds a model as main.go leaves it: validated fields don't matter here, only
// the names, so it goes straight through ProcessModelAdditionalFields
func loaded(service, table string, history bool, uks []string, idx []string, serial ...string) models.DataModel {
	m := models.DataModel{Service: p(service), Table_name: p(table), Track_history: p(history), Fields: map[string]models.DataModelField{}}
	if len(uks) > 0 { m.Unique_keys = map[string]models.UniqueKey{} }
	for _, n := range uks { m.Unique_keys[n] = models.UniqueKey{} }
	if len(idx) > 0 { m.Indexes = map[string]models.Index{} }
	for _, n := range idx { m.Indexes[n] = models.Index{} }
	for _, f := range serial { m.Fields[f] = models.DataModelField{DB_type: p("bigserial")} }
	datamodel.ProcessModelAdditionalFields(&m)
	return m
}

func has(t *testing.T, stmts []string, want string) {
	t.Helper()
	if !slices.Contains(stmts, want) {
		t.Errorf("missing statement:\n  %s\ngot:\n  %s", want, strings.Join(stmts, "\n  "))
	}
}

func TestPlanQualifiedRenames(t *testing.T) {
	catalog := []catalogEntry{
		{"r", "users", ""}, {"i", "users_pkey", "users"}, {"i", "uk_users_username", "users"},
		{"r", "refresh_tokens", ""}, {"i", "refresh_tokens_pkey", "refresh_tokens"}, {"i", "family_id", "refresh_tokens"},
		{"i", "uk-refresh-tokens-tokenhash", "refresh_tokens"}, {"S", "refresh_tokens_id_seq", "refresh_tokens"},
		{"r", "diffs", ""}, {"r", "batch_numbers", ""},
		{"r", "asset_data", ""}, {"i", "asset_data_pkey", "asset_data"},
		{"r", "asset_data_history", ""}, {"i", "asset_data_history_pkey", "asset_data_history"},
		{"S", "asset_data_history_change_id_seq", "asset_data_history"},
		{"f", "fk_asset_data_history_pk", "asset_data_history"}, {"f", "fk_asset_data_history_user", "asset_data_history"},
		// already migrated: only the qualified table exists
		{"r", "inventory_building", ""},
	}
	models := []models.DataModel{
		loaded("ohc", "users", false, []string{"uk_users_username"}, nil),
		loaded("ohc", "refresh_tokens", false, []string{"uk-refresh-tokens-tokenhash"}, []string{"family_id"}, "id"),
		loaded("ohc", "diffs", false, nil, nil),
		loaded("ohc", "batch_numbers", false, nil, nil),
		loaded("inventory", "asset_data", true, nil, nil),
		loaded("inventory", "building", false, nil, nil),
		loaded("inventory", "new_table", false, nil, nil), // never created: nothing to do
	}

	plan := planQualifiedRenames(catalog, models)
	s := plan.Statements

	has(t, s, `ALTER TABLE "users" RENAME TO "ohc_users"`)
	has(t, s, `ALTER INDEX "users_pkey" RENAME TO "ohc_users_pkey"`)
	has(t, s, `ALTER INDEX "uk_users_username" RENAME TO "ohc_uk_users_username"`)
	has(t, s, `ALTER TABLE "refresh_tokens" RENAME TO "ohc_refresh_tokens"`)
	has(t, s, `ALTER INDEX "family_id" RENAME TO "ohc_family_id"`)
	has(t, s, `ALTER INDEX "uk-refresh-tokens-tokenhash" RENAME TO "ohc_uk-refresh-tokens-tokenhash"`)
	has(t, s, `ALTER SEQUENCE "refresh_tokens_id_seq" RENAME TO "ohc_refresh_tokens_id_seq"`)
	has(t, s, `ALTER TABLE "asset_data" RENAME TO "inventory_asset_data"`)
	has(t, s, `ALTER TABLE "asset_data_history" RENAME TO "inventory_asset_data_history"`)
	has(t, s, `ALTER INDEX "asset_data_history_pkey" RENAME TO "inventory_asset_data_history_pkey"`)
	has(t, s, `ALTER SEQUENCE "asset_data_history_change_id_seq" RENAME TO "inventory_asset_data_history_change_id_seq"`)
	has(t, s, `ALTER TABLE "inventory_asset_data_history" RENAME CONSTRAINT "fk_asset_data_history_pk" TO "fk_inventory_asset_data_history_pk"`)
	has(t, s, `ALTER TABLE "inventory_asset_data_history" RENAME CONSTRAINT "fk_asset_data_history_user" TO "fk_inventory_asset_data_history_user"`)

	// The columns holding table names are widened before the long names go in
	has(t, s, `ALTER TABLE ohc_diffs ALTER COLUMN diff_type TYPE character varying(63)`)
	has(t, s, `ALTER TABLE ohc_batch_numbers ALTER COLUMN table_name TYPE character varying(63)`)
	widen := slices.Index(s, `ALTER TABLE ohc_diffs ALTER COLUMN diff_type TYPE character varying(63)`)
	update := slices.Index(s, `UPDATE ohc_diffs SET diff_type = 'inventory_asset_data' WHERE diff_type = 'asset_data'`)
	if widen > update {
		t.Errorf("expected diff_type to be widened before it is updated")
	}

	// Rows naming a table by its old name follow it, after the system tables are renamed
	has(t, s, `UPDATE ohc_diffs SET diff_type = 'inventory_asset_data' WHERE diff_type = 'asset_data'`)
	has(t, s, `UPDATE ohc_batch_numbers SET table_name = 'inventory_asset_data', service = 'inventory' WHERE table_name = 'asset_data'`)
	lastDDL := -1
	firstUpdate := len(s)
	for i, st := range s {
		if strings.HasPrefix(st, "ALTER") { lastDDL = i }
		if strings.HasPrefix(st, "UPDATE") && i < firstUpdate { firstUpdate = i }
	}
	if lastDDL > firstUpdate {
		t.Errorf("expected every rename before the first data update")
	}

	for _, st := range s {
		if strings.Contains(st, "building") || strings.Contains(st, "new_table") {
			t.Errorf("expected nothing for a migrated or missing table, got %s", st)
		}
	}
	if len(plan.Renamed) != 5 {
		t.Errorf("expected 5 tables renamed, got %v", plan.Renamed)
	}
}

// A database with no bare tables, such as a fresh one or one already migrated, needs nothing
func TestPlanQualifiedRenamesNothingToDo(t *testing.T) {
	catalog := []catalogEntry{{"r", "ohc_users", ""}, {"i", "ohc_users_pkey", "ohc_users"}}
	plan := planQualifiedRenames(catalog, []models.DataModel{loaded("ohc", "users", false, nil, nil)})
	if len(plan.Statements) != 0 || len(plan.Renamed) != 0 {
		t.Errorf("expected no work, got %v", plan.Statements)
	}
}

// When both the bare and the qualified table exist, guessing could lose data, so neither moves
func TestPlanQualifiedRenamesConflictIsSkipped(t *testing.T) {
	catalog := []catalogEntry{{"r", "users", ""}, {"r", "ohc_users", ""}}
	plan := planQualifiedRenames(catalog, []models.DataModel{loaded("ohc", "users", false, nil, nil)})
	if len(plan.Statements) != 0 || len(plan.Skipped) != 1 {
		t.Errorf("expected the table to be skipped and reported, got %v / %v", plan.Statements, plan.Skipped)
	}
}

// An index that can't take its new name is reported, not renamed onto another object
func TestPlanQualifiedRenamesIndexNameTaken(t *testing.T) {
	catalog := []catalogEntry{{"r", "users", ""}, {"i", "uk_users_username", "users"}, {"i", "ohc_uk_users_username", "other"}}
	plan := planQualifiedRenames(catalog, []models.DataModel{loaded("ohc", "users", false, []string{"uk_users_username"}, nil)})
	for _, st := range plan.Statements {
		if strings.Contains(st, "ALTER INDEX") {
			t.Errorf("expected no index rename onto a taken name, got %s", st)
		}
	}
	if len(plan.Skipped) != 1 {
		t.Errorf("expected the clash to be reported, got %v", plan.Skipped)
	}
}

func TestMigrateToQualifiedNames(t *testing.T) {
	// Sidecar files live under ./config/database, so work in a scratch folder
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll(hclDir, 0o755); err != nil { t.Fatal(err) }
	for _, f := range []string{"users.pg.hcl", "users.pg.hcl.version", "users.current.yaml"} {
		if err := os.WriteFile(filepath.Join(hclDir, f), []byte(f), 0o644); err != nil { t.Fatal(err) }
	}

	db := &modelstest.FakeDB{}
	db.QueryFunc = func(sql string, args []any) (pgx.Rows, error) {
		return &modelstest.FakeRows{Cols: []string{"kind", "name", "tbl"}, Data: [][]any{
			{"r", "users", ""}, {"i", "users_pkey", "users"},
		}}, nil
	}
	ms := []models.DataModel{loaded("ohc", "users", false, nil, nil)}

	if err := MigrateToQualifiedNames(t.Context(), db, ms, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("MigrateToQualifiedNames: %v", err)
	}
	if db.Tx == nil || !db.Tx.Committed {
		t.Fatalf("expected the renames to run in a committed transaction")
	}
	var execs []string
	for _, c := range db.Calls { if c.Method == "Exec" { execs = append(execs, c.SQL) } }
	if !slices.Equal(execs, []string{`ALTER TABLE "users" RENAME TO "ohc_users"`, `ALTER INDEX "users_pkey" RENAME TO "ohc_users_pkey"`}) {
		t.Errorf("unexpected statements: %v", execs)
	}

	// The version and last-good config follow the table; the stale HCL goes, to be rewritten
	for f, want := range map[string]bool{
		"ohc_users.pg.hcl.version": true, "ohc_users.current.yaml": true,
		"users.pg.hcl.version": false, "users.current.yaml": false, "users.pg.hcl": false, "ohc_users.pg.hcl": false,
	} {
		_, err := os.Stat(filepath.Join(hclDir, f))
		if (err == nil) != want {
			t.Errorf("%s: expected exists=%v", f, want)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(hclDir, "ohc_users.pg.hcl.version")); string(b) != "users.pg.hcl.version" {
		t.Errorf("expected the version file's contents to move with it, got %q", b)
	}
}

// A failed statement leaves the database as it was: nothing is committed, no files move
func TestMigrateToQualifiedNamesRollsBack(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll(hclDir, 0o755); err != nil { t.Fatal(err) }
	os.WriteFile(filepath.Join(hclDir, "users.pg.hcl.version"), []byte("1.0.0"), 0o644)

	db := &modelstest.FakeDB{}
	db.QueryFunc = func(sql string, args []any) (pgx.Rows, error) {
		return &modelstest.FakeRows{Cols: []string{"kind", "name", "tbl"}, Data: [][]any{{"r", "users", ""}}}, nil
	}
	db.ExecFunc = func(sql string, args []any) (pgconn.CommandTag, error) { return pgconn.CommandTag{}, errors.New("boom") }

	err := MigrateToQualifiedNames(t.Context(), db, []models.DataModel{loaded("ohc", "users", false, nil, nil)}, slog.New(slog.DiscardHandler))
	if err == nil {
		t.Fatalf("expected the failure to be returned")
	}
	if db.Tx == nil || db.Tx.Committed {
		t.Errorf("expected the transaction not to commit")
	}
	if _, err := os.Stat(filepath.Join(hclDir, "users.pg.hcl.version")); err != nil {
		t.Errorf("expected the sidecar files to stay put after a failure")
	}
}
