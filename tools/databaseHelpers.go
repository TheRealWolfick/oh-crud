package tools

import (
	"errors"
	"net/http"
	"reflect"
)

// Save the struct fields into the query builder values data
func setFromStruct(v any, setFunc func(string, any)) {
	val := reflect.ValueOf(v)
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}

	// Get all database field tags
	dbFields := GetDatabaseFields(v)

	// Iterate through db fields and set values
	for _, field_name := range dbFields {

		field := val.FieldByName(field_name)

		// Skip nil pointers
		if field.Kind() == reflect.Ptr && field.IsNil() {
			continue
		}

		// Get actual value (dereference if pointer)
		var actualValue interface{}
		if field.Kind() == reflect.Ptr {
			actualValue = field.Elem().Interface()
		} else {
			actualValue = field.Interface()
		}

		setFunc(GetDBTagFromField(v, field_name), actualValue)
	}
}

// Save the struct fields into the query builder values data
func SetValueFromStruct(qb *QueryBuilder, v interface{}) {
	setFromStruct(v, qb.SetValue)
}

// Save the struct fields into the query builder where data
func SetWhereFromStruct(qb *QueryBuilder, v interface{}) {
	setFromStruct(v, qb.SetWhereAbsolute)
}

// Function to extract the checksum from the url param 'checksum'
func GetChecksum(r *http.Request) string {
	// Parse the form
	if err := r.ParseForm(); err != nil {
		return ""
	}

	if len(r.URL.Query()) < 1 {
		return ""
	}

	return r.URL.Query().Get("checksum")
}

func SetWhereFromURL[T any](qb *QueryBuilder, r *http.Request, model T) error {
	// Parse the form
	if err := r.ParseForm(); err != nil {
		return err
	}

	if len(r.URL.Query()) < 1 {
		return nil
	}

	val := reflect.ValueOf(model)
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}

	typ := reflect.TypeOf(model)

	if typ.Kind() == reflect.Ptr {
		typ = typ.Elem()
	}

	// Ensure a struct was passed in
	if typ.Kind() != reflect.Struct {
		return errors.New("Not a struct!")
	}

	// Get all database fields
	structFields := GetDatabaseFields(model)

	// Iterate through db fields and set where clauses
	for _, field_name := range structFields {
		db_field := GetDBTagFromField(model, field_name)
		if db_field == "" || db_field == "-" {
			continue
		}

		field_value := r.FormValue(GetDBTagFromField(model, field_name))
		customWhere := GetCustomWhereFromField(model, field_name)

		if field_value == "" {
			continue
		}

		// Check to make sure it is valid
		field, _ := typ.FieldByName(field_name)
		fieldType := field.Type
		if fieldType.Kind() == reflect.Ptr {
			fieldType = fieldType.Elem()
		}

		if IsAbsolute(model, field_name) {
			if ValidateValue(fieldType.Kind(), field_value) == false {
				continue
			}
			if customWhere != "" {
				qb.SetWhereAbsolute(customWhere, field_value)
			} else {
				qb.SetWhereAbsolute(GetDBTagFromField(model, field_name), field_value)
			}
		} else {
			if customWhere != "" {
				qb.SetWhere(customWhere, field_value, fieldType.Kind())
			} else {
				qb.SetWhere(GetDBTagFromField(model, field_name), field_value, fieldType.Kind())
			}
		}
	}

	// Made obsolete due to modification in dbfield params
	/*
	// Iterate over url params.
	for key, val := range r.URL.Query() {
		if strings.Contains(key, ".") {
			q := strings.Split(key, ".")

			// Ensure q[0] is a valid db field
			if db_field := GetDBTagFromField(model, q[0]); db_field == "" {
				continue
			}

			// Add potentially valid json fields
			valid_json_fields := []string{q[0]}
			for _, json_field := range q[1:] {
				if len(json_field) > 0 && len(val) > 0 {
					// Save the json field into qb values (to disallow potential sql injection)
					field_pos := qb.SaveArbitraryValue(json_field)
					valid_json_fields = append(valid_json_fields, fmt.Sprintf("%v", field_pos))

				}
			}

			// Concatenate the json fields
			valid_json := strings.Join(valid_json_fields[:len(valid_json_fields)-1], "->")
			valid_json = fmt.Sprintf("%s->>%s", valid_json, valid_json_fields[len(valid_json_fields)-1])

			// Convert the value into its potential type
			processed_vals := ConvertURLValToAny(val)

			// Save the custom where field
			qb.SetWhereAbsolute(valid_json, processed_vals)
		}
	}
	*/

	return nil
}

func GetDatabaseColumns[T any](model T) []string {
	val := reflect.TypeOf(model)
	cols := make([]string, 0)

	if val.Kind() != reflect.Struct {
		cols = append(cols, "*")
		return cols
	}

	for i := 0; i < val.NumField(); i++ {
		dbCol := val.Field(i).Tag.Get("db")

		if dbCol == "" || dbCol == "-" {
			continue
		}

		cols = append(cols, dbCol)
	}

	return cols
}
