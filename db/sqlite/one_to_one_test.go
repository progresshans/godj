package sqlite

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/progresshans/godj/internal/onetoonetest"
	"github.com/progresshans/godj/migrations"
	mb "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/query"
)

type oneToOneWithoutAlterCapability struct{ *Backend }

func (b oneToOneWithoutAlterCapability) MigrationCapabilities() mb.MigrationCapabilities {
	capabilities := b.Backend.MigrationCapabilities()
	capabilities.AlterFieldRelation = false
	return capabilities
}

func TestSQLiteOneToOneAlterFailureRetryReverseAndReopen(t *testing.T) {
	for _, unique := range []bool{false, true} {
		t.Run(fmt.Sprintf("initial_unique_%t", unique), func(t *testing.T) {
			ctx := t.Context()
			path := filepath.Join(t.TempDir(), "one-to-one.sqlite3")
			backend := openMigrationHistoryFileBackend(t, path)
			changes, parent, before, after := onetoonetest.History(t, unique)
			loaded := sqliteUniqueHistory(t, changes...)
			initial := migrations.TargetedLifecycleRequest(migrations.NamedTarget(changes[0].Key()))
			executor := migrations.Executor{Backend: backend}
			if _, err := executor.Migrate(ctx, loaded, initial); err != nil {
				t.Fatal(err)
			}
			sqliteUniqueExec(t, backend, `INSERT INTO otohistory_owner (id) VALUES (1)`)
			sqliteUniqueExec(t, backend, `INSERT INTO otohistory_child (owner_id) VALUES (1)`)
			sql, err := migrations.RenderMigrationSQL(ctx, loaded, changes[1].Key(), NewMigrationSQLRenderer())
			wantSQL := 1
			if unique {
				wantSQL = 0
			}
			if err != nil || len(sql) != wantSQL {
				t.Fatal("cardinality SQL confused metadata and physical change", sql, err)
			}
			unchanged := sqliteUniqueReadSnapshot(t, backend, parent, before)
			_, err = (migrations.Executor{Backend: oneToOneWithoutAlterCapability{backend}}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
			var rejected *migrations.Error
			if !errors.As(err, &rejected) || rejected.Category != migrations.CategoryCapability || rejected.Code != migrations.CodeUnsupported {
				t.Fatal("relation alteration did not require its own backend capability", err)
			}
			if current := sqliteUniqueReadSnapshot(t, backend, parent, before); !reflect.DeepEqual(unchanged, current) {
				t.Fatal("unsupported relation alteration mutated durable state")
			}
			if !unique {
				sqliteUniqueExec(t, backend, `INSERT INTO otohistory_child (owner_id) VALUES (1)`)
				snapshot := sqliteUniqueReadSnapshot(t, backend, parent, before)
				_, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
				assertSQLiteNativeUniqueError(t, err)
				if current := sqliteUniqueReadSnapshot(t, backend, parent, before); !reflect.DeepEqual(snapshot, current) {
					t.Fatal("failed relation conversion changed rows, catalog, sequence, FK setting or revision/history")
				}
				sqliteUniqueExec(t, backend, `DELETE FROM otohistory_child WHERE id=2`)
			}
			state, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
			if err != nil {
				t.Fatal(err)
			}
			model, ok := state.Model("otohistory", "child")
			if !ok || !reflect.DeepEqual(model, after) {
				t.Fatal("one-to-one state differs from declared result")
			}
			if err := assertSQLiteUniqueIndexes(ctx, backend.database, after, after.Fields); err != nil {
				t.Fatal(err)
			}
			if err := backend.Close(); err != nil {
				t.Fatal(err)
			}
			backend = openMigrationHistoryFileBackend(t, path)
			executor.Backend = backend
			if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
				t.Fatal("reopen lost one-to-one history", err)
			}
			id := query.NewFieldRef("id", "id", query.FieldInteger, false)
			fk := query.NewFieldRef("owner", "owner_id", query.FieldInteger, false)
			insert := query.NewInsertPlanReturningKey(before.DBTable, []query.Assignment{query.NewAssignment(fk, query.Integer(1))}, id)
			_, err = backend.Insert(ctx, insert)
			assertSQLiteUniqueError(t, err)
			if _, err := executor.Migrate(ctx, loaded, initial); err != nil {
				t.Fatal("reverse conversion failed", err)
			}
			_, err = backend.Insert(ctx, insert)
			if unique {
				assertSQLiteUniqueError(t, err)
			} else if err != nil {
				t.Fatal("reverse did not restore ordinary FK", err)
			}
			if err := assertSQLiteUniqueIndexes(ctx, backend.database, before, before.Fields); err != nil {
				t.Fatal(err)
			}
		})
	}
}
