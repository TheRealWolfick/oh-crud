package schematools

import (
	"io"
	"log/slog"
	"testing"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
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
