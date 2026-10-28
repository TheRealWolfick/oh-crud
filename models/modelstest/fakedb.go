// Package modelstest provides in-memory fakes of the models database interfaces for tests.
// It lives in its own package so it is never compiled into the server binary.
package modelstest

import (
	"context"
	"fmt"
	"reflect"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"lotusforge.au/api-server/models"
)

// Compile-time checks that the fakes satisfy the interfaces they stand in for
var (
	_ models.DBTxBeginner = (*FakeDB)(nil)
	_ pgx.Tx              = (*FakeTx)(nil)
	_ pgx.Rows            = (*FakeRows)(nil)
	_ pgx.Row             = (*fakeRow)(nil)
)

// Call is one recorded Exec, Query or QueryRow
type Call struct {
	Method string
	SQL    string
	Args   []any
}

// FakeDB records every call and returns whatever results it is configured with.
// Calls made through a transaction from Begin are recorded here too, in order.
type FakeDB struct {
	mu    sync.Mutex
	Calls []Call

	// Exec results. ExecFunc, when set, overrides ExecTag and ExecErr,
	// which lets a test give different results to successive statements.
	ExecTag  pgconn.CommandTag
	ExecErr  error
	ExecFunc func(sql string, args []any) (pgconn.CommandTag, error)

	// QueryRow results. A nil Row with a nil RowErr scans as pgx.ErrNoRows.
	Row    []any
	RowErr error

	// Query results. Each Query gets a fresh copy of Rows, so the cursor is never shared.
	// QueryFunc, when set, overrides Rows and QueryErr, for tests that run different queries.
	Rows      FakeRows
	QueryErr  error
	QueryFunc func(sql string, args []any) (pgx.Rows, error)

	// Begin results. Tx is the most recent transaction returned by Begin.
	BeginErr  error
	CommitErr error
	Tx        *FakeTx
}

func (f *FakeDB) record(method, sql string, args []any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, Call{Method: method, SQL: sql, Args: args})
}

// LastCall returns the most recent call, or a zero Call if there were none
func (f *FakeDB) LastCall() Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.Calls) == 0 {
		return Call{}
	}
	return f.Calls[len(f.Calls)-1]
}

func (f *FakeDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	f.record("Exec", sql, args)
	if f.ExecFunc != nil {
		return f.ExecFunc(sql, args)
	}
	return f.ExecTag, f.ExecErr
}

func (f *FakeDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	f.record("QueryRow", sql, args)
	return &fakeRow{values: f.Row, err: f.RowErr}
}

func (f *FakeDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	f.record("Query", sql, args)
	if f.QueryFunc != nil {
		return f.QueryFunc(sql, args)
	}
	if f.QueryErr != nil {
		return nil, f.QueryErr
	}
	rows := &FakeRows{Cols: f.Rows.Cols, Data: f.Rows.Data, IterErr: f.Rows.IterErr}
	return rows, nil
}

func (f *FakeDB) Begin(ctx context.Context) (pgx.Tx, error) {
	if f.BeginErr != nil {
		return nil, f.BeginErr
	}
	tx := &FakeTx{db: f, commitErr: f.CommitErr}
	f.Tx = tx
	return tx, nil
}

// FakeTx sends Exec, Query and QueryRow to its parent FakeDB.
// The embedded pgx.Tx is nil, so any other method panics, which makes unexpected use obvious.
type FakeTx struct {
	pgx.Tx
	db         *FakeDB
	commitErr  error
	Committed  bool
	RolledBack bool
}

func (tx *FakeTx) closed() bool { return tx.Committed || tx.RolledBack }

func (tx *FakeTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if tx.closed() {
		return pgconn.CommandTag{}, pgx.ErrTxClosed
	}
	return tx.db.Exec(ctx, sql, args...)
}

func (tx *FakeTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if tx.closed() {
		return &fakeRow{err: pgx.ErrTxClosed}
	}
	return tx.db.QueryRow(ctx, sql, args...)
}

func (tx *FakeTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if tx.closed() {
		return nil, pgx.ErrTxClosed
	}
	return tx.db.Query(ctx, sql, args...)
}

// Commit fails with the FakeDB's CommitErr. A failed commit still closes the transaction, as in pgx.
func (tx *FakeTx) Commit(ctx context.Context) error {
	if tx.closed() {
		return pgx.ErrTxClosed
	}
	if tx.commitErr != nil {
		tx.RolledBack = true
		return tx.commitErr
	}
	tx.Committed = true
	return nil
}

// Rollback after Commit returns pgx.ErrTxClosed and changes nothing, so it is safe to defer
func (tx *FakeTx) Rollback(ctx context.Context) error {
	if tx.closed() {
		return pgx.ErrTxClosed
	}
	tx.RolledBack = true
	return nil
}

// fakeRow is what QueryRow returns
type fakeRow struct {
	values []any
	err    error
}

func (r *fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if r.values == nil {
		return pgx.ErrNoRows
	}
	return scanInto(dest, r.values)
}

// FakeRows is what Query returns. Data holds one []any per row, in column order.
// IterErr is returned by Err, which callers check after Next returns false.
type FakeRows struct {
	Cols    []string
	Data    [][]any
	IterErr error

	idx     int
	current []any
	closed  bool
}

func (rows *FakeRows) FieldDescriptions() []pgconn.FieldDescription {
	fds := make([]pgconn.FieldDescription, len(rows.Cols))
	for i, c := range rows.Cols {
		fds[i] = pgconn.FieldDescription{Name: c}
	}
	return fds
}

func (rows *FakeRows) Close() {
	rows.closed = true
	rows.current = nil
}

func (rows *FakeRows) Err() error { return rows.IterErr }

func (rows *FakeRows) CommandTag() pgconn.CommandTag {
	return pgconn.NewCommandTag(fmt.Sprintf("SELECT %d", len(rows.Data)))
}

// Next closes the rows once they are used up, like pgx does
func (rows *FakeRows) Next() bool {
	if rows.closed || rows.idx >= len(rows.Data) {
		rows.Close()
		return false
	}
	rows.current = rows.Data[rows.idx]
	rows.idx++
	return true
}

func (rows *FakeRows) Scan(dest ...any) error {
	if rows.current == nil {
		return fmt.Errorf("fakeRows: Scan called without a current row")
	}
	// A lone RowScanner reads the row itself, which is how pgx.RowToMap and friends work
	if len(dest) == 1 {
		if rs, ok := dest[0].(pgx.RowScanner); ok {
			return rs.ScanRow(rows)
		}
	}
	return scanInto(dest, rows.current)
}

func (rows *FakeRows) Values() ([]any, error) {
	if rows.current == nil {
		return nil, fmt.Errorf("fakeRows: Values called without a current row")
	}
	return append([]any(nil), rows.current...), nil
}

// RawValues has no wire encoding to give back, so it is always nil
func (rows *FakeRows) RawValues() [][]byte { return nil }
func (rows *FakeRows) Conn() *pgx.Conn     { return nil }

// scanInto copies each value into the matching destination. A nil destination skips that column, as in pgx.
func scanInto(dest []any, values []any) error {
	if len(dest) != len(values) {
		return fmt.Errorf("fake scan: expected %d dest args, got %d", len(values), len(dest))
	}
	for i := range values {
		if dest[i] == nil {
			continue
		}
		if err := assign(dest[i], values[i]); err != nil {
			return fmt.Errorf("fake scan: column %d: %w", i, err)
		}
	}
	return nil
}

// assign sets *dest to src, converting when the types differ but are convertible.
// A nil src (SQL NULL) zeroes nillable destinations and errors for the rest, like pgx.
func assign(dest, src any) error {
	dv := reflect.ValueOf(dest)
	if dv.Kind() != reflect.Pointer || dv.IsNil() {
		return fmt.Errorf("destination must be a non-nil pointer, got %T", dest)
	}
	elem := dv.Elem()

	sv := reflect.ValueOf(src)
	if !sv.IsValid() {
		switch elem.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Map, reflect.Interface:
			elem.SetZero()
			return nil
		}
		return fmt.Errorf("cannot scan NULL into %T", dest)
	}

	// Go converts integers to strings as runes (5 -> "\x05"), which is never what a scan means
	intToString := elem.Kind() == reflect.String && (sv.CanInt() || sv.CanUint())

	switch {
	case sv.Type().AssignableTo(elem.Type()):
	case sv.Type().ConvertibleTo(elem.Type()) && !intToString:
		sv = sv.Convert(elem.Type())
	default:
		return fmt.Errorf("cannot assign %T into %T", src, dest)
	}
	elem.Set(sv)
	return nil
}
