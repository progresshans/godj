package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/progresshans/godj/query"
)

const (
	sqlStateNotNullViolation  = "23502"
	sqlStateForeignKey        = "23503"
	sqlStateUniqueViolation   = "23505"
	sqlStateStringTruncation  = "22001"
	sqlStateUndefinedTable    = "42P01"
	sqlStateInvalidSchemaName = "3F000"
	sqlStateQueryCanceled     = "57014"
)

// classifyDatabaseError derives stable GoDj errors only from PostgreSQL's
// structured SQLSTATE fields. It never parses localized driver messages.
func classifyDatabaseError(ctx context.Context, operation, schema, table string, err error) error {
	if err == nil {
		return nil
	}
	if ctx != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
	}
	if redacted, connectFailure := redactPostgresConnectFailure(err); connectFailure {
		return fmt.Errorf("%s PostgreSQL: %w", operation, redacted)
	}

	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		return fmt.Errorf("%s PostgreSQL: %w", operation, err)
	}

	detail := fmt.Sprintf("PostgreSQL SQLSTATE %s rejected %s", postgresError.Code, operation)
	switch postgresError.Code {
	case sqlStateUndefinedTable:
		return &query.Error{
			Category: query.CategoryBackend,
			Code:     query.CodeMissingTable,
			Detail:   detail,
			Cause:    err,
		}
	case sqlStateInvalidSchemaName:
		return &query.Error{
			Category: query.CategoryBackend,
			Code:     query.CodeInvalidPlan,
			Detail:   detail,
			Cause:    err,
		}
	case sqlStateNotNullViolation:
		return &query.Error{
			Category: query.CategoryIntegrity,
			Code:     query.CodeRequiredField,
			Field:    postgresError.ColumnName,
			Detail:   detail,
			Cause:    err,
		}
	case sqlStateForeignKey:
		if operation == "delete" {
			return &query.Error{
				Category: query.CategoryIntegrity,
				Code:     query.CodeProtectedForeignKey,
				Detail:   detail,
				Cause:    err,
			}
		}
		if operation != "insert" && operation != "update" {
			break
		}
		return &query.Error{
			Category: query.CategoryIntegrity,
			Code:     query.CodeRelatedObjectMissing,
			Field:    postgresError.ColumnName,
			Detail:   detail,
			Cause:    err,
		}
	case sqlStateStringTruncation:
		return &query.Error{
			Category: query.CategoryField,
			Code:     query.CodeInvalidValue,
			Field:    postgresError.ColumnName,
			Detail:   detail,
			Cause:    err,
		}
	case sqlStateUniqueViolation:
		if isPrimaryKeyViolation(postgresError, schema, table) {
			return &query.Error{
				Category: query.CategoryIntegrity,
				Code:     query.CodeUniquePrimaryKey,
				Detail:   detail,
				Cause:    err,
			}
		}
	case sqlStateQueryCanceled:
		// A server-side cancellation without a canceled Go context remains a
		// driver error; treating it as context.Canceled would invent ownership.
	}
	return fmt.Errorf("%s PostgreSQL SQLSTATE %s: %w", operation, postgresError.Code, err)
}

func redactPostgresConnectFailure(err error) (error, bool) {
	var connectError *pgconn.ConnectError
	if !errors.As(err, &connectError) {
		return nil, false
	}
	// Never retain the original carrier: pgconn.ConnectError exposes the full
	// parsed Config, including User, Database, and Password, through errors.As.
	return redactConnectionError(err), true
}

func isPrimaryKeyViolation(postgresError *pgconn.PgError, schema, table string) bool {
	if postgresError == nil || postgresError.Code != sqlStateUniqueViolation {
		return false
	}
	if postgresError.SchemaName != "" && postgresError.SchemaName != schema {
		return false
	}
	if postgresError.TableName != "" && postgresError.TableName != table {
		return false
	}
	if postgresError.ConstraintName == defaultPrimaryKeyConstraintName(table) {
		return true
	}
	migrationConstraint, err := postgresPrimaryKeyConstraintName(table)
	return err == nil && postgresError.ConstraintName == migrationConstraint
}

func defaultPrimaryKeyConstraintName(table string) string {
	const suffix = "_pkey"
	maximumTableBytes := postgresIdentifierMaxBytes - len(suffix)
	if len(table) > maximumTableBytes {
		table = table[:maximumTableBytes]
	}
	return table + suffix
}

func transactionUnknown(detail string, cause error) *query.Error {
	return &query.Error{
		Category: query.CategoryBackend,
		Code:     query.CodeTransactionOutcomeUnknown,
		Detail:   detail,
		Cause:    cause,
	}
}

func commitUnknown(cause error) *query.Error {
	return &query.Error{
		Category: query.CategoryBackend,
		Code:     query.CodeCommitOutcomeUnknown,
		Detail:   "PostgreSQL commit outcome is unknown; do not retry automatically",
		Cause:    cause,
	}
}
