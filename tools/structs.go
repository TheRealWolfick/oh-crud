package tools

import (
	"reflect"
)

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


// This function takes a struct and will return it inside a valid or invalid slice. This
// is just a wrapper for ValidateMultiStruct.
// Validation is done via the "req" tag which are extracted from the first struct being
// passed to GetRequiredFields.
// 
// Return: []valid, []invalid
func ValidateStruct[T any](s T) ([]T, []T) {
	asStruct := []T{s}
	return ValidateMultiStruct(asStruct)
}


// This function takes a slice of any struct and will return it as valid and invalid slices. 
// Validation is done via the "req" tag which are extracted from the first struct being
// passed to GetRequiredFields.
// 
// Return: []valid, []invalid
func ValidateMultiStruct[T any](s []T) ([]T, []T) {
	if len(s) < 1 {
		return make([]T, 0), make([]T, 0)
	}
	if reflect.TypeOf(s[0]).Kind() != reflect.Struct {
		return make([]T, 0), make([]T, 0)
	}

	req_fields := GetRequiredFields(s[0])
	valid_structs := make([]T, 0)
	invalid_structs := make([]T, 0)
	is_valid := true

	for _, m := range s {
		vals := reflect.ValueOf(m)

		if vals.Kind() == reflect.Ptr {
			vals = vals.Elem()
		}
		
		for _, fieldName := range req_fields {
			field := vals.FieldByName(fieldName)

			if !field.IsValid() {
				invalid_structs = append(invalid_structs, m)
				is_valid = false
				break
			}

			// Handle pointer fields and add to invalid if it is an invalid struct
			if field.Kind() == reflect.Ptr {
				if field.IsNil() || field.Elem().IsZero() {
					invalid_structs = append(invalid_structs, m)
					is_valid = false
					break
				}
			} else {
				if field.IsZero() {
					invalid_structs = append(invalid_structs, m)
					is_valid = false
					break
				}
			}
		}

		// Was it a valid struct
		if is_valid {
			valid_structs = append(valid_structs, m)
		}

		// Reset valid status
		is_valid = true
	}

	return valid_structs, invalid_structs
}

func ToAnySlice[T any](slice []T) []any {
	var result []any
	for _, item := range slice {
		result = append(result, item)
	}
	return result
}


func GetFields[T any](model T, tag string, value string) []string {
	if reflect.TypeOf(model).Kind() != reflect.Struct {
		return nil
	}

	reqFields := make([]string, 0)

	vals := reflect.ValueOf(model)
	typ := reflect.TypeOf(model)

	for i := 0; i < vals.NumField(); i++ {
		// Read if required
		req := typ.Field(i).Tag.Get(tag)

		if value == "exists" && req != "" && req != "-" {
			reqFields = append(reqFields, typ.Field(i).Name)
		} else if req == value {
			reqFields = append(reqFields, typ.Field(i).Name)
		}
	}

	return reqFields
}

// Get all field where the struct has the tag req:"true"
func GetRequiredFields[T any](model T) []string {
	return GetFields(model, "req", "true")
}

// Get all the fields which have a "pk" tag
func GetPrimaryKeys[T any](model T) []string {
	return GetFields(model, "pk", "true")
}

// Get all the fields with a db tag
func GetDatabaseFields[T any](model T) []string {
	return GetFields(model, "db", "exists")
}

func GetDBTagFromField[T interface{}](model T, field string) string {
	f, _ := reflect.TypeOf(model).FieldByName(field)
	return f.Tag.Get("db")
}

// Remove all the items in element b from element a
func StringSliceSubtract(a []string, b []string) []string {
	new_slice := make([]string, 0, len(a))
	var add bool

	for _, a_val := range a {
		add = true
		for _, b_val := range b {
			if a_val == b_val {
				add = false
				break
			}
		}
		if add {
			new_slice = append(new_slice, a_val)
		}
	}

	return  new_slice
}

