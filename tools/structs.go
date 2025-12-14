package tools

import "reflect"

func StructIsEmpty[T any](s *T) bool {
	if s == nil {
		return true
	}
	v := reflect.ValueOf(s).Elem()  // Elem() dereferences the pointer
	return v.IsZero()
}

func Deref(v reflect.Value) reflect.Value {
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			// Return zero value of the type the pointer points to
			return reflect.Zero(v.Type().Elem())
		}
		return v.Elem()
	}
	return v
}

func ExtractValueFromMultiStruct[T any](key string, s []T) ([]any, bool) {
	if len(s) < 1 {
		return nil, false
	}

	ret := make([]any, 0, len(s))

	for _, v := range s {
		val := reflect.ValueOf(v)

		field := val.FieldByName(key)

		if !field.IsValid() || field.IsZero() {
			continue
		}

		ret = append(ret, field.Interface())
	}

	if len(ret) < 1 {
		return nil, false
	}

	return ret, true
}

func ToAnySlice[T any](slice []T) []any {
    var result []any
    for _, item := range slice {
        result = append(result, item)
    }
    return result
}
