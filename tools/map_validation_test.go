package tools

import (
	"testing"

	"lotusforge.au/api-server/models"
)

// mapTestModel builds a DataModel for map/validation tests.
// Uses field_name as DB column (= YAML key).
// id:     int, PK, skip-insert
// code:   string, required-on-insert, unique key (uq_code), JSON alias "code_alias"
// label:  string, nullable
// score:  float, nullable
func mapTestModel() *models.DataModel {
	return &models.DataModel{
		Name:        ptr("Map Test Model"),
		Version:     ptr("1.0.0"),
		Table_name:  ptr("map_test_table"),
		End_point:   ptr("map-test"),
		Primary_key: ptr("id"),
		End_points_allowed: &models.End_pointsAllowed{
			GET:  []string{},
			POST: []string{},
			PUT:  []string{},
		},
		Unique_keys: map[string]models.UniqueKey{
			"uq_code": {Fields: []string{"code"}},
		},
		Fields: map[string]models.DataModelField{
			"id": {
				Type:        ptr("int"),
				JSON:        ptr("id"),
				DB_type:     ptr("int"),
				Skip_insert: ptr(true),
			},
			"code": {
				Type:               ptr("string"),
				JSON:               ptr("code"),
				JSON_alias:         []string{"code_alias"},
				DB_type:            ptr("text"),
				Required_on_insert: ptr(true),

			},
			"label": {
				Type:     ptr("string"),
				JSON:     ptr("label"),
				DB_type:  ptr("text"),
				Nullable: ptr(true),
			},
			"score": {
				Type:     ptr("float"),
				JSON:     ptr("score"),
				DB_type:  ptr("numeric"),
				Nullable: ptr(true),
			},
			"secret": {
				Type: 		ptr("string"),
				DB_type:  ptr("text"),
				Nullable: ptr(false),
				Rules: 		&models.DataModelFieldRules{
					Max_length: ptr(5),
				},
			},
		},
	}
}

// --- Validate_Map_AgainstConfig ---

func TestValidate_Map_AgainstConfig(t *testing.T) {
	cfg := mapTestModel()

	t.Run("valid single row insert — returns 1 valid 0 invalid", func(t *testing.T) {
		row := map[string]any{"code": "ABC", "label": "Test"}
		valid, invalid := Validate_Map_AgainstConfig(cfg, row, false, true)
		if len(valid) != 1 || len(invalid) != 0 {
			t.Errorf("expected 1 item for valid and 0 items for invalid, got %d (valid) %d (invalid)", len(valid), len(invalid))
		}
	})

	t.Run("missing required-on-insert field — returns 0 valid 1 invalid", func(t *testing.T) {
		row := map[string]any{"label": "No Code"}
		valid, invalid := Validate_Map_AgainstConfig(cfg, row, false, true)
		if len(valid) != 0 || len(invalid) != 1 {
			t.Errorf("expected 0 valid 1 invalid, got %d valid %d invalid", len(valid), len(invalid))
		}
	})

	t.Run("valid single row update with key", func(t *testing.T) {
		row := map[string]any{"id": 1, "label": "Updated"}
		valid, invalid := Validate_Map_AgainstConfig(cfg, row, true, false)
		if len(valid) != 1 || len(invalid) != 0 {
			t.Errorf("expected 1 valid 0 invalid, got %d valid %d invalid", len(valid), len(invalid))
		}
	})

	t.Run("missing key for update — returns 0 valid 1 invalid", func(t *testing.T) {
		row := map[string]any{"label": "No Key"}
		valid, invalid := Validate_Map_AgainstConfig(cfg, row, true, false)
		if len(valid) != 0 || len(invalid) != 1 {
			t.Errorf("expected 0 valid 1 invalid, got %d valid %d invalid", len(valid), len(invalid))
		}
	})
}

// --- Validate_SliceOfMaps_AgainstConfig ---

func TestValidate_SliceOfMaps_AgainstConfig(t *testing.T) {
	cfg := mapTestModel()

	t.Run("empty input returns empty slices", func(t *testing.T) {
		valid, invalid := Validate_SliceOfMaps_AgainstConfig(cfg, []map[string]any{}, false, false)
		if len(valid) != 0 || len(invalid) != 0 {
			t.Errorf("expected empty results, got %d valid %d invalid", len(valid), len(invalid))
		}
	})

	t.Run("insert mode — valid rows coerced and separated from invalid", func(t *testing.T) {
		rows := []map[string]any{
			{"code": "A1", "label": "Alpha", "score": 1.5},   // valid
			{"label": "No code"},                              // invalid: missing required code
			{"code": "A2"},                                    // valid: code present, other fields optional
		}
		valid, invalid := Validate_SliceOfMaps_AgainstConfig(cfg, rows, false, true)
		if len(valid) != 2 {
			t.Errorf("expected 2 valid, got %d: %v", len(valid), valid)
		}
		if len(invalid) != 1 {
			t.Errorf("expected 1 invalid, got %d: %v", len(invalid), invalid)
		}
	})

	t.Run("update mode — valid rows have key, invalid rows do not", func(t *testing.T) {
		rows := []map[string]any{
			{"id": 1, "label": "Updated"},    // valid: has PK
			{"code": "ABC", "label": "UQ"},   // valid: has unique key
			{"label": "No key"},              // invalid
		}
		valid, invalid := Validate_SliceOfMaps_AgainstConfig(cfg, rows, true, false)
		if len(valid) != 2 {
			t.Errorf("expected 2 valid, got %d", len(valid))
		}
		if len(invalid) != 1 {
			t.Errorf("expected 1 invalid, got %d", len(invalid))
		}
	})

	t.Run("no checks — all rows coerced (coercible fields only)", func(t *testing.T) {
		rows := []map[string]any{
			{"label": "Anything"},
			{"score": 9.9},
		}
		valid, invalid := Validate_SliceOfMaps_AgainstConfig(cfg, rows, false, false)
		if len(valid) != 2 {
			t.Errorf("expected 2 valid (no checks), got %d invalid: %v", len(invalid), invalid)
		}
	})

	t.Run("coercion failure causes row to be invalid", func(t *testing.T) {
		rows := []map[string]any{
			{"code": "OK", "score": "not-a-float"},  // score fails coercion
		}
		valid, invalid := Validate_SliceOfMaps_AgainstConfig(cfg, rows, false, true)
		if len(valid) != 0 || len(invalid) != 1 {
			t.Errorf("expected 0 valid 1 invalid after coercion failure, got %d valid %d invalid", len(valid), len(invalid))
		}
	})

	t.Run("json alias resolves to field", func(t *testing.T) {
		// "code_alias" is an alias for "code"
		rows := []map[string]any{
			{"code_alias": "ALIAS_VAL", "label": "via alias"},
		}
		valid, invalid := Validate_SliceOfMaps_AgainstConfig(cfg, rows, false, true)
		if len(valid) != 1 || len(invalid) != 0 {
			t.Errorf("expected 1 valid via alias, got %d valid %d invalid", len(valid), len(invalid))
		}
		// coerced row should use field_name "code" as key
		if valid[0]["code"] != "ALIAS_VAL" {
			t.Errorf("expected coerced row to have code=ALIAS_VAL, got %v", valid[0])
		}
	})

	t.Run("all valid rows are coerced with correct types", func(t *testing.T) {
		rows := []map[string]any{
			{"code": "T1", "score": "3.14"},  // score as string — should be coerced to float
		}
		valid, _ := Validate_SliceOfMaps_AgainstConfig(cfg, rows, false, true)
		if len(valid) != 1 {
			t.Fatalf("expected 1 valid row, got %d", len(valid))
		}
		scoreVal, ok := valid[0]["score"].(float64)
		if !ok {
			t.Errorf("expected score to be float64, got %T: %v", valid[0]["score"], valid[0]["score"])
		}
		if scoreVal != 3.14 {
			t.Errorf("expected score=3.14, got %v", scoreVal)
		}
	})

	t.Run("insert+update checks combined — row needs both key and required fields", func(t *testing.T) {
		rows := []map[string]any{
			{"id": 1, "code": "C1"},            // valid: has PK + required field
			{"id": 2},                          // invalid: missing required code
			{"code": "C2"},                     // invalid: has required field but unique key only — PK check needed?
											   // Note: GetUpdateKeyOptions returns PK option + UQ option.
											   // code IS the unique key so this should pass key check.
		}
		valid, invalid := Validate_SliceOfMaps_AgainstConfig(cfg, rows, true, true)
		// row 0: has id (PK) and code (required) → valid
		// row 1: has id (key ok) but no code (required) → invalid
		// row 2: has code (UQ key ok, required ok) → valid
		if len(valid) != 2 {
			t.Errorf("expected 2 valid, got %d: %v", len(valid), valid)
		}
		if len(invalid) != 1 {
			t.Errorf("expected 1 invalid, got %d: %v", len(invalid), invalid)
		}
	})
}

// --- SetValueAndWhereFromMap ---

func TestSetValueAndWhereFromMap(t *testing.T) {
	t.Run("where_fields go to SetWhereAbsolute, others go to SetValue", func(t *testing.T) {
		log := GetBasicLogger()
		qb := NewQueryBuilder(log)
		m := map[string]any{
			"id":    42,
			"label": "hello",
			"score": 9.5,
		}
		SetValueAndWhereFromMap(qb, m, []string{"id"})

		// verify that "id" is in where args and "label"/"score" are in set args
		q := qb.BuildUpdate(&models.DataModel{
			Table_name: ptr("t"),
			Fields:     map[string]models.DataModelField{},
		})
		// The query must be a valid UPDATE with a WHERE clause
		if q == "" {
			t.Error("expected non-empty query")
		}
		// id should appear as WHERE, label and score as SET
		// We can verify via arg ordering: where args come after value args
		args := qb.GetArgs()
		if len(args) != 3 {
			t.Errorf("expected 3 args (label, score, id), got %d: %v", len(args), args)
		}
	})

	t.Run("all fields as where — nothing in SET", func(t *testing.T) {
		log := GetBasicLogger()
		qb := NewQueryBuilder(log)
		m := map[string]any{"id": 1}
		SetValueAndWhereFromMap(qb, m, []string{"id"})
		args := qb.GetArgs()
		if len(args) != 1 {
			t.Errorf("expected 1 arg, got %d: %v", len(args), args)
		}
	})

	t.Run("empty where_fields — all go to SetValue", func(t *testing.T) {
		log := GetBasicLogger()
		qb := NewQueryBuilder(log)
		m := map[string]any{"label": "test", "score": 1.0}
		SetValueAndWhereFromMap(qb, m, []string{})
		args := qb.GetArgs()
		if len(args) != 2 {
			t.Errorf("expected 2 value args, got %d: %v", len(args), args)
		}
	})
}
