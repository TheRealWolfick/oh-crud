package tools

import (
	"sort"
	"testing"

	"lotusforge.au/api-server/models"
)

// --- helpers ---

func ptr[T any](v T) *T { return &v }

// configModel builds a DataModel suitable for config/validation tests.
// id:   int, PK, skip-insert
// code: string, required-on-insert, unique key (test_uq_code)
// name: string, nullable, include-in-diff: true
// amt:  float, nullable
func configTestModel() *models.DataModel {
	return &models.DataModel{
		Name:        ptr("Test Model"),
		Version:     ptr("1.0.0"),
		Table_name:  ptr("test_table"),
		End_point:   ptr("test"),
		Primary_key: ptr("id"),
		Diff_comparator: ptr("code"),
		End_points_allowed: &models.End_pointsAllowed{
			GET:  []string{"user","report"},
			POST: []string{"user"},
			PUT:  []string{"user"},
		},
		Unique_keys: map[string]models.UniqueKey{
			"test_uq_code": {Fields: []string{"code"}},
		},
		Fields: map[string]models.DataModelField{
			"id": {
				Type:        ptr("int"),
				JSON:        ptr("id"),
				DB_type:     ptr("int"),
				Skip_insert: ptr(true),
				Rules: &models.DataModelFieldRules{
					Min: ptr(0),
					Max: ptr(20),
					Enum: []string{"1", "2", "3"},
				},
			},
			"code": {
				Type:               ptr("string"),
				JSON:               ptr("code"),
				DB_type:            ptr("text"),
				Required_on_insert: ptr(true),
				Include_in_diff:    ptr(false),
				Rules: &models.DataModelFieldRules{
					Pattern: ptr("[A-Z]*"),
				},
			},
			"name": {
				Type:            ptr("string"),
				JSON:            ptr("name"),
				DB_type:         ptr("text"),
				Nullable:        ptr(true),
				Include_in_diff: ptr(true),
				Rules: &models.DataModelFieldRules{
					Max_length: ptr(6),
					Enum: []string{"John","Smith","Jones"},
				},
			},
			"amt": {
				Type:    ptr("float"),
				JSON:    ptr("amt"),
				DB_type: ptr("numeric"),
				Nullable: ptr(true),
				Rules: &models.DataModelFieldRules{
					Min: ptr(0),
					Max: ptr(20),
					Enum: []string{"5.0", "13.35", "20.0"},
				},
			},
		},
	}
}

// sortedStrings is a helper for order-insensitive slice comparisons.
func sortedStrings(s []string) []string {
	c := make([]string, len(s))
	copy(c, s)
	sort.Strings(c)
	return c
}

// --- GetInsertRequiredFields ---

func TestGetInsertRequiredFields(t *testing.T) {
	t.Run("returns json key of required-on-insert field", func(t *testing.T) {
		cfg := configTestModel()
		got := GetInsertRequiredFields(cfg)
		if len(got) != 1 || got[0] != "code" {
			t.Errorf("expected [code], got %v", got)
		}
	})

	t.Run("returns empty when no required-on-insert fields", func(t *testing.T) {
		cfg := configTestModel()
		// clear the flag
		f := cfg.Fields["code"]
		f.Required_on_insert = ptr(false)
		cfg.Fields["code"] = f

		got := GetInsertRequiredFields(cfg)
		if len(got) != 0 {
			t.Errorf("expected [], got %v", got)
		}
	})

	t.Run("skips fields with no JSON key", func(t *testing.T) {
		cfg := configTestModel()
		// add a field with required-on-insert but no JSON key
		cfg.Fields["nojson"] = models.DataModelField{
			Type:               ptr("string"),
			DB_type:            ptr("text"),
			Required_on_insert: ptr(true),
		}
		got := GetInsertRequiredFields(cfg)
		// nojson should not appear
		for _, k := range got {
			if k == "nojson" {
				t.Error("field with nil JSON should be skipped")
			}
		}
	})

	t.Run("returns multiple required fields", func(t *testing.T) {
		cfg := configTestModel()
		f := cfg.Fields["name"]
		f.Required_on_insert = ptr(true)
		cfg.Fields["name"] = f

		got := sortedStrings(GetInsertRequiredFields(cfg))
		want := []string{"code", "name"}
		if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
			t.Errorf("expected %v, got %v", want, got)
		}
	})
}

// --- GetUpdateKeyOptions ---

func TestGetUpdateKeyOptions(t *testing.T) {
	t.Run("returns pk option and unique key option", func(t *testing.T) {
		cfg := configTestModel()
		opts := GetUpdateKeyOptions(cfg)
		// expect at least 2 options: [[id], [code]]
		if len(opts) < 2 {
			t.Fatalf("expected at least 2 options, got %d: %v", len(opts), opts)
		}
		// first option is PK
		if len(opts[0]) != 1 || opts[0][0] != "id" {
			t.Errorf("expected PK option [id], got %v", opts[0])
		}
		// second option is unique key
		if len(opts[1]) != 1 || opts[1][0] != "code" {
			t.Errorf("expected UQ option [code], got %v", opts[1])
		}
	})

	t.Run("no pk yields only unique key options", func(t *testing.T) {
		cfg := configTestModel()
		cfg.Primary_key = nil
		opts := GetUpdateKeyOptions(cfg)
		if len(opts) != 1 {
			t.Fatalf("expected 1 option (the unique key), got %d", len(opts))
		}
		if len(opts[0]) != 1 || opts[0][0] != "code" {
			t.Errorf("expected [code], got %v", opts[0])
		}
	})

	t.Run("no unique keys yields only pk option", func(t *testing.T) {
		cfg := configTestModel()
		cfg.Unique_keys = nil
		opts := GetUpdateKeyOptions(cfg)
		if len(opts) != 1 || opts[0][0] != "id" {
			t.Errorf("expected [[id]], got %v", opts)
		}
	})

	t.Run("multi-field unique key produces multi-element option", func(t *testing.T) {
		cfg := configTestModel()
		cfg.Unique_keys["multi_uq"] = models.UniqueKey{Fields: []string{"code", "name"}}
		opts := GetUpdateKeyOptions(cfg)
		// find the multi-field option
		found := false
		for _, opt := range opts {
			if len(opt) == 2 {
				sorted := sortedStrings(opt)
				if sorted[0] == "code" && sorted[1] == "name" {
					found = true
					break
				}
			}
		}
		if !found {
			t.Errorf("multi-field unique key option not found in %v", opts)
		}
	})

	t.Run("unique key with missing field config is skipped", func(t *testing.T) {
		cfg := configTestModel()
		cfg.Unique_keys["bad_uq"] = models.UniqueKey{Fields: []string{"nonexistent_field"}}
		opts := GetUpdateKeyOptions(cfg)
		for _, opt := range opts {
			for _, key := range opt {
				if key == "nonexistent_field" {
					t.Error("option with undefined field should be skipped")
				}
			}
		}
	})

	t.Run("empty config returns empty options", func(t *testing.T) {
		cfg := &models.DataModel{}
		opts := GetUpdateKeyOptions(cfg)
		if len(opts) != 0 {
			t.Errorf("expected 0 options, got %v", opts)
		}
	})
}

// --- FindRowKeyFields ---

func TestFindRowKeyFields(t *testing.T) {
	cfg := configTestModel()

	t.Run("finds primary key in coerced row", func(t *testing.T) {
		// coerced rows use field_name (= DB column) as keys
		row := map[string]any{"id": 1, "code": "ABC", "name": "Test"}
		fields, ok := FindRowKeyFields(row, cfg)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if len(fields) != 1 || fields[0] != "id" {
			t.Errorf("expected [id], got %v", fields)
		}
	})

	t.Run("finds unique key when pk absent", func(t *testing.T) {
		row := map[string]any{"code": "ABC", "name": "Test"}
		fields, ok := FindRowKeyFields(row, cfg)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if len(fields) != 1 || fields[0] != "code" {
			t.Errorf("expected [code], got %v", fields)
		}
	})

	t.Run("returns false when no key present", func(t *testing.T) {
		row := map[string]any{"name": "Test", "amt": 1.5}
		_, ok := FindRowKeyFields(row, cfg)
		if ok {
			t.Error("expected ok=false when no key fields present")
		}
	})

	t.Run("pk takes priority over unique key", func(t *testing.T) {
		row := map[string]any{"id": 99, "code": "ABC"}
		fields, ok := FindRowKeyFields(row, cfg)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if fields[0] != "id" {
			t.Errorf("expected pk 'id' to take priority, got %v", fields)
		}
	})

	t.Run("no pk in config uses unique key", func(t *testing.T) {
		cfgNoPK := configTestModel()
		cfgNoPK.Primary_key = nil
		row := map[string]any{"code": "ABC"}
		fields, ok := FindRowKeyFields(row, cfgNoPK)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if len(fields) != 1 || fields[0] != "code" {
			t.Errorf("expected [code], got %v", fields)
		}
	})

	t.Run("empty row returns false", func(t *testing.T) {
		_, ok := FindRowKeyFields(map[string]any{}, cfg)
		if ok {
			t.Error("expected ok=false for empty row")
		}
	})

	t.Run("multi-field unique key requires all fields present", func(t *testing.T) {
		cfgMulti := configTestModel()
		cfgMulti.Primary_key = nil
		cfgMulti.Unique_keys = map[string]models.UniqueKey{
			"multi_uq": {Fields: []string{"code", "name"}},
		}
		// only one field present — should fail
		row := map[string]any{"code": "ABC"}
		_, ok := FindRowKeyFields(row, cfgMulti)
		if ok {
			t.Error("expected ok=false when only partial unique key present")
		}

		// both fields present — should succeed
		row["name"] = "Test"
		fields, ok := FindRowKeyFields(row, cfgMulti)
		if !ok {
			t.Fatal("expected ok=true with all unique key fields present")
		}
		sorted := sortedStrings(fields)
		if sorted[0] != "code" || sorted[1] != "name" {
			t.Errorf("expected [code name], got %v", fields)
		}
	})

}

func TestRowFieldRules(t *testing.T) {
	cfg := configTestModel()
	
	t.Run("string field more than maximum character limit", func(t *testing.T) {
		field_rules := cfg.Fields["name"].Rules
	  _, valid := ValidateStringRules("name", "brethren", field_rules)

		if valid {
			t.Errorf("Expected string of 'brethren' to error with max=6")
		}
	})
	
	t.Run("string field within character limits and correct field rules", func(t *testing.T) {
		field_rules := cfg.Fields["name"].Rules
	  errs, valid := ValidateStringRules("name", "John", field_rules)

		if !valid {
			t.Errorf("Expected string of 'John' to not error. Got {%s}", errs)
		}
	})

	t.Run("string field to match pattern", func(t *testing.T) {
		field_rules := cfg.Fields["code"].Rules
	  errs, valid := ValidateStringRules("code", "TEST", field_rules)

		if !valid {
			t.Errorf("Expected string of 'TEST' to not error. Got {%s}", errs)
		}
	})

	t.Run("string field to not match pattern", func(t *testing.T) {
		field_rules := cfg.Fields["code"].Rules
	  _, valid := ValidateStringRules("code", "Test", field_rules)

		if valid {
			t.Errorf("Expected string of 'test' to error.")
		}
	})

	t.Run("string field to not match pattern with numbers", func(t *testing.T) {
		field_rules := cfg.Fields["code"].Rules
	  _, valid := ValidateStringRules("code", "TEST5T", field_rules)

		if valid {
			t.Errorf("Expected string of 'TEST5T' to error.")
		}
	})

	t.Run("string field not matching enum values", func(t *testing.T) {
		field_rules := cfg.Fields["name"].Rules
	  _, valid := ValidateStringRules("name", "James", field_rules)

		if valid {
			t.Errorf("Expected string of 'James' to error")
		}
	})

	// Integers

	t.Run("int field to below min value", func(t *testing.T) {
		field_rules := cfg.Fields["id"].Rules
	  _, valid := ValidateIntRules("id", -4, field_rules)

		if valid {
			t.Errorf("Expected int of -4 (min=0) to error.")
		}
	})

	t.Run("int field to above max value", func(t *testing.T) {
		field_rules := cfg.Fields["id"].Rules
	  _, valid := ValidateIntRules("id", 25, field_rules)

		if valid {
			t.Errorf("Expected int of 25 (max=20) to error.")
		}
	})

	t.Run("int field within min and max values", func(t *testing.T) {
		field_rules := cfg.Fields["id"].Rules
	  errs, valid := ValidateIntRules("id", 1, field_rules)

		if !valid {
			t.Errorf("Expected int of 1 (min=0,max=20) to not error. Got {%s}", errs)
		}
	})

	t.Run("int field not a valid enum value", func(t *testing.T) {
		field_rules := cfg.Fields["id"].Rules
	  _, valid := ValidateIntRules("id", 15, field_rules)

		if valid {
			t.Errorf("Expected int of 15 (min=0,max=20) to error")
		}
	})

	// Floats

	t.Run("float field to below min value", func(t *testing.T) {
		field_rules := cfg.Fields["amt"].Rules
	  _, valid := ValidateFloatRules("amt", -4.5, field_rules)

		if valid {
			t.Errorf("Expected float of -4.5 (min=0) to error.")
		}
	})

	t.Run("float field to above max value", func(t *testing.T) {
		field_rules := cfg.Fields["amt"].Rules
	  _, valid := ValidateFloatRules("amt", 25.35, field_rules)

		if valid {
			t.Errorf("Expected float of 25.35 (max=20) to error.")
		}
	})

	t.Run("float field within min and max values", func(t *testing.T) {
		field_rules := cfg.Fields["amt"].Rules
	  errs, valid := ValidateFloatRules("amt", 20.0, field_rules)

		if !valid {
			t.Errorf("Expected float of 20.0 (min=0,max=20) to not error. Got {%s}", errs)
		}
	})

	t.Run("float field not a valid enum value", func(t *testing.T) {
		field_rules := cfg.Fields["amt"].Rules
	  _, valid := ValidateFloatRules("amt", 17.05, field_rules)

		if valid {
			t.Errorf("Expected float of 17.05 (min=0,max=20) to error")
		}
	})
}

// --- DynamicGetDatabaseColumns ---

func TestDynamicGetDatabaseColumns(t *testing.T) {
	cfg := configTestModel()
	allFields := []string{"id", "code", "name", "amt"}

	t.Run("all columns when pk_only=false req_only=false", func(t *testing.T) {
		cols := DynamicGetDatabaseColumns(cfg, false, false)
		if len(cols) != len(allFields) {
			t.Errorf("expected %d columns, got %d: %v", len(allFields), len(cols), cols)
		}
	})

	t.Run("pk_only returns only the primary key column", func(t *testing.T) {
		cols := DynamicGetDatabaseColumns(cfg, true, false)
		if len(cols) != 1 || cols[0] != "id" {
			t.Errorf("expected [id], got %v", cols)
		}
	})

	t.Run("req_only returns pk and required-on-insert columns", func(t *testing.T) {
		cols := sortedStrings(DynamicGetDatabaseColumns(cfg, false, true))
		// id (pk) + code (required-on-insert)
		if len(cols) != 2 {
			t.Fatalf("expected 2 columns, got %d: %v", len(cols), cols)
		}
		if cols[0] != "code" || cols[1] != "id" {
			t.Errorf("expected [code id], got %v", cols)
		}
	})

	t.Run("pk_only takes priority over req_only", func(t *testing.T) {
		cols := DynamicGetDatabaseColumns(cfg, true, true)
		if len(cols) != 1 || cols[0] != "id" {
			t.Errorf("expected only pk [id] when pk_only=true, got %v", cols)
		}
	})

	t.Run("no pk set returns nothing in pk_only mode", func(t *testing.T) {
		cfgNoPK := configTestModel()
		cfgNoPK.Primary_key = nil
		cols := DynamicGetDatabaseColumns(cfgNoPK, true, false)
		if len(cols) != 0 {
			t.Errorf("expected [], got %v", cols)
		}
	})
}

// --- BuildExcludeKeysFromConfig ---

func TestBuildExcludeKeysFromConfig(t *testing.T) {
	t.Run("returns field names with include-in-diff: false", func(t *testing.T) {
		cfg := configTestModel()
		excluded := BuildExcludeKeysFromConfig(cfg)
		// "code" has include-in-diff: false
		if !excluded["code"] {
			t.Error("expected 'code' to be in exclude set")
		}
		// "name" has include-in-diff: true — should not be excluded
		if excluded["name"] {
			t.Error("expected 'name' to not be in exclude set")
		}
		// "amt" has no include-in-diff flag — should not be excluded
		if excluded["amt"] {
			t.Error("expected 'amt' to not be in exclude set")
		}
	})

	t.Run("empty when all fields are diffable", func(t *testing.T) {
		cfg := configTestModel()
		for k, f := range cfg.Fields {
			f.Include_in_diff = ptr(true)
			cfg.Fields[k] = f
		}
		excluded := BuildExcludeKeysFromConfig(cfg)
		if len(excluded) != 0 {
			t.Errorf("expected empty exclude set, got %v", excluded)
		}
	})

	t.Run("returns multiple excluded fields", func(t *testing.T) {
		cfg := configTestModel()
		f := cfg.Fields["amt"]
		f.Include_in_diff = ptr(false)
		cfg.Fields["amt"] = f

		excluded := BuildExcludeKeysFromConfig(cfg)
		if !excluded["code"] || !excluded["amt"] {
			t.Errorf("expected both 'code' and 'amt' excluded, got %v", excluded)
		}
	})
}

// --- GetDiffComparatorKey ---

func TestGetDiffComparatorKey(t *testing.T) {
	t.Run("returns the comparator field name", func(t *testing.T) {
		cfg := configTestModel()
		got := GetDiffComparatorKey(cfg)
		if got != "code" {
			t.Errorf("expected 'code', got %q", got)
		}
	})

	t.Run("returns empty string when nil", func(t *testing.T) {
		cfg := configTestModel()
		cfg.Diff_comparator = nil
		got := GetDiffComparatorKey(cfg)
		if got != "" {
			t.Errorf("expected empty string, got %q", got)
		}
	})

	t.Run("returns empty string when set to empty string", func(t *testing.T) {
		cfg := configTestModel()
		cfg.Diff_comparator = ptr("")
		got := GetDiffComparatorKey(cfg)
		if got != "" {
			t.Errorf("expected empty string, got %q", got)
		}
	})
}
