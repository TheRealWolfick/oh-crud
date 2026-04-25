package tools

import (
	"fmt"
	"regexp"
	"strings"

	"lotusforge.au/api-server/models"
)

// types: string, int, float, time

func ValidateStringRules(field_name string, coerced_data string, field_rules *models.DataModelFieldRules) (string, bool) {
	errors := []string{}

	// Valid rules: pattern, max-length
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
			errors = append(errors, fmt.Sprintf("Field {%s} did not match the pattern of {%s}", field_name, *field_rules.Pattern)) 
		}
	}

	// Return
	if len(errors) > 0 {return strings.Join(errors, ", "), false}
	return "", true
}

