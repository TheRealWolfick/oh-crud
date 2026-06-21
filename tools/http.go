package tools

import (
	"net/http"
	"net/url"
	"reflect"
	"strings"
)

func GetIP(r *http.Request) string {
	ip := r.Header.Get("X-Forwarded-For")
	if ip == "" {
		ip = r.RemoteAddr
	}
	return ip
}

// GetChecksum extracts the "checksum" query parameter from the request URL.
// Returns an empty string if the parameter is absent or the URL cannot be parsed.
func GetChecksum(r *http.Request) string {
	if err := r.ParseForm(); err != nil {
		return ""
	}
	if len(r.URL.Query()) < 1 {
		return ""
	}
	return r.URL.Query().Get("checksum")
}

// Convert url.Values into a map[string]any, coercing each field
// to match the target struct field's type where possible.
func UrlValuesToMap[T any](values url.Values, s *T) map[string]any {
	typ := DerefType(reflect.TypeOf(s))
	m := make(map[string]any)

	if typ.Kind() != reflect.Struct {
		return m
	}

	for i := 0; i < typ.NumField(); i++ {
		jsonTag := typ.Field(i).Tag.Get("json")
		if jsonTag == "" || jsonTag == "-" {
			continue
		}
		// strip ",omitempty" etc if present
		if idx := strings.Index(jsonTag, ","); idx != -1 {
			jsonTag = jsonTag[:idx]
		}

		raw, ok := values[jsonTag]
		if !ok || len(raw) == 0 {
			continue
		}

		fieldType := DerefType(typ.Field(i).Type)

		// Slice field: keep all values, converted to the slice's element type
		if fieldType.Kind() == reflect.Slice {
			elemType := fieldType.Elem()
			slice := reflect.MakeSlice(fieldType, 0, len(raw))
			for _, r := range raw {
				cv, err := convertString(r, elemType)
				if err != nil {
					continue
				}
				slice = reflect.Append(slice, reflect.ValueOf(cv))
			}
			m[jsonTag] = slice.Interface()
			continue
		}

		// Scalar field: take the first value, convert to field's type
		cv, err := convertString(raw[0], fieldType)
		if err != nil {
			continue
		}
		m[jsonTag] = cv
	}

	return m
}
