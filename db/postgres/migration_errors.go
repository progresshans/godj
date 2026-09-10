package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
)

const (
	postgresSQLStateSerializationFailure = "40001"
	postgresSQLStateDeadlockDetected     = "40P01"
	postgresSQLStateLockNotAvailable     = "55P03"
)

func newPostgresRevisionFenceError(
	kind migrationbackend.RevisionFenceFailureKind,
	cause error,
) *migrationbackend.RevisionFenceError {
	return &migrationbackend.RevisionFenceError{Kind: kind, Cause: cause}
}

// classifyPostgresMigrationIO preserves backend capability and revision-fence
// carriers while attaching the migration stage to ordinary driver failures.
// Context cancellation owns the result only when the supplied context has in
// fact been canceled; SQLSTATE 57014 alone does not invent that ownership.
func classifyPostgresMigrationIO(ctx context.Context, stage string, err error) error {
	if err == nil {
		return nil
	}
	if migrationbackend.IsCapabilityError(err) || migrationbackend.IsRevisionFenceError(err) {
		return err
	}
	if ctx != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
	}
	if redacted, connectFailure := redactPostgresConnectFailure(err); connectFailure {
		return fmt.Errorf("%s PostgreSQL migration: %w", stage, redacted)
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		return fmt.Errorf("%s PostgreSQL SQLSTATE %s: %w", stage, postgresError.Code, err)
	}
	return fmt.Errorf("%s PostgreSQL migration: %w", stage, err)
}

// classifyPostgresRevisionContention is used only before a COMMIT attempt.
// PostgreSQL can report lock/deadlock/serialization conflicts at any statement
// in the fenced transaction. A literal COMMIT error bypasses this helper and is
// always returned with CommitUnknown durability.
func classifyPostgresRevisionContention(ctx context.Context, stage string, err error) error {
	if err == nil {
		return nil
	}
	if migrationbackend.IsCapabilityError(err) || migrationbackend.IsRevisionFenceError(err) {
		return err
	}
	if ctx != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
	}
	if _, connectFailure := redactPostgresConnectFailure(err); connectFailure {
		return classifyPostgresMigrationIO(ctx, stage, err)
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case postgresSQLStateSerializationFailure,
			postgresSQLStateDeadlockDetected,
			postgresSQLStateLockNotAvailable:
			return newPostgresRevisionFenceError(
				migrationbackend.RevisionFenceFailureContended,
				fmt.Errorf("%s PostgreSQL SQLSTATE %s: %w", stage, postgresError.Code, err),
			)
		}
	}
	return classifyPostgresMigrationIO(ctx, stage, err)
}

func postgresRevisionIntegrity(detail string, cause error) error {
	if cause == nil {
		cause = errors.New(detail)
	} else if detail != "" {
		cause = fmt.Errorf("%s: %w", detail, cause)
	}
	return newPostgresRevisionFenceError(migrationbackend.RevisionFenceFailureIntegrity, cause)
}
