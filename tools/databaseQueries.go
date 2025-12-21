package tools

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strconv"

	"lotusforge.au/api-server/models"
)

func SingleInsert(
	ctx context.Context,
	db models.DBExecutor,
	tableName string,
	item interface{},
) (bool, error) {
	result := RecursiveBatchInsert(ctx, db, tableName, []interface{}{item})
	
	if result.SuccessCount == 1 {
		return true, nil
	}
	
	// Return false with no error since the item is in FailedItems
	return false, nil
}

func RecursiveBatchInsert(
	ctx context.Context,
	db models.DBExecutor, // interface { Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) }
	tableName string,
	items []interface{},
) models.BatchInsertResult {
	result := models.BatchInsertResult{
		SuccessCount: 0,
		FailedItems:  make([]interface{}, 0),
	}

	if len(items) == 0 {
		return result
	}

	// Try to insert the batch
	qb := NewBlankQueryBuilder()
	query := qb.BuildMultiInsert(tableName, items)
	
	cmdTag, err := db.Exec(ctx, query, qb.GetArgs()...)
	
	if err == nil {
		// Success - all items inserted
		result.SuccessCount = int(cmdTag.RowsAffected())
		return result
	}

	// If there's only one item and it failed, add it to failed items
	if len(items) == 1 {
		result.FailedItems = append(result.FailedItems, items[0])
		return result
	}

	// Split the slice in half and try each half recursively
	mid := len(items) / 2
	leftItems := items[:mid]
	rightItems := items[mid:]

	// Process left half
	leftResult := RecursiveBatchInsert(ctx, db, tableName, leftItems)
	result.SuccessCount += leftResult.SuccessCount
	result.FailedItems = append(result.FailedItems, leftResult.FailedItems...)

	// Process right half
	rightResult := RecursiveBatchInsert(ctx, db, tableName, rightItems)
	result.SuccessCount += rightResult.SuccessCount
	result.FailedItems = append(result.FailedItems, rightResult.FailedItems...)

	return result
}


// Save the struct fields into the query builder values data
func setFromStruct(qb QueryBuildTool, v interface{}, setFunc func(string, any)) {
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
func SetValueFromStruct(qb QueryBuildTool, v interface{}) {
	setFromStruct(qb, v, qb.SetValue)
}

// Save the struct fields into the query builder where data
func SetWhereFromStruct(qb QueryBuildTool, v interface{}) {
	setFromStruct(qb, v, qb.SetWhere)
}

func SetWhereFromURL[T any](qb QueryBuildTool, r *http.Request, model T) error {
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
	dbFields := GetDatabaseFields(model)
	
	// Iterate through db fields and set where clauses
	for _, field_name := range dbFields {
		fieldValue := r.FormValue(GetDBTagFromField(model, field_name))
		if fieldValue == "" {
			continue
		}
		
		// Check to make sure it is valid
		field, _ := typ.FieldByName(field_name)
		fieldType := field.Type
		if fieldType.Kind() == reflect.Ptr {
			fieldType = fieldType.Elem()
		}


		switch fieldType.Kind() {
		case reflect.Int, reflect.Int32, reflect.Int64:
			if _, err := strconv.ParseInt(fieldValue, 10, 64); err != nil {
				continue
			}
			
		case reflect.Bool:
			if _, err := strconv.ParseBool(fieldValue); err != nil {
				continue
			}
			
		case reflect.Float32, reflect.Float64:
			if _, err := strconv.ParseFloat(fieldValue, 64); err != nil {
				continue
			}
		}
		
		qb.SetWhere(GetDBTagFromField(model, field_name), fieldValue)
	}
	
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
