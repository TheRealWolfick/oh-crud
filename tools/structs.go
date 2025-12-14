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
		value := reflect.ValueOf(v).Elem()

		if value.IsZero() {
			continue
		}

		ret = append(ret, value)
	}

	if len(ret) < 1 {
		return nil, false
	}

	return ret, true
}
