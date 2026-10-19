package querybuilder

import (
	"fmt"
	"strings"

	"lotusforge.au/api-server/datamodel"
	"lotusforge.au/api-server/models"
)

func ASCorDESC(s string) string {
	switch s {
	case "":
		return "ASC"
	case "desc", "DESC", "d", "D", "descending", "DESCENDING":
		return "DESC"
	}
	return "ASC"
}

// Process an aggregate function string. This effectively transforms the function and ensures it
// is in a valid format and the columns selected are valid.
func ParseAggregateFuncString(field string, qb *QueryBuilder, cfg *models.DataModel) (string, bool, bool, []string ) {
	if field == "count" {
		return "count(*)", true, false, nil
	}

	// Skip if not a valid field
	if !strings.Contains(field, ":") { return "", false, false, nil }

	// Process functions
	s := strings.Split(field, ":"); if len(s) != 2 { return "", false, false, nil }
	fnc, sub_field := s[0], s[1]

	switch fnc {
	case "distinct":
		if strings.Contains(sub_field, "~") {
			sub_sub_fields := strings.Split(sub_field, "~")
			allowed_sub_fields := []string{}
			for _, ssf := range sub_sub_fields {
				f, allowed := datamodel.CheckFieldGetValid(ssf, cfg)
				if allowed { allowed_sub_fields = append(allowed_sub_fields, f) }
			}
			if len(allowed_sub_fields) > 0 {
				return fmt.Sprintf("%s(%s)", fnc, strings.Join(allowed_sub_fields, ",")), true, true, allowed_sub_fields
			}
		} else {
			f, allowed := datamodel.CheckFieldGetValid(sub_field, cfg)
			if allowed { 
				return  fmt.Sprintf("%s(%s)", fnc, f), true, true, []string{f}
			}
		}
	case "avg", "min", "max", "sum":
		f, allowed := datamodel.CheckFieldGetValid(sub_field, cfg)
		if allowed { 
			return  fmt.Sprintf("%s(%s)", fnc, f), true, false, nil
		}
	default:
		qb.logger.Debug("Invalid function passed into aggregate function", "func", fnc)
	}
	return "", false, false, nil
}
