package tools

import (
	"encoding/json"
	"reflect"
)

func DereferencedString[T any](model T) string {
	data, _ := json.MarshalIndent(model, "", "  ")
	return string(data)
}

// Deref dereferences a reflect.Value pointer, returning the zero value of the pointed-to type
// if the pointer is nil.
func Deref(v reflect.Value) reflect.Value {
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			if !v.CanSet() {
				// can't allocate into an unsettable pointer field — fall back
				return reflect.Zero(v.Type().Elem())
			}
			v.Set(reflect.New(v.Type().Elem())) // allocate and assign
		}
		return v.Elem()
	}
	return v
}

// Deref dereferences a reflect.Value pointer, returning the zero value of the pointed-to type
// if the pointer is nil.
func DerefType(t reflect.Type) reflect.Type {
	if t.Kind() == reflect.Ptr {
		return t.Elem()
	}
	return t
}

// ValueDeref dereferences any pointer value, returning the zero value if nil.
func ValueDeref(v any) reflect.Value {
	val := reflect.ValueOf(v)

	for val.Kind() == reflect.Ptr {
		if val.IsNil() {
			return reflect.Zero(val.Type().Elem())
		}
		return val.Elem()
	}
	return val
}

func StringDeref(v any) string {
	return ValueDeref(v).String()
}

func BoolDeref(v any) bool {
	return ValueDeref(v).Bool()
}
