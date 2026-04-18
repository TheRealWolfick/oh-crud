package tools

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"lotusforge.au/api-server/middleware"
	"lotusforge.au/api-server/models"
)

// SingleInsert queues a single-row insert using the config-driven schema.
func SingleInsert(
	ctx context.Context,
	db models.DBExecutor,
	cfg *models.DataModel,
	item map[string]any,
) func(context.Context, ...any) (map[string]any, error) {
	return RecursiveBatchInsert(ctx, db, cfg, []map[string]any{item})
}

// CreateDiff queues a diff comparing supplied rows against the stored table.
func CreateDiff(
	ctx context.Context,
	db models.DBExecQuery,
	cfg *models.DataModel,
	supplied []map[string]any,
	note string,
) func(context.Context, ...any) (map[string]any, error) {
	return func(ctx context.Context, a ...any) (map[string]any, error) {
		return createDiff(ctx, db, cfg, supplied, note)
	}
}

func createDiff(
	ctx context.Context,
	db models.DBExecQuery,
	cfg *models.DataModel,
	supplied []map[string]any,
	note string,
) (map[string]any, error) {
	log, ok := middleware.GetLogger(ctx)
	if !ok {
		log = GetBasicLogger()
	}

	comparatorKey := GetDiffComparatorKey(cfg)
	if comparatorKey == "" {
		return nil, fmt.Errorf("no diff comparator field found in config for table %s", *cfg.Table_name)
	}
	excludeKeys := BuildExcludeKeysFromConfig(cfg)

	cols := []string{}
	for field_name := range cfg.Fields {
		cols = append(cols, field_name)
	}
	qb := NewQueryBuilder(log)
	query := qb.BuildSelect(*cfg.Table_name, cols)

	rows, err := db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("error reading rows in createDiff_Dynamic: %w", err)
	}
	stored, err := pgx.CollectRows(rows, pgx.RowToMap)
	if err != nil {
		return nil, fmt.Errorf("error collecting rows in createDiff_Dynamic: %w", err)
	}
	coerced_stored := []map[string]any{}
	for _, row := range stored {
		coerced_row, err := DecodeAndCoerceFromDB(row, cfg, comparatorKey)
		if err != nil {
			continue
		}
		coerced_stored = append(coerced_stored, coerced_row)
	}

	diff_struct := DiffMapSlices(supplied, coerced_stored, comparatorKey, excludeKeys)
	if diff_struct == nil {
		log.Debug("Supplied[0:10]", "data", supplied[0:10])
		log.Debug("Stored[0:10]", "data", stored[0:10])
		log.Debug("Coerced[0:10]", "data", coerced_stored[0:10])
		return map[string]any{
			"table":         *cfg.Table_name,
			"rows_affected": 0,
			"error":         "no differences found or invalid comparator",
		}, fmt.Errorf("no valid diff created")
	}

	totalDiffs := len(diff_struct.Diffs) + len(diff_struct.MissingFromSupplied) + len(diff_struct.MissingFromStored)
	if totalDiffs == 0 {
		return map[string]any{
			"action":        "diff",
			"on_table":      *cfg.Table_name,
			"rows_affected": 0,
			"message":       "no differences found between supplied and stored data",
		}, nil
	}

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
	tableName := *cfg.Table_name

	diff_struct.Checksum = &checksum
	diff_struct.UserGenerated = &user.Username
	diff_struct.TaskID = &task.Id
	diff_struct.DiffType = &tableName
	diff_struct.Note = &note

	insertQb := NewQueryBuilder(log)
	insertQuery := insertQb.BuildInsert("diffs", diff_struct)
	cmdtag, err := db.Exec(ctx, insertQuery, insertQb.GetArgs()...)

	ret_map := map[string]any{
		"table":         *cfg.Table_name,
		"rows_affected": cmdtag.RowsAffected(),
	}
	if err != nil {
		ret_map["error"] = err.Error()
		return ret_map, err
	}
	return ret_map, nil
}

// MultiUpdate queues an update for each supplied row using the config-driven schema.
func MultiUpdate(
	ctx context.Context,
	db models.DBExecQuery,
	cfg *models.DataModel,
	supplied []map[string]any,
) func(context.Context, ...any) (map[string]any, error) {
	return func(ctx context.Context, a ...any) (map[string]any, error) {
		return multiUpdate(ctx, db, cfg, supplied)
	}
}

// MultiDelete queues a delete for each supplied row using the config-driven schema.
func MultiDelete(
	ctx context.Context,
	db models.DBExecutor,
	cfg *models.DataModel,
	supplied []map[string]any,
) func(context.Context, ...any) (map[string]any, error) {
	return func(ctx context.Context, a ...any) (map[string]any, error) {
		return multiDelete(ctx, db, cfg, supplied)
	}
}

// RecursiveBatchInsert inserts a batch of config-typed rows.
// On failure the batch is split in half and retried recursively, isolating failing rows.
func RecursiveBatchInsert(
	ctx context.Context,
	db models.DBExecutor,
	cfg *models.DataModel,
	items []map[string]any,
) func(context.Context, ...any) (map[string]any, error) {
	return func(context.Context, ...any) (map[string]any, error) {
		result := recursiveBatchInsertProcess(ctx, db, cfg, items)
		failed_count := len(result.FailedItems)
		logData := map[string]any{
			"total_count":   result.SuccessCount + failed_count,
			"success_count": result.SuccessCount,
			"failed_count":  failed_count,
			"failed_items":  result.FailedItems,
		}
		return logData, nil
	}
}

func recursiveBatchInsertProcess(
	ctx context.Context,
	db models.DBExecutor,
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

	log, ok := middleware.GetLogger(ctx)
	if !ok {
		log = GetBasicLogger()
	}
	qb := NewQueryBuilder(log)
	query := qb.BuildMultiInsert(cfg, items)

	cmdTag, err := db.Exec(ctx, query, qb.GetArgs()...)

	if err == nil {
		log.Debug("Insert successful", "count", cmdTag.RowsAffected())
		result.SuccessCount = int(cmdTag.RowsAffected())
		return result
	}

	if len(items) == 1 {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			result.FailedItems = append(result.FailedItems, map[string]any{
				"item":           items[0],
				"rectified":      strings.Contains(pgErr.Message, "duplicate key value violates"),
				"date_rectified": nil,
				"error":          pgErr,
			})
			return result
		}
	}

	mid := len(items) / 2
	leftResult := recursiveBatchInsertProcess(ctx, db, cfg, items[:mid])
	result.SuccessCount += leftResult.SuccessCount
	result.FailedItems = append(result.FailedItems, leftResult.FailedItems...)

	rightResult := recursiveBatchInsertProcess(ctx, db, cfg, items[mid:])
	result.SuccessCount += rightResult.SuccessCount
	result.FailedItems = append(result.FailedItems, rightResult.FailedItems...)

	result.Query = qb.query
	return result
}

func multiUpdate(
	ctx context.Context,
	db models.DBExecutor,
	cfg *models.DataModel,
	supplied []map[string]any,
) (map[string]any, error) {
	report := models.MultiUpdateResult{
		TotalUpdates: len(supplied),
		SuccessCount: 0,
		Errors:       []models.MultiUpdateError{},
	}

	log, ok := middleware.GetLogger(ctx)
	if !ok {
		log = GetBasicLogger()
	}

	for idx, row := range supplied {
		where_fields, ok := FindRowKeyFields(row, cfg)
		if !ok {
			log.Error("Multi Update: no key fields found in row", "idx", idx)
			report.Errors = append(report.Errors, models.MultiUpdateError{ID: idx, Error: fmt.Errorf("no identifying key (PK or unique key) found in row")})
			continue
		}

		where_set := map[string]bool{}
		for _, f := range where_fields {
			where_set[f] = true
		}

		qb := NewQueryBuilder(log.With("key_fields", where_fields))
		for k, v := range row {
			if where_set[k] {
				qb.SetWhereAbsolute(k, v)
			} else {
				qb.SetValue(k, v)
			}
		}

		query := qb.BuildUpdate_Dynamic(cfg)
		cmdtag, err := db.Exec(ctx, query, qb.GetArgs()...)

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

func multiDelete(
	ctx context.Context,
	db models.DBExecutor,
	cfg *models.DataModel,
	supplied []map[string]any,
) (map[string]any, error) {
	report := models.MultiUpdateResult{
		TotalUpdates: len(supplied),
		SuccessCount: 0,
		Errors:       []models.MultiUpdateError{},
	}

	log, ok := middleware.GetLogger(ctx)
	if !ok {
		log = GetBasicLogger()
	}

	for idx, row := range supplied {
		where_fields, ok := FindRowKeyFields(row, cfg)
		if !ok {
			log.Error("Multi Delete: no key fields found in row", "idx", idx)
			report.Errors = append(report.Errors, models.MultiUpdateError{ID: idx, Error: fmt.Errorf("no identifying key (PK or unique key) found in row")})
			continue
		}

		qb := NewQueryBuilder(log.With("key_fields", where_fields))
		for _, f := range where_fields {
			qb.SetWhereAbsolute(f, row[f])
		}

		query := qb.BuildDelete_Dynamic(cfg)
		cmdtag, err := db.Exec(ctx, query, qb.GetArgs()...)
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
