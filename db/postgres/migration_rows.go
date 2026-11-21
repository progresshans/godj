package postgres

import (
	"context"
	"database/sql"
	"errors"
)

// queryPostgresCatalogRows owns the bounded application-catalog transport.
// The query and typed scanner retain the physical SQL and row shape at each
// call site. Control-table integrity checks have a different error contract.
func queryPostgresCatalogRows[T any](
	ctx context.Context,
	subject string,
	maximum int,
	query func() (*sql.Rows, error),
	scan func(*sql.Rows) (T, error),
) (result []T, resultErr error) {
	rows, err := query()
	if rows != nil {
		defer func() {
			if closeErr := rows.Close(); closeErr != nil {
				resultErr = errors.Join(resultErr, classifyPostgresMigrationIO(ctx, "close PostgreSQL "+subject+" rows", closeErr))
			}
		}()
	}
	if err != nil {
		return nil, classifyPostgresMigrationIO(ctx, "inspect PostgreSQL "+subject, err)
	}
	if rows == nil {
		return nil, classifyPostgresMigrationIO(ctx, "inspect PostgreSQL "+subject, errors.New("backend returned nil rows"))
	}
	for rows.Next() {
		if len(result) >= maximum {
			return nil, postgresMigrationCapability("PostgreSQL "+subject+" exceeds the current row limit", errPostgresMigrationPhysicalDrift)
		}
		value, err := scan(rows)
		if err != nil {
			return nil, classifyPostgresMigrationIO(ctx, "scan PostgreSQL "+subject, err)
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyPostgresMigrationIO(ctx, "iterate PostgreSQL "+subject, err)
	}
	return result, nil
}
