package tools

import (
	"crypto/rand"
	"fmt"
	"reflect"
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

func ParseAggregateFuncString(field string, qb *QueryBuilder, cfg *models.DataModel) (string, bool) {
	if field == "count" {
		return "count(*)", true
	}

	// Skip if not a valid field
	if !strings.Contains(field, ":") { return "", false }

	// Process functions
	s := strings.Split(field, ":"); if len(s) != 2 { return "", false }
	fnc, sub_field := s[0], s[1]

	switch fnc {
	case "avg", "min", "max", "sum":
		f, allowed := CheckFieldGetValid(sub_field, cfg)
		if allowed { 
			return  fmt.Sprintf("%s(%s)", fnc, f), true
		}
	default:
		qb.logger.Debug("Invalid function passed into aggregate function", "func", fnc)
	}
	return "", false
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
