package models

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type FailedItem struct {
	Row   map[string]any  `json:"row"`
	Error string          `json:"error"`
}

type UpdateSuccessItem struct {
	WhereFields map[string]any `json:"where_fields"`
	UpdatedValues map[string]any `json:"updated_values"`
}

type DeleteSuccessItem struct {
	WhereFields map[string]any
}

type BatchInsertResult struct {
	SuccessCount int
	SuccessItems []interface{}
	FailedItems  []FailedItem
	Query 			 string
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

