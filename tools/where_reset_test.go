package tools

import "testing"

// TestSetWhereAbsolute_ReSetUpdatesOwnArgSlot is a regression test for a bug where
// re-setting an already-bound WHERE field (SetWhereAbsolute called twice for the same
// field, or after SetWhere already bound it) indexed into the wrong args slot: it read
// qb.values[field] — a map used only by SetValue for INSERT/UPDATE columns, almost
// always unset for a WHERE-only field — instead of qb.where[field], and without the
// placeholder-number-to-slice-index correction. In practice this silently overwrote
// args[0] (whichever field happened to bind first) while leaving the field being
// re-set at its stale original value.
//
// This matters most for getDiff's resource-table scoping: it calls
// SetWhereAbsolute("diff_type", resourceTable) *after* URL params may have already
// bound "diff_type" via a caller-supplied query parameter — exactly the re-set path
// this test exercises. Before the fix, a caller-supplied ?diff_type=... could survive
// instead of being overridden by the server's own scoping value.
func TestSetWhereAbsolute_ReSetUpdatesOwnArgSlot(t *testing.T) {
	qb := NewQueryBuilder(GetBasicLogger())
	qb.SetWhereAbsolute("other_field", "first")         // binds $1 / args[0]
	qb.SetWhere("diff_type", "malicious", FieldString)  // binds $2 / args[1] via ~*
	qb.SetWhereAbsolute("diff_type", "assets")          // must overwrite args[1], not args[0]

	args := qb.GetArgs()
	if len(args) != 2 {
		t.Fatalf("expected 2 bound args, got %d: %v", len(args), args)
	}
	if args[0] != "first" {
		t.Errorf("re-setting diff_type must not disturb other_field's bound value, got args[0]=%v", args[0])
	}
	if args[1] != "assets" {
		t.Errorf("re-setting diff_type must overwrite its own bound value, got args[1]=%v (still the caller-supplied value)", args[1])
	}
}

// TestSetWhere_ReSetUpdatesOwnArgSlot covers the same bug on the non-absolute
// (innerSetWhere) path.
func TestSetWhere_ReSetUpdatesOwnArgSlot(t *testing.T) {
	qb := NewQueryBuilder(GetBasicLogger())
	qb.SetWhereAbsolute("other_field", "first") // binds $1 / args[0]
	qb.SetWhere("amount", 5, FieldInt)          // binds $2 / args[1]
	qb.SetWhere("amount", 9, FieldInt)          // re-set: must overwrite args[1], not args[0]

	args := qb.GetArgs()
	if len(args) != 2 {
		t.Fatalf("expected 2 bound args, got %d: %v", len(args), args)
	}
	if args[0] != "first" {
		t.Errorf("re-setting amount must not disturb other_field's bound value, got args[0]=%v", args[0])
	}
	if args[1] != 9 {
		t.Errorf("re-setting amount must overwrite its own bound value with 9, got args[1]=%v", args[1])
	}
}
