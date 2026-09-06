package tools

import (
	"crypto/rand"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"lotusforge.au/api-server/models"
)

func IsInt(val string) bool {
	_, err := strconv.Atoi(val)
	if err != nil {
		return false
	}
	return true
}

// ConvertToInt should ONLY be used when 100% confident the value is an integer (e.g. after IsInt).
func ConvertToInt(val string) int {
	i, _ := strconv.Atoi(val)
	return i
}

func ASCorDESC(s string) string {
	switch s {
	case "":
		return "ASC"
	case "desc", "DESC", "d", "D", "descending", "DESCENDING":
		return "DESC"
	}
	return "ASC"
}

func generateRandomString(string_length int) (string, error) {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890"

	return_str := make([]byte, string_length)
	_, err := rand.Read(return_str)

	if err != nil {
		return "", err
	}

	for i := range string_length {
		return_str[i] = chars[int(return_str[i])%len(chars)]
	}

	return string(return_str), nil
}

func Generate32CharString() (string, error) {
	return generateRandomString(32)
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
				f, allowed := CheckFieldGetValid(ssf, cfg)
				if allowed { allowed_sub_fields = append(allowed_sub_fields, f) }
			}
			if len(allowed_sub_fields) > 0 {
				return fmt.Sprintf("%s(%s)", fnc, strings.Join(allowed_sub_fields, ",")), true, true, allowed_sub_fields
			}
		} else {
			f, allowed := CheckFieldGetValid(sub_field, cfg)
			if allowed { 
				return  fmt.Sprintf("%s(%s)", fnc, f), true, true, []string{f}
			}
		}
	case "avg", "min", "max", "sum":
		f, allowed := CheckFieldGetValid(sub_field, cfg)
		if allowed { 
			return  fmt.Sprintf("%s(%s)", fnc, f), true, false, nil
		}
	default:
		qb.logger.Debug("Invalid function passed into aggregate function", "func", fnc)
	}
	return "", false, false, nil
}

// convertString converts a raw form string into the requested kind.
func convertString(s string, t reflect.Type) (any, error) {
	switch t.Kind() {
	case reflect.String:
		return s, nil
	case reflect.Bool:
		return strconv.ParseBool(s)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, err
		}
		return reflect.ValueOf(v).Convert(t).Interface(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return nil, err
		}
		return reflect.ValueOf(v).Convert(t).Interface(), nil
	case reflect.Float32, reflect.Float64:
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, err
		}
		return reflect.ValueOf(v).Convert(t).Interface(), nil
	default:
		return nil, fmt.Errorf("unsupported kind: %s", t.Kind())
	}
}
