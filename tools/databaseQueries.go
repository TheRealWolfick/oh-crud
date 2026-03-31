package tools

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
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
func SingleInsert_Dynamic(
	ctx context.Context,
	db models.DBExecutor,
	cfg *models.DataModel,
	item map[string]any,
) (func(context.Context, ...any) (map[string]any, error)) {
	return RecursiveBatchInsert_Dynamic(ctx, db, cfg, []map[string]any{item})
}


func CreateDiff[T any](
	ctx context.Context,
	db models.DBExecQuery,
	tableName string,
	supplied []T,
	note string,
) (func(context.Context, ...any) (map[string]any, error)) {
	return func(ctx context.Context, a ...any) (map[string]any, error) {
		return createDiff(ctx, db, tableName, supplied, note)
	}
}


func MultiUpdate[T any](
	ctx context.Context,
	db models.DBExecQuery,
	tableName string,
	supplied []T,
) (func(context.Context, ...any) (map[string]any, error)) {
	return func(ctx context.Context, a ...any) (map[string]any, error) {
		return multiUpdate(ctx, db, tableName, supplied)
	}
}
func MultiUpdate_Dynamic(
	ctx context.Context,
	db models.DBExecQuery,
	cfg *models.DataModel,
	supplied []map[string]any,
) (func(context.Context, ...any) (map[string]any, error)) {
	return func(ctx context.Context, a ...any) (map[string]any, error) {
		return multiUpdate_Dynamic(ctx, db, cfg, supplied)
	}
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
func RecursiveBatchInsert_Dynamic(
	ctx context.Context,
	db models.DBExecutor, // interface { Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) }
	cfg *models.DataModel,
	items []map[string]any,
) (func(context.Context, ...any) (map[string]any, error)) {
	return func(context.Context, ...any) (map[string]any, error) {
		result := recursiveBatchInsertProcess_Dynamic(ctx, db, cfg, items)
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
	log, ok := middleware.GetLogger(ctx); if !ok { log = GetBasicLogger() }
	qb := NewQueryBuilder(log)
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
func recursiveBatchInsertProcess_Dynamic(
	ctx context.Context,
	db models.DBExecutor, // interface { Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) }
	cfg *models.DataModel,
	items []map[string]any,
) models.BatchInsertResult {
	result := models.BatchInsertResult{
		SuccessCount: 0,
		FailedItems:  make([]any, 0),
	}

	if len(items) == 0 {
		return result
	}

	// Try to insert the batch
	log, ok := middleware.GetLogger(ctx); if !ok { log = GetBasicLogger() }
	qb := NewQueryBuilder(log)
	query := qb.BuildMultiInsert_Dynamic(cfg, items)

	cmdTag, err := db.Exec(ctx, query, qb.GetArgs()...)

	if err == nil {
		// Success - all items inserted
		log.Debug("Insert successful", "count", cmdTag.RowsAffected())
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
	leftResult := recursiveBatchInsertProcess_Dynamic(ctx, db, cfg, leftItems)
	result.SuccessCount += leftResult.SuccessCount
	result.FailedItems = append(result.FailedItems, leftResult.FailedItems...)

	// Process right half
	rightResult := recursiveBatchInsertProcess_Dynamic(ctx, db, cfg, rightItems)
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
	note string,
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
	task, _ := middleware.GetTask(ctx)
	if len(task.Id) != 32 {
		task.Id, _ = Generate32CharString()
	}

	tempjson := DereferencedString(diff_struct)
	h.Write([]byte(tempjson))
	checksum := fmt.Sprintf("%x", h.Sum(nil))
	
	diff_struct.Checksum = &checksum
	diff_struct.UserGenerated = &user.Username
	diff_struct.TaskID = &task.Id
	diff_struct.DiffType = &tableName
	diff_struct.Note = &note

	// Create query building
	log, ok := middleware.GetLogger(ctx); if !ok { log = GetBasicLogger() }
	qb := NewQueryBuilder(log)
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


// Iterates over all the supplied items, extracts the primary keys into the where column, and runs an update call on all items.
// 
// Function does not check whether there is any actual updates and will report an update as "successful" even if there is no changes.
// It only reports an error when the database has an error.
func multiUpdate[T any](
	ctx context.Context,
	db models.DBExecutor,
	tableName string,
	supplied []T,
) (map[string]any, error) {

	// Create report update
	report := models.MultiUpdateResult{
		TotalUpdates: len(supplied),
		SuccessCount: 0,
		Errors: []models.MultiUpdateError{},
	}

	log, ok := middleware.GetLogger(ctx); if !ok { log = GetBasicLogger() }
	// Update items
	for idx := range supplied {
		// Create a new query builder 
		update_item := &supplied[idx]
		qb := NewQueryBuilder(log.With(update_item))

		// Extract the primary keys from the struct and save them into the where clause
		prim_keys := GetPrimaryKeys(*update_item)
		val := reflect.ValueOf(update_item)
		if val.Kind() == reflect.Ptr {val = val.Elem()}
		for _, key := range prim_keys {
			// Check if value in struct
			value := val.FieldByName(key)
			if value.Kind() == reflect.Ptr {
				if value.IsNil() {continue}
				value = value.Elem()
			}
			if value.IsZero() {continue}

			// Move value to where clause and remove from struct
			qb.SetWhere(key, value.Interface(), value.Kind())
			field := val.FieldByName(key)
			field.Set(reflect.Zero(field.Type()))
		}

		// Save the value into the query builder
		SetValueFromStruct(qb, update_item)

		// Build the query
		query := qb.BuildUpdateNoURLParams(tableName, *update_item)

		// Execute
		cmdtag, err := db.Exec(ctx, query, qb.GetArgs()...)
		
		// Add to report update
		if err == nil && cmdtag.RowsAffected() > 0 {
			report.SuccessCount = report.SuccessCount + int(cmdtag.RowsAffected())
		} else {
			report.Errors = append(report.Errors, models.MultiUpdateError{ID: idx, Error: err})
		}
	}

	report_encoded, _ := json.Marshal(report)
	report_to_return := map[string]any{}
	err := json.Unmarshal(report_encoded, &report_to_return)
	return report_to_return, err
}
func multiUpdate_Dynamic(
	ctx context.Context,
	db models.DBExecutor,
	cfg *models.DataModel,
	supplied []map[string]any,
) (map[string]any, error) {

	// Create report update
	report := models.MultiUpdateResult{
		TotalUpdates: len(supplied),
		SuccessCount: 0,
		Errors: []models.MultiUpdateError{},
	}

	log, _ := middleware.GetLogger(ctx)

	log, ok := middleware.GetLogger(ctx); if !ok { log = GetBasicLogger() }
	prim_keys := GetRequiredJSONFields_FromConfig(cfg, true)
	if len(prim_keys) < 1 {
		log.Error("Multi Update: Could not extract primary key from config")
		return nil, fmt.Errorf("Multi Update: Could not extract primary key from config")
	}
	prim_key := prim_keys[0]

	// Update items
	for idx, row := range supplied {
		// Create a new query builder 
		qb := NewQueryBuilder(log.With("primary_key", row[prim_key]))

		for k, v := range row {
			if k == prim_key {
				qb.SetWhereAbsolute(k, v)
			} else {
				qb.SetValue(k, v)
			}
		}

		query := qb.BuildUpdate_Dynamic(cfg)

		// Execute
		cmdtag, err := db.Exec(ctx, query, qb.GetArgs()...)
		
		// Add to report update
		if err == nil && cmdtag.RowsAffected() > 0 {
			report.SuccessCount = report.SuccessCount + int(cmdtag.RowsAffected())
		} else {
			report.Errors = append(report.Errors, models.MultiUpdateError{ID: idx, Error: err})
		}
	}

	report_encoded, _ := json.Marshal(report)
	report_to_return := map[string]any{}
	err := json.Unmarshal(report_encoded, &report_to_return)
	return report_to_return, err
}
