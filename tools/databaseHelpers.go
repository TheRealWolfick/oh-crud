package tools

import (
	"errors"
	"fmt"
	"net/http"
	"reflect"

	"lotusforge.au/api-server/models"
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

func DynamicSetWhereFromURL(qb *QueryBuilder, r *http.Request, cfg *models.DataModel) error {
	// Parse the form
	qb.logger.Debug("Setting where values from URL")
	if err := r.ParseForm(); err != nil {
		return err
	}

	if len(r.URL.Query()) < 1 {
		qb.logger.Debug("No valid URL values passed")
		return nil
	}
	
	// Get valid elements
	for _, field_cfg := range cfg.Fields {
		// Read the database field name
		if field_cfg.DB == nil || *field_cfg.DB == "" || *field_cfg.DB == "-" { continue }

		// Search for any values passed in with the query in the url
		url_value := r.FormValue(*field_cfg.JSON)
		if url_value == "" { continue }

		// Get field type and validate it
		dereferenced := DynamicValueDeref(field_cfg.Type)
		qb.logger.Debug("Dereferenced the field type", "field_type", dereferenced)
		if !dereferenced.IsValid() {
			return fmt.Errorf("An invalid data type was found in the config", "config name", *cfg.Name)
		}
		field_type, err := DecodeDynamicFieldType(dereferenced.Interface().(string))
		if err != nil { return err }

		// Is this an absolute value (all non absolute values are passed as strings for parsing in setwhere
		is_abs := *field_cfg.Absolute
		if is_abs {
			if ValidateValue(field_type, url_value) == false {
				continue
			}
			if field_cfg.Custom_Where != nil {
				qb.SetWhereAbsolute(*field_cfg.Custom_Where, url_value)
			} else {
				qb.SetWhereAbsolute(*field_cfg.DB, url_value)
			}
		} else {
			if field_cfg.Custom_Where != nil {
				qb.SetWhere(*field_cfg.Custom_Where, url_value, field_type)
			} else {
				qb.SetWhere(*field_cfg.DB, url_value, field_type)
			}
		}
	}

	return nil
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

	return nil
}

func DynamicGetDatabaseColumns(cfg *models.DataModel, pk_only bool, req_only bool) []string {
	database_columns := []string{}
	for _, field_cfg := range cfg.Fields {
		if pk_only || req_only {
			if pk_only {
				if *field_cfg.PK { database_columns = append(database_columns, *field_cfg.DB) }
			} else {
				if *field_cfg.Req || *field_cfg.PK { database_columns = append(database_columns, *field_cfg.DB) }
			}
		} else {
			database_columns = append(database_columns, *field_cfg.DB)
		}
		database_columns = append(database_columns, *field_cfg.DB)
	}
	return database_columns
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
