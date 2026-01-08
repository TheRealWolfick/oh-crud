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

type ExecError struct {
	Code int
	File string
	Hint string
	Line int
	Where string
	Detail string
	Message string
	Routine string
	Position int
	Severity string
	TableName string
	ColumnName string
	SchemaName string
	DataTypeName string
	InternalQuery string
	ConstraintName string
	InternalPosition int
	SeverityUnlocalized string
}

type DBExecutor interface {
	Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error)
}

type DBQueryer interface {
	QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row
}

// CommandTag interface to abstract the database command tag
type CommandTag interface {
	RowsAffected() int64
}

type DBHandler interface {
	DBExecutor
	DBQueryer
}

type DBReportable interface {
	ResultAsJSONString() string
}

