package tools

import (
	"encoding/json"
	"reflect"
)

func DereferencedString[T any](model T) string {
    data, _ := json.MarshalIndent(model, "", "  ")
    return string(data)
}

// Supply a reflect.value object and it will return a dereferenced version of that value
// If it is a nil reference, it will return the zero value of the type.
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

func DynamicValueDeref(v any) reflect.Value {
	val := reflect.ValueOf(v)

	for val.Kind() == reflect.Ptr {
		if val.IsNil() {
			// Return zero value of the type the pointer points to
			return reflect.Zero(val.Type().Elem())
		}
		return val.Elem()
	}
	return val
}
