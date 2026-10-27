package tools

import (
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

func FilterValidStructValues[T comparable](s []T, valid []T) []T {
  v := []T{}

	for _, val := range s {
		if slices.Contains(valid, val) {v = append(v, val)}
	}

	return v
}


// ParseToSlice safely converts a raw payload (Postgres string literal, JSON string,
// native slice, or primitive) into a clean Go slice of type T.
// Returns an error if the payload is malformed, is a non-scalar type that can't be
// read as a list, or contains an element that can't be converted to T.
func ParseToSlice[T any](raw any) ([]T, error) {
	if raw == nil {
		return []T{}, nil
	}

  raw = ValueDeref(raw).Interface()

	// 1. Short-circuit if it's already a native slice of the exact target type
	if exactSlice, ok := raw.([]T); ok {
		return exactSlice, nil
	}

	// 2. Handle generic slice iterations (e.g., []any or []string converting to something else)
	// []byte is treated as text, not as a slice of elements
	if byteSlice, ok := raw.([]byte); ok {
		raw = string(byteSlice)
	}
	val := reflect.ValueOf(raw)
	switch val.Kind() {
	case reflect.Slice, reflect.Array:
		res := make([]T, 0, val.Len())
		for i := 0; i < val.Len(); i++ {
			elem := val.Index(i).Interface()
			converted, ok := convertToT[T](elem)
			if !ok {
				return nil, fmt.Errorf("element %d (%v) cannot be converted to %T", i, elem, *new(T))
			}
			res = append(res, converted)
		}
		return res, nil

	case reflect.Map, reflect.Struct, reflect.Chan, reflect.Func, reflect.UnsafePointer:
		// fmt.Sprint of these produces Go syntax, not a parseable list
		return nil, fmt.Errorf("cannot parse %T into a slice", raw)
	}

	// 3. Fall back to string extraction for structural strings or raw scalars
	var asString string
	switch v := raw.(type) {
	case string:
		asString = v
	default:
		asString = fmt.Sprint(v)
	}

	asString = strings.TrimSpace(asString)
	if asString == "" {
		return []T{}, nil
	}

	// 4. Strip structural wrappers (handles Postgres '{...}' and JSON '[...]')
	asString, err := stripListWrapper(asString)
	if err != nil {
		return nil, err
	}

	asString = strings.TrimSpace(asString)
	if asString == "" {
		return []T{}, nil
	}

	// 5. Split by comma and convert individual tokens to type T
	parts := strings.Split(asString, ",")
	result := make([]T, 0, len(parts))

	for i, part := range parts {
		cleaned := strings.Trim(part, `" '`)
		cleaned = strings.TrimSpace(cleaned)

		if cleaned != "" {
			converted, ok := convertToT[T](cleaned)
			if !ok {
				return nil, fmt.Errorf("element %d (%q) cannot be converted to %T", i, cleaned, *new(T))
			}
			result = append(result, converted)
		}
	}

	return result, nil
}

// stripListWrapper removes a matching pair of [], {} or () from around s.
// An opening or closing bracket without its partner is treated as malformed input.
func stripListWrapper(s string) (string, error) {
	pairs := map[byte]byte{'[': ']', '{': '}', '(': ')'}
	first, last := s[0], s[len(s)-1]

	if closer, isOpener := pairs[first]; isOpener {
		if len(s) < 2 || last != closer {
			return "", fmt.Errorf("unclosed %q in list %q", first, s)
		}
		return s[1 : len(s)-1], nil
	}

	for opener, closer := range pairs {
		if last == closer {
			return "", fmt.Errorf("unopened %q in list %q (expected leading %q)", closer, s, opener)
		}
	}

	return s, nil
}

// convertToT is a private helper that forces an unknown value into the target type T.
func convertToT[T any](val any) (T, bool) {
	var zero T

	// Fast track if types match exactly
	if typed, ok := val.(T); ok {
		return typed, true
	}

	// Otherwise, evaluate target type T and perform conversion
	switch any(zero).(type) {
	case string:
		return any(fmt.Sprint(val)).(T), true

	case int:
		strVal := fmt.Sprint(val)
		if i, err := strconv.Atoi(strVal); err == nil {
			return any(i).(T), true
		}

	case bool:
		strVal := fmt.Sprint(val)
		if b, err := strconv.ParseBool(strVal); err == nil {
			return any(b).(T), true
		}

	case float64:
		strVal := fmt.Sprint(val)
		if f, err := strconv.ParseFloat(strVal, 64); err == nil {
			return any(f).(T), true
		}
	}

	return zero, false
}


// Wrap each element in s in the string w. 
// i.e ([Apple, Banana], "'") -> ['Apple', 'Banana']
func WrapStringSliceElements(s []string, w string) []string {
	new_slice := make([]string, 0, len(s))

	for _, item := range s {
		new_slice = append(new_slice, fmt.Sprintf("%s%s%s", w, item, w)) 
	}

	return new_slice
}

// Unique returns the elements of s with duplicates removed, keeping first-seen order.
func Unique[T comparable](s []T) []T {
	seen := make(map[T]bool, len(s))
	u := make([]T, 0, len(s))

	for _, val := range s {
		if !seen[val] {
			seen[val] = true
			u = append(u, val)
		}
	}

	return u
}
