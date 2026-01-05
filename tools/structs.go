package tools

import (
	"fmt"
	"reflect"
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

    requestedFields := make([]string, 0)

    for i := 0; i < val.NumField(); i++ {
        // Read tag
        tagVal := typ.Field(i).Tag.Get(tag)

        if value == "exists" && tagVal != "" && tagVal != "-" {
            requestedFields = append(requestedFields, typ.Field(i).Name)
        } else if tagVal == value {
            requestedFields = append(requestedFields, typ.Field(i).Name)
        }
    }

    return requestedFields
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

func GetAbsolute[T any](model T) []string {
	return GetFields(model, "absolute", "true")
}

func getTagFromField[T any](model T, field string, tag string) string {
	mod := reflect.TypeOf(model)

	if mod.Kind() == reflect.Ptr {
		mod = mod.Elem()
	}

	f, _ := mod.FieldByName(field)

	return f.Tag.Get(tag)
}

func GetDBTagFromField[T interface{}](model T, field string) string {
	return getTagFromField(model, field, "db")
}

func getAbsoluteTagFromField[T interface{}](model T, field string) string {
	return getTagFromField(model, field, "absolute")
}

func IsAbsolute[T any](model T, field string) bool {
	absolute := getAbsoluteTagFromField(model, field)
	return absolute == "true"
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


// Test whether the string B is of type A. Currently does not support mods
func ValidateValue(A reflect.Kind, B any) bool {

	if B == nil {
		return false
	}

	if A == reflect.TypeOf(B).Kind() {
		return  true
	}

	as_string := fmt.Sprintf("%v", B)
	switch A {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		// test if int
		_, err := strconv.ParseInt(as_string, 10, 64)
		return err == nil

	case reflect.Bool:
		_, err := strconv.ParseBool(as_string)
		return err == nil

	case reflect.Float32, reflect.Float64:
		_, err := strconv.ParseFloat(as_string, 64)
		return err == nil

	default:
		return false
	}
}
