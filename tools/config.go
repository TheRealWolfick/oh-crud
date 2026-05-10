package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"gopkg.in/yaml.v3"
	"lotusforge.au/api-server/models"
)

// ── Config loading ────────────────────────────────────────────────────────────

func LoadYAMLIntoModel[T any](path string) (*T, error) {
	if filepath.Ext(path) == ".yaml" {
		model := new(T)
		file, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		err = yaml.Unmarshal(file, &model)
		if err != nil {
			return nil, err
		}
		return model, nil
	}
	return nil, errors.New("File passed was not a yaml file")
}

func DecodeFieldType(typ string) (reflect.Kind, error) {
	switch typ {
	case "int":
		return reflect.Int, nil
	case "float":
		return reflect.Float64, nil
	case "string":
		return reflect.String, nil
	case "bool":
		return reflect.Bool, nil
	}
	return reflect.Invalid, fmt.Errorf("Unsupported data format %s", typ)
}

// GetDiffComparatorKey returns the YAML field name of the diff comparator.
func GetDiffComparatorKey(cfg *models.DataModel) string {
	if cfg.Diff_comparator != nil && *cfg.Diff_comparator != "" {
		return *cfg.Diff_comparator
	}
	return ""
}

// BuildExcludeKeysFromConfig returns the set of DB column names that should be excluded
// from diff comparison — those with include-in-diff: false.
func BuildExcludeKeysFromConfig(cfg *models.DataModel) map[string]bool {
	excluded := map[string]bool{}
	for field_name, field_cfg := range cfg.Fields {
		if field_cfg.Include_in_diff != nil && !*field_cfg.Include_in_diff {
			excluded[field_name] = true
		}
	}
	return excluded
}

// GetInsertRequiredFields returns the JSON keys of all fields marked required-on-insert.
func GetInsertRequiredFields(cfg *models.DataModel) []string {
	req_fields := []string{}
	for _, field_cfg := range cfg.Fields {
		if field_cfg.Required_on_insert != nil && *field_cfg.Required_on_insert && field_cfg.JSON != nil {
			req_fields = append(req_fields, *field_cfg.JSON)
		}
	}
	return req_fields
}

// Get the field that is used for the history function
func GetHistoryUniqueField(cfg *models.DataModel) string {
	if cfg.Track_history_field != nil {
		return *cfg.Track_history_field
	}
	return *cfg.Primary_key
}

// GetUpdateKeyOptions returns all valid key sets (PK + each unique key) expressed as JSON keys.
// Currently does not handle json aliases.
func GetUpdateKeyOptions(cfg *models.DataModel) [][]string {
	options := [][]string{}

	if cfg.Primary_key != nil {
		if field, ok := cfg.Fields[*cfg.Primary_key]; ok && field.JSON != nil {
			options = append(options, []string{*field.JSON})
		}
	}

	// _ is the unique key name, uk is the *models.UniqueKey which is a list of all the required
	// fields for the unique key. 
	for _, uk := range cfg.Unique_keys {
		// Option is a list of all the required fields for this key to be fulfiled
		option := []string{}
		valid := true

		// Iterate through each key of the unique key
		for _, field_name := range uk.Fields {

			// Check that this field is in the data field
			field, ok := cfg.Fields[field_name]
			if !ok || field.JSON == nil {
				valid = false
				break
			}
			option = append(option, *field.JSON)
		}

		// Check that there is at least one valid option for the unique identifier and append if yes
		if valid && len(option) > 0 {
			options = append(options, option)
		}
	}

	return options
}

// FindRowKeyFields locates the key fields present in a coerced row for use as a WHERE clause.
// Tries the primary key first, then each unique key set.
func FindRowKeyFields(row map[string]any, cfg *models.DataModel) ([]string, bool) {
	if cfg.Primary_key != nil {
		if _, ok := row[*cfg.Primary_key]; ok {
			return []string{*cfg.Primary_key}, true
		}
	}
	for _, uk := range cfg.Unique_keys {
		all_present := true
		for _, field_name := range uk.Fields {
			if _, ok := row[field_name]; !ok {
				all_present = false
				break
			}
		}
		if all_present && len(uk.Fields) > 0 {
			return uk.Fields, true
		}
	}
	return nil, false
}

// CheckFieldExists validates that a key matches a field by name, JSON key, or alias.
// Returns the field name (DB column name) if found.
func CheckFieldExists(key string, cfg *models.DataModel) (string, bool) {
	for field_name, field_cfg := range cfg.Fields {
		if key == field_name || key == *field_cfg.JSON {
			return field_name, true
		}
		if len(field_cfg.JSON_alias) > 0 {
			if slices.Contains(field_cfg.JSON_alias, key) {
				return field_name, true
			}
		}
	}
	return "", false
}

// CheckFieldGetValid validates is very similar to CheckFieldExists in that a key matches a field 
// by name, JSON key, or alias, but also checks that it is not a private field. Returns the field name
func CheckFieldGetValid(key string, cfg *models.DataModel) (string, bool) {
	for field_name, field_cfg := range cfg.Fields {
		if key == field_name || key == *field_cfg.JSON {
			if field_cfg.Private != nil && *field_cfg.Private { return "", false }
			return field_name, true
		}
		if len(field_cfg.JSON_alias) > 0 {
			if slices.Contains(field_cfg.JSON_alias, key) {
				if field_cfg.Private != nil && *field_cfg.Private { return "", false }
				return field_name, true
			}
		}
	}
	return "", false
}


// ── DataModel constructors and coercion ───────────────────────────────────────

func NewDataModel() *models.DataModel {
	return &models.DataModel{}
}

// DecodeAndCoerceFromUser type-coerces a raw JSON map against the DataModel config.
// Returns a new map keyed by YAML field name (= DB column name).
func DecodeAndCoerceFromUser(raw map[string]any, cfg *models.DataModel) (map[string]any, error) {
	errors := []string{}
	row_data := map[string]any{}

	// Read the field name and its configuration for each field
	for field_name, field_cfg := range cfg.Fields {
		// Quick continue if the field is not one that data can be sent into be the user
		if field_cfg.JSON == nil {
			continue
		}

		// Check if this field is in the raw data
		val, exists := raw[*field_cfg.JSON]

		// If it was not field in the raw data, check if any aliases for this field are in the data
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

		// If it does not exist or it does not have a type, there is nothing to be coerced
		if !exists { continue }
		if field_cfg.Type == nil { continue }

		// Coerce the value based on its type
		coerced_val, err := CoerceType(val, *field_cfg.Type)
		if err != nil {
			errors = append(errors, fmt.Sprintf("field %q: %s", field_name, err.Error()))
		} else {
			row_data[field_name] = coerced_val
		}

		// Check that no rule violations have occured
		var rule_errs string
		var valid bool

		if field_cfg.Rules != nil {
			switch *field_cfg.Type {
			case "float":
				rule_errs, valid = ValidateFloatRules(field_name, coerced_val.(float64), field_cfg.Rules)
			case "int":
				rule_errs, valid = ValidateIntRules(field_name, coerced_val.(int), field_cfg.Rules)
			case "string":
				rule_errs, valid = ValidateStringRules(field_name, coerced_val.(string), field_cfg.Rules)
			default:
				valid = true
			}
			if !valid {
				errors = append(errors, rule_errs)
			}
		}

	}
	if len(errors) > 0 { return nil, fmt.Errorf("ERRORS:: %s", strings.Join(errors, "; ")) }
	return row_data, nil
}

// DecodeAndCoerceFromDB type-coerces DB output against the DataModel config.
// Non-critical fields that fail coercion retain their raw value.
func DecodeAndCoerceFromDB(raw map[string]any, cfg *models.DataModel, comparatorKey string) (map[string]any, error) {
	row_data := map[string]any{}

	for field_name, field_cfg := range cfg.Fields {
		val, exists := raw[field_name]
		if !exists {
			continue
		}
		coerced_val, err := CoerceType(val, *field_cfg.Type)
		if err != nil {
			if field_name == comparatorKey {
				return nil, fmt.Errorf("failed to coerce comparator key %s: %w", field_name, err)
			}
			row_data[field_name] = val
			continue
		}
		row_data[field_name] = coerced_val
	}
	return row_data, nil
}

// CoerceType converts a raw value to the named Go type.
// Supports: "int", "float", "string", "bool", "json", "time", "uuid"
func CoerceType(raw any, type_name string) (any, error) {
	if raw == nil {
		return nil, nil
	}

	switch type_name {

	case "int":
		switch v := raw.(type) {
		case int:
			return v, nil
		case int16:
			return int(v), nil
		case int32:
			return int(v), nil
		case int64:
			return int(v), nil
		case uint:
			return int(v), nil
		case uint16:
			return int(v), nil
		case uint32:
			return int(v), nil
		case uint64:
			return int(v), nil
		case float64:
			return int(v), nil
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
		case float64:
			return v, nil
		case float32:
			return float64(v), nil
		case int:
			return float64(v), nil
		case int32:
			return float64(v), nil
		case int64:
			return float64(v), nil
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
		case string:
			return v, nil
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
	case bool:
		return v, nil
	case float64:
		return v != 0, nil
	case int:
		return v != 0, nil
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

// ── Version management ────────────────────────────────────────────────────────

func CheckVersionIncrease(old string, now string) (bool, error) {
	oldParts, err := parseVersion(old)
	if err != nil {
		return false, fmt.Errorf("invalid current version %q: %w", old, err)
	}
	newParts, err := parseVersion(now)
	if err != nil {
		return false, fmt.Errorf("invalid new version %q: %w", now, err)
	}

	if newParts[0] > oldParts[0] {
		return true, nil
	}
	if newParts[0] == oldParts[0] && newParts[1] > oldParts[1] {
		return true, nil
	}
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

// ── Model validation ──────────────────────────────────────────────────────────

// ValidateDataModel checks a fully loaded DataModel config for structural correctness.
// Call this after YAML unmarshalling and before any DB or handler registration.
func ValidateDataModel(m models.DataModel) error {
	var errs []string

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
	//if m.End_point == nil || strings.TrimSpace(*m.End_point) == "" {
	//	errs = append(errs, "end-point is required")
	//}
	if len(m.Fields) == 0 {
		errs = append(errs, "at least one field is required")
	}

	if m.Primary_key == nil || strings.TrimSpace(*m.Primary_key) == "" {
		errs = append(errs, "primary-key is required")
	} else if _, ok := m.Fields[*m.Primary_key]; !ok {
		errs = append(errs, fmt.Sprintf("primary-key %q does not match any field name", *m.Primary_key))
	}

	validTypes := map[string]bool{
		"int": true, "float": true, "string": true,
		"bool": true, "json": true, "time": true, "uuid": true,
	}
	validDBTypes := map[string]bool{
		"int": true, "integer": true, "bigint": true, "smallint": true,
		"boolean": true, "jsonb": true, "json": true, "uuid": true,
		"timestamptz": true, "text": true, "numeric": true, "timestamp without time zone": true,
		"timestamp with time zone": true, "date": true, "varchar": true, "character varying": true,
		"char": true, "character": true, "decimal": true, "serial": true, "smallserial": true,
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
			baseType := strings.ToLower(strings.Split(*field.DB_type, "(")[0])
			if !validDBTypes[baseType] && baseType != "varchar" {
				errs = append(errs, fmt.Sprintf("%s: unknown db-type %q", prefix, *field.DB_type))
			}
		}

		//if field.JSON == nil || strings.TrimSpace(*field.JSON) == "" {
		//	errs = append(errs, fmt.Sprintf("%s: json is required", prefix))
		//}

		if field.Migration != nil && !validMigrations[*field.Migration] {
			errs = append(errs, fmt.Sprintf("%s: unknown migration strategy %q", prefix, *field.Migration))
		}

		if m.Primary_key != nil && fieldName == *m.Primary_key {
			if field.Nullable != nil && *field.Nullable {
				errs = append(errs, fmt.Sprintf("%s: primary key field cannot be nullable", prefix))
			}
		}
	}

	for fkName, fk := range m.Foreign_keys {
		prefix := fmt.Sprintf("foreign-keys.%s", fkName)

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

	if m.Allow_diff != nil && *m.Allow_diff {
		if m.Diff_comparator == nil || strings.TrimSpace(*m.Diff_comparator) == "" {
			errs = append(errs, "diff-comparator is required when allow-diff is true")
		} else if _, ok := m.Fields[*m.Diff_comparator]; !ok {
			errs = append(errs, fmt.Sprintf("diff-comparator %q does not match any field name", *m.Diff_comparator))
		}
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

// ── User validation ───────────────────────────────────────────────────────────

// ValidateUserRequest validates and sanitizes a UserRequest, ensuring username is present
// and within the allowed length.
func ValidateUserRequest(r *models.UserRequest) error {
	r.Username = strings.TrimSpace(r.Username)
	r.Api_Key = strings.TrimSpace(r.Api_Key)

	if r.Username == "" {
		return errors.New("A username must be supplied.")
	}
	if len(r.Username) > 20 {
		return errors.New("Username too long")
	}
	return nil
}

// ── Internal helpers ──────────────────────────────────────────────────────────

func ptrStr(s *string, fallback string) string {
	if s == nil {
		return fallback
	}
	return *s
}

// ── Config readers ────────────────────────────────────────────────────────────

// DynamicGetDatabaseColumns returns DB column names from the DataModel config.
// Pass pk_only=true for only the primary key, req_only=true for required+PK columns,
// or both false for all columns.
func DynamicGetDatabaseColumns(cfg *models.DataModel, pk_only bool, req_only bool) []string {
	database_columns := []string{}
	pk := ""
	if cfg.Primary_key != nil {
		pk = *cfg.Primary_key
	}

	for field_name, field_cfg := range cfg.Fields {
		if pk_only || req_only {
			if pk_only {
				if field_name == pk {
					database_columns = append(database_columns, field_name)
				}
			} else {
				is_pk := field_name == pk
				is_req := field_cfg.Required_on_insert != nil && *field_cfg.Required_on_insert
				if is_pk || is_req {
					database_columns = append(database_columns, field_name)
				}
			}
		} else {
			if field_cfg.Private != nil && *field_cfg.Private {
				continue
			}
			database_columns = append(database_columns, field_name)
		}
	}
	return database_columns
}
