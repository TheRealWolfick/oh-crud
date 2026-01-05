package tools

import (
	"context"
	"errors"
	"net/http"
	"reflect"

	"lotusforge.au/api-server/models"
)

func SingleInsert(
	ctx context.Context,
	db models.DBExecutor,
	tableName string,
	item any,
) (func(context.Context, ...any) (map[string]any, error)) {
	return RecursiveBatchInsert(ctx, db, tableName, []any{item})
}


func RecursiveBatchInsert(
	ctx context.Context,
	db models.DBExecutor, // interface { Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) }
	tableName string,
	items []any,
) (func(context.Context, ...any) (map[string]any, error)) {
	return func(context.Context, ...any) (map[string]any, error) {
		result := recursiveBatchInsertProcess(ctx, db, tableName, items)
		logData := map[string]any{
			"success_count": result.SuccessCount,
			"failed_items": result.FailedItems,
		}
		return logData, nil
	}
}	

func recursiveBatchInsertProcess(
	ctx context.Context,
	db models.DBExecutor, // interface { Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) }
	tableName string,
	items []any,
) models.BatchInsertResult {
	result := models.BatchInsertResult{
		SuccessCount: 0,
		FailedItems:  make([]any, 0),
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
		result.FailedItems = append(result.FailedItems, map[string]any{"item": items[0], "error": map[string]any{
			"database": err,
		}})
		return result
	}

	// Split the slice in half and try each half recursively
	mid := len(items) / 2
	leftItems := items[:mid]
	rightItems := items[mid:]

	// Process left half
	leftResult := recursiveBatchInsertProcess(ctx, db, tableName, leftItems)
	result.SuccessCount += leftResult.SuccessCount
	result.FailedItems = append(result.FailedItems, leftResult.FailedItems...)

	// Process right half
	rightResult := recursiveBatchInsertProcess(ctx, db, tableName, rightItems)
	result.SuccessCount += rightResult.SuccessCount
	result.FailedItems = append(result.FailedItems, rightResult.FailedItems...)

	result.Query = qb.query
	return result
}


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
func SetValueFromStruct(qb QueryBuildTool, v interface{}) {
	setFromStruct(v, qb.SetValue)
}

// Save the struct fields into the query builder where data
func SetWhereFromStruct(qb QueryBuildTool, v interface{}) {
	setFromStruct(v, qb.SetWhereAbsolute)
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
		field_value := r.FormValue(GetDBTagFromField(model, field_name))
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
			qb.SetWhereAbsolute(GetDBTagFromField(model, field_name), field_value)
		} else {
			qb.SetWhere(GetDBTagFromField(model, field_name), field_value, fieldType.Kind())
		}
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
