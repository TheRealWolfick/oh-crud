package tools

import (
	"net/http/httptest"
	"strings"
	"testing"

	"lotusforge.au/api-server/models"
)

// nullFilterTestModel gives every relevant field type a home, plus one
// absolute-match field, so the #NULL / #NOTNULL sigil can be exercised across
// both the standard (setWhere) and absolute-match (SetWhereAbsolute) paths.
func nullFilterTestModel() *models.DataModel {
	return &models.DataModel{
		Name:        ptr("widgets"),
		Version:     ptr("1.0.0"),
		Table_name:  ptr("widgets"),
		End_point:   ptr("widgets"),
		Primary_key: ptr("id"),
		Fields: map[string]models.DataModelField{
			"id": {
				Type: ptr("int"), JSON: ptr("id"), DB_type: ptr("serial"),
			},
			"description": {
				Type: ptr("string"), JSON: ptr("description"), DB_type: ptr("text"),
			},
			"retired_at": {
				Type: ptr("time"), JSON: ptr("retired_at"), DB_type: ptr("timestamp without time zone"),
			},
			"owner_ref": {
				// absolute-match: real data equal to the literal string "NULL" must still
				// be an exact "=" match; only the #NULL / #NOTNULL sigil forces IS.
				Type: ptr("string"), JSON: ptr("owner_ref"), DB_type: ptr("text"),
				Absolute_match: ptr(true),
			},
		},
	}
}

// TestNullSigilValue locks in the exact sigil spelling: "#NULL" / "#NOTNULL" only.
func TestNullSigilValue(t *testing.T) {
	cases := []struct {
		raw    string
		want   string
		wantOK bool
	}{
		{"#NULL", "NULL", true},
		{"#NOTNULL", "NOT NULL", true},
		{"#null", "", false},     // case-sensitive
		{"#NOT NULL", "", false}, // no space in the sigil
		{"NULL", "", false},      // bare literal is not a sigil
		{"#", "", false},
		{"#NULLX", "", false},
	}
	for _, c := range cases {
		got, ok := nullSigilValue(c.raw)
		if ok != c.wantOK || got != c.want {
			t.Errorf("nullSigilValue(%q) = (%q, %v), want (%q, %v)", c.raw, got, ok, c.want, c.wantOK)
		}
	}
}

// TestSetWhere_NullSigil_AcrossFieldTypes verifies #NULL / #NOTNULL produce an IS
// clause regardless of field type, and that a single such filter alone doesn't hit
// the buildWhereClause off-by-one (args[val] vs args[val-1]) that used to panic.
func TestSetWhere_NullSigil_AcrossFieldTypes(t *testing.T) {
	types := []FieldKind{FieldInt, FieldFloat, FieldBool, FieldTime, FieldUUID, FieldString}
	for _, ft := range types {
		t.Run(string(ft)+"/#NULL", func(t *testing.T) {
			qb := NewQueryBuilder(GetBasicLogger())
			qb.SetWhere("f", "#NULL", ft)
			where := qb.buildWhereClause()
			if where != " WHERE f IS NULL" {
				t.Errorf("got %q", where)
			}
		})
		t.Run(string(ft)+"/#NOTNULL", func(t *testing.T) {
			qb := NewQueryBuilder(GetBasicLogger())
			qb.SetWhere("f", "#NOTNULL", ft)
			where := qb.buildWhereClause()
			if where != " WHERE f IS NOT NULL" {
				t.Errorf("got %q", where)
			}
		})
	}
}

// TestSetWhere_HashPrefix_NonSigil covers the "continue only if it is a string field"
// rule: an unrecognized "#..." value is dropped for typed fields (never a valid int,
// float, etc. anyway) but still treated as free text for string fields.
func TestSetWhere_HashPrefix_NonSigil(t *testing.T) {
	t.Run("non-string field: dropped", func(t *testing.T) {
		qb := NewQueryBuilder(GetBasicLogger())
		qb.SetWhere("f", "#5", FieldInt)
		if where := qb.buildWhereClause(); where != "" {
			t.Errorf("expected no clause, got %q", where)
		}
	})
	t.Run("string field: falls through to free-text search", func(t *testing.T) {
		qb := NewQueryBuilder(GetBasicLogger())
		qb.SetWhere("tag", "#urgent", FieldString)
		where := qb.buildWhereClause()
		if where != " WHERE tag ~* $1" {
			t.Errorf("got %q", where)
		}
		if qb.GetArgs()[0] != "#urgent" {
			t.Errorf("expected the literal value (including '#') to be bound, got %v", qb.GetArgs()[0])
		}
	})
}

// TestProcessWhereFromConfig_NullFilters is the end-to-end regression: both the
// standard and absolute-match paths accept the same #NULL / #NOTNULL sigil, and an
// absolute-match field's real data value of "NULL" (no sigil) still equality-matches
// instead of being reinterpreted as IS NULL.
func TestProcessWhereFromConfig_NullFilters(t *testing.T) {
	cfg := nullFilterTestModel()

	t.Run("standard field: #NULL", func(t *testing.T) {
		qb := NewQueryBuilder(GetBasicLogger())
		req := httptest.NewRequest("GET", "/widgets?retired_at=%23NULL", nil) // "#NULL"
		if err := qb.ProcessURLParamsNoFunc(req, cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		query := qb.BuildSelect("widgets", []string{"*"})
		if !strings.Contains(query, "retired_at IS NULL") {
			t.Errorf("expected retired_at IS NULL, got: %s", query)
		}
	})

	t.Run("standard field: #NOTNULL", func(t *testing.T) {
		qb := NewQueryBuilder(GetBasicLogger())
		req := httptest.NewRequest("GET", "/widgets?retired_at=%23NOTNULL", nil) // "#NOTNULL"
		if err := qb.ProcessURLParamsNoFunc(req, cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		query := qb.BuildSelect("widgets", []string{"*"})
		if !strings.Contains(query, "retired_at IS NOT NULL") {
			t.Errorf("expected retired_at IS NOT NULL, got: %s", query)
		}
	})

	t.Run("absolute-match field: #NULL", func(t *testing.T) {
		qb := NewQueryBuilder(GetBasicLogger())
		req := httptest.NewRequest("GET", "/widgets?owner_ref=%23NULL", nil) // "#NULL"
		if err := qb.ProcessURLParamsNoFunc(req, cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		query := qb.BuildSelect("widgets", []string{"*"})
		if !strings.Contains(query, "owner_ref IS NULL") {
			t.Errorf("expected owner_ref IS NULL, got: %s", query)
		}
	})

	t.Run("absolute-match field: real data literally \"NULL\" still equality-matches", func(t *testing.T) {
		qb := NewQueryBuilder(GetBasicLogger())
		req := httptest.NewRequest("GET", "/widgets?owner_ref=NULL", nil) // no sigil
		if err := qb.ProcessURLParamsNoFunc(req, cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		query := qb.BuildSelect("widgets", []string{"*"})
		if !strings.Contains(query, "owner_ref = $1") {
			t.Errorf("expected an exact = match, got: %s", query)
		}
		if qb.GetArgs()[0] != "NULL" {
			t.Errorf("expected the literal value \"NULL\" to be bound, got %v", qb.GetArgs()[0])
		}
	})

	t.Run("single lone NULL filter does not panic (buildWhereClause off-by-one regression)", func(t *testing.T) {
		qb := NewQueryBuilder(GetBasicLogger())
		req := httptest.NewRequest("GET", "/widgets?retired_at=%23NULL", nil)
		if err := qb.ProcessURLParamsNoFunc(req, cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		_ = qb.BuildSelect("widgets", []string{"*"}) // must not panic
	})
}
