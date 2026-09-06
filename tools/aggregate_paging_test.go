package tools

import (
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"lotusforge.au/api-server/models"
)

// aggregateTestModel is a minimal model for exercising the aggregate/grouped
// query path: two group-able columns and one numeric column to aggregate.
func aggregateTestModel() *models.DataModel {
	return &models.DataModel{
		Name:        ptr("assets"),
		Version:     ptr("1.0.0"),
		Table_name:  ptr("assets"),
		End_point:   ptr("assets"),
		Primary_key: ptr("id"),
		Fields: map[string]models.DataModelField{
			"id":               {Type: ptr("int"), JSON: ptr("id"), DB_type: ptr("serial")},
			"building":         {Type: ptr("string"), JSON: ptr("building"), DB_type: ptr("text")},
			"floor":            {Type: ptr("string"), JSON: ptr("floor"), DB_type: ptr("text")},
			"room":             {Type: ptr("string"), JSON: ptr("room"), DB_type: ptr("text")},
			"condition_rating": {Type: ptr("float"), JSON: ptr("condition_rating"), DB_type: ptr("double precision")},
		},
	}
}

// aggregateReq routes a request at GET /assets/fn/aggregate with the {function}
// path value set (matching the router), runs it through the query builder, and
// returns the builder plus the rendered SELECT and count queries.
func aggregateReq(query string) (qb *QueryBuilder, q string, count string) {
	return aggregateReqCfg(aggregateTestModel(), query)
}

// aggregateReqCfg is aggregateReq against an explicit model. It mirrors
// getResource: SetDefaults (soft-delete WHERE, etc.) runs before URL parsing.
func aggregateReqCfg(cfg *models.DataModel, query string) (qb *QueryBuilder, q string, count string) {
	table := *cfg.Table_name
	req := httptest.NewRequest("GET", "/"+*cfg.End_point+"/fn/aggregate"+query, nil)
	req.SetPathValue("function", "aggregate")
	qb = NewQueryBuilder(GetBasicLogger())
	if err := qb.SetDefaults(cfg, req); err != nil {
		panic(err)
	}
	if err := qb.ProcessURLParams(req, cfg); err != nil {
		panic(err)
	}
	if qb.HasFields() {
		q = qb.BuildSelect(table, qb.GetFields())
	} else {
		q = qb.BuildSelect(table, []string{"*"})
	}
	count = qb.BuildCountWithWhere(table)
	return qb, q, count
}

// softDeleteAggregateModel mirrors the shape that triggered the production bug:
// a soft-delete model (SetDefaults binds `deleted_flag = $1`) with a plain
// column and its description column, both distinct-able.
func softDeleteAggregateModel() *models.DataModel {
	return &models.DataModel{
		Name:        ptr("Building"),
		Version:     ptr("1.0.0"),
		Table_name:  ptr("buildings"),
		End_point:   ptr("building"),
		Primary_key: ptr("building_id"),
		Soft_delete: ptr(true),
		Fields: map[string]models.DataModelField{
			"building_id":          {Type: ptr("int"), JSON: ptr("building_id"), DB_type: ptr("smallserial")},
			"building":             {Type: ptr("string"), JSON: ptr("building"), DB_type: ptr("character varying(15)")},
			"building_description": {Type: ptr("string"), JSON: ptr("building_description"), DB_type: ptr("character varying(200)")},
		},
	}
}

// placeholderCount counts consecutive $1..$N tokens in a SQL string.
func placeholderCount(sql string) int {
	n := 0
	for i := 1; strings.Contains(sql, "$"+strconv.Itoa(i)); i++ {
		n++
	}
	return n
}

// TestAggregateURLPath_PaginatesWithDefault locks in that the built-in
// /fn/aggregate route now honours page/page_size — previously it ignored them
// entirely and returned every group row.
func TestAggregateURLPath_PaginatesWithDefault(t *testing.T) {
	qb, q, _ := aggregateReq("?group_by=building&aggregate=count")

	if !strings.Contains(q, "GROUP BY building") {
		t.Fatalf("expected GROUP BY building, got: %s", q)
	}
	if !strings.Contains(q, "LIMIT 25") {
		t.Errorf("expected default LIMIT 25 on the aggregate query, got: %s", q)
	}
	if qb.GetPageSize() != 25 {
		t.Errorf("GetPageSize should report the row limit (25), got %d", qb.GetPageSize())
	}
	if qb.GetPage() != 1 {
		t.Errorf("GetPage should be 1, got %d", qb.GetPage())
	}
}

// TestAggregateURLPath_DeterministicOrdering ensures LIMIT/OFFSET paging over
// groups is stable: every group-by column must appear in ORDER BY even when the
// caller supplies no sort_by.
func TestAggregateURLPath_DeterministicOrdering(t *testing.T) {
	_, q, _ := aggregateReq("?group_by=building,floor&aggregate=count")

	idx := strings.Index(q, "ORDER BY ")
	if idx == -1 {
		t.Fatalf("expected an ORDER BY on the grouped query, got: %s", q)
	}
	orderBy := q[idx:]
	if !strings.Contains(orderBy, "building ASC") || !strings.Contains(orderBy, "floor ASC") {
		t.Errorf("every group-by column must be in ORDER BY, got: %s", orderBy)
	}
}

// TestAggregateURLPath_CallerSortKeptAsPrimary checks the caller's sort_by stays
// primary and the group columns are appended only as tiebreakers.
func TestAggregateURLPath_CallerSortKeptAsPrimary(t *testing.T) {
	_, q, _ := aggregateReq("?group_by=building&aggregate=count&sort_by=count~desc")

	idx := strings.Index(q, "ORDER BY ")
	if idx == -1 {
		t.Fatalf("expected ORDER BY, got: %s", q)
	}
	orderBy := q[idx:]
	if !strings.HasPrefix(orderBy, "ORDER BY count DESC") {
		t.Errorf("caller sort must stay primary, got: %s", orderBy)
	}
	if !strings.Contains(orderBy, "building ASC") {
		t.Errorf("group column should be appended as a tiebreaker, got: %s", orderBy)
	}
}

// TestAggregateURLPath_PageAllDisablesLimit confirms page=all still opts out.
func TestAggregateURLPath_PageAllDisablesLimit(t *testing.T) {
	qb, q, _ := aggregateReq("?group_by=building&aggregate=count&page=all")

	if strings.Contains(q, "LIMIT") {
		t.Errorf("page=all must disable the row limit, got: %s", q)
	}
	if qb.GetPageSize() != 0 {
		t.Errorf("GetPageSize should be 0 (unpaginated), got %d", qb.GetPageSize())
	}
}

// TestAggregateURLPath_ExplicitPage checks page/page_size math.
func TestAggregateURLPath_ExplicitPage(t *testing.T) {
	qb, q, _ := aggregateReq("?group_by=building&aggregate=count&page=3&page_size=10")

	if !strings.Contains(q, "LIMIT 10") {
		t.Errorf("expected LIMIT 10, got: %s", q)
	}
	if !strings.Contains(q, "OFFSET 20") {
		t.Errorf("expected OFFSET 20 for page 3 of size 10, got: %s", q)
	}
	if qb.GetPage() != 3 || qb.GetPageSize() != 10 {
		t.Errorf("page/size metadata wrong: page=%d size=%d", qb.GetPage(), qb.GetPageSize())
	}
}

// TestAggregateCount_GroupAware verifies total_count counts group rows, not base
// rows, when the query groups.
func TestAggregateCount_GroupAware(t *testing.T) {
	_, _, count := aggregateReq("?group_by=building,floor&aggregate=count")

	want := "SELECT COUNT(*) FROM (SELECT building, floor FROM assets GROUP BY building, floor) AS sub;"
	if count != want {
		t.Errorf("group-aware count mismatch:\n  got:  %s\n  want: %s", count, want)
	}
}

// TestAggregateCount_NoGroupByIsOne verifies a bare scalar aggregate with no
// GROUP BY counts its single collapsed row while still carrying the table/WHERE
// so any bound args (soft-delete, filters) match the placeholders.
func TestAggregateCount_NoGroupByIsOne(t *testing.T) {
	_, _, count := aggregateReq("?aggregate=count,avg:condition_rating")

	want := "SELECT COUNT(*) FROM (SELECT 1 FROM assets LIMIT 1) AS sub;"
	if count != want {
		t.Errorf("bare aggregate count mismatch:\n  got:  %s\n  want: %s", count, want)
	}
}

// TestAggregateDistinct_Single covers `?aggregate=distinct:col` with no
// group_by: it selects DISTINCT on many rows, so it needs a deterministic
// ORDER BY for paging and a distinct-row count (not the collapsed "SELECT 1").
func TestAggregateDistinct_Single(t *testing.T) {
	qb, q, count := aggregateReq("?aggregate=distinct:building&page=2&page_size=20")

	if !strings.Contains(q, "distinct(building)") {
		t.Fatalf("expected distinct(building) in SELECT, got: %s", q)
	}
	if !strings.Contains(q, "ORDER BY building ASC") {
		t.Errorf("distinct projection must get a deterministic ORDER BY, got: %s", q)
	}
	if !strings.Contains(q, "LIMIT 20") || !strings.Contains(q, "OFFSET 20") {
		t.Errorf("page/page_size must both apply, got: %s", q)
	}
	want := "SELECT COUNT(*) FROM (SELECT DISTINCT building FROM assets) AS sub;"
	if count != want {
		t.Errorf("distinct count mismatch:\n  got:  %s\n  want: %s", count, want)
	}
	if qb.GetPage() != 2 {
		t.Errorf("GetPage should be 2, got %d", qb.GetPage())
	}
}

// TestAggregateDistinct_Multi covers `?aggregate=distinct:a~b~c`, which renders
// a row constructor in the SELECT list; ORDER BY / count must use the same
// row form.
func TestAggregateDistinct_Multi(t *testing.T) {
	_, q, count := aggregateReq("?aggregate=distinct:building~floor~room&page=2&page_size=20")

	if !strings.Contains(q, "distinct(building,floor,room)") {
		t.Fatalf("expected distinct(building,floor,room) in SELECT, got: %s", q)
	}
	if !strings.Contains(q, "ORDER BY (building, floor, room) ASC") {
		t.Errorf("multi-field distinct must order by the row form, got: %s", q)
	}
	if !strings.Contains(q, "LIMIT 20") || !strings.Contains(q, "OFFSET 20") {
		t.Errorf("page/page_size must both apply, got: %s", q)
	}
	want := "SELECT COUNT(*) FROM (SELECT DISTINCT (building, floor, room) FROM assets) AS sub;"
	if count != want {
		t.Errorf("multi distinct count mismatch:\n  got:  %s\n  want: %s", count, want)
	}
}

// TestAggregateDistinct_GroupByWins ensures a GROUP BY alongside a distinct
// token falls back to the grouped ordering/count path.
func TestAggregateDistinct_GroupByWins(t *testing.T) {
	_, q, count := aggregateReq("?group_by=building&aggregate=distinct:floor")

	if !strings.Contains(q, "GROUP BY building") || !strings.Contains(q, "ORDER BY building ASC") {
		t.Errorf("group_by should drive ordering, got: %s", q)
	}
	if !strings.HasPrefix(count, "SELECT COUNT(*) FROM (SELECT building FROM assets GROUP BY building)") {
		t.Errorf("group_by should drive the count, got: %s", count)
	}
}

// TestAggregate_SoftDelete_CountKeepsWhereArgs is the regression for the
// production error "expected 0 arguments, got 1": on a soft-delete model
// SetDefaults binds `deleted_flag = $1`, and every count-query branch must carry
// that WHERE placeholder so it matches qb.GetArgs().
func TestAggregate_SoftDelete_CountKeepsWhereArgs(t *testing.T) {
	cases := []string{
		"?aggregate=distinct:building~building_description",
		"?aggregate=distinct:building",
		"?aggregate=count",
		"?aggregate=count,avg:building_id",
		"?group_by=building&aggregate=count",
	}
	for _, q := range cases {
		t.Run(q, func(t *testing.T) {
			qb, _, count := aggregateReqCfg(softDeleteAggregateModel(), q)

			nArgs := len(qb.GetArgs())
			if nArgs != 1 {
				t.Fatalf("expected soft-delete to bind exactly 1 arg, got %d", nArgs)
			}
			if got := placeholderCount(count); got != nArgs {
				t.Errorf("count query has %d placeholders but %d args would be passed\n  count: %s",
					got, nArgs, count)
			}
			if !strings.Contains(count, "WHERE") {
				t.Errorf("count query dropped the WHERE clause: %s", count)
			}
			if strings.TrimSpace(count) == "SELECT 1;" {
				t.Errorf("count query is a bare SELECT 1 (args mismatch): %s", count)
			}
		})
	}
}

// TestAggregate_DistinctDescription_FullShape pins the exact queries for the
// request the user hit.
func TestAggregate_DistinctDescription_FullShape(t *testing.T) {
	qb, q, count := aggregateReqCfg(softDeleteAggregateModel(),
		"?aggregate=distinct:building~building_description&page=2&page_size=20")

	wantData := "SELECT distinct(building,building_description) FROM buildings WHERE deleted_flag = $1 ORDER BY (building, building_description) ASC LIMIT 20 OFFSET 20;"
	if q != wantData {
		t.Errorf("data query:\n  got:  %s\n  want: %s", q, wantData)
	}
	wantCount := "SELECT COUNT(*) FROM (SELECT DISTINCT (building, building_description) FROM buildings WHERE deleted_flag = $1) AS sub;"
	if count != wantCount {
		t.Errorf("count query:\n  got:  %s\n  want: %s", count, wantCount)
	}
	if qb.GetPage() != 2 || qb.GetPageSize() != 20 {
		t.Errorf("page/size metadata: page=%d size=%d", qb.GetPage(), qb.GetPageSize())
	}
}

// TestPlainQueryCount_Unchanged makes sure the non-aggregating count path is
// untouched by the group-aware branch.
func TestPlainQueryCount_Unchanged(t *testing.T) {
	cfg := aggregateTestModel()
	req := httptest.NewRequest("GET", "/assets?building=A", nil)
	req.SetPathValue("function", "")
	qb := NewQueryBuilder(GetBasicLogger())
	if err := qb.ProcessURLParams(req, cfg); err != nil {
		t.Fatal(err)
	}
	count := qb.BuildCountWithWhere("assets")
	if !strings.HasPrefix(count, "SELECT COUNT(*) FROM assets WHERE ") {
		t.Errorf("plain count query changed shape: %s", count)
	}
}

// TestApplyPaginationUnbounded covers the declarative-function pagination mode:
// no params means no limit, but an explicit page_size is still honoured.
func TestApplyPaginationUnbounded(t *testing.T) {
	t.Run("no params -> unbounded", func(t *testing.T) {
		qb := NewQueryBuilder(GetBasicLogger())
		qb.ApplyPaginationUnbounded(httptest.NewRequest("GET", "/assets/fn/by-building", nil))
		if q := qb.BuildSelect("assets", []string{"*"}); strings.Contains(q, "LIMIT") {
			t.Errorf("declarative function should be unbounded by default, got: %s", q)
		}
		if qb.GetPageSize() != 0 {
			t.Errorf("GetPageSize should be 0 when unbounded, got %d", qb.GetPageSize())
		}
	})

	t.Run("explicit page_size honoured", func(t *testing.T) {
		qb := NewQueryBuilder(GetBasicLogger())
		qb.ApplyPaginationUnbounded(httptest.NewRequest("GET", "/assets/fn/by-building?page=2&page_size=50", nil))
		q := qb.BuildSelect("assets", []string{"*"})
		if !strings.Contains(q, "LIMIT 50") || !strings.Contains(q, "OFFSET 50") {
			t.Errorf("explicit paging must still apply, got: %s", q)
		}
	})
}

// TestApplyPagination_DefaultUnchanged guards the standard 25-row default that
// the plain GET / diff / history endpoints rely on.
func TestApplyPagination_DefaultUnchanged(t *testing.T) {
	qb := NewQueryBuilder(GetBasicLogger())
	qb.ApplyPagination(httptest.NewRequest("GET", "/assets", nil))
	if qb.GetPageSize() != 25 {
		t.Errorf("standard pagination default should stay 25, got %d", qb.GetPageSize())
	}
}
