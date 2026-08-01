package tools

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"lotusforge.au/api-server/models"
)

// basicModel builds a minimal DataModel used across model-function tests.
func basicModel() models.DataModel {
	return models.DataModel{
		Name:        ptr("Test Model"),
		Version:     ptr("1.0.0"),
		Table_name:  ptr("test_table"),
		End_point:   ptr("test"),
		Primary_key: ptr("id"),
		End_points_allowed: &models.End_pointsAllowed{
			GET:  []string{},
			POST: []string{},
			PUT:  []string{},
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
			},
			"code": {
				Type:               ptr("string"),
				JSON:               ptr("code"),
				DB_type:            ptr("text"),
				Required_on_insert: ptr(true),
			},
			"name": {
				Type:     ptr("string"),
				JSON:     ptr("name"),
				DB_type:  ptr("text"),
				Nullable: ptr(true),
			},
			"count": {
				Type:     ptr("int"),
				JSON:     ptr("count"),
				DB_type:  ptr("int"),
				Nullable: ptr(true),
			},
			"active": {
				Type:    ptr("bool"),
				JSON:    ptr("active"),
				DB_type: ptr("boolean"),
				Default: ptr("true"),
			},
			"secret": {
				Type: 	 ptr("string"),
				DB_type: ptr("text"),
				Private: ptr(true),
				Rules: &models.DataModelFieldRules{
					Max_length: ptr(5),
				},
			},
		},
	}
}

// ── ValidateDataModel ─────────────────────────────────────────────────────────

func TestValidateDataModel(t *testing.T) {
	t.Run("valid model passes", func(t *testing.T) {
		m := basicModel()
		if err := ValidateDataModel(m); err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
	})

	t.Run("missing name", func(t *testing.T) {
		m := basicModel()
		m.Name = nil
		if err := ValidateDataModel(m); err == nil {
			t.Error("expected error for missing name")
		}
	})

	t.Run("missing version", func(t *testing.T) {
		m := basicModel()
		m.Version = nil
		if err := ValidateDataModel(m); err == nil {
			t.Error("expected error for missing version")
		}
	})

	t.Run("malformed version", func(t *testing.T) {
		m := basicModel()
		m.Version = ptr("1.0")
		if err := ValidateDataModel(m); err == nil {
			t.Error("expected error for malformed version")
		}
	})

	t.Run("missing table-name", func(t *testing.T) {
		m := basicModel()
		m.Table_name = nil
		if err := ValidateDataModel(m); err == nil {
			t.Error("expected error for missing table-name")
		}
	})

	t.Run("missing primary-key", func(t *testing.T) {
		m := basicModel()
		m.Primary_key = nil
		if err := ValidateDataModel(m); err == nil {
			t.Error("expected error for missing primary-key")
		}
	})

	t.Run("primary-key references non-existent field", func(t *testing.T) {
		m := basicModel()
		m.Primary_key = ptr("nonexistent")
		if err := ValidateDataModel(m); err == nil {
			t.Error("expected error for primary-key referencing unknown field")
		}
	})

	t.Run("nullable primary key is rejected", func(t *testing.T) {
		m := basicModel()
		field := m.Fields["id"]
		field.Nullable = ptr(true)
		m.Fields["id"] = field
		if err := ValidateDataModel(m); err == nil {
			t.Error("expected error for nullable primary key")
		}
	})

	t.Run("no fields", func(t *testing.T) {
		m := basicModel()
		m.Fields = map[string]models.DataModelField{}
		if err := ValidateDataModel(m); err == nil {
			t.Error("expected error for empty fields")
		}
	})

	t.Run("field missing type", func(t *testing.T) {
		m := basicModel()
		f := m.Fields["name"]
		f.Type = nil
		m.Fields["name"] = f
		if err := ValidateDataModel(m); err == nil {
			t.Error("expected error for field missing type")
		}
	})

	t.Run("field unknown type", func(t *testing.T) {
		m := basicModel()
		f := m.Fields["name"]
		f.Type = ptr("badtype")
		m.Fields["name"] = f
		if err := ValidateDataModel(m); err == nil {
			t.Error("expected error for field with unknown type")
		}
	})

	t.Run("field missing db-type", func(t *testing.T) {
		m := basicModel()
		f := m.Fields["name"]
		f.DB_type = nil
		m.Fields["name"] = f
		if err := ValidateDataModel(m); err == nil {
			t.Error("expected error for field missing db-type")
		}
	})

	t.Run("field unknown migration strategy", func(t *testing.T) {
		m := basicModel()
		f := m.Fields["name"]
		f.Migration = ptr("badstrategy")
		m.Fields["name"] = f
		if err := ValidateDataModel(m); err == nil {
			t.Error("expected error for unknown migration strategy")
		}
	})

	t.Run("valid migration strategy", func(t *testing.T) {
		m := basicModel()
		f := m.Fields["name"]
		f.Migration = ptr("alter")
		m.Fields["name"] = f
		if err := ValidateDataModel(m); err != nil {
			t.Errorf("unexpected error for valid migration strategy: %v", err)
		}
	})

	t.Run("unique-key references non-existent field", func(t *testing.T) {
		m := basicModel()
		m.Unique_keys = map[string]models.UniqueKey{
			"bad_uk": {Fields: []string{"nonexistent"}},
		}
		if err := ValidateDataModel(m); err == nil {
			t.Error("expected error for unique-key referencing unknown field")
		}
	})

	t.Run("unique-key with no fields", func(t *testing.T) {
		m := basicModel()
		m.Unique_keys = map[string]models.UniqueKey{
			"empty_uk": {Fields: []string{}},
		}
		if err := ValidateDataModel(m); err == nil {
			t.Error("expected error for unique-key with no fields")
		}
	})

	t.Run("foreign-key field count mismatch", func(t *testing.T) {
		m := basicModel()
		m.Foreign_keys = map[string]models.ForeignKey{
			"fk_test": {
				Fields:        []string{"id", "code"},
				Target_table:  ptr("other_table"),
				Target_fields: []string{"other_id"},
			},
		}
		if err := ValidateDataModel(m); err == nil {
			t.Error("expected error for field/target-field count mismatch")
		}
	})

	t.Run("allow-diff without comparator", func(t *testing.T) {
		m := basicModel()
		m.Allow_diff = ptr(true)
		if err := ValidateDataModel(m); err == nil {
			t.Error("expected error for allow-diff without diff-comparator")
		}
	})

	t.Run("allow-diff with no include-in-diff fields", func(t *testing.T) {
		m := basicModel()
		m.Allow_diff = ptr(true)
		m.Diff_comparator = ptr("code")
		// No field has include-in-diff: true
		if err := ValidateDataModel(m); err == nil {
			t.Error("expected error for allow-diff with no include-in-diff fields")
		}
	})

	t.Run("valid allow-diff config", func(t *testing.T) {
		m := basicModel()
		m.Allow_diff = ptr(true)
		m.Diff_comparator = ptr("code")
		f := m.Fields["name"]
		f.Include_in_diff = ptr(true)
		m.Fields["name"] = f
		if err := ValidateDataModel(m); err != nil {
			t.Errorf("unexpected error for valid diff config: %v", err)
		}
	})
}

// ── CheckVersionIncrease ──────────────────────────────────────────────────────

func TestCheckVersionIncrease(t *testing.T) {
	cases := []struct {
		old    string
		now    string
		wantOk bool
	}{
		{"1.0.0", "1.0.1", true},
		{"1.0.0", "1.1.0", true},
		{"1.0.0", "2.0.0", true},
		{"1.0.0", "1.0.0", false}, // same version
		{"1.0.1", "1.0.0", false}, // older patch
		{"1.2.0", "1.1.9", false}, // older minor
		{"2.0.0", "1.9.9", false}, // older major
	}
	for _, c := range cases {
		ok, err := CheckVersionIncrease(c.old, c.now)
		if ok != c.wantOk {
			t.Errorf("CheckVersionIncrease(%q, %q): got ok=%v err=%v, want ok=%v", c.old, c.now, ok, err, c.wantOk)
		}
	}
}

// ── CoerceType ────────────────────────────────────────────────────────────────

func TestCoerceType_Int(t *testing.T) {
	cases := []struct {
		in   any
		want int
	}{
		{42, 42},
		{int16(10), 10},
		{int32(20), 20},
		{int64(30), 30},
		{float64(7.9), 7},
		{"15", 15},
	}
	for _, c := range cases {
		got, err := CoerceType(c.in, "int")
		if err != nil {
			t.Errorf("CoerceType(%v, int): unexpected error %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("CoerceType(%v, int): got %v, want %v", c.in, got, c.want)
		}
	}

	t.Run("nil returns nil", func(t *testing.T) {
		got, err := CoerceType(nil, "int")
		if err != nil || got != nil {
			t.Errorf("expected nil, nil; got %v, %v", got, err)
		}
	})

	t.Run("invalid string errors", func(t *testing.T) {
		if _, err := CoerceType("notanumber", "int"); err == nil {
			t.Error("expected error for non-numeric string")
		}
	})

	t.Run("pgtype.Numeric valid", func(t *testing.T) {
		n := pgtype.Numeric{}
		_ = n.Scan("42")
		got, err := CoerceType(n, "int")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if got != 42 {
			t.Errorf("got %v, want 42", got)
		}
	})
}

func TestCoerceType_Float(t *testing.T) {
	cases := []struct {
		in   any
		want float64
	}{
		{float64(3.14), 3.14},
		{float32(1.5), float64(float32(1.5))},
		{int(10), 10.0},
		{"2.71", 2.71},
	}
	for _, c := range cases {
		got, err := CoerceType(c.in, "float")
		if err != nil {
			t.Errorf("CoerceType(%v, float): unexpected error %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("CoerceType(%v, float): got %v, want %v", c.in, got, c.want)
		}
	}
}

func TestCoerceType_String(t *testing.T) {
	t.Run("plain string", func(t *testing.T) {
		got, err := CoerceType("hello", "string")
		if err != nil || got != "hello" {
			t.Errorf("got %v, %v", got, err)
		}
	})
	t.Run("time.Time formats as RFC3339", func(t *testing.T) {
		ts := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		got, err := CoerceType(ts, "string")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if got != "2024-01-15T10:30:00Z" {
			t.Errorf("got %v, want 2024-01-15T10:30:00Z", got)
		}
	})
	t.Run("pgtype.Text valid", func(t *testing.T) {
		txt := pgtype.Text{String: "pg text", Valid: true}
		got, err := CoerceType(txt, "string")
		if err != nil || got != "pg text" {
			t.Errorf("got %v, %v", got, err)
		}
	})
	t.Run("pgtype.Text invalid returns nil", func(t *testing.T) {
		txt := pgtype.Text{Valid: false}
		got, err := CoerceType(txt, "string")
		if err != nil || got != nil {
			t.Errorf("got %v, %v", got, err)
		}
	})
}

func TestCoerceType_Bool(t *testing.T) {
	cases := []struct {
		in   any
		want bool
	}{
		{true, true},
		{false, false},
		{"true", true},
		{"false", false},
		{"1", true},
		{"0", false},
		{1, true},
		{0, false},
	}
	for _, c := range cases {
		got, err := CoerceType(c.in, "bool")
		if err != nil {
			t.Errorf("CoerceType(%v, bool): unexpected error %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("CoerceType(%v, bool): got %v, want %v", c.in, got, c.want)
		}
	}
}

func TestCoerceType_UUID(t *testing.T) {
	t.Run("valid string UUID", func(t *testing.T) {
		uuid := "123e4567-e89b-12d3-a456-426614174000"
		got, err := CoerceType(uuid, "uuid")
		if err != nil || got != uuid {
			t.Errorf("got %v, %v", got, err)
		}
	})
	t.Run("invalid string UUID errors", func(t *testing.T) {
		if _, err := CoerceType("not-a-uuid", "uuid"); err == nil {
			t.Error("expected error for short UUID string")
		}
	})
	t.Run("pgtype.UUID valid", func(t *testing.T) {
		raw := pgtype.UUID{
			Bytes: [16]byte{0x12, 0x3e, 0x45, 0x67, 0xe8, 0x9b, 0x12, 0xd3, 0xa4, 0x56, 0x42, 0x66, 0x14, 0x17, 0x40, 0x00},
			Valid: true,
		}
		got, err := CoerceType(raw, "uuid")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if got == nil || got == "" {
			t.Error("expected non-empty UUID string")
		}
	})
}

func TestCoerceType_Time(t *testing.T) {
	t.Run("time.Time passthrough", func(t *testing.T) {
		ts := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
		got, err := CoerceType(ts, "time")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if got.(time.Time) != ts {
			t.Errorf("got %v, want %v", got, ts)
		}
	})
	t.Run("RFC3339 string parses", func(t *testing.T) {
		got, err := CoerceType("2024-06-01T12:00:00Z", "time")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		ts := got.(time.Time)
		if ts.Year() != 2024 || ts.Month() != 6 || ts.Day() != 1 {
			t.Errorf("unexpected time value: %v", ts)
		}
	})
	t.Run("invalid string errors", func(t *testing.T) {
		if _, err := CoerceType("not-a-time", "time"); err == nil {
			t.Error("expected error for invalid time string")
		}
	})
}

func TestCoerceType_JSON(t *testing.T) {
	t.Run("[]byte JSON", func(t *testing.T) {
		got, err := CoerceType([]byte(`{"key":"value"}`), "json")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		m, ok := got.(map[string]any)
		if !ok || m["key"] != "value" {
			t.Errorf("unexpected result: %v", got)
		}
	})
	t.Run("string JSON", func(t *testing.T) {
		got, err := CoerceType(`[1,2,3]`, "json")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if got == nil {
			t.Error("expected non-nil result")
		}
	})
	t.Run("already decoded map passes through", func(t *testing.T) {
		m := map[string]any{"a": 1}
		got, err := CoerceType(m, "json")
		if err != nil || got == nil {
			t.Errorf("got %v, %v", got, err)
		}
	})
}

func TestCoerceType_Unknown(t *testing.T) {
	_, err := CoerceType("something", "unknowntype")
	if err == nil {
		t.Error("expected error for unknown type name")
	}
}

// ── DecodeAndCoerceFromUser ───────────────────────────────────────────────────

func TestDecodeAndCoerceFromUser(t *testing.T) {
	cfg := basicModel()

	t.Run("coerces known fields from JSON keys to field names", func(t *testing.T) {
		raw := map[string]any{"id": "5", "code": "ABC", "count": "10", "active": "true"}
		got, err := DecodeAndCoerceFromUser(raw, &cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got["id"] != 5 {
			t.Errorf("id: got %v, want 5", got["id"])
		}
		if got["code"] != "ABC" {
			t.Errorf("code: got %v, want ABC", got["code"])
		}
		if got["count"] != 10 {
			t.Errorf("count: got %v, want 10", got["count"])
		}
		if got["active"] != true {
			t.Errorf("active: got %v, want true", got["active"])
		}
	})

	t.Run("missing field is omitted from result", func(t *testing.T) {
		raw := map[string]any{"code": "ABC"}
		got, err := DecodeAndCoerceFromUser(raw, &cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, exists := got["name"]; exists {
			t.Error("expected 'name' to be absent from result")
		}
	})

	t.Run("unknown JSON keys are ignored", func(t *testing.T) {
		raw := map[string]any{"code": "ABC", "not_in_config": "ignore_me"}
		got, err := DecodeAndCoerceFromUser(raw, &cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, exists := got["not_in_config"]; exists {
			t.Error("unknown key should not appear in result")
		}
	})

	t.Run("coercion error returns error", func(t *testing.T) {
		raw := map[string]any{"count": "not-a-number"}
		_, err := DecodeAndCoerceFromUser(raw, &cfg)
		if err == nil {
			t.Error("expected error for unconvertable type")
		}
	})

	t.Run("json-alias fallback is used", func(t *testing.T) {
		cfgWithAlias := basicModel()
		f := cfgWithAlias.Fields["name"]
		f.JSON_alias = []string{"full_name", "display_name"}
		cfgWithAlias.Fields["name"] = f

		raw := map[string]any{"full_name": "John"}
		got, err := DecodeAndCoerceFromUser(raw, &cfgWithAlias)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got["name"] != "John" {
			t.Errorf("alias not used: got %v", got["name"])
		}
	})

	t.Run("primary json key takes precedence over alias", func(t *testing.T) {
		cfgWithAlias := basicModel()
		f := cfgWithAlias.Fields["name"]
		f.JSON_alias = []string{"full_name"}
		cfgWithAlias.Fields["name"] = f

		raw := map[string]any{"name": "Primary", "full_name": "Alias"}
		got, err := DecodeAndCoerceFromUser(raw, &cfgWithAlias)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got["name"] != "Primary" {
			t.Errorf("primary JSON key not used: got %v", got["name"])
		}
	})
}
