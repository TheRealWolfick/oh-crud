package httpx

import (
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"lotusforge.au/api-server/reflectx"
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
	typ := reflectx.DerefType(reflect.TypeOf(s))
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

		fieldType := reflectx.DerefType(typ.Field(i).Type)

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
