package sqlite

import (
	"errors"
	"strings"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	modernsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// SQLite reports errors from scalar functions through the native driver both
// when opening a query and while advancing its rows. Reads and writes expose
// the same field error while retaining the original driver cause.
func classifySQLiteScalarError(err error) error {
	if err == nil || errors.Is(err, &query.Error{Category: query.CategoryField, Code: query.CodeInvalidValue}) {
		return err
	}
	var native *modernsqlite.Error
	if !errors.As(err, &native) || native.Code() != sqlite3.SQLITE_ERROR {
		return err
	}
	message, valid := strings.CutPrefix(native.Error(), "SQL logic error: ")
	if !valid {
		return err
	}
	// A missing table/column may itself contain one of these marker strings.
	// Only the native function's diagnostic prefix owns this classification.
	matches := func(marker string) bool {
		tail, found := strings.CutPrefix(message, marker)
		return found && (tail == " (1)" || strings.HasPrefix(tail, "\n"))
	}
	var detail string
	switch {
	case matches(invalidIntegerExpression):
		detail = "SQLite integer expression is outside the exact int64 domain"
	case matches(invalidDecimalExpression), matches(errDecimalAggregateInput.Error()):
		detail = "SQLite decimal expression is outside its exact value or declared field domain"
	default:
		return err
	}
	return &query.Error{Category: query.CategoryField, Code: query.CodeInvalidValue, Detail: detail, Cause: err}
}

// This adapter only classifies errors. The underlying rowset and its existing
// transaction or stream owner retain cancellation, closing and lease lifetime.
type scalarQueryRows struct{ db.Rows }

func (rows scalarQueryRows) Scan(destinations ...any) error {
	return classifySQLiteScalarError(rows.Rows.Scan(destinations...))
}
func (rows scalarQueryRows) Err() error   { return classifySQLiteScalarError(rows.Rows.Err()) }
func (rows scalarQueryRows) Close() error { return classifySQLiteScalarError(rows.Rows.Close()) }
