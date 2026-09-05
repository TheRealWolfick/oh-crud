package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"lotusforge.au/api-server/middleware"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/tools"
)

func ptr[T any](v T) *T { return &v }

// diffResourceCfg is a stand-in for a base model like asset-data.yaml: it owns the
// /diff endpoint and carries the DIFF role list and the resource table name that
// getDiff must filter the diffs table by.
func diffResourceCfg() *models.DataModel {
	return &models.DataModel{
		Name:       ptr("asset-data"),
		Version:    ptr("1.0.0"),
		Table_name: ptr("assets"),
		End_point:  ptr("assets"),
		Allow_diff: ptr(true),
		End_points_allowed: &models.End_pointsAllowed{
			DIFF: []string{"editor"},
			PUT:  []string{"editor"},
			POST: []string{"editor"},
		},
	}
}

// diffModelCfg mirrors config/default/diffs.yaml: no end-points-allowed block (the
// permission check must never be evaluated against this model), plus one abstracted
// jsonb field so tests can assert the select/where abstraction is wired through.
func diffModelCfg() *models.DataModel {
	return &models.DataModel{
		Name:       ptr("diffs"),
		Version:    ptr("1.0.0"),
		Table_name: ptr("diffs"),
		End_point:  ptr(""),
		Fields: map[string]models.DataModelField{
			"diff_id":   {Type: ptr("int"), JSON: ptr("diff_id"), DB_type: ptr("serial")},
			"diff_type": {Type: ptr("string"), JSON: ptr("diff_type"), DB_type: ptr("character varying(20)")},
			"checksum":  {Type: ptr("string"), JSON: ptr("checksum"), DB_type: ptr("character varying(64)")},
			"missing_from_supplied": {
				Type: ptr("json"), JSON: ptr("missing_from_supplied"), DB_type: ptr("jsonb"),
				JSON_select: &models.DataModelJsonSelect{Action: ptr("count"), ApplyOn: ptr("list"), TreatAs: ptr("int")},
			},
		},
	}
}

func newTestQueueManager(db *fakeDB) *tools.QueueManager {
	return tools.NewQueue(db, 1, tools.GetBasicLogger(), nil)
}

// requestWithRoles builds a GET request carrying the user/roles context the diff
// handlers read via middleware.GetUser / CheckUserHasAllowedRole.
func requestWithRoles(target string, roles []string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	ctx := middleware.SetUser(req.Context(), &models.User{UserInfoResponse: models.UserInfoResponse{Username: "tester"}})
	ctx = middleware.SetRoles(ctx, roles)
	return req.WithContext(ctx)
}

func TestGetDiff_PermissionDeniedDoesNotPanic(t *testing.T) {
	db := &fakeDB{}
	qm := newTestQueueManager(db)
	cfg := diffResourceCfg()
	diffCfg := diffModelCfg()

	handler := getDiff(qm, cfg, diffCfg, &models.ServerConfig{})
	req := requestWithRoles("/assets/diff", []string{"viewer"}) // lacks "editor"
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a role without DIFF access, got %d (body: %s)", rr.Code, rr.Body.String())
	}
}

func TestGetDiff_QueriesDiffsTableFilteredByResourceTable(t *testing.T) {
	db := &fakeDB{
		queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			// One row/column is enough to satisfy both the RowToMap (data) and
			// RowTo[int] (count) collection paths exercised by getDiff.
			return &fakeRows{cols: []string{"count"}, data: [][]any{{1}}}, nil
		},
	}
	qm := newTestQueueManager(db)
	cfg := diffResourceCfg()
	diffCfg := diffModelCfg()

	handler := getDiff(qm, cfg, diffCfg, &models.ServerConfig{})
	req := requestWithRoles("/assets/diff", []string{"editor"})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}

	queries := db.Queries()
	if len(queries) != 2 {
		t.Fatalf("expected 2 queries (data + count), got %d: %v", len(queries), queries)
	}
	for _, q := range queries {
		if !strings.Contains(q, "FROM diffs") {
			t.Errorf("expected query to read FROM diffs, got: %s", q)
		}
		if !strings.Contains(q, "diff_type = $1") {
			t.Errorf("expected the resource-table filter on diff_type, got: %s", q)
		}
		if strings.Contains(q, "table_name") {
			t.Errorf("query must not reference the nonexistent table_name column, got: %s", q)
		}
		if strings.Contains(q, "FROM assets") {
			t.Errorf("query must select from the diffs table, not the resource table, got: %s", q)
		}
	}
}

// TestGetDiff_CallerSuppliedDiffTypeCannotOverrideScoping is a regression test for the
// QueryBuilder re-set bug (see tools/where_reset_test.go): getDiff sets the resource-table
// scope via SetWhereAbsolute("diff_type", ...) *after* URL params are processed, so if a
// caller also passes ?diff_type=..., that value must be discarded in favor of the server's
// own scoping value — never bound as-is.
//
// checksum is supplied alongside diff_type so at least one other field is bound before
// getDiff's own SetWhereAbsolute("diff_type", ...) call runs — the exact shape that
// exposed the bug: the corrupted write landed at args[0] regardless of which field
// actually owned that slot. The assertions check bound argument *values* rather than the
// SQL text, because the buggy re-set still corrected diff_type's operator to "=" in the
// query string even while binding the wrong value to it — a text-only assertion would not
// have caught it.
//
// processWhereFromConfig walks cfg.Fields, a Go map, so which of diff_type/checksum binds
// first is randomized per call — against the buggy code this test only failed on the
// unlucky ordering (observed ~1 in 20 single attempts). Running many attempts, each with a
// fresh model/QueryBuilder so the map is freshly (re-)randomized, makes a regression fail
// reliably instead of depending on map iteration luck; the deterministic, order-independent
// version of this same bug is tools.TestSetWhereAbsolute_ReSetUpdatesOwnArgSlot, which is
// the primary regression guard — this is the integration-level companion.
func TestGetDiff_CallerSuppliedDiffTypeCannotOverrideScoping(t *testing.T) {
	const attempts = 64
	for i := 0; i < attempts; i++ {
		db := &fakeDB{
			queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
				return &fakeRows{cols: []string{"count"}, data: [][]any{{1}}}, nil
			},
		}
		qm := newTestQueueManager(db)
		cfg := diffResourceCfg() // Table_name: "assets"
		diffCfg := diffModelCfg()

		handler := getDiff(qm, cfg, diffCfg, &models.ServerConfig{})
		req := requestWithRoles("/assets/diff?diff_type=some-other-table&checksum=realchecksum", []string{"editor"})
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("attempt %d: expected 200, got %d (body: %s)", i, rr.Code, rr.Body.String())
		}

		args := db.AllArgs()
		for _, a := range args {
			if a == "some-other-table" {
				t.Fatalf("attempt %d: caller-supplied diff_type value leaked into a bound query argument: %v", i, args)
			}
		}
		var foundResourceScope, foundChecksum bool
		for _, a := range args {
			if a == "assets" {
				foundResourceScope = true
			}
			if a == "realchecksum" {
				foundChecksum = true
			}
		}
		if !foundResourceScope {
			t.Fatalf(`attempt %d: expected the resource table "assets" to be bound for diff_type scoping, got args: %v`, i, args)
		}
		if !foundChecksum {
			t.Fatalf("attempt %d: expected the caller's own checksum filter to survive uncorrupted, got args: %v", i, args)
		}
	}
}
