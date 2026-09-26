package sqlite

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/cascadetest"
	"github.com/progresshans/godj/migrations"
	mb "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	sqlitedriver "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

func TestSQLiteCascadePolicyMigrationPreservesRowsConstraintsSequenceAndReopens(t *testing.T) {
	for _, nullable := range []bool{false, true} {
		for _, one := range []bool{false, true} {
			t.Run(fmt.Sprintf("nullable_%t_one_%t", nullable, one), func(t *testing.T) {
				ctx := t.Context()
				path := filepath.Join(t.TempDir(), "cascade.sqlite")
				backend := openMigrationHistoryFileBackend(t, path)
				changes, _, before, after := cascadetest.History(t, nullable, one)
				other := after.Fields[1].Clone()
				other.Name, other.GoName, other.Column = "other", "OtherID", "other_id"
				other.Nullable, other.Unique = true, false
				other.Relation.Cardinality, other.Relation.Reverse = ir.RelationManyToOne, ir.ReverseRelation{Disabled: true}
				changes = append(changes, migrations.Migration{App: "cascadehistory", Name: "0003_other", Dependencies: []migrations.MigrationKey{changes[1].Key()},
					Operations: []migrations.Operation{migrations.AddField{AppLabel: "cascadehistory", ModelName: "child", Field: other}}})
				loaded := sqliteUniqueHistory(t, changes...)
				executor := migrations.Executor{Backend: backend}
				initial := migrations.TargetedLifecycleRequest(migrations.NamedTarget(changes[0].Key()))
				if _, err := executor.Migrate(ctx, loaded, initial); err != nil {
					t.Fatal(err)
				}
				sqliteUniqueExec(t, backend, `INSERT INTO cascadehistory_owner(id) VALUES (1)`)
				sqliteUniqueExec(t, backend, `INSERT INTO cascadehistory_child(id,owner_id,code) VALUES (1,1,'kept'),(100,1,'removed')`)
				sqliteUniqueExec(t, backend, `DELETE FROM cascadehistory_child WHERE id=100`)
				assertSQLiteCascadeTiming(t, backend, false)
				projected, err := migrations.RenderMigrationSQL(ctx, loaded, changes[1].Key(), NewMigrationSQLRenderer())
				if err != nil || len(projected) < 8 || !strings.Contains(strings.Join(projected, "\n"), "DEFERRABLE INITIALLY DEFERRED") {
					t.Fatal("policy change was not projected as a physical remake", projected, err)
				}
				state, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
				if err != nil {
					t.Fatal(err)
				}
				model, _ := state.Model("cascadehistory", "child")
				if len(model.Fields) != len(after.Fields)+1 || model.Fields[1].Relation.OnDelete != ir.DeleteCascade {
					t.Fatal("historical policy/add-field state was lost")
				}
				assertSQLiteCascadeTiming(t, backend, true)
				if err := assertSQLiteUniqueIndexes(ctx, backend.database, model, model.Fields); err != nil {
					t.Fatal(err)
				}
				if err := backend.Close(); err != nil {
					t.Fatal(err)
				}
				backend = openMigrationHistoryFileBackend(t, path)
				executor.Backend = backend
				if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
					t.Fatal("reopened cascade catalog does not match history", err)
				}
				if _, err := executor.Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(changes[1].Key()))); err != nil {
					t.Fatal("removing another FK did not retain the cascade constraint", err)
				}
				assertSQLiteCascadeTiming(t, backend, true)
				if _, err := executor.Migrate(ctx, loaded, initial); err != nil {
					t.Fatal("policy reverse failed", err)
				}
				assertSQLiteCascadeTiming(t, backend, false)
				if err := assertSQLiteUniqueIndexes(ctx, backend.database, before, before.Fields); err != nil {
					t.Fatal(err)
				}
				if sqliteUniqueCount(t, backend, `SELECT COUNT(*) FROM cascadehistory_child WHERE id=1 AND owner_id=1 AND code='kept'`) != 1 ||
					sqliteUniqueCount(t, backend, `SELECT seq FROM sqlite_sequence WHERE name='cascadehistory_child'`) != 100 ||
					sqliteUniqueCount(t, backend, `PRAGMA foreign_keys`) != 1 {
					t.Fatal("migration/reverse lost rows, sequence or FK enforcement")
				}
			})
		}
	}
}

func assertSQLiteCascadeTiming(t *testing.T, backend *Backend, deferred bool) {
	t.Helper()
	ctx := t.Context()
	var declaration string
	if err := backend.database.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE name='cascadehistory_child'`).Scan(&declaration); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(declaration, "DEFERRABLE INITIALLY DEFERRED") != deferred {
		t.Fatal("physical FK timing differs from declaration", declaration)
	}
	before := sqliteUniqueCount(t, backend, `SELECT COUNT(*) FROM cascadehistory_child`)
	key := query.NewFieldRef("id", "id", query.FieldInteger, false)
	rollback := errors.New("observe native FK timing without keeping the deletes")
	err := backend.AtomicRelation(ctx, func(session db.RelationSession) error {
		count, err := session.Delete(ctx, query.NewDeletePlan("cascadehistory_owner", key, query.Integer(1)))
		if !deferred {
			var native *sqlitedriver.Error
			if !errors.As(err, &native) || native.Code() != sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY {
				return errors.Join(errors.New("ordinary FK did not check the parent deletion immediately"), err)
			}
			return rollback
		}
		if err != nil || count != 1 {
			return errors.Join(fmt.Errorf("cascade FK did not defer the temporary missing parent: rows=%d", count), err)
		}
		count, err = session.Delete(ctx, query.NewDeletePlan("cascadehistory_child", key, query.Integer(1)))
		if err != nil || count != 1 {
			return errors.Join(fmt.Errorf("SQL action deleted a child that belongs to the ORM collector: rows=%d", count), err)
		}
		return rollback
	})
	if !errors.Is(err, rollback) || sqliteUniqueCount(t, backend, `SELECT COUNT(*) FROM cascadehistory_child`) != before {
		t.Fatal("native timing observation did not roll back", err)
	}
}

func TestSQLiteCascadePolicyLateFailureRestoresCompleteMigrationState(t *testing.T) {
	ctx := t.Context()
	backend := openMigrationHistoryFileBackend(t, filepath.Join(t.TempDir(), "late.sqlite"))
	changes, parent, before, _ := cascadetest.History(t, false, false)
	changes[1].Operations = append(changes[1].Operations, migrations.AddConstraint{AppLabel: "cascadehistory", ModelName: "child",
		Constraint: ir.UniqueConstraint{Name: "all_codes", Fields: []string{"code"}}})
	loaded := sqliteUniqueHistory(t, changes...)
	executor := migrations.Executor{Backend: backend}
	initial := migrations.TargetedLifecycleRequest(migrations.NamedTarget(changes[0].Key()))
	if _, err := executor.Migrate(ctx, loaded, initial); err != nil {
		t.Fatal(err)
	}
	sqliteUniqueExec(t, backend, `INSERT INTO cascadehistory_owner(id) VALUES (1),(2)`)
	sqliteUniqueExec(t, backend, `INSERT INTO cascadehistory_child(owner_id,code) VALUES (1,'same'),(2,'same')`)
	snapshot := sqliteUniqueReadSnapshot(t, backend, parent, before)
	_, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
	assertSQLiteNativeUniqueError(t, err)
	if current := sqliteUniqueReadSnapshot(t, backend, parent, before); !reflect.DeepEqual(snapshot, current) {
		t.Fatal("late uniqueness failure left a partial policy, row, sequence, catalog or history change")
	}
	sqliteUniqueExec(t, backend, `UPDATE cascadehistory_child SET code='different' WHERE id=2`)
	if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal("retry after explicit data repair failed", err)
	}
	if _, err := executor.Migrate(ctx, loaded, initial); err != nil {
		t.Fatal("reverse after repair failed", err)
	}
	assertSQLiteCascadeTiming(t, backend, false)
}

func TestSQLiteCascadeSQLProjectionPreservesSequencePresenceAndHighWater(t *testing.T) {
	for _, sequence := range []string{"absent", "empty", "high"} {
		t.Run(sequence, func(t *testing.T) {
			ctx := t.Context()
			backend := openMigrationHistoryFileBackend(t, filepath.Join(t.TempDir(), "projection.sqlite"))
			changes, _, _, after := cascadetest.History(t, false, false)
			loaded := sqliteUniqueHistory(t, changes...)
			if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(changes[0].Key()))); err != nil {
				t.Fatal(err)
			}
			sqliteUniqueExec(t, backend, `INSERT INTO cascadehistory_owner(id) VALUES (1)`)
			if sequence != "absent" {
				sqliteUniqueExec(t, backend, `INSERT INTO cascadehistory_child(id,owner_id,code) VALUES (200,1,'past')`)
				sqliteUniqueExec(t, backend, `DELETE FROM cascadehistory_child`)
			}
			if sequence == "high" {
				sqliteUniqueExec(t, backend, `INSERT INTO cascadehistory_child(id,owner_id,code) VALUES (1,1,'kept')`)
			}
			bodies, err := migrations.RenderMigrationSQL(ctx, loaded, changes[1].Key(), NewMigrationSQLRenderer())
			if err != nil {
				t.Fatal(err)
			}
			executeSQLiteCascadeBodies(t, backend, bodies)
			count := sqliteUniqueCount(t, backend, `SELECT COUNT(*) FROM sqlite_sequence WHERE name='cascadehistory_child'`)
			if sequence == "absent" && count != 0 || sequence != "absent" && count != 1 {
				t.Fatal("projection changed sequence presence", count)
			}
			if sequence != "absent" && sqliteUniqueCount(t, backend, `SELECT seq FROM sqlite_sequence WHERE name='cascadehistory_child'`) != 200 {
				t.Fatal("projection used the copied row maximum instead of the original high water")
			}
			want := int64(0)
			if sequence == "high" {
				want = 1
			}
			if sqliteUniqueCount(t, backend, `SELECT COUNT(*) FROM cascadehistory_child`) != want {
				t.Fatal("projection changed row count")
			}
			if want != 0 && sqliteUniqueCount(t, backend, `SELECT COUNT(*) FROM cascadehistory_child WHERE id=1 AND owner_id=1 AND code='kept'`) != 1 {
				t.Fatal("projection changed stored row contents")
			}
			if err := assertSQLiteUniqueIndexes(ctx, backend.database, after, after.Fields); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Execute operation bodies with a test-owned lifecycle. Rendering itself has
// no connection, FK mode, transaction or migration recorder authority.
func executeSQLiteCascadeBodies(t *testing.T, backend *Backend, bodies []string) {
	t.Helper()
	ctx := t.Context()
	connection, err := backend.database.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	inTransaction := false
	defer func() {
		if inTransaction {
			if _, err := connection.ExecContext(context.Background(), "ROLLBACK"); err != nil {
				t.Error("cleanup projected migration", err)
			}
		}
		if _, err := connection.ExecContext(context.Background(), "PRAGMA foreign_keys=ON"); err != nil {
			t.Error("restore projected migration FK mode", err)
		}
		if err := connection.Close(); err != nil {
			t.Error(err)
		}
	}()
	for _, statement := range []string{"PRAGMA foreign_keys=OFF", "BEGIN IMMEDIATE"} {
		if _, err := connection.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	inTransaction = true
	for _, statement := range bodies {
		if _, err := connection.ExecContext(ctx, statement); err != nil {
			t.Fatal("execute projected remake", statement, err)
		}
	}
	var violations int
	if err := connection.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&violations); err != nil || violations != 0 {
		t.Fatal("projection left invalid foreign keys", violations, err)
	}
	if _, err := connection.ExecContext(ctx, "COMMIT"); err != nil {
		t.Fatal(err)
	}
	inTransaction = false
}

func TestSQLiteCascadeCreateModelAndConstraintTimingDrift(t *testing.T) {
	ctx := t.Context()
	backend := openMigrationHistoryFileBackend(t, filepath.Join(t.TempDir(), "drift.sqlite"))
	changes, parent, before, after := cascadetest.History(t, false, false)
	initial := changes[0]
	initial.Operations[1] = migrations.CreateModel{AppLabel: initial.App, Model: after}
	executor := migrations.Executor{Backend: backend}
	if _, err := executor.Migrate(ctx, sqliteUniqueHistory(t, initial), migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	sqliteUniqueExec(t, backend, `INSERT INTO cascadehistory_owner(id) VALUES (1)`)
	sqliteUniqueExec(t, backend, `INSERT INTO cascadehistory_child(id,owner_id,code) VALUES (1,1,'kept')`)
	assertSQLiteCascadeTiming(t, backend, true)
	manual := migrations.Migration{App: initial.App, Name: "0002_manual", Dependencies: []migrations.MigrationKey{initial.Key()},
		Operations: []migrations.Operation{migrations.AlterField{AppLabel: initial.App, ModelName: after.Name, Before: after.Fields[1], After: before.Fields[1]}}}
	bodies, err := migrations.RenderMigrationSQL(ctx, sqliteUniqueHistory(t, initial, manual), manual.Key(), NewMigrationSQLRenderer())
	if err != nil || len(bodies) < 8 {
		t.Fatal("manual timing change had no physical projection", err)
	}
	// Change physical timing without recording the policy in migration history.
	executeSQLiteCascadeBodies(t, backend, bodies)
	assertSQLiteCascadeTiming(t, backend, false)
	snapshot := sqliteUniqueReadSnapshot(t, backend, parent, after)
	next := migrations.Migration{App: initial.App, Name: "0002_note", Dependencies: []migrations.MigrationKey{initial.Key()},
		Operations: []migrations.Operation{migrations.AddField{AppLabel: initial.App, ModelName: after.Name,
			Field: ir.Field{Name: "note", GoName: "Note", Column: "note", Kind: ir.FieldText, Nullable: true}}}}
	_, err = executor.Migrate(ctx, sqliteUniqueHistory(t, initial, next), migrations.LatestLifecycleRequest())
	var capability *mb.CapabilityError
	if !errors.As(err, &capability) || !strings.Contains(err.Error(), "canonical declaration") {
		t.Fatal("policy timing drift was not rejected before the schema change", err)
	}
	if current := sqliteUniqueReadSnapshot(t, backend, parent, after); !reflect.DeepEqual(snapshot, current) {
		t.Fatal("timing drift changed rows, sequence, FK mode, schema or history")
	}
}

func TestSQLiteCascadeRequiredCycleCreateAddDeleteAndReverse(t *testing.T) {
	initial, models := cascadetest.RequiredCycle(t)
	ctx := t.Context()
	backend := openMigrationHistoryFileBackend(t, filepath.Join(t.TempDir(), "cycle.sqlite"))
	loaded := sqliteUniqueHistory(t, initial)
	executor := migrations.Executor{Backend: backend}
	if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	transaction, err := backend.database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	for _, statement := range []string{`INSERT INTO cascadecycle_first(id,second_id) VALUES (1,2)`, `INSERT INTO cascadecycle_second(id,first_id) VALUES (2,1)`} {
		if _, err := transaction.ExecContext(ctx, statement); err != nil {
			t.Fatal("required cycle could not be built in one transaction", err)
		}
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	key := query.NewFieldRef("id", "id", query.FieldInteger, false)
	if err := backend.AtomicRelation(ctx, func(session db.RelationSession) error {
		for i, model := range models {
			count, err := session.Delete(ctx, query.NewDeletePlan(model.DBTable, key, query.Integer(int64(i+1))))
			if err != nil || count != 1 {
				return fmt.Errorf("delete required cycle row: count=%d error=%w", count, err)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.ZeroTarget("cascadecycle"))); err != nil {
		t.Fatal("reverse required cycle history", err)
	}
}
