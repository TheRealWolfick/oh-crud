package tools

import (
	"net/http/httptest"
	"strings"
	"testing"

	"lotusforge.au/api-server/models"
)

// jsonSelectTestModel mirrors the shape of diffs.yaml's abstracted jsonb columns:
// a jsonb field with a "count" json-select override, plus a plain int field and a
// plain (non-abstracted) jsonb field, so tests can assert the override is applied
// only where configured.
func jsonSelectTestModel() *models.DataModel {
	return &models.DataModel{
		Name:        ptr("diffs"),
		Version:     ptr("1.0.0"),
		Table_name:  ptr("diffs"),
		End_point:   ptr("diffs"),
		Primary_key: ptr("diff_id"),
		Fields: map[string]models.DataModelField{
			"diff_id": {
				Type: ptr("int"), JSON: ptr("diff_id"), DB_type: ptr("serial"),
			},
			"diff_type": {
				Type: ptr("string"), JSON: ptr("diff_type"), DB_type: ptr("character varying(20)"),
			},
			"missing_from_supplied": {
				Type: ptr("json"), JSON: ptr("missing_from_supplied"), DB_type: ptr("jsonb"),
				JSON_select: &models.DataModelJsonSelect{
					Action: ptr("count"), ApplyOn: ptr("list"), TreatAs: ptr("int"),
				},
			},
			"raw_payload": {
				// jsonb with no json-select override — must never be abstracted.
				Type: ptr("json"), JSON: ptr("raw_payload"), DB_type: ptr("jsonb"),
			},
		},
	}
}

func TestJsonSelectExpr(t *testing.T) {
	cfg := jsonSelectTestModel()

	t.Run("count action on jsonb field produces jsonb_array_length", func(t *testing.T) {
		field := cfg.Fields["missing_from_supplied"]
		expr, ok := jsonSelectExpr(&field)
		if !ok || expr != "jsonb_array_length(missing_from_supplied)" {
			t.Errorf("expected jsonb_array_length(missing_from_supplied), got %q (ok=%v)", expr, ok)
		}
	})

	t.Run("field without json-select is not abstracted", func(t *testing.T) {
		field := cfg.Fields["raw_payload"]
		if _, ok := jsonSelectExpr(&field); ok {
			t.Errorf("expected no abstraction for a field with no json-select config")
		}
	})

	t.Run("non-jsonb db-type is never abstracted even with json-select set", func(t *testing.T) {
		field := models.DataModelField{
			JSON: ptr("count_field"), DB_type: ptr("integer"),
			JSON_select: &models.DataModelJsonSelect{Action: ptr("count"), ApplyOn: ptr("list"), TreatAs: ptr("int")},
		}
		if _, ok := jsonSelectExpr(&field); ok {
			t.Errorf("expected no abstraction for a non-jsonb db-type")
		}
	})

	t.Run("unsupported action is not abstracted", func(t *testing.T) {
		field := models.DataModelField{
			JSON: ptr("x"), DB_type: ptr("jsonb"),
			JSON_select: &models.DataModelJsonSelect{Action: ptr("sum"), ApplyOn: ptr("list"), TreatAs: ptr("int")},
		}
		if _, ok := jsonSelectExpr(&field); ok {
			t.Errorf("expected no abstraction for an unsupported action")
		}
	})
}

func TestConvertFieldJsonSelect(t *testing.T) {
	cfg := jsonSelectTestModel()
	field := cfg.Fields["missing_from_supplied"]

	expr, ok := ConvertFieldJsonSelect(&field)
	want := "jsonb_array_length(missing_from_supplied) AS missing_from_supplied"
	if !ok || expr != want {
		t.Errorf("expected %q, got %q (ok=%v)", want, expr, ok)
	}
}

func TestBuildSchema_DescribesJsonSelectOverride(t *testing.T) {
	cfg := jsonSelectTestModel()
	qb := NewQueryBuilder(GetBasicLogger())
	schema := qb.BuildSchema(cfg)

	overridden := schema.Fields["missing_from_supplied"]
	if overridden.Select_override == nil {
		t.Fatalf("expected Select_override to be populated for an abstracted field")
	}
	if overridden.Select_expression != "jsonb_array_length(missing_from_supplied)" {
		t.Errorf("unexpected Select_expression: %q", overridden.Select_expression)
	}

	plain := schema.Fields["raw_payload"]
	if plain.Select_override != nil || plain.Select_expression != "" {
		t.Errorf("expected no override on a plain jsonb field, got %+v", plain)
	}
}

func TestBuildSchema_CopiesDescriptionAndMeta(t *testing.T) {
	cfg := jsonSelectTestModel()
	cfg.Description = ptr("Row-level diffs awaiting review.")
	cfg.Meta = map[string]any{"icon": "diff"}

	field := cfg.Fields["diff_type"]
	field.Description = ptr("What kind of change this row represents.")
	field.Meta = map[string]any{"widget": "select"}
	cfg.Fields["diff_type"] = field

	qb := NewQueryBuilder(GetBasicLogger())
	schema := qb.BuildSchema(cfg)

	if schema.Description != "Row-level diffs awaiting review." {
		t.Errorf("expected model Description to be copied through, got %+v", schema.Description)
	}
	if schema.Meta["icon"] != "diff" {
		t.Errorf("expected model Meta to be copied through, got %+v", schema.Meta)
	}

	got := schema.Fields["diff_type"]
	if got.Description != "What kind of change this row represents." {
		t.Errorf("expected field Description to be copied through, got %+v", got.Description)
	}
	if got.Meta["widget"] != "select" {
		t.Errorf("expected field Meta to be copied through, got %+v", got.Meta)
	}

	untouched := schema.Fields["raw_payload"]
	if untouched.Description != "" || untouched.Meta != nil {
		t.Errorf("expected no Description/Meta on a field that didn't set them, got %+v", untouched)
	}
}

func TestDynamicGetDatabaseColumns_JsonSelect(t *testing.T) {
	cfg := jsonSelectTestModel()
	cols := DynamicGetDatabaseColumns(cfg, false, false)

	found := false
	for _, c := range cols {
		if c == "jsonb_array_length(missing_from_supplied) AS missing_from_supplied" {
			found = true
		}
		if strings.Contains(c, "raw_payload") && c != "raw_payload" {
			t.Errorf("raw_payload should be selected unabstracted, got %q", c)
		}
	}
	if !found {
		t.Errorf("expected missing_from_supplied to be abstracted in select columns, got %v", cols)
	}
}

// TestProcessWhereFromConfig_JsonSelect locks in the WHERE-clause half of the
// json-select override: filtering on an abstracted jsonb field must compare against
// the same expression the SELECT list uses (jsonb_array_length(col)), not the raw
// jsonb column — otherwise the filter silently matches nothing.
func TestProcessWhereFromConfig_JsonSelect(t *testing.T) {
	cfg := jsonSelectTestModel()

	t.Run("abstracted jsonb field filters on jsonb_array_length", func(t *testing.T) {
		qb := NewQueryBuilder(GetBasicLogger())
		req := httptest.NewRequest("GET", "/diffs?missing_from_supplied=3", nil)
		if err := qb.ProcessURLParamsNoFunc(req, cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		query := qb.BuildSelect("diffs", []string{"*"})
		if !strings.Contains(query, "jsonb_array_length(missing_from_supplied) = $1") {
			t.Errorf("expected WHERE clause to filter on jsonb_array_length(missing_from_supplied), got: %s", query)
		}
		if strings.Contains(query, "WHERE missing_from_supplied = ") {
			t.Errorf("WHERE clause must not compare the raw jsonb column directly, got: %s", query)
		}
	})

	t.Run("abstracted jsonb field honors range operators", func(t *testing.T) {
		qb := NewQueryBuilder(GetBasicLogger())
		req := httptest.NewRequest("GET", "/diffs?missing_from_supplied=%3E5", nil) // ">5"
		if err := qb.ProcessURLParamsNoFunc(req, cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		query := qb.BuildSelect("diffs", []string{"*"})
		if !strings.Contains(query, "jsonb_array_length(missing_from_supplied) > $1") {
			t.Errorf("expected a > comparison against jsonb_array_length, got: %s", query)
		}
	})

	t.Run("plain field is filtered on its own column, unabstracted", func(t *testing.T) {
		qb := NewQueryBuilder(GetBasicLogger())
		req := httptest.NewRequest("GET", "/diffs?diff_type=assets", nil)
		if err := qb.ProcessURLParamsNoFunc(req, cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		query := qb.BuildSelect("diffs", []string{"*"})
		if !strings.Contains(query, "diff_type ~* $1") {
			t.Errorf("expected a plain filter on diff_type, got: %s", query)
		}
	})
}
