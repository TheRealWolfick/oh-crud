package models

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)


type ConfigError struct {
	Field    string
	Message  string
}

type ForeignKey struct {
	Table          *string   `yaml:"foreign-key-table"`
	Fields         []string  `yaml:"foreign-key-fields"`
	Target_table   *string   `yaml:"foreign-key-target-table"`
	Target_fields  []string  `yaml:"foreign-key-target-fields"`
	ON_UPDATE      *string   `yaml:"foreign-key-on-update"`
	ON_DELETE      *string   `yaml:"foreign-key-on-delete"`
}

type UniqueKey struct {
	Fields  []string  `yaml:"unique-key-fields"`
}

// DataModelField describes the schema for a single field within a DataModel.
// Values are populated from the corresponding YAML config file.
type DataModelField struct {
	// Field metadata
	Type                *string  `yaml:"type"`
	JSON                *string  `yaml:"json"`
	JSON_alias          []string `yaml:"json-alias"`
	Include_in_diff     *bool    `yaml:"include-in-diff"`
	Required_on_insert  *bool    `yaml:"required-on-insert"`
	Absolute_match      *bool    `yaml:"absolute-match"`
	Skip_insert         *bool    `yaml:"skip-insert"`
	// Database metadata
	DB_type             *string  `yaml:"db-type"`
	Nullable            *bool    `yaml:"nullable"`
	Default             *string  `yaml:"default"`
	// Atlas metadata
	Migration           *string  `yaml:"migration"`
}

// End_pointsAllowed controls which HTTP methods are enabled for a given End_point.
type End_pointsAllowed struct {
	GET           *bool  `yaml:"GET"`
	PUT           *bool  `yaml:"PUT"`
	POST 	        *bool  `yaml:"POST"`
	DELETE        *bool  `yaml:"DELETE"`
	PUT_GROUP     *bool  `yaml:"PUT-GROUP"`
	POST_GROUP    *bool  `yaml:"POST-GROUP"`
	DELETE_GROUP  *bool  `yaml:"DELETE-GROUP"`
}

// DataModel is the top-level representation of a YAML config file.
// Each file in config/base-models/ and config/special-models/ is loaded into one of these.
type DataModel struct {
	// Model metadata
	Name                *string                    `yaml:"name"`
	Type                *string                    `yaml:"type"`
	Version             *string                    `yaml:"version"`
	// Database metadata
	Table_name          *string                    `yaml:"table-name"`
  End_point           *string                    `yaml:"end-point"`
	End_points_allowed  *End_pointsAllowed          `yaml:"end-points-allowed"`
	Allow_diff          *bool                      `yaml:"allow-diff"`
	Diff_comparator     *string                    `yaml:"diff-comparator"`
	Primary_key         *string                    `yaml:"primary-key"`
	Foreign_keys        map[string]ForeignKey      `yaml:"foreign-keys"`
	Unique_keys         map[string]UniqueKey       `yaml:"unique-keys"`
	Fields              map[string]DataModelField  `yaml:"fields"`
}

func NewDataModel() *DataModel {
	return &DataModel{}
}

// DecodeAndCoerceFromUser type-coerces a raw JSON map against the DataModel config.
// Returns a new map keyed by YAML field name (= DB column name) with all values cast to
// their declared types. Presence validation (required fields, key fields) is the caller's
// responsibility — use Validate_SliceOfMaps_AgainstConfig for that.
func DecodeAndCoerceFromUser(raw map[string]any, cfg *DataModel) (map[string]any, error) {
	row_data := map[string]any{}

	for field_name, field_cfg := range cfg.Fields {
		if field_cfg.JSON == nil {
			continue
		}
		// Check primary JSON key
		val, exists := raw[*field_cfg.JSON]

		// Check field aliases
		if !exists && field_cfg.JSON_alias != nil {
			for _, alias := range field_cfg.JSON_alias {
				v, e := raw[alias]
				if e {
					val = v
					exists = true
					break
				}
			}
		}

		if !exists {
			continue
		}
		if field_cfg.Type == nil {
			continue
		}
		coerced_val, err := CoerceType(val, *field_cfg.Type)
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", field_name, err)
		}

		row_data[field_name] = coerced_val
	}
	return row_data, nil
}

func DecodeAndCoerceFromDB(raw map[string]any, cfg *DataModel, comparatorKey string) (map[string]any, error) {
    row_data := map[string]any{}

    for field_name, field_cfg := range cfg.Fields {
        val, exists := raw[field_name]
        if !exists {
            continue
        }

        coerced_val, err := CoerceType(val, *field_cfg.Type)
        if err != nil {
            if field_name == comparatorKey {
                // Can't match this row without a comparator, skip it
                return nil, fmt.Errorf("failed to coerce comparator key %s: %w", field_name, err)
            }
            // Non-critical field — keep the raw value and move on
            row_data[field_name] = val
            continue
        }

        row_data[field_name] = coerced_val
    }
    return row_data, nil
}

// CoerceType converts a raw value to the named Go type.
// Supports: "int", "float", "string", "bool", "json", "time", "uuid"
// Returns nil for nil inputs and an error if the conversion fails.
func CoerceType(raw any, type_name string) (any, error) {
    if raw == nil {
        return nil, nil
    }

    switch type_name {

    case "int":
        switch v := raw.(type) {
        case int:     return v, nil
        case int16:   return int(v), nil // pgx smallint
        case int32:   return int(v), nil // pgx integer
        case int64:   return int(v), nil // pgx bigint
        case uint:    return int(v), nil
        case uint16:  return int(v), nil
        case uint32:  return int(v), nil
        case uint64:  return int(v), nil
        case float64: return int(v), nil
        case string:
            if v == "" {
                return nil, nil
            }
            return strconv.Atoi(v)
        case pgtype.Numeric:
            if !v.Valid {
                return nil, nil
            }
            f, err := v.Float64Value()
            if err != nil || !f.Valid {
                return nil, nil
            }
            return int(f.Float64), nil
        }

    case "float":
        switch v := raw.(type) {
        case float64: return v, nil
        case float32: return float64(v), nil
        case int:     return float64(v), nil
        case int32:   return float64(v), nil
        case int64:   return float64(v), nil
        case string:
            if v == "" {
                return nil, nil
            }
            return strconv.ParseFloat(v, 64)
        case pgtype.Numeric:
            if !v.Valid {
                return nil, nil
            }
            f, err := v.Float64Value()
            if err != nil || !f.Valid {
                return nil, nil
            }
            return f.Float64, nil
        }

    case "string":
        switch v := raw.(type) {
        case string: return v, nil
        case pgtype.Text:
            if !v.Valid {
                return nil, nil
            }
            return v.String, nil
        case pgtype.Timestamptz:
            if !v.Valid {
                return nil, nil
            }
            return v.Time.UTC().Format(time.RFC3339), nil
        case pgtype.UUID:
            if !v.Valid {
                return nil, nil
            }
            return fmt.Sprintf("%x-%x-%x-%x-%x",
                v.Bytes[0:4], v.Bytes[4:6], v.Bytes[6:8], v.Bytes[8:10], v.Bytes[10:16],
            ), nil
        case time.Time:
            return v.UTC().Format(time.RFC3339), nil
        case []byte:
            return string(v), nil
        default:
            return fmt.Sprintf("%v", v), nil
        }

    case "bool":
        switch v := raw.(type) {
        case bool:    return v, nil
        case float64: return v != 0, nil
        case int:     return v != 0, nil
        case string:
            if v == "" {
                return nil, nil
            }
            return strconv.ParseBool(v)
        case pgtype.Bool:
            if !v.Valid {
                return nil, nil
            }
            return v.Bool, nil
        }

    case "json":
        switch v := raw.(type) {
        case []byte:
            var out any
            if err := json.Unmarshal(v, &out); err != nil {
                return nil, fmt.Errorf("json unmarshal failed: %w", err)
            }
            return out, nil
        case string:
            if v == "" {
                return nil, nil
            }
            var out any
            if err := json.Unmarshal([]byte(v), &out); err != nil {
                return nil, fmt.Errorf("json unmarshal failed: %w", err)
            }
            return out, nil
        case map[string]any, []any:
            // Already a decoded JSON structure
            return v, nil
        }

    case "time":
        switch v := raw.(type) {
        case time.Time:
            return v.UTC(), nil
        case pgtype.Timestamptz:
            if !v.Valid {
                return nil, nil
            }
            return v.Time.UTC(), nil
        case string:
            if v == "" {
                return nil, nil
            }
            t, err := time.Parse(time.RFC3339, v)
            if err != nil {
                return nil, fmt.Errorf("could not parse time string %q: %w", v, err)
            }
            return t.UTC(), nil
        }

    case "uuid":
        switch v := raw.(type) {
        case string:
            if v == "" {
                return nil, nil
            }
            // Basic UUID format check
            if len(v) != 36 {
                return nil, fmt.Errorf("invalid UUID string: %q", v)
            }
            return v, nil
        case pgtype.UUID:
            if !v.Valid {
                return nil, nil
            }
            return fmt.Sprintf("%x-%x-%x-%x-%x",
                v.Bytes[0:4], v.Bytes[4:6], v.Bytes[6:8], v.Bytes[8:10], v.Bytes[10:16],
            ), nil
        case [16]byte:
            return fmt.Sprintf("%x-%x-%x-%x-%x",
                v[0:4], v[4:6], v[6:8], v[8:10], v[10:16],
            ), nil
        }
    }

    return nil, fmt.Errorf("could not convert %v (%T) into type %s", raw, raw, type_name)
}


func CheckVersionIncrease(old string, now string) (bool, error) {
    oldParts, err := parseVersion(old)
    if err != nil {
        return false, fmt.Errorf("invalid current version %q: %w", old, err)
    }
    newParts, err := parseVersion(now)
    if err != nil {
        return false, fmt.Errorf("invalid new version %q: %w", now, err)
    }

    if newParts[0] > oldParts[0] { return true, nil }
    if newParts[0] == oldParts[0] && newParts[1] > oldParts[1] { return true, nil }
    if newParts[0] == oldParts[0] && newParts[1] == oldParts[1] && newParts[2] > oldParts[2] {
        return true, nil
    }

    return false, fmt.Errorf("new version %q is not greater than current version %q", now, old)
}

func parseVersion(v string) ([3]int, error) {
    parts := strings.Split(v, ".")
    if len(parts) != 3 {
        return [3]int{}, fmt.Errorf("expected major.minor.incremental format")
    }
    var out [3]int
    for i, p := range parts {
        n, err := strconv.Atoi(p)
        if err != nil {
            return [3]int{}, fmt.Errorf("part %d is not an integer: %q", i, p)
        }
        out[i] = n
    }
    return out, nil
}

// ValidateDataModel checks a fully loaded DataModel config for structural correctness.
// This should be called after YAML unmarshalling and before any DB or handler registration.
func ValidateDataModel(m DataModel) error {
    var errs []string

    // ── Top-level required fields ──────────────────────────────────────────────

    if m.Name == nil || strings.TrimSpace(*m.Name) == "" {
        errs = append(errs, "name is required")
    }
    if m.Version == nil || strings.TrimSpace(*m.Version) == "" {
        errs = append(errs, "version is required")
    } else if _, err := parseVersion(*m.Version); err != nil {
        errs = append(errs, fmt.Sprintf("version: %v", err))
    }
    if m.Table_name == nil || strings.TrimSpace(*m.Table_name) == "" {
        errs = append(errs, "table-name is required")
    }
    if m.End_point == nil || strings.TrimSpace(*m.End_point) == "" {
        errs = append(errs, "end-point is required")
    }
    if len(m.Fields) == 0 {
        errs = append(errs, "at least one field is required")
    }

    // ── Primary key ───────────────────────────────────────────────────────────

    if m.Primary_key == nil || strings.TrimSpace(*m.Primary_key) == "" {
        errs = append(errs, "primary-key is required")
    } else if _, ok := m.Fields[*m.Primary_key]; !ok {
        errs = append(errs, fmt.Sprintf("primary-key %q does not match any field name", *m.Primary_key))
    }

    // ── Fields ────────────────────────────────────────────────────────────────

    validTypes := map[string]bool{
        "int": true, "float": true, "string": true,
        "bool": true, "json": true, "time": true, "uuid": true,
    }
    validDBTypes := map[string]bool{
        "int": true, "bigint": true, "smallint": true,
        "boolean": true, "jsonb": true, "uuid": true,
        "timestamptz": true, "text": true, "numeric": true,
    }
    validMigrations := map[string]bool{
        "alter": true, "skip": true, "recreate": true,
    }

    for fieldName, field := range m.Fields {
        prefix := fmt.Sprintf("fields.%s", fieldName)

        if field.Type == nil || strings.TrimSpace(*field.Type) == "" {
            errs = append(errs, fmt.Sprintf("%s: type is required", prefix))
        } else if !validTypes[*field.Type] {
            errs = append(errs, fmt.Sprintf("%s: unknown type %q", prefix, *field.Type))
        }

        if field.DB_type == nil || strings.TrimSpace(*field.DB_type) == "" {
            errs = append(errs, fmt.Sprintf("%s: db-type is required", prefix))
        } else {
            // Strip size parameter for validation e.g. varchar(255) -> varchar
            baseType := strings.ToLower(strings.Split(*field.DB_type, "(")[0])
            if !validDBTypes[baseType] && baseType != "varchar" {
                errs = append(errs, fmt.Sprintf("%s: unknown db-type %q", prefix, *field.DB_type))
            }
        }

        if field.JSON == nil || strings.TrimSpace(*field.JSON) == "" {
            errs = append(errs, fmt.Sprintf("%s: json is required", prefix))
        }

        if field.Migration != nil && !validMigrations[*field.Migration] {
            errs = append(errs, fmt.Sprintf("%s: unknown migration strategy %q", prefix, *field.Migration))
        }

        // Primary key fields must not be nullable
        if m.Primary_key != nil && fieldName == *m.Primary_key {
            if field.Nullable != nil && *field.Nullable {
                errs = append(errs, fmt.Sprintf("%s: primary key field cannot be nullable", prefix))
            }
        }
    }

    // ── Foreign keys ──────────────────────────────────────────────────────────

    for fkName, fk := range m.Foreign_keys {
        prefix := fmt.Sprintf("foreign-keys.%s", fkName)

        if fk.Table == nil || strings.TrimSpace(*fk.Table) == "" {
            errs = append(errs, fmt.Sprintf("%s: foreign-key-table is required", prefix))
        }
        if fk.Target_table == nil || strings.TrimSpace(*fk.Target_table) == "" {
            errs = append(errs, fmt.Sprintf("%s: foreign-key-target-table is required", prefix))
        }
        if len(fk.Fields) == 0 {
            errs = append(errs, fmt.Sprintf("%s: foreign-key-fields cannot be empty", prefix))
        }
        if len(fk.Target_fields) == 0 {
            errs = append(errs, fmt.Sprintf("%s: foreign-key-target-fields cannot be empty", prefix))
        }
        if len(fk.Fields) != len(fk.Target_fields) {
            errs = append(errs, fmt.Sprintf("%s: foreign-key-fields and foreign-key-target-fields must have the same length", prefix))
        }

        // Verify FK fields exist in this model
        for _, f := range fk.Fields {
            if _, ok := m.Fields[f]; !ok {
                errs = append(errs, fmt.Sprintf("%s: foreign-key-field %q does not match any field name", prefix, f))
            }
        }

        validActions := map[string]bool{
            "CASCADE": true, "SET NULL": true, "SET DEFAULT": true,
            "RESTRICT": true, "NO ACTION": true,
        }
        if fk.ON_UPDATE != nil && !validActions[strings.ToUpper(*fk.ON_UPDATE)] {
            errs = append(errs, fmt.Sprintf("%s: unknown on-update action %q", prefix, *fk.ON_UPDATE))
        }
        if fk.ON_DELETE != nil && !validActions[strings.ToUpper(*fk.ON_DELETE)] {
            errs = append(errs, fmt.Sprintf("%s: unknown on-delete action %q", prefix, *fk.ON_DELETE))
        }
    }

    // ── Unique keys ───────────────────────────────────────────────────────────

    for ukName, uk := range m.Unique_keys {
        prefix := fmt.Sprintf("unique-keys.%s", ukName)

        if len(uk.Fields) == 0 {
            errs = append(errs, fmt.Sprintf("%s: unique-key-fields cannot be empty", prefix))
        }
        for _, f := range uk.Fields {
            if _, ok := m.Fields[f]; !ok {
                errs = append(errs, fmt.Sprintf("%s: unique-key-field %q does not match any field name", prefix, f))
            }
        }
    }

    // ── Diff config ───────────────────────────────────────────────────────────

    if m.Allow_diff != nil && *m.Allow_diff {
        if m.Diff_comparator == nil || strings.TrimSpace(*m.Diff_comparator) == "" {
            errs = append(errs, "diff-comparator is required when allow-diff is true")
        } else if _, ok := m.Fields[*m.Diff_comparator]; !ok {
            errs = append(errs, fmt.Sprintf("diff-comparator %q does not match any field name", *m.Diff_comparator))
        }
        // At least one field must have include-in-diff: true
        hasDiffField := false
        for _, f := range m.Fields {
            if f.Include_in_diff != nil && *f.Include_in_diff {
                hasDiffField = true
                break
            }
        }
        if !hasDiffField {
            errs = append(errs, "allow-diff is true but no fields have include-in-diff: true")
        }
    }

    if len(errs) > 0 {
        return fmt.Errorf("invalid config for %q:\n  - %s", ptrStr(m.Name, "unknown"), strings.Join(errs, "\n  - "))
    }
    return nil
}

func ptrStr(s *string, fallback string) string {
    if s == nil {
        return fallback
    }
    return *s
}
