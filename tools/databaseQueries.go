package tools

import (
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"lotusforge.au/api-server/middleware"
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

func createDiff[T any](
	ctx context.Context,
	db models.DBExecQuery,
	tableName string,
	supplied []T,
) (map[string]any, error) {

	// Read data into stored - select all fields to match struct
	query := fmt.Sprintf("SELECT * FROM %s;", tableName)
	rows, err := db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("error reading rows in createDiff: %w", err)
	}
	stored, err := pgx.CollectRows(rows, pgx.RowToStructByName[T])
	if err != nil {
		return nil, fmt.Errorf("error collecting rows in createDiff: %w", err)
	}

	// Split the data between left, right, and diffs
	diff_struct := DiffStructSlices(supplied, stored)

	// Check if diff_struct is nil or empty
	if diff_struct == nil {
		fmt.Println("[createDiff] ERROR: diff_struct is nil - no valid comparator or empty slices")
		return map[string]any{
			"table": tableName,
			"rows_affected": 0,
			"error": "no differences found or invalid comparator",
		}, fmt.Errorf("no valid diff created")
	}

	// Check if there are any actual differences
	totalDiffs := len(diff_struct.Diffs) + len(diff_struct.MissingFromSupplied) + len(diff_struct.MissingFromStored)

	if totalDiffs == 0 {
		return map[string]any{
			"action": "diff",
			"on_table": tableName,
			"rows_affected": 0,
			"message": "no differences found between supplied and stored data",
		}, nil
	}

	// Build checksum and add additional features
	h := md5.New()
	user, userOk := middleware.GetUser(ctx)
	if !userOk {
		user = &models.User{}
	}
	task, taskOk := middleware.GetTask(ctx)
	if !taskOk {
		task = ""
	}

	tempjson := DereferencedString(diff_struct)
	h.Write([]byte(tempjson))
	checksum := fmt.Sprintf("%x", h.Sum(nil))
	
	diff_struct.Checksum = &checksum
	diff_struct.UserGenerated = &user.Username
	diff_struct.TaskID = &task
	diff_struct.DiffType = &tableName

	// Create query building
	qb := NewQueryBuilder()
	query = qb.BuildInsert("diffs", diff_struct)

	// Save the diff into the database
	cmdtag, err := db.Exec(ctx, query, qb.GetArgs()...)

	// Create return map
	ret_map := map[string]any{
		"table": tableName,
		"rows_affected": cmdtag.RowsAffected(),
	}
	
	if err != nil {
		ret_map["error"] = err.Error()
		return ret_map, err
	}

	return ret_map, nil
}

func CreateDiff[T any](
	ctx context.Context,
	db models.DBExecQuery,
	tableName string,
	supplied []T,
) (func(context.Context, ...any) (map[string]any, error)) {
	return func(ctx context.Context, a ...any) (map[string]any, error) {
		return createDiff(ctx, db, tableName, supplied)
	}
}

/*
func createDiff[T any](
	ctx context.Context,
	db models.DBExecQuery,
	tableName string,
	supplied []T,
) (map[string]any, error) {

	// Read data into stored
	rows, err := db.Query(ctx, fmt.Sprintf("SELECT * FROM %s;", tableName))
	if err != nil {
		return nil, fmt.Errorf("error reading rows in createDiff: %w", err)
	}
	stored, err := pgx.CollectRows(rows, pgx.RowToStructByName[T])
	if err != nil {
		return nil, fmt.Errorf("error collecting rows in createDiff: %w", err)
	}

	// Split the data between left, right, and diffs
	fmt.Println(tableName)
	fmt.Println(len(supplied))
	fmt.Println(len(stored))
	diff_struct := DiffStructSlices(supplied, stored)

	// Check if diff_struct is nil or empty
	if diff_struct == nil {
		return map[string]any{
			"table": tableName,
			"rows_affected": 0,
			"error": "no differences found or invalid comparator",
		}, fmt.Errorf("no valid diff created")
	}

	// Build checksum and add additional features
	h := md5.New()
	user, userOk := middleware.GetUser(ctx)
	if !userOk {
		user = &models.User{}
	}
	task, taskOk := middleware.GetTask(ctx)
	if !taskOk {
		task = ""
	}

	tempjson := DereferencedString(diff_struct)
	h.Write([]byte(tempjson))
	checksum := fmt.Sprintf("%x", h.Sum(nil))
	
	diff_struct.Checksum = &checksum
	diff_struct.UserGenerated = &user.Username
	diff_struct.TaskID = &task
	diff_struct.DiffType = &tableName

	// Create query building
	qb := NewQueryBuilder()
	query := qb.BuildInsert("diffs", diff_struct)

	// Debug: print the query and args
	fmt.Printf("Query: %s\n", query)
	fmt.Printf("Args: %v\n", qb.GetArgs())

	// Save the diff into the database
	cmdtag, err := db.Exec(ctx, query, qb.GetArgs()...)

	// Create return map
	ret_map := map[string]any{
		"table": tableName,
		"rows_affected": cmdtag.RowsAffected(),
	}
	
	if err != nil {
		ret_map["error"] = err.Error()
		return ret_map, err
	}

	return ret_map, nil
}
*/
