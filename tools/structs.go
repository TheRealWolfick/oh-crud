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
