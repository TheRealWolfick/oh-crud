package tools

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
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

// ConvertString converts a raw form string into the requested kind.
func ConvertString(s string, t reflect.Type) (any, error) {
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

// Extract the tyoe and type size from a string under the assumption that it is
// of format `type(size)`. If there is no size, then it returns the type and -1 as 
// the size. If the size is not an int, it returns 0 as the size and an error.
// Maximum int size is Int32
func ExtractTypeAndSize(s string) (string, int, error) {
	if s == "" || !strings.Contains(s, "(") { return s, -1, nil }

  // Remove the trailing `)` and split it into type and size as strings
	spliced := strings.Split(strings.ReplaceAll(s, ")", ""), "(")
	if len(spliced) != 2 { return "", 0, fmt.Errorf("Type was an invalid format of `type(size)`. Splitting failed") }

	// Convert length
	_, err := strconv.ParseInt(spliced[1], 10, 32)
	if err != nil {
		return "", 0, err
	}

	// Can be successfully converted now
	return spliced[0], ConvertToInt(spliced[1]), nil
}

// ParseDate tries common date formats, returning an error if none match.
func ParseDate(dateStr string) (time.Time, error) {
	formats := []string{
		"2006-01-02",
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"01/02/2006",
		"02/01/2006",
		time.RFC3339,
		time.RFC3339Nano,
	}
	var lastErr error
	for _, format := range formats {
		if parsed, err := time.Parse(format, dateStr); err == nil {
			return parsed, nil
		} else {
			lastErr = err
		}
	}
	return time.Time{}, lastErr
}
