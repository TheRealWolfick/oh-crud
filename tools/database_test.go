package tools

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// An exec supplies sql, variables, and returns a command tag and error
// A query supplies sql, variables, and returns rows and error
type fake_execquery struct {
	gotSql     string
	gotArgs    []any
	rows       fakeRows
	cmdtag     pgconn.CommandTag
	err        error
}

func (f *fake_execquery) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	f.gotSql = sql
	f.gotArgs = args
	return pgconn.CommandTag{}, fmt.Errorf("testing")
}

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

	for i, d := range r.values{
		if err := assign(d, r.values[i]); err != nil {
			return err
		}
	}

	return nil
}

func assign(dest, src any) error {
	dv := reflect.ValueOf(dest)
	if dv.Kind() != reflect.Ptr || dv.IsNil() {
		return fmt.Errorf("Destination must be a pointer, got %T", dest)
	}
	sv := reflect.ValueOf(src)
	if !sv.IsValid() {
		return nil
	}

	elem := dv.Elem()
	if sv.Type().AssignableTo(elem.Type()) {
		if !sv.Type().ConvertibleTo(elem.Type()) {
			sv = sv.Convert(elem.Type())
		} else {
			return fmt.Errorf("Cannot assign %T into %T", src, dest)
		}
	}
	elem.Set(sv)

	return nil
}

type fakeRows struct {
	rows        [][]byte
	sql         string
	args        []any
	current     []any
	idx         int
	rowCount    int
}

func (rows *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (rows *fakeRows) Close() {}
func (rows *fakeRows) CommandTag() pgconn.CommandTag {return pgconn.NewCommandTag("fake cmdtag")}
func (rows *fakeRows) Err() error { return nil }
func (rows *fakeRows) Next() bool { return rows.idx < rows.rowCount }
func (rows *fakeRows) Values() ([]any, error) { return rows.current, nil }
func (rows *fakeRows) RawValues() [][]byte { return rows.rows }
func (rows *fakeRows) Conn() *pgx.Conn { return nil }
func (rows *fakeRows) Scan(dest ...any) error {
	return nil
}

func (f *fake_execquery) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	f.gotSql = sql
	f.gotArgs = args
	return &fakeRow{values: f.rows.current, err: f.err}
}

func (f *fake_execquery)	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	f.gotSql = sql
	f.gotArgs = args
	return &f.rows, f.err
}

// Testing scripts

func TestUpdate(t *testing.T) {
	// Setup
	fake_db := &fake_execquery{}
	model := basicModel()
	values_a := map[string]any{
		"code": "foo", "name": "bar", "count": 5,
	}
	values_b := map[string]any{
		"code": "foobar", "name": "barst", "count": 3,
	}
	values_c := map[string]any{
		"code": "hill", "count": 223,
	}
	values_wrong := map[string]any{
		"count": 0,
	}
	values_all := []map[string]any{values_a, values_b, values_c}
	values_all_with_wrong := []map[string]any{values_a, values_b, values_wrong}

	// Test a single insert
	t.Run("Update a single value", func(t *testing.T) {
		expected_sql := "UPDATE test_table SET name = $2, count = $3 WHERE code = $1;"
		expected_args := "[foo bar 5]"
		multiUpdate(context.Background(), fake_db, &model, []map[string]any{values_a})
		if (!strings.Contains(fake_db.gotSql, "name") || !strings.Contains(fake_db.gotSql, "count") || !strings.Contains(fake_db.gotSql, "WHERE code")) {
			t.Errorf("Expected Sql similar to: %s\nReceived Sql: %s", expected_sql, fake_db.gotSql)
		}
		if (len(fake_db.gotArgs) != 3) {
			t.Errorf("Expected args: %s\nReceived args: %s", expected_args, fmt.Sprint(fake_db.gotArgs))
		}
	})

	t.Run("Update multiple values", func(t *testing.T) {
		expected_sql := "UPDATE test_table SET name = $2, count = $3 WHERE code = $1;"
		expected_args := "[foo bar 5]"
		multiUpdate(context.Background(), fake_db, &model, values_all)
		// The values in the fake db should now be the last item, which has only two items
		if (!strings.Contains(fake_db.gotSql, "count") || !strings.Contains(fake_db.gotSql, "WHERE code")) {
			t.Errorf("Expected Sql: %s\nReceived Sql: %s", expected_sql, fake_db.gotSql)
		}
		if (len(fake_db.gotArgs) != 2) {
			t.Errorf("Expected args: %s\nReceived args: %s - len: %v", expected_args, fmt.Sprint(fake_db.gotArgs), len(fake_db.gotArgs))
		}
	})

	t.Run("Update multiple values - final value invalid", func(t *testing.T) {
		expected_sql := "UPDATE test_table SET name = $2, count = $3 WHERE code = $1;"
		expected_args := "[foo bar 5]"
		multiUpdate(context.Background(), fake_db, &model, values_all_with_wrong)
		// The values in the fake db should now be the second last item, which has three items
		if (!strings.Contains(fake_db.gotSql, "name") || !strings.Contains(fake_db.gotSql, "count") || !strings.Contains(fake_db.gotSql, "WHERE code")) {
			t.Errorf("Expected Sql: %s\nReceived Sql: %s", expected_sql, fake_db.gotSql)
		}
		if (len(fake_db.gotArgs) != 3) {
			t.Errorf("Expected args: %s\nReceived args: %s - len: %v", expected_args, fmt.Sprint(fake_db.gotArgs), len(fake_db.gotArgs))
		}
	})
}
