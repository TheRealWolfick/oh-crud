package tools

import (
	"fmt"
	"reflect"
	"slices"
	"strconv"
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
	if reflect.TypeOf(s[0]).Kind() != reflect.Struct && reflect.TypeOf(s[0]).Kind() != reflect.Ptr {
		return make([]T, 0), make([]T, 0)
	}

	req_fields := GetRequiredFields(s[0]) // struct field names
	valid_structs := make([]T, 0)
	invalid_structs := make([]T, 0)
	is_valid := true

	// For each struct
	for _, m := range s {	

		// Reflect the struct
		vals := reflect.ValueOf(m)

		if vals.Kind() == reflect.Ptr {
			vals = vals.Elem()
		}
		
		// For each required field name
		for _, fieldName := range req_fields {
			field := vals.FieldByName(fieldName)
			if field.Kind() == reflect.Ptr {
				field = field.Elem()
			}

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
    typ := reflect.TypeOf(model)
    val := reflect.ValueOf(model)
    
    // Handle pointers
    if typ.Kind() == reflect.Ptr {
        typ = typ.Elem()
        val = val.Elem()
    }
    
    // Now check if it's a struct
    if typ.Kind() != reflect.Struct {
        return nil
    }

    reqFields := make([]string, 0)

    for i := 0; i < val.NumField(); i++ {
        // Read tag
        tagVal := typ.Field(i).Tag.Get(tag)

        if value == "exists" && tagVal != "" && tagVal != "-" {
            reqFields = append(reqFields, typ.Field(i).Name)
        } else if tagVal == value {
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
	mod := reflect.TypeOf(model)

	if mod.Kind() == reflect.Ptr {
		mod = mod.Elem()
	}

	f, _ := mod.FieldByName(field)

	return f.Tag.Get("db")
}

// Remove all the items in element b from element a
func StringSliceSubtract(a []string, b []string) []string {
	new_slice := make([]string, 0, len(a))
	var add bool

	// For each value in A
	for _, a_val := range a {
		add = true

		// if value is in B
		if slices.Contains(b, a_val) {
			add = false
		}

		// Add it to the new slice if it wasn't in B
		if add {
			new_slice = append(new_slice, a_val)
		}
	}

	return  new_slice
}


// Check if a field of a specific model is absolute (mod must be '=')
func IsAbsolute[T interface{}](model T, field string) bool {
	fields := GetFields(model, "absolute", "true")

	return slices.Contains(fields, field)
}


// Validates that a value is of the specified type. This is used when the value MUST be a specific type. Currently does not support the identification of modifiers
func ValidateValue(typ reflect.Kind, val any) bool {
	if val == nil || val == reflect.Struct {
		return false
	}

	val_type := reflect.TypeOf(val)
	val_to_check := reflect.ValueOf(val)

	if val_type.Kind() == reflect.Ptr {
		val_type = val_type.Elem()
	}
	if val_to_check.Kind() == reflect.Ptr {
		val_to_check = val_to_check.Elem()
	}

	if val_type.Kind() == typ {
		return true
	}

	// Determine the field type and extract mod based on that
	if val_type.Kind() == reflect.String {
		value_as_string := fmt.Sprintf("%s", val_to_check)

		switch typ {
		case reflect.Int, reflect.Int32, reflect.Int64:
			if _, err := strconv.ParseInt(value_as_string, 10, 64); err == nil {
				return true
			} else {
				return false
			}

		case reflect.Bool:
			if _, err := strconv.ParseBool(value_as_string); err == nil {
				return true
			} else {
				return false
			}

		case reflect.Float32, reflect.Float64:
			if _, err := strconv.ParseFloat(value_as_string, 64); err == nil {
				return true
			} else {
				return false
			}

		default:
			return false
		}
	}
	return false
}
