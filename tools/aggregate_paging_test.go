package tools

import (
	"net/http/httptest"
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
			"condition_rating": {Type: ptr("float"), JSON: ptr("condition_rating"), DB_type: ptr("double precision")},
		},
	}
}

// aggregateReq routes a request at GET /assets/fn/aggregate with the {function}
// path value set (matching the router), runs it through the query builder, and
// returns the builder plus the rendered SELECT and count queries.
func aggregateReq(query string) (qb *QueryBuilder, q string, count string) {
	cfg := aggregateTestModel()
	req := httptest.NewRequest("GET", "/assets/fn/aggregate"+query, nil)
	req.SetPathValue("function", "aggregate")
	qb = NewQueryBuilder(GetBasicLogger())
	if err := qb.ProcessURLParams(req, cfg); err != nil {
		panic(err)
	}
	q = qb.BuildSelect("assets", qb.GetFields())
	count = qb.BuildCountWithWhere("assets")
	return qb, q, count
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

// TestAggregateCount_NoGroupByIsOne verifies an aggregate with no GROUP BY (a
// single collapsed row) reports a count of 1.
func TestAggregateCount_NoGroupByIsOne(t *testing.T) {
	_, _, count := aggregateReq("?aggregate=count,avg:condition_rating")

	if strings.TrimSpace(count) != "SELECT 1;" {
		t.Errorf("aggregate without GROUP BY should count as 1 row, got: %s", count)
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
