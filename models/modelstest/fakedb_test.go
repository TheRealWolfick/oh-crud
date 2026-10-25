package modelstest

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestAssign(t *testing.T) {
	var i int
	if err := assign(&i, int64(7)); err != nil || i != 7 {
		t.Errorf("int64 into *int: got %v, %v", i, err)
	}

	var s string
	if err := assign(&s, 5); err == nil {
		t.Errorf("int into *string should error, got %q", s)
	}
	if err := assign(&s, nil); err == nil {
		t.Errorf("NULL into *string should error")
	}

	b := []byte("old")
	if err := assign(&b, nil); err != nil || b != nil {
		t.Errorf("NULL into *[]byte should zero it: got %v, %v", b, err)
	}

	if err := assign(s, "x"); err == nil {
		t.Errorf("non-pointer destination should error")
	}
}

func TestQueryRow(t *testing.T) {
	db := &FakeDB{}
	var name string
	if err := db.QueryRow(context.Background(), "SELECT").Scan(&name); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("Expected pgx.ErrNoRows with no Row set, got %v", err)
	}

	db.Row = []any{"bob", 3}
	var count int
	if err := db.QueryRow(context.Background(), "SELECT").Scan(&name, &count); err != nil || name != "bob" || count != 3 {
		t.Errorf("Scan: got %q %d %v", name, count, err)
	}
}

func TestQueryFreshCursor(t *testing.T) {
	db := &FakeDB{Rows: FakeRows{Cols: []string{"code", "count"}, Data: [][]any{{"a", 1}, {"b", 2}}}}

	for range 2 {
		rows, err := db.Query(context.Background(), "SELECT")
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		got, err := pgx.CollectRows(rows, pgx.RowToMap)
		if err != nil || len(got) != 2 || got[1]["code"] != "b" {
			t.Errorf("CollectRows: got %v, %v", got, err)
		}
	}
	if len(db.Calls) != 2 {
		t.Errorf("Expected 2 recorded calls, got %d", len(db.Calls))
	}
}

func TestTx(t *testing.T) {
	db := &FakeDB{}
	tx, err := db.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := tx.Exec(context.Background(), "UPDATE x"); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := tx.Rollback(context.Background()); !errors.Is(err, pgx.ErrTxClosed) {
		t.Errorf("Rollback after commit should be ErrTxClosed, got %v", err)
	}
	if !db.Tx.Committed || db.Tx.RolledBack || db.LastCall().SQL != "UPDATE x" {
		t.Errorf("Unexpected tx state: %+v, last call %+v", db.Tx, db.LastCall())
	}
}
