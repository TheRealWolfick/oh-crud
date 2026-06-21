package tools

import (
	"fmt"
	"reflect"
	"strings"

	"lotusforge.au/api-server/models"
)

// Validate_Map_AgainstConfig is a single-row wrapper around Validate_SliceOfMaps_AgainstConfig.
//
// Return: []valid, []invalid
func Validate_Map_AgainstConfig(cfg *models.DataModel, m map[string]any, require_key_fields bool, require_insert_fields bool) ([]map[string]any, []map[string]any) {
	return Validate_SliceOfMaps_AgainstConfig(cfg, []map[string]any{m}, require_key_fields, require_insert_fields)
}

// Validate_SliceOfMaps_AgainstConfig validates and coerces a slice of raw JSON maps against
// the DataModel config. Each row is checked for:
//   - require_key_fields: presence of at least one complete key (PK or any unique key)
//   - require_insert_fields: presence of all required-on-insert fields
//
// Required-field checking respects json_alias: a field is considered present if either its
// primary JSON key or any of its aliases is found in the row.
//
// Rows that pass both checks are coerced via DecodeAndCoerceFromUser; coercion failure = invalid.
//
// Return: []valid (coerced, keyed by field name), []invalid (original raw maps)
func Validate_SliceOfMaps_AgainstConfig(cfg *models.DataModel, rows []map[string]any, require_identify_unique bool, require_insert_fields bool) ([]map[string]any, []map[string]any) {
	if len(rows) < 1 {
		return []map[string]any{}, []map[string]any{}
	}

	// Get all possible json combinations that ensure uniqueness
	unique_identifiers := [][]string{}
	if require_identify_unique {
		unique_identifiers = GetUpdateKeyOptions(cfg)
	}

	// Build required field entries: each entry holds the primary json key + aliases.
	type requiredField struct {
		json_key string
		aliases  []string
	}

	// Get all the fields required for an insert statement
	insert_required := []requiredField{}
	if require_insert_fields {
		for _, field_cfg := range cfg.Fields {
			if field_cfg.Required_on_insert != nil && *field_cfg.Required_on_insert && field_cfg.JSON != nil {
				insert_required = append(insert_required, requiredField{
					json_key: *field_cfg.JSON,
					aliases:  field_cfg.JSON_alias,
				})
			}
		}
	}

	valid_structs := []map[string]any{}
	invalid_structs := []map[string]any{}

	// Begin checking validity of row
	for _, row := range rows {
		is_valid := true
		err := []string{}

		// Check required-on-insert fields (primary key or any alias that satisfies the check)
		if require_insert_fields {
			for _, req := range insert_required {
				found := false
				if _, exists := row[req.json_key]; exists {
					found = true
				} else {
					for _, alias := range req.aliases {
						if _, exists := row[alias]; exists {
							found = true
							break
						}
					}
				}
				if !found {
					is_valid = false
					err = append(err, fmt.Sprintf("Missing field: %s", req.json_key))
					break
				}
			}
		}

		// Check that at least one key set (PK or unique key) is fully present
		if require_identify_unique {
			key_found := false
			// For each list of fields that identify uniqueness
			for _, option := range unique_identifiers {
				all_present := true
				// For each required json field name
				for _, json_key := range option {
					// If it doesn't exist, break to next option
					if _, exists := row[json_key]; !exists {
						all_present = false
						break
					}
				}
				// All were present, thus update flag to identify that a key was found
				if all_present {
					key_found = true
					break
				}
			}
			// Check that there was at least one key found in the list of options.
			if !key_found {
				is_valid = false

				// Build and append error message with all the possible unique identifiers
				err_msgs := []string{}
				for _, e := range unique_identifiers {
					err_msgs = append(err_msgs, strings.Join(e, "+"))
				}
				err = append(err, fmt.Sprintf("Missing at least one valid unique identifying construct: [%s]", strings.Join(err_msgs, ", ")))
			}
		}

		// If data so far is valid (has not been flagged previously as invalid)
		if is_valid {
			// Decode and coerce the row values
			coerced_vals, err := DecodeAndCoerceFromUser(row, cfg)
			if err != nil {
				invalid_structs = append(invalid_structs, map[string]any{"data": row, "reasons": err.Error()})
			} else {
				valid_structs = append(valid_structs, coerced_vals)
			}
		} else {
			invalid_structs = append(invalid_structs, map[string]any{"data": row, "reasons": err})
		}
	}

	return valid_structs, invalid_structs
}

func SetValueFromMap(qb *QueryBuilder, m map[string]any) {
	setFromMap(m, qb.SetValue)
}

// SetValueAndWhereFromMap sets fields into the query builder.
// Fields whose names appear in where_fields go to SetWhereAbsolute; all others go to SetValue.
func SetValueAndWhereFromMap(qb *QueryBuilder, m map[string]any, where_fields []string) {
	where_set := map[string]bool{}
	for _, f := range where_fields {
		where_set[f] = true
	}
	for k, v := range m {
		if where_set[k] {
			qb.SetWhereAbsolute(k, v)
		} else {
			qb.SetValue(k, v)
		}
	}
}

// setFromMap is an internal helper that calls setFunc for every key-value pair in m.
func setFromMap(m map[string]any, setFunc func(string, any)) {
	for k, v := range m {
		setFunc(k, v)
	}
}

// Turns a slice of keys and a slice of values of equivalent sizes into a map
func BuildMapFromSlices(k []string, v []any) map[string]any {
	if len(k) != len(v) { return nil }
	r_map := map[string]any{}
	for i, key := range k { r_map[key] = v[i] }
	return r_map
}


// Take a map and parse it into the supplied struct
func BuildStructFromMap[T any](m map[string]any, s *T) {
    val := Deref(reflect.ValueOf(s))
    typ := DerefType(reflect.TypeOf(s))

    // Check to ensure it is a valid struct to work on
    if typ.Kind() != reflect.Struct { return }
    
    // Iterate through each field of the struct
    for i := 0; i < val.NumField(); i++ {
        json_lookup_val := typ.Field(i).Tag.Get("json")

        // Check to make sure it has a json lookup
        if json_lookup_val != "" && json_lookup_val != "-" {

            // Get the json value from the supplied map, skipping if it is no value
            value, ok := m[json_lookup_val]
            if !ok { continue }

            // Get the value and type of the field itself
            field_val := Deref(val.Field(i))
            if ValidateValue(field_val.Kind(), value) {
                field_val.Set(reflect.ValueOf(value))
            }
        }
    }
}


