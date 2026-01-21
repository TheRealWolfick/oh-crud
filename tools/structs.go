package tools

import (
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strconv"

	"lotusforge.au/api-server/models"
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

func getFields[T any](model T, tag string, value string) []string {
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

		if value == "all" {
			requestedFields = append(requestedFields, typ.Field(i).Name)
		} else if value == "exists" && tagVal != "" && tagVal != "-" {
			requestedFields = append(requestedFields, typ.Field(i).Name)
		} else if tagVal == value {
			requestedFields = append(requestedFields, typ.Field(i).Name)
		}
	}

	return requestedFields
}

// Get all field where the struct has the tag req:"true"
func GetRequiredFields[T any](model T) []string {
	return getFields(model, "req", "true")
}

// Get all the fields which have a "pk" tag
func GetPrimaryKeys[T any](model T) []string {
	return getFields(model, "pk", "true")
}

// Get all the fields with a db tag
func GetDatabaseFields[T any](model T) []string {
	return getFields(model, "db", "exists")
}

func GetAbsolute[T any](model T) []string {
	return getFields(model, "absolute", "true")
}

func GetAllFieldNames[T any](model T) []string {
	return getFields(model, "ignore", "all")
}

func GetDiffFieldName[T any](model T) string {
	diff_fields := getFields(model, "diff", "true")
	if len(diff_fields) > 0 {
		return diff_fields[0]
	}
	return ""
}

func getTagFromField[T any](model T, field string, tag string) string {
	mod := reflect.TypeOf(model)

	if mod.Kind() == reflect.Ptr {
		mod = mod.Elem()
	}

	f, found := mod.FieldByName(field)

	if !found {
		return ""
	}

	return f.Tag.Get(tag)
}

func GetDBTagFromField[T interface{}](model T, field string) string {
	return getTagFromField(model, field, "db")
}

func GetCustomWhereFromField[T any](model T, field string) string {
	return getTagFromField(model, field, "customwhere")
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


func DiffStruct[T any](supplied T, stored T, comparatorField string) models.Item_Diff[T] {
	// reflect on the data
	val_supplied := reflect.ValueOf(supplied)
	val_stored := reflect.ValueOf(stored)

	// dereference the data
	if val_supplied.Kind() == reflect.Ptr {
		val_supplied = val_supplied.Elem()
	}
	if val_stored.Kind() == reflect.Ptr {
		val_stored = val_stored.Elem()
	}

	// Validate that we have struct types after dereferencing
	if val_supplied.Kind() != reflect.Struct || val_stored.Kind() != reflect.Struct {
		return models.Item_Diff[T]{}
	}

	// Check to ensure the comparator is matching in both fields and not a nil value
	supplied_comparator := val_supplied.FieldByName(comparatorField)
	stored_comparator := val_stored.FieldByName(comparatorField)

	// Validate that comparator field exists
	if !supplied_comparator.IsValid() || !stored_comparator.IsValid() {
		return models.Item_Diff[T]{}
	}

	// Dereference if pointer
	if supplied_comparator.Kind() == reflect.Ptr {
		if supplied_comparator.IsNil() {
			return models.Item_Diff[T]{}
		}
		supplied_comparator = supplied_comparator.Elem()
	}
	if stored_comparator.Kind() == reflect.Ptr {
		if stored_comparator.IsNil() {
			return models.Item_Diff[T]{}
		}
		stored_comparator = stored_comparator.Elem()
	}

	// Check if comparators match
	if !reflect.DeepEqual(supplied_comparator.Interface(), stored_comparator.Interface()) {
		return models.Item_Diff[T]{}
	}

	// Check if comparator is zero value
	if supplied_comparator.IsZero() {
		return models.Item_Diff[T]{}
	}

	comparator_value := fmt.Sprintf("%v", supplied_comparator.Interface())

	// Determine if T is a pointer type
	tType := reflect.TypeOf((*T)(nil)).Elem()
	isPointerType := tType.Kind() == reflect.Ptr

	// Create new instances based on the actual struct type
	supplied_return_val := reflect.New(val_supplied.Type()).Elem()
	stored_return_val := reflect.New(val_stored.Type()).Elem()

	// Track if we found any actual differences
	hasDifferences := false

	// Get all the fields from the supplied struct
	struct_fields := GetAllFieldNames(supplied)

	for _, field := range struct_fields {
		// Skip fields marked to exclude from diffs
		fieldType := reflect.TypeOf(supplied)
		if fieldType.Kind() == reflect.Ptr {
			fieldType = fieldType.Elem()
		}
		structField, found := fieldType.FieldByName(field)
		if found && structField.Tag.Get("exclude_diff") == "true" {
			continue
		}

		supplied_field := val_supplied.FieldByName(field)
		stored_field := val_stored.FieldByName(field)

		// Validate fields exist
		if !supplied_field.IsValid() || !stored_field.IsValid() {
			continue
		}

		// Always include the comparator field
		if field == comparatorField {
			if supplied_return_val.FieldByName(field).CanSet() {
				supplied_return_val.FieldByName(field).Set(supplied_field)
			}
			if stored_return_val.FieldByName(field).CanSet() {
				stored_return_val.FieldByName(field).Set(stored_field)
			}
			continue
		}

		// For comparison, dereference if needed
		supplied_compare := supplied_field
		stored_compare := stored_field
		if supplied_compare.Kind() == reflect.Ptr && !supplied_compare.IsNil() {
			supplied_compare = supplied_compare.Elem()
		}
		if stored_compare.Kind() == reflect.Ptr && !stored_compare.IsNil() {
			stored_compare = stored_compare.Elem()
		}

		// Compare the actual values
		// Only add to diff if they're different AND at least one is non-zero
		if !reflect.DeepEqual(supplied_compare.Interface(), stored_compare.Interface()) {
			// Check if at least one value is non-zero/non-nil
			supplied_has_value := supplied_field.Kind() != reflect.Ptr || !supplied_field.IsNil()
			stored_has_value := stored_field.Kind() != reflect.Ptr || !stored_field.IsNil()

			if supplied_has_value || stored_has_value {
				supField := supplied_return_val.FieldByName(field)
				stoField := stored_return_val.FieldByName(field)
				if supField.CanSet() {
					supField.Set(supplied_field)
				}
				if stoField.CanSet() {
					stoField.Set(stored_field)
				}
				hasDifferences = true
			}
		}
	}

	// If no actual differences found (besides comparator), return empty diff
	if !hasDifferences {
		return models.Item_Diff[T]{}
	}

	// Convert back to T, handling both pointer and non-pointer cases
	var sup, sto T
	if isPointerType {
		// T is a pointer type, so we need to get the address
		supPtr := reflect.New(val_supplied.Type())
		supPtr.Elem().Set(supplied_return_val)
		sup = supPtr.Interface().(T)

		stoPtr := reflect.New(val_stored.Type())
		stoPtr.Elem().Set(stored_return_val)
		sto = stoPtr.Interface().(T)
	} else {
		// T is not a pointer type, use the value directly
		sup = supplied_return_val.Interface().(T)
		sto = stored_return_val.Interface().(T)
	}

	diffs_to_return := &models.Item_Diff[T]{
		Comparator: &comparator_value,
		Supplied:   &sup,
		Stored:     &sto,
	}

	return *diffs_to_return
}


func DiffStructSlices[T any](left []T, right []T) *models.Diff[T] {
	if len(left) < 1 || len(right) < 1 {
		return nil
	}

	comparator_field := GetDiffFieldName(left[0])
	
	if comparator_field == "" {
		return nil
	}

	left_only := make([]T, 0)
	matches := make([]models.Item_Diff[T], 0)

	// Sort left and right data for optimizing search
	SortSliceOfStructs(left, comparator_field)
	SortSliceOfStructs(right, comparator_field)

	// Do the merge
	for _, item := range left {
		// Read the comparator
		comparator_prep := reflect.ValueOf(item)
		if comparator_prep.Kind() == reflect.Ptr {comparator_prep = comparator_prep.Elem()}
		comparator_prep = comparator_prep.FieldByName(comparator_field)
		if comparator_prep.Kind() == reflect.Ptr {comparator_prep = comparator_prep.Elem()}
		comparator := comparator_prep.Interface().(string)
		
		// Check if comparator is in right slice
		right_index, found := BinarySearch(comparator, right, comparator_field)

		if !found {
			left_only = append(left_only, item)
			continue
		}

		// It was found
		matches = append(matches, DiffStruct(item, right[right_index], comparator_field))

		// Remove item from right
		right = slices.Concat(right[:right_index], right[right_index+1:])
	}

	return &models.Diff[T]{
		DiffType: &comparator_field,
		MissingFromSupplied: left_only,
		MissingFromStored: right,
		Diffs: matches,
	}
}


func SortSliceOfStructs[T any](arr []T, field_name string) {
	sort.Slice(arr, func(i, j int) bool {
		i_val := reflect.ValueOf(arr[i])
		j_val := reflect.ValueOf(arr[j])

		if i_val.Kind() == reflect.Ptr {
			i_val = i_val.Elem()
			j_val = j_val.Elem()
		}

		i_field := i_val.FieldByName(field_name)
		j_field := j_val.FieldByName(field_name)

		if i_field.Kind() == reflect.Ptr {
			i_field = i_field.Elem()
			j_field = j_field.Elem()
		}

		return i_field.Interface().(string) < j_field.Interface().(string)
	})
}
