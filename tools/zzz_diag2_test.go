package tools

import (
	"context"
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"lotusforge.au/api-server/middleware"
	"lotusforge.au/api-server/models"
)

type diag2Rows struct {
	cols []string
	data [][]any
	idx  int
}

func (r *diag2Rows) FieldDescriptions() []pgconn.FieldDescription {
	fds := make([]pgconn.FieldDescription, len(r.cols))
	for i, c := range r.cols {
		fds[i] = pgconn.FieldDescription{Name: c}
	}
	return fds
}
func (r *diag2Rows) Close()                        {}
func (r *diag2Rows) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }
func (r *diag2Rows) Err() error                    { return nil }
func (r *diag2Rows) Next() bool {
	if r.idx >= len(r.data) {
		return false
	}
	r.idx++
	return true
}
func (r *diag2Rows) Values() ([]any, error) { return r.data[r.idx-1], nil }
func (r *diag2Rows) RawValues() [][]byte    { return nil }
func (r *diag2Rows) Conn() *pgx.Conn        { return nil }
func (r *diag2Rows) Scan(dest ...any) error {
	if len(dest) == 1 {
		if rc, ok := dest[0].(pgx.RowScanner); ok {
			return rc.ScanRow(r)
		}
	}
	return nil
}

type diag2DB struct {
	execCalls []struct {
		sql  string
		args []any
	}
	queryCalls []string
}

func (d *diag2DB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	d.queryCalls = append(d.queryCalls, sql)
	// Existing row: building unchanged (same as the where value), building_suburb old value differs.
	building := args[0]
	return &diag2Rows{
		cols: []string{"building", "building_suburb"},
		data: [][]any{{building, "OldSuburb"}},
	}, nil
}
func (d *diag2DB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row { return nil }
func (d *diag2DB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	d.execCalls = append(d.execCalls, struct {
		sql  string
		args []any
	}{sql, args})
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

var _ models.DBExecQuery = (*diag2DB)(nil)

func TestDiag2BuildingRecordField(t *testing.T) {
	tru := true
	cfg := &models.DataModel{
		Name:                ptr("Building"),
		Table_name:          ptr("buildings"),
		Primary_key:         ptr("building_id"),
		Track_history:       &tru,
		Track_history_field: ptr("building"),
		Unique_keys: map[string]models.UniqueKey{
			"uk_buildings_building":        {Fields: []string{"building"}},
			"uk_buildings_building_domain": {Fields: []string{"building", "db_domain"}},
		},
		Fields: map[string]models.DataModelField{
			"building_id":     {Type: ptr("int"), JSON: ptr("building_id"), DB_type: ptr("smallserial")},
			"building":        {Type: ptr("string"), JSON: ptr("building"), DB_type: ptr("character varying(15)")},
			"db_domain":       {Type: ptr("string"), JSON: ptr("db_domain"), DB_type: ptr("character varying(3)")},
			"building_suburb": {Type: ptr("string"), JSON: ptr("building_suburb"), DB_type: ptr("character varying(40)")},
		},
	}

	db := &diag2DB{}
	ctx := middleware.SetLogger(context.Background(), slog.Default())
	ctx = middleware.SetUser(ctx, &models.User{})

	// Exactly the user's reported group-update payload shape: identify by "building",
	// update "building_suburb", building value unchanged (== current DB value).
	supplied := []map[string]any{
		{"building": "314ASOMETHING", "building_suburb": "Foo"},
		{"building": "314B/SOMETHING", "building_suburb": "Bar"},
	}

	_, log_data, err := multiUpdate(ctx, db, cfg, supplied)
	t.Logf("log_data: %+v err: %v", log_data, err)

	for i, c := range db.execCalls {
		t.Logf("exec[%d] sql=%s args=%v", i, c.sql, c.args)
	}
}
