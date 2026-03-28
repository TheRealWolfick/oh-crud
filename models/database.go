package models

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type BatchInsertResult struct {
	SuccessCount int
	FailedItems  []interface{}
	Query 			 string
}

type MultiUpdateError struct {
	ID    int   `json:"idx"`
	Error any   `json:"db_error"`
}

type MultiUpdateResult struct {
	TotalUpdates int                `json:"total_updates"`
	SuccessCount int                `json:"success_count"`
	Errors       []MultiUpdateError   `json:"errors"`
}

type DBExecutor interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type DBQueryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type DBExecQuery interface {
	DBExecutor
	DBQueryer
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// CommandTag interface to abstract the database command tag
type CommandTag interface {
	RowsAffected() int64
}

type DBHandler interface {
	DBExecutor
	DBQueryer
	DBExecQuery
}

type DBReportable interface {
	ResultAsJSONString() string
}

