package tools

import "reflect"

func BinarySearch[T any](key any, slice []T, key_field string) (int, bool) {
	pos, found := binarySearch(key, slice, key_field)
	if found {
		return pos, found
	}
	return -1, found
}

func binarySearch[T any](key any, slice []T, key_field string) (int, bool) {
	slice_length := len(slice)
	if slice_length == 0 {
		return -1, false
	}

	var slice_pos int

	if slice_length > 1 {
		slice_pos = slice_length / 2
	} else {
		slice_pos = len(slice) - 1
	}

	slice_checked_value := reflect.ValueOf(slice[slice_pos])

	if slice_checked_value.Kind() == reflect.Ptr {
		slice_checked_value = slice_checked_value.Elem()
	}

	slice_checked_value = slice_checked_value.FieldByName(key_field)

	if slice_checked_value.Kind() == reflect.Ptr {
		slice_checked_value = slice_checked_value.Elem()
	}

	switch slice_checked_value.Kind() {

	case reflect.String:
		val_as_string := slice_checked_value.Interface().(string)
		key_as_string := key.(string)

		if val_as_string == key {
			return slice_pos, true
		}
		if slice_length == 1 {
			return -1, false
		}
		if val_as_string > key_as_string {
			return binarySearch(key, slice[:slice_pos], key_field)
		}
		pos, found := binarySearch(key, slice[slice_pos+1:], key_field)
		return pos + slice_pos + 1, found

	case reflect.Int, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Int8:
		val_as_int := slice_checked_value.Interface().(int)
		key_as_int := key.(int)

		if val_as_int == key {
			return slice_pos, true
		}
		if slice_length == 1 {
			return -1, false
		}
		if val_as_int > key_as_int {
			return binarySearch(key, slice[:slice_pos], key_field)
		}
		pos, found := binarySearch(key, slice[slice_pos+1:], key_field)
		return pos + slice_pos + 1, found

	case reflect.Float64, reflect.Float32:
		val_as_float := slice_checked_value.Interface().(float64)
		key_as_float := key.(float64)

		if val_as_float == key {
			return slice_pos, true
		}
		if slice_length == 1 {
			return -1, false
		}
		if val_as_float > key_as_float {
			return binarySearch(key, slice[:slice_pos], key_field)
		}
		pos, found := binarySearch(key, slice[slice_pos+1:], key_field)
		return pos + slice_pos + 1, found
	}
	return -1, false
}
