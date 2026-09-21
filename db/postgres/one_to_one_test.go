package postgres

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/progresshans/godj/internal/onetoonetest"
	"github.com/progresshans/godj/migrations"
	mb "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/query"
)

func TestPostgresOneToOneGeneratedProduct(t *testing.T) {
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
	backend := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
	onetoonetest.RunProduct(t, backend)
}

func TestPostgresOneToOneAlterFailureRetryReverseAndReopen(t *testing.T) {
	url := postgresIntegrationURL(t)
	for _, unique := range []bool{false, true} {
		t.Run(fmt.Sprintf("initial_unique_%t", unique), func(t *testing.T) {
			ctx := t.Context()
			namespace := postgresMigrationIntegrationSchema(t, ctx, url)
			backend := openPostgresMigrationIntegrationBackend(t, ctx, url, namespace)
			changes, parent, before, after := onetoonetest.History(t, unique)
			loaded := postgresUniqueHistory(t, changes...)
			initial := migrations.TargetedLifecycleRequest(migrations.NamedTarget(changes[0].Key()))
			executor := migrations.Executor{Backend: backend}
			if _, err := executor.Migrate(ctx, loaded, initial); err != nil {
				t.Fatal(err)
			}
			ownerTable, _ := quoteTable(namespace, parent.DBTable)
			childTable, _ := quoteTable(namespace, before.DBTable)
			exec := func(statement string) {
				t.Helper()
				if _, err := backend.database.ExecContext(ctx, statement); err != nil {
					t.Fatal(err)
				}
			}
			exec("INSERT INTO " + ownerTable + " DEFAULT VALUES")
			exec("INSERT INTO " + childTable + " (owner_id) VALUES (1)")
			renderer := NewMigrationSQLRenderer(MigrationSQLConfig{Schema: namespace})
			sql, err := migrations.RenderMigrationSQL(ctx, loaded, changes[1].Key(), renderer)
			wantSQL := 1
			if unique {
				wantSQL = 0
			}
			if err != nil || len(sql) != wantSQL {
				t.Fatal("cardinality SQL confused metadata and physical change", sql, err)
			}
			if !unique {
				exec("INSERT INTO " + childTable + " (owner_id) VALUES (1)")
				held := postgresMigrationIntegrationSession(t, ctx, backend)
				records, err := held.ReadAppliedMigrations(ctx)
				if err != nil {
					t.Fatal(err)
				}
				token := held.(*postgresRevisionFencedSession).token
				catalog, _, err := loadPostgresMigrationTableCatalog(ctx, backend.database, namespace, before.DBTable)
				if err != nil {
					t.Fatal(err)
				}
				_, err = executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
				var native *pgconn.PgError
				if !errors.As(err, &native) || native.Code != "23505" || mb.IsCapabilityError(err) || mb.IsRevisionFenceError(err) {
					t.Fatal("duplicate conversion lost execution failure", err)
				}
				current, _, err := loadPostgresMigrationTableCatalog(ctx, backend.database, namespace, before.DBTable)
				if err != nil || !reflect.DeepEqual(catalog, current) {
					t.Fatal("failed conversion changed physical catalog", err)
				}
				check := postgresMigrationIntegrationSession(t, ctx, backend)
				now, err := check.ReadAppliedMigrations(ctx)
				if err != nil || !reflect.DeepEqual(records, now) || check.(*postgresRevisionFencedSession).token != token {
					t.Fatal("failed conversion changed recorder/revision", err)
				}
				if err := check.Close(ctx); err != nil {
					t.Fatal(err)
				}
				if err := held.Close(ctx); err != nil {
					t.Fatal(err)
				}
				var retained int
				if err := backend.database.QueryRowContext(ctx, "SELECT count(*) FROM "+childTable+" WHERE owner_id=1 AND id IN (1,2)").Scan(&retained); err != nil || retained != 2 {
					t.Fatal("failed conversion changed existing rows", retained, err)
				}
				exec("DELETE FROM " + childTable + " WHERE id=2")
			}
			state, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
			if err != nil {
				t.Fatal(err)
			}
			model, ok := state.Model("otohistory", "child")
			if !ok || !reflect.DeepEqual(model, after) {
				t.Fatal("one-to-one state differs from declared result")
			}
			if err := backend.Close(); err != nil {
				t.Fatal(err)
			}
			backend = openPostgresMigrationIntegrationBackend(t, ctx, url, namespace)
			executor.Backend = backend
			if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
				t.Fatal("reopen lost one-to-one history", err)
			}
			id := query.NewFieldRef("id", "id", query.FieldInteger, false)
			fk := query.NewFieldRef("owner", "owner_id", query.FieldInteger, false)
			insert := query.NewInsertPlanReturningKey(before.DBTable, []query.Assignment{query.NewAssignment(fk, query.Integer(1))}, id)
			_, err = backend.Insert(ctx, insert)
			assertPostgresUniqueError(t, err)
			if _, err := executor.Migrate(ctx, loaded, initial); err != nil {
				t.Fatal("reverse conversion failed", err)
			}
			_, err = backend.Insert(ctx, insert)
			if unique {
				assertPostgresUniqueError(t, err)
			} else if err != nil {
				t.Fatal("reverse did not restore ordinary FK", err)
			}
		})
	}
}
