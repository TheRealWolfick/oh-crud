package tools

import (
	"reflect"

	"lotusforge.au/api-server/reflectx"
)

// Turns a slice of keys and a slice of values of equivalent sizes into a map
func BuildMapFromSlices(k []string, v []any) map[string]any {
	if len(k) != len(v) { return nil }
	r_map := map[string]any{}
	for i, key := range k { r_map[key] = v[i] }
	return r_map
}

// Take a map and parse it into the supplied struct
func BuildStructFromMap[T any](m map[string]any, s *T) {
    val := reflectx.Deref(reflect.ValueOf(s))
    typ := reflectx.DerefType(reflect.TypeOf(s))

    // Check to ensure it is a valid struct to work on
    if typ.Kind() != reflect.Struct { return }
    
    // Iterate through each field of the struct
    for i := 0; i < val.NumField(); i++ {
        json_lookup_val := typ.Field(i).Tag.Get("json")

        // Check to make sure it has a json lookup
        if json_lookup_val != "" && json_lookup_val != "-" {

            // Get the json value from the supplied map, skipping if it is no value
            value, ok := m[json_lookup_val]
            if !ok { continue }

            // Get the value and type of the field itself
            field_val := reflectx.Deref(val.Field(i))
            if ValidateValue(field_val.Kind(), value) {
                field_val.Set(reflect.ValueOf(value))
            }
        }
    }
}

