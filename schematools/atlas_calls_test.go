package schematools

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"lotusforge.au/api-server/models"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func strp(s string) *string { return &s }

// writeNeedsSyncFixture lays down an .hcl file and its .version sidecar in a temp
// dir and returns a model wired to that table plus the hcl path needsSync expects.
func writeNeedsSyncFixture(t *testing.T, table, storedVersion, configVersion string) (*models.DataModel, string) {
	t.Helper()
	dir := t.TempDir()
	hclPath := filepath.Join(dir, table+".pg.hcl")
	if err := os.WriteFile(hclPath, []byte("schema \"public\" {}\n"), 0o644); err != nil {
		t.Fatalf("write hcl: %v", err)
	}
	if storedVersion != "" {
		if err := os.WriteFile(hclPath+".version", []byte(storedVersion), 0o644); err != nil {
			t.Fatalf("write version sidecar: %v", err)
		}
	}
	cfgPath := filepath.Join(dir, table+".yaml")
	if err := os.WriteFile(cfgPath, []byte("name: "+table+"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	m := &models.DataModel{
		Table_name: strp(table),
		Version:    strp(configVersion),
		Filepath:   strp(cfgPath),
	}
	return m, hclPath
}

// TestNeedsSync_VersionIncrease confirms a genuine bump still triggers a sync.
func TestNeedsSync_VersionIncrease(t *testing.T) {
	m, hclPath := writeNeedsSyncFixture(t, "buildings", "1.3.10", "1.3.11")
	if !needsSync(m, hclPath) {
		t.Errorf("needsSync = false for a version increase, want true")
	}
}

// TestNeedsSync_VersionUnchanged confirms an unchanged version is a no-op (this
// path runs for every model on every startup, so it must stay quiet and cheap).
func TestNeedsSync_VersionUnchanged(t *testing.T) {
	m, hclPath := writeNeedsSyncFixture(t, "buildings", "1.3.11", "1.3.11")
	if needsSync(m, hclPath) {
		t.Errorf("needsSync = true for an unchanged version, want false")
	}
}

// TestNeedsSync_VersionDecrease is the regression guard: pushing an older config
// must not drive a schema sync, because Atlas would revert the live table to the
// older shape and the non-destructive reverts would slip past the approval gate.
func TestNeedsSync_VersionDecrease(t *testing.T) {
	m, hclPath := writeNeedsSyncFixture(t, "buildings", "1.3.11", "1.3.10")
	if needsSync(m, hclPath) {
		t.Errorf("needsSync = true for a version decrease, want false (silent rollback risk)")
	}
}

// TestNeedsSync_UnparseableVersion covers a malformed config version: not a strict
// increase, so it must not sync.
func TestNeedsSync_UnparseableVersion(t *testing.T) {
	m, hclPath := writeNeedsSyncFixture(t, "buildings", "1.3.11", "not-a-version")
	if needsSync(m, hclPath) {
		t.Errorf("needsSync = true for an unparseable version, want false")
	}
}

// TestNeedsSync_MissingHCL treats a table with no generated HCL yet as new.
func TestNeedsSync_MissingHCL(t *testing.T) {
	m, hclPath := writeNeedsSyncFixture(t, "buildings", "1.3.11", "1.3.11")
	if err := os.Remove(hclPath); err != nil {
		t.Fatalf("remove hcl: %v", err)
	}
	if !needsSync(m, hclPath) {
		t.Errorf("needsSync = false when the .hcl file is missing, want true")
	}
}

// TestClearPending_RemovesSupersededEntry covers the core of the stuck-table fix: once
// a table's diff is known to be non-destructive, any change previously recorded for it
// must be dropped so the file monitor stops skipping the table's route registration.
func TestClearPending_RemovesSupersededEntry(t *testing.T) {
	gate := NewPendingApprovalGate()
	gate.Record("app_diff_state", `DROP COLUMN "app_diff_state"."legacy"`, nil)

	if _, pending := gate.Pending("app_diff_state"); !pending {
		t.Fatalf("precondition: expected a pending change for app_diff_state")
	}

	clearPending(gate, "app_diff_state", quietLogger())

	if _, pending := gate.Pending("app_diff_state"); pending {
		t.Errorf("clearPending did not remove the superseded entry")
	}
}

// TestClearPending_NoEntry_IsNoOp guards against clearPending disturbing unrelated
// state when there is nothing recorded for the table.
func TestClearPending_NoEntry_IsNoOp(t *testing.T) {
	gate := NewPendingApprovalGate()
	gate.Record("other_table", "DROP TABLE \"other_table\"", nil)

	clearPending(gate, "app_diff_state", quietLogger())

	if _, pending := gate.Pending("other_table"); !pending {
		t.Errorf("clearPending removed an entry for an unrelated table")
	}
}

// TestClearPending_NilGate_DoesNotPanic covers the dev-mode path where no gate is wired.
func TestClearPending_NilGate_DoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("clearPending panicked with a nil gate: %v", r)
		}
	}()
	clearPending(nil, "app_diff_state", quietLogger())
}
