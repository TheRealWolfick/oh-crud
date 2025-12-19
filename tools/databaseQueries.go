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

//
func SetFromStruct(qb *QueryBuilder, v interface{}) {
	val := reflect.ValueOf(v)
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}
	
	typ := val.Type()
	
	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		dbTag := typ.Field(i).Tag.Get("db")
		
		if dbTag == "" || dbTag == "-" {
			continue
		}
		
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
		
		qb.Set(dbTag, actualValue)
	}
}



func SetFromURL[T any](qb QueryBuildTool, r *http.Request, model T) error {
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

	// Ensure a struct was passed in
	if reflect.TypeOf(model).Kind() != reflect.Struct {
		return errors.New("Not a struct!")
	}

	typ := val.Type()
	for i := 0; i < val.NumField(); i++ {
		
		field_name := typ.Field(i).Tag.Get("db")
		if field_name == "" || field_name == "-" {
			continue
		}
		
		field_value := r.FormValue(field_name)
		if field_value == "" {
			continue
		}

		// Check to make sure it is valid
		switch typ.Field(i).Type.Kind() {
		case reflect.Int, reflect.Int32, reflect.Int64:
			if _, err := strconv.ParseInt(field_value, 10, 64); err != nil {
				continue 
			}

		case reflect.Bool:
			if _, err := strconv.ParseBool(field_value); err != nil {
				continue
			}

		case reflect.Float32, reflect.Float64:
			if _, err := strconv.ParseFloat(field_value, 64); err != nil {
				continue
			}
		}
		qb.SetWhere(field_name, field_value)
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
