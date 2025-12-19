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

func ValidateStruct[T any](s T) ([]T, []T) {
	asStruct := []T{s}
	return ValidateMultiStruct(asStruct)
}

func ValidateMultiStruct[T any](s []T) ([]T, []T) {
	if len(s) < 1 {
		return make([]T, 0), make([]T, 0)
	}
	if reflect.TypeOf(s[0]).Kind() != reflect.Struct {
		return make([]T, 0), make([]T, 0)
	}

	req_fields := make([]string, 0)
	valid_structs := make([]T, 0)
	invalid_structs := make([]T, 0)
	is_valid := true

	// Get required fields
	check_vals := reflect.ValueOf(s[0])
	check_typ := check_vals.Type()

	for i := 0; i < check_typ.NumField(); i++ {
		fieldType := check_typ.Field(i)
		if fieldType.Tag.Get("req") == "true" {
			req_fields = append(req_fields, fieldType.Name)
		}
	}

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
