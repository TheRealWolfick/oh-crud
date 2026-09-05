package handlers

import (
	"context"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"lotusforge.au/api-server/models"
)

// fakeRow implements pgx.Row for a single QueryRow call.
type fakeRow struct {
	values []any
	err    error
}

func (r *fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return fmt.Errorf("fakeRow: expected %d dest args, got %d", len(r.values), len(dest))
	}
	for i, d := range dest {
		if err := scanAssign(d, r.values[i]); err != nil {
			return err
		}
	}
	return nil
}

// fakeRows implements pgx.Rows over a fixed table (column names + one row of values per
// entry), which is enough to support pgx.CollectRows(rows, pgx.RowToMap) and
// pgx.CollectOneRow(rows, pgx.RowTo[T]) — the two collection helpers the diff handlers use.
type fakeRows struct {
	cols []string
	data [][]any
	idx  int
	err  error
}

func (rows *fakeRows) FieldDescriptions() []pgconn.FieldDescription {
	fds := make([]pgconn.FieldDescription, len(rows.cols))
	for i, c := range rows.cols {
		fds[i] = pgconn.FieldDescription{Name: c}
	}
	return fds
}
func (rows *fakeRows) Close()                        {}
func (rows *fakeRows) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }
func (rows *fakeRows) Err() error                    { return rows.err }
func (rows *fakeRows) Next() bool {
	if rows.idx >= len(rows.data) {
		return false
	}
	rows.idx++
	return true
}
func (rows *fakeRows) Values() ([]any, error) { return rows.data[rows.idx-1], nil }
func (rows *fakeRows) RawValues() [][]byte    { return nil }
func (rows *fakeRows) Conn() *pgx.Conn        { return nil }

// Scan mirrors pgx's real dispatch: a single RowScanner destination (as used by
// pgx.RowToMap) gets the whole row via ScanRow; otherwise dest are assigned positionally
// (as used by pgx.RowTo[T]).
func (rows *fakeRows) Scan(dest ...any) error {
	if len(dest) == 1 {
		if rc, ok := dest[0].(pgx.RowScanner); ok {
			return rc.ScanRow(rows)
		}
	}
	values := rows.data[rows.idx-1]
	if len(dest) != len(values) {
		return fmt.Errorf("fakeRows: expected %d dest args, got %d", len(values), len(dest))
	}
	for i, d := range dest {
		if err := scanAssign(d, values[i]); err != nil {
			return err
		}
	}
	return nil
}

func scanAssign(dest, src any) error {
	switch d := dest.(type) {
	case *int:
		v, ok := src.(int)
		if !ok {
			return fmt.Errorf("cannot assign %T into *int", src)
		}
		*d = v
	case *int64:
		v, ok := src.(int64)
		if !ok {
			return fmt.Errorf("cannot assign %T into *int64", src)
		}
		*d = v
	case *string:
		v, ok := src.(string)
		if !ok {
			return fmt.Errorf("cannot assign %T into *string", src)
		}
		*d = v
	default:
		return fmt.Errorf("scanAssign: unsupported dest type %T", dest)
	}
	return nil
}

// fakeDB implements models.DBExecQuery, recording the SQL/args it was called with and
// dispatching to caller-supplied handlers so a test can return different fake rows per
// statement (e.g. the data query vs. the count query in getDiff).
type fakeDB struct {
	queryFn    func(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	queryRowFn func(ctx context.Context, sql string, args ...any) pgx.Row
	execFn     func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)

	mu          sync.Mutex
	queries     []string
	queryArgs   [][]any
}

// getDiff/dynamicActionDiff issue concurrent Query calls (via errgroup), so recording
// must be safe under -race.
func (f *fakeDB) record(sql string, args []any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, sql)
	f.queryArgs = append(f.queryArgs, args)
}

func (f *fakeDB) Queries() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.queries...)
}

// AllArgs flattens every bound argument across every recorded call. Useful for asserting
// a value never got bound anywhere, regardless of which query or placeholder position it
// would have landed at — important for the WHERE re-set bug (tools/where_reset_test.go),
// where a corrupted arg can end up at the wrong position while the SQL text's operator
// still looks correct.
func (f *fakeDB) AllArgs() []any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var all []any
	for _, a := range f.queryArgs {
		all = append(all, a...)
	}
	return all
}

func (f *fakeDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	f.record(sql, args)
	if f.queryFn != nil {
		return f.queryFn(ctx, sql, args...)
	}
	return &fakeRows{}, nil
}

func (f *fakeDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	f.record(sql, args)
	if f.queryRowFn != nil {
		return f.queryRowFn(ctx, sql, args...)
	}
	return &fakeRow{}
}

func (f *fakeDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	f.record(sql, args)
	if f.execFn != nil {
		return f.execFn(ctx, sql, args...)
	}
	return pgconn.CommandTag{}, nil
}

var _ models.DBExecQuery = (*fakeDB)(nil)
