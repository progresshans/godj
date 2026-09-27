package systemstate

import (
	"context"
	"errors"
	"slices"
	"testing"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
)

type identityHistoryReader struct {
	rows  []migrationbackend.AppliedMigration
	err   error
	reads int
}

func (reader *identityHistoryReader) ReadAppliedMigrations(ctx context.Context) ([]migrationbackend.AppliedMigration, error) {
	reader.reads++
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return slices.Clone(reader.rows), reader.err
}

func TestIdentityHistoryRequiresEveryOwnedMigrationExactlyOnce(t *testing.T) {
	// This explicit independent roster prevents a missing embedded migration
	// from shrinking both the implementation's expected graph and this assertion.
	complete := []migrationbackend.AppliedMigration{
		{App: "godj_system", Name: "0001_initial"},
		{App: "godj_system", Name: "0002_identity_transition"},
		{App: "godj_identity", Name: "0001_initial"},
		{App: "godj_identity", Name: "0002_permission_revision"},
		{App: "godj_identity", Name: "0003_alter_user_email"},
	}
	check := func(t *testing.T, rows []migrationbackend.AppliedMigration, valid bool) {
		t.Helper()
		reader := &identityHistoryReader{rows: rows}
		err := requireIdentityMigrations(t.Context(), reader)
		if reader.reads != 1 {
			t.Fatal("history did not use one read")
		}
		if valid {
			if err != nil {
				t.Fatal(err)
			}
		} else if !errors.Is(err, &Error{Code: CodeSchemaUnavailable, Field: "migration_history"}) {
			t.Fatal("invalid history was admitted", err)
		}
	}
	t.Run("complete", func(t *testing.T) { check(t, complete, true) })
	t.Run("unrelated_app_and_different_order", func(t *testing.T) {
		rows := slices.Clone(complete)
		slices.Reverse(rows)
		rows = append(rows, migrationbackend.AppliedMigration{App: "host", Name: "0001_initial"})
		check(t, rows, true)
	})
	for i, row := range complete {
		t.Run("missing_"+row.App+"_"+row.Name, func(t *testing.T) { check(t, append(slices.Clone(complete[:i]), complete[i+1:]...), false) })
		t.Run("duplicate_"+row.App+"_"+row.Name, func(t *testing.T) { check(t, append(slices.Clone(complete), row), false) })
	}
	for _, app := range []string{"godj_system", "godj_identity"} {
		t.Run("unknown_"+app, func(t *testing.T) {
			check(t, append(slices.Clone(complete), migrationbackend.AppliedMigration{App: app, Name: "9999_unknown"}), false)
		})
	}
	t.Run("reader_failure", func(t *testing.T) {
		cause := errors.New("history unavailable")
		reader := &identityHistoryReader{rows: complete, err: cause}
		if err := requireIdentityMigrations(t.Context(), reader); !errors.Is(err, cause) {
			t.Fatal("history read error lost", err)
		}
	})
	t.Run("canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := requireIdentityMigrations(ctx, &identityHistoryReader{rows: complete}); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled history read admitted", err)
		}
	})
}
