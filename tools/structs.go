package tools

import "reflect"

func StructIsEmpty[T any](s *T) bool {
	if s == nil {
		return true
	}
	v := reflect.ValueOf(s).Elem()  // Elem() dereferences the pointer
	return v.IsZero()
}
