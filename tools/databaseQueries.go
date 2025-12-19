package tools

import (
	"context"
	"reflect"

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
