package tools

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"lotusforge.au/api-server/models"
)

// Validate that a string field is valid to its relevent rules
func ValidateStringRules(field_name string, coerced_data string, field_rules *models.DataModelFieldRules) (string, bool) {
	errors := []string{}

	// Test max-length
	if field_rules.Max_length != nil {
		if len(coerced_data) > *field_rules.Max_length { 
			errors = append(errors, fmt.Sprintf("Field {%s} exceeds max length of {%v}", field_name, *field_rules.Max_length)) 
		}
	}

	// Test pattern
	if field_rules.Pattern != nil {
		pat := regexp.MustCompile(*field_rules.Pattern)
		if len(pat.FindString(coerced_data)) != len(coerced_data) {
			errors = append(errors, fmt.Sprintf(`Field {%s} did not match the pattern of r"%s"`, field_name, *field_rules.Pattern)) 
		}
	}

	// Test enum
	if field_rules.Enum != nil {
		if !slices.Contains(field_rules.Enum, coerced_data) {
			errors = append(errors, fmt.Sprintf(`Field {%s} is not a valid value. Allowed values: [%s]"`, field_name, strings.Join(field_rules.Enum, ", "))) 
		}
	}

	// Return
	if len(errors) > 0 {return strings.Join(errors, ", "), false}
	return "", true
}


// Validate that an int field is valid to its relevant rules
func ValidateIntRules(field_name string, coerced_data int, field_rules *models.DataModelFieldRules) (string, bool) {
	errors := []string{}

	// Test min
  if field_rules.Min != nil {
		if coerced_data < *field_rules.Min {
			errors = append(errors, fmt.Sprintf("Field {%s} violated minimum of {%v}", field_name, *field_rules.Min)) 
		}
	}

	// Test max
  if field_rules.Max != nil {
		if coerced_data > *field_rules.Max {
			errors = append(errors, fmt.Sprintf("Field {%s} violated maximum of {%v}", field_name, *field_rules.Max)) 
		}
	}

	// Test enum
	if field_rules.Enum != nil {
		matched := slices.ContainsFunc(field_rules.Enum, func(v string) bool {
			as_int, err := strconv.Atoi(v)
			if err != nil { return false }
			return as_int == coerced_data
		})
		if !matched {
			errors = append(errors, fmt.Sprintf(`Field {%s} is not a valid value. Allowed values: [%s]"`, field_name, strings.Join(field_rules.Enum, ", "))) 
		}
	}

	if len(errors) > 0 {return strings.Join(errors, ", "), false}
	return "", true
}


// Validate that a float field is valid to its float relevant rules
func ValidateFloatRules(field_name string, coerced_data float64, field_rules *models.DataModelFieldRules) (string, bool) {
	errors := []string{}

	// Test min
  if field_rules.Min != nil {
		if coerced_data < float64(*field_rules.Min) {
			errors = append(errors, fmt.Sprintf("Field {%s} violated minimum of {%v}", field_name, *field_rules.Min)) 
		}
	}

	// Test max
  if field_rules.Max != nil {
		if coerced_data > float64(*field_rules.Max) {
			errors = append(errors, fmt.Sprintf("Field {%s} violated maximum of {%v}", field_name, *field_rules.Max)) 
		}
	}

	// Test enum
	if field_rules.Enum != nil {
		matched := slices.ContainsFunc(field_rules.Enum, func(v string) bool {
			as_float, err := strconv.ParseFloat(v, 64)
			if err != nil { return false }
			return as_float == coerced_data
		})
		if !matched {
			errors = append(errors, fmt.Sprintf(`Field {%s} is not a valid value. Allowed values: [%s]"`, field_name, strings.Join(field_rules.Enum, ", "))) 
		}
	}

	if len(errors) > 0 {return strings.Join(errors, ", "), false}
	return "", true
}
