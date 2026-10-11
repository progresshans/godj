package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
)

type postgresConnectErrorCarrier struct {
	connect *pgconn.ConnectError
}

func (e *postgresConnectErrorCarrier) Error() string {
	return "connect user=secret_user database=secret_database password=secret_password"
}

func (e *postgresConnectErrorCarrier) As(target any) bool {
	connect, ok := target.(**pgconn.ConnectError)
	if !ok {
		return false
	}
	*connect = e.connect
	return true
}

func TestPostgresRevisionContentionUsesStructuredSQLSTATE(t *testing.T) {
	t.Parallel()
	for _, state := range []string{
		postgresSQLStateSerializationFailure,
		postgresSQLStateDeadlockDetected,
		postgresSQLStateLockNotAvailable,
	} {
		state := state
		t.Run(state, func(t *testing.T) {
			t.Parallel()
			cause := &pgconn.PgError{Code: state, Message: "localized text is not an API"}
			err := classifyPostgresRevisionContention(context.Background(), "claim revision", cause)
			var fenceError *migrationbackend.RevisionFenceError
			if !errors.As(err, &fenceError) || fenceError == nil || fenceError.Kind != migrationbackend.RevisionFenceFailureContended {
				t.Fatalf("error = %v, want contended revision fence", err)
			}
			if !errors.Is(err, cause) {
				t.Fatalf("error lost structured cause: %v", err)
			}
		})
	}
}

func TestPostgresRevisionContentionIsConservative(t *testing.T) {
	t.Parallel()
	cause := &pgconn.PgError{Code: sqlStateUniqueViolation, Message: "duplicate"}
	err := classifyPostgresRevisionContention(context.Background(), "record migration", cause)
	if migrationbackend.IsRevisionFenceError(err) {
		t.Fatalf("unique violation was misclassified as revision contention: %v", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("error lost cause: %v", err)
	}
}

func TestPostgresMigrationErrorOwnership(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cause := &pgconn.PgError{Code: postgresSQLStateLockNotAvailable}
	if err := classifyPostgresRevisionContention(ctx, "claim revision", cause); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context error = %v", err)
	}

	fence := newPostgresRevisionFenceError(migrationbackend.RevisionFenceFailureStale, errors.New("stale"))
	if got := classifyPostgresMigrationIO(ctx, "read", fence); got != fence {
		t.Fatalf("revision carrier was replaced: %v", got)
	}
	capability := migrationbackend.NewCapabilityError("postgres_schema_migration", "unsupported", nil)
	if got := classifyPostgresMigrationIO(ctx, "schema", capability); got != capability {
		t.Fatalf("capability carrier was replaced: %v", got)
	}
}

func TestPostgresConnectErrorCarrierIsRemovedFromPublicErrors(t *testing.T) {
	t.Parallel()
	cause := &postgresConnectErrorCarrier{connect: &pgconn.ConnectError{Config: &pgconn.Config{
		User:     "secret_user",
		Password: "secret_password",
		Database: "secret_database",
	}}}

	classifiers := []struct {
		name     string
		classify func(error) error
	}{
		{
			name: "query",
			classify: func(err error) error {
				return classifyDatabaseError(context.Background(), "query", "godj_app", "article", err)
			},
		},
		{
			name: "migration IO",
			classify: func(err error) error {
				return classifyPostgresMigrationIO(context.Background(), "read history", err)
			},
		},
		{
			name: "revision contention",
			classify: func(err error) error {
				return classifyPostgresRevisionContention(context.Background(), "claim revision", err)
			},
		},
	}
	for _, classifier := range classifiers {
		classifier := classifier
		t.Run(classifier.name, func(t *testing.T) {
			t.Parallel()
			got := classifier.classify(cause)
			for _, secret := range []string{"secret_user", "secret_password", "secret_database"} {
				if strings.Contains(got.Error(), secret) {
					t.Fatalf("classified error leaked %q: %v", secret, got)
				}
			}
			var retained *pgconn.ConnectError
			if errors.As(got, &retained) {
				t.Fatalf("classified error retained credential-bearing ConnectError: %#v", retained.Config)
			}
		})
	}
}
