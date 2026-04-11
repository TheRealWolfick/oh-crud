package tools

import "lotusforge.au/api-server/models"

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

	unique_idenifiers := [][]string{}
	if require_identify_unique {
		unique_idenifiers = GetUpdateKeyOptions(cfg)
	}

	// Build required field entries: each entry holds the primary json key + aliases.
	type requiredField struct {
		json_key string
		aliases  []string
	}
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

	for _, row := range rows {
		is_valid := true

		// Check required-on-insert fields (primary key or any alias satisfies the check)
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
					break
				}
			}
		}

		// Check that at least one key set (PK or unique key) is fully present
		if is_valid && require_identify_unique {
			key_found := false
			for _, option := range unique_idenifiers {
				all_present := true
				for _, json_key := range option {
					if _, exists := row[json_key]; !exists {
						all_present = false
						break
					}
				}
				if all_present {
					key_found = true
					break
				}
			}
			if !key_found {
				is_valid = false
			}
		}

		if is_valid {
			coerced_vals, err := DecodeAndCoerceFromUser(row, cfg)
			if err != nil {
				invalid_structs = append(invalid_structs, row)
			} else {
				valid_structs = append(valid_structs, coerced_vals)
			}
		} else {
			invalid_structs = append(invalid_structs, row)
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
