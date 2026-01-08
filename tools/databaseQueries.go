package tools

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
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
		failed_count := len(result.FailedItems)
		logData := map[string]any{
			"total_count": result.SuccessCount + failed_count,
			"success_count": result.SuccessCount,
			"failed_count": failed_count,
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
	qb := NewQueryBuilder()
	query := qb.BuildMultiInsert(tableName, items)

	cmdTag, err := db.Exec(ctx, query, qb.GetArgs()...)

	if err == nil {
		// Success - all items inserted
		result.SuccessCount = int(cmdTag.RowsAffected())
		return result
	}

	// If there's only one item and it failed, add it to failed items
	if len(items) == 1 {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			result.FailedItems = append(result.FailedItems, map[string]any{
				"item": items[0], 
				"rectified": strings.Contains(pgErr.Message, "duplicate key value violates"),
				"date_rectified": nil,
				"error": pgErr,
			})

			return result
		}
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
func SetValueFromStruct(qb *QueryBuilder, v interface{}) {
	setFromStruct(v, qb.SetValue)
}

// Save the struct fields into the query builder where data
func SetWhereFromStruct(qb *QueryBuilder, v interface{}) {
	setFromStruct(v, qb.SetWhereAbsolute)
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
