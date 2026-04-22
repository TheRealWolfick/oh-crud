package tools

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"time"

	"lotusforge.au/api-server/models"
)

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

func GetAllFieldNames[T any](model T) []string {
	return getFields(model, "ignore", "all")
}

func GetStructAsDict[T any](model T) map[string]any {
	fieldnames := GetAllFieldNames(model)
	return_dict := make(map[string]any)
	val := reflect.ValueOf(model)
	val = Deref(val)

	for _, field := range fieldnames {
		value := val.FieldByName(field)
		return_dict[field] = Deref(value).Interface()
	}
	return return_dict
}

func GetDiffFieldName[T any](model T) string {
	diff_fields := getFields(model, "diff", "true")
	if len(diff_fields) > 0 {
		return diff_fields[0]
	}
	return ""
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


// DiffMap compares two maps by all keys except those in excludeKeys.
// Returns an empty Item_Diff if the comparator values don't match or there are no differences.
func DiffMap(
	supplied map[string]any,
	stored map[string]any,
	comparatorKey string,
	excludeKeys map[string]bool,
) models.Item_Diff[map[string]any] {
	supplied_comparator, ok1 := supplied[comparatorKey]
	stored_comparator, ok2 := stored[comparatorKey]
	if !ok1 || !ok2 || supplied_comparator != stored_comparator {
		return models.Item_Diff[map[string]any]{}
	}
	comp := fmt.Sprintf("%v", supplied_comparator)
	if comp != fmt.Sprintf("%v", stored_comparator) || comp == "" {
		return models.Item_Diff[map[string]any]{}
	}

	suppliedDiff := map[string]any{comparatorKey: supplied_comparator}
	storedDiff := map[string]any{comparatorKey: stored_comparator}
	hasDiffs := false

	// Collect all keys from both maps
	allKeys := map[string]struct{}{}
	for k := range supplied { allKeys[k] = struct{}{} }
	for k := range stored   { allKeys[k] = struct{}{} }

	// For each key
	for k := range allKeys {
		// Skip keys not to be checked
		if k == comparatorKey || excludeKeys[k] { continue }
		// Extract the value of the keys and normalize the values to ensure they are the same type
		supplied_value, supplied_exists := supplied[k]
		stored_value, stored_exists := stored[k]
		sVal := fmt.Sprintf("%v", normalizeVal(supplied_value, supplied_exists))
		stVal := fmt.Sprintf("%v", normalizeVal(stored_value, stored_exists))
		// Compare the values
		if sVal != stVal {
			suppliedDiff[k] = supplied[k]
			storedDiff[k] = stored[k]
			hasDiffs = true
		}
	}

	if !hasDiffs {
		return models.Item_Diff[map[string]any]{}
	}

	return models.Item_Diff[map[string]any]{
		Comparator: &comp,
		Supplied:   &suppliedDiff,
		Stored:     &storedDiff,
	}
}
func normalizeVal(v any, exists bool) string {
    if !exists || v == nil {
        return ""
    }
    switch val := v.(type) {
    case time.Time:
        return val.UTC().Format("2006-01-02")
    case float64:
        // Avoids scientific notation and trailing zeros
        return strconv.FormatFloat(val, 'f', -1, 64)
    case float32:
        return strconv.FormatFloat(float64(val), 'f', -1, 32)
    case int, int32, int64, uint, uint32, uint64:
        return fmt.Sprintf("%d", val)
    default:
        return fmt.Sprintf("%v", val)
    }
}

// SortMapSlice sorts a []map[string]any in-place by the string representation of key.
// Entries missing key are placed at the end.
func SortMapSlice(slice []map[string]any, key string) {
	sort.Slice(slice, func(i, j int) bool {
		ival, iok := slice[i][key]
		jval, jok := slice[j][key]
		if !iok { return false }
		if !jok { return true }
		return normalizeVal(ival, true) < normalizeVal(jval, true)
	})
}

// DiffMapSlices compares two slices of maps using comparatorKey to match rows.
// excludeKeys lists fields that should not be compared per-field.
// Both slices are sorted in-place by comparatorKey before the merge walk.
func DiffMapSlices(
	left []map[string]any,
	right []map[string]any,
	comparatorKey string,
	excludeKeys map[string]bool,
) *models.Diff[map[string]any] {
	if len(left) < 1 || len(right) < 1 {
		return nil
	}

	leftOnly := make([]map[string]any, 0)
	rightRemaining := make([]map[string]any, 0)
	diffs := make([]models.Item_Diff[map[string]any], 0)

	// Sort both slices so matched rows are adjacent, enabling a single merge walk
	SortMapSlice(left, comparatorKey)
	SortMapSlice(right, comparatorKey)

	i, j := 0, 0
	for i < len(left) && j < len(right) {
		lval, lok := left[i][comparatorKey]
		rval, rok := right[j][comparatorKey]

		// Rows missing the comparator key cannot be matched; drain them individually
		if !lok {
			leftOnly = append(leftOnly, left[i])
			i++
			continue
		}
		if !rok {
			rightRemaining = append(rightRemaining, right[j])
			j++
			continue
		}

		lStr := normalizeVal(lval, true)
		rStr := normalizeVal(rval, true)

		switch {
		case lStr == rStr:
			d := DiffMap(left[i], right[j], comparatorKey, excludeKeys)
			if d.Comparator != nil {
				diffs = append(diffs, d)
			}
			i++
			j++
		case lStr < rStr:
			leftOnly = append(leftOnly, left[i])
			i++
		default:
			rightRemaining = append(rightRemaining, right[j])
			j++
		}
	}

	// Drain any remaining unmatched entries
	for ; i < len(left); i++ {
		leftOnly = append(leftOnly, left[i])
	}
	for ; j < len(right); j++ {
		rightRemaining = append(rightRemaining, right[j])
	}

	return &models.Diff[map[string]any]{
		MissingFromStored:   leftOnly,        // in supplied, not in stored
		MissingFromSupplied: rightRemaining,  // in stored, not in supplied
		Diffs:               diffs,
	}
}
