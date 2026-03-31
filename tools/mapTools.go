package tools

import (

	"lotusforge.au/api-server/models"
)

// This function takes a map and will return it inside a valid or invalid slice. This
// is just a wrapper for Validate_SliceOfMaps_AgainstConfig.
// Validation is done via the "Req" or "PK" config option. This is used when you want to separate
// valid rows from invalid rows without an automatic fail on the first invalid row.
//
// Return: []valid, []invalid
func Validate_Map_AgainstConfig(cfg *models.DataModel, m map[string]any, enforce_pk bool, enforce_req bool) ([]map[string]any, []map[string]any) {
	asSlice := []map[string]any{m}
	return Validate_SliceOfMaps_AgainstConfig(cfg, asSlice, enforce_pk, enforce_req)
}


// This function takes a slice of map[string]any and will return it as valid and invalid slices. 
// Validation is done via the "Req" and "PK" config options which are extracted from the first config struct
// Each row will also be coerced based on the config and anything that fails is an invalid row.
// 
// Return: []valid, []invalid
func Validate_SliceOfMaps_AgainstConfig(cfg *models.DataModel, rows []map[string]any, enforce_pk bool, enforce_req bool) ([]map[string]any, []map[string]any) {
	if len(rows) < 1 {
		return []map[string]any{}, []map[string]any{}
	}

	// Get the required fields and early return if there are none, enforce_pk means only the primary key is enforced
	req_fields := GetRequiredJSONFields_FromConfig(cfg, enforce_pk)
	valid_structs := []map[string]any{}
	invalid_structs := []map[string]any{}
	is_valid := true

	// For each map
	for _, row := range rows {	

		if len(req_fields) > 0 {
			// For each required field name
			for _, fieldName := range req_fields {

				// Check if the value exists
				_, exists := row[fieldName]
				if !exists {
					invalid_structs = append(invalid_structs, row)
					is_valid = false
					break
				}
			}
		}
		// Was it a valid struct
		if is_valid {
			// Attempt to decode and coerce the values into their proper type
			coerced_vals, err := models.DecodeAndCoerce(row, cfg, enforce_req, enforce_pk)
			// Test is coercion works correctly. If yes, it was a valid struct
			if err != nil {
				invalid_structs = append(invalid_structs, row)
			} else {
				valid_structs = append(valid_structs, coerced_vals)
			}
		}

		// Reset valid status
		is_valid = true
	}

	return valid_structs, invalid_structs
}

func SetValueFromMap(qb *QueryBuilder, m map[string]any) {
	setFromMap(m, qb.SetValue)
}

func SetValueAndWhereFromMap(qb *QueryBuilder, m map[string]any, where string) {
	for k, v := range m {
		if k == where {
			qb.SetWhereAbsolute(k, v)
		} else {
			qb.SetValue(k, v)
		}
	}
}

// Save the struct fields into the query builder values data
func setFromMap(m map[string]any, setFunc func(string, any)) {
	for k, v := range m {
		setFunc(k, v)
	}
}

