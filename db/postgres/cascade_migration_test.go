package postgres

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/cascadetest"
	"github.com/progresshans/godj/migrations"
	mb "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestPostgresCascadePolicyMigrationPreservesRowsConstraintsAndReopens(t *testing.T) {
	url := postgresIntegrationURL(t)
	for _, nullable := range []bool{false, true} {
		for _, one := range []bool{false, true} {
			t.Run(fmt.Sprintf("nullable_%t_one_%t", nullable, one), func(t *testing.T) {
				ctx := t.Context()
				namespace := postgresMigrationIntegrationSchema(t, ctx, url)
				backend := openPostgresMigrationIntegrationBackend(t, ctx, url, namespace)
				changes, parent, before, after := cascadetest.History(t, nullable, one)
				loaded := postgresUniqueHistory(t, changes...)
				executor := migrations.Executor{Backend: backend}
				initial := migrations.TargetedLifecycleRequest(migrations.NamedTarget(changes[0].Key()))
				if _, err := executor.Migrate(ctx, loaded, initial); err != nil {
					t.Fatal(err)
				}
				ownerTable, _ := quoteTable(namespace, parent.DBTable)
				childTable, _ := quoteTable(namespace, before.DBTable)
				for _, statement := range []string{
					"INSERT INTO " + ownerTable + " (id) VALUES (1)",
					"INSERT INTO " + childTable + " (owner_id,code) VALUES (1,'kept')",
				} {
					if _, err := backend.database.ExecContext(ctx, statement); err != nil {
						t.Fatal(err)
					}
				}
				assertPostgresCascadeTiming(t, backend, namespace, parent, before, false)
				projected, err := migrations.RenderMigrationSQL(ctx, loaded, changes[1].Key(), NewMigrationSQLRenderer(MigrationSQLConfig{Schema: namespace}))
				want := 1
				if one {
					want++
				}
				if err != nil || len(projected) != want {
					t.Fatal("policy/uniqueness SQL projection lost an operation", projected, err)
				}
				if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
					t.Fatal(err)
				}
				assertPostgresCascadeTiming(t, backend, namespace, parent, after, true)
				if err := backend.Close(); err != nil {
					t.Fatal(err)
				}
				backend = openPostgresMigrationIntegrationBackend(t, ctx, url, namespace)
				executor.Backend = backend
				if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
					t.Fatal("reopened policy history", err)
				}
				assertPostgresCascadeTiming(t, backend, namespace, parent, after, true)
				if _, err := executor.Migrate(ctx, loaded, initial); err != nil {
					t.Fatal("reverse policy migration", err)
				}
				assertPostgresCascadeTiming(t, backend, namespace, parent, before, false)
				var count int
				if err := backend.database.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+childTable+" WHERE id=1 AND owner_id=1 AND code='kept'").Scan(&count); err != nil || count != 1 {
					t.Fatal("policy migration lost stored rows", count, err)
				}
			})
		}
	}
}

func assertPostgresCascadeTiming(t *testing.T, backend *Backend, namespace string, parent, child ir.Model, deferred bool) {
	t.Helper()
	ctx := t.Context()
	catalog, exists, err := loadPostgresMigrationTableCatalog(ctx, backend.database, namespace, child.DBTable)
	if err != nil || !exists {
		t.Fatal("missing policy catalog", err)
	}
	targets := []mb.MigrationTarget{{SourceField: child.Fields[1], TargetModel: parent, TargetKey: parent.Fields[0]}}
	if err := assertPostgresMigrationModelCatalog(catalog, namespace, child, targets); err != nil {
		t.Fatal("policy catalog does not match complete model", err)
	}
	for _, constraint := range catalog.constraints {
		if constraint.kind == "f" && (constraint.deferrable != deferred || constraint.deferred != deferred || constraint.deleteAction != "a") {
			t.Fatal("FK timing/action differs from policy")
		}
	}
	rollback := errors.New("rollback native timing observation")
	key := query.NewFieldRef("id", "id", query.FieldInteger, false)
	err = backend.AtomicRelation(ctx, func(session db.RelationSession) error {
		count, err := session.Delete(ctx, query.NewDeletePlan(parent.DBTable, key, query.Integer(1)))
		if !deferred {
			var cause *pgconn.PgError
			if !errors.As(err, &cause) || cause.Code != "23503" {
				return errors.Join(errors.New("ordinary policy did not reject the parent delete immediately"), err)
			}
			return rollback
		}
		if err != nil || count != 1 {
			return errors.Join(fmt.Errorf("cascade parent delete: rows=%d", count), err)
		}
		count, err = session.Delete(ctx, query.NewDeletePlan(child.DBTable, key, query.Integer(1)))
		if err != nil || count != 1 {
			return errors.Join(fmt.Errorf("SQL action erased an ORM-owned child: rows=%d", count), err)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
}

func TestPostgresCascadePolicyLateFailureRestoresCompleteMigrationState(t *testing.T) {
	ctx := t.Context()
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, ctx, url)
	backend := openPostgresMigrationIntegrationBackend(t, ctx, url, namespace)
	changes, parent, before, _ := cascadetest.History(t, false, false)
	changes[1].Operations = append(changes[1].Operations, migrations.AddConstraint{AppLabel: "cascadehistory", ModelName: "child",
		Constraint: ir.UniqueConstraint{Name: "all_codes", Fields: []string{"code"}}})
	loaded := postgresUniqueHistory(t, changes...)
	executor := migrations.Executor{Backend: backend}
	initial := migrations.TargetedLifecycleRequest(migrations.NamedTarget(changes[0].Key()))
	if _, err := executor.Migrate(ctx, loaded, initial); err != nil {
		t.Fatal(err)
	}
	ownerTable, _ := quoteTable(namespace, parent.DBTable)
	childTable, _ := quoteTable(namespace, before.DBTable)
	for _, statement := range []string{"INSERT INTO " + ownerTable + " (id) VALUES (1),(2)",
		"INSERT INTO " + childTable + " (owner_id,code) VALUES (1,'same'),(2,'same')"} {
		if _, err := backend.database.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := postgresNamedSnapshot(t, backend, namespace, before)
	_, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
	var native *pgconn.PgError
	if !errors.As(err, &native) || native.Code != "23505" {
		t.Fatal("late constraint error lost native cause", err)
	}
	if current := postgresNamedSnapshot(t, backend, namespace, before); !reflect.DeepEqual(snapshot, current) {
		t.Fatal("failed policy migration changed catalog, rows, sequence or history")
	}
	if _, err := backend.database.ExecContext(ctx, "UPDATE "+childTable+" SET code='different' WHERE id=2"); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal("retry after explicit repair failed", err)
	}
	if _, err := executor.Migrate(ctx, loaded, initial); err != nil {
		t.Fatal("reverse after repair failed", err)
	}
	assertPostgresCascadeTiming(t, backend, namespace, parent, before, false)
}

func TestPostgresCascadeDeferredCommitFailureDoesNotReturnPendingWritesToPool(t *testing.T) {
	url := postgresIntegrationURL(t)
	for _, mode := range []string{"ordinary", "relation"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			namespace := postgresMigrationIntegrationSchema(t, ctx, url)
			backend := openPostgresMigrationIntegrationBackend(t, ctx, url, namespace)
			changes, parent, _, child := cascadetest.History(t, false, false)
			if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, postgresUniqueHistory(t, changes...), migrations.LatestLifecycleRequest()); err != nil {
				t.Fatal(err)
			}
			backend.database.SetMaxOpenConns(1)
			key := query.NewFieldRef("id", "id", query.FieldInteger, false)
			owner := query.NewFieldRef("owner", "owner_id", query.FieldInteger, false)
			code := query.NewFieldRef("code", "code", query.FieldString, false)
			callback := func(session db.Session) error {
				id, err := session.Insert(ctx, query.NewInsertPlanReturningKey(child.DBTable,
					[]query.Assignment{query.NewAssignment(owner, query.Integer(404)), query.NewAssignment(code, query.String("orphan"))}, key))
				if err == nil && id <= 0 {
					return errors.New("deferred insert did not execute")
				}
				return err
			}
			var err error
			if mode == "ordinary" {
				err = backend.Atomic(ctx, callback)
			} else {
				err = backend.AtomicRelation(ctx, func(session db.RelationSession) error { return callback(session) })
			}
			var failure *query.Error
			if !errors.As(err, &failure) || failure.Code != query.CodeCommitOutcomeUnknown {
				t.Fatal("deferred failure lost commit outcome ownership", err)
			}
			var native *pgconn.PgError
			if !errors.As(err, &native) || native.Code != "23503" {
				t.Fatal("deferred failure lost its native FK cause", err)
			}
			table, _ := quoteTable(namespace, child.DBTable)
			var count int
			if err := backend.database.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 0 {
				t.Fatal("failed COMMIT leaked pending writes", count, err)
			}
			if err := backend.Atomic(ctx, func(session db.Session) error {
				_, err := session.Insert(ctx, query.NewInsertPlanReturningKey(parent.DBTable, nil, key))
				return err
			}); err != nil {
				t.Fatal("failed COMMIT blocked a fresh transaction", err)
			}
		})
	}
}

func TestPostgresCascadeRequiredCycleCreateAddDeleteAndReverse(t *testing.T) {
	initial, models := cascadetest.RequiredCycle(t)
	ctx := t.Context()
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, ctx, url)
	backend := openPostgresMigrationIntegrationBackend(t, ctx, url, namespace)
	loaded := postgresUniqueHistory(t, initial)
	executor := migrations.Executor{Backend: backend}
	if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	first, _ := quoteTable(namespace, models[0].DBTable)
	second, _ := quoteTable(namespace, models[1].DBTable)
	transaction, err := backend.database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	for _, statement := range []string{"INSERT INTO " + first + " (id,second_id) VALUES (1,2)", "INSERT INTO " + second + " (id,first_id) VALUES (2,1)"} {
		if _, err := transaction.ExecContext(ctx, statement); err != nil {
			t.Fatal("required cycle could not be created atomically", err)
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
				return errors.Join(fmt.Errorf("required cycle delete: rows=%d", count), err)
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

func TestPostgresCascadeCreateModelAndConstraintTimingDrift(t *testing.T) {
	ctx := t.Context()
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, ctx, url)
	backend := openPostgresMigrationIntegrationBackend(t, ctx, url, namespace)
	changes, parent, _, after := cascadetest.History(t, false, false)
	initial := changes[0]
	initial.Operations[1] = migrations.CreateModel{AppLabel: initial.App, Model: after}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, postgresUniqueHistory(t, initial), migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	table, _ := quoteTable(namespace, after.DBTable)
	ownerTable, _ := quoteTable(namespace, parent.DBTable)
	for _, statement := range []string{"INSERT INTO " + ownerTable + " (id) VALUES (1)", "INSERT INTO " + table + " (owner_id,code) VALUES (1,'kept')"} {
		if _, err := backend.database.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	assertPostgresCascadeTiming(t, backend, namespace, parent, after, true)
	name, _ := postgresForeignKeyConstraintName(after.DBTable, after.Fields[1].Column)
	constraint, _ := quoteIdentifier(name)
	if _, err := backend.database.ExecContext(ctx, "ALTER TABLE "+table+" ALTER CONSTRAINT "+constraint+" NOT DEFERRABLE"); err != nil {
		t.Fatal(err)
	}
	snapshot := postgresNamedSnapshot(t, backend, namespace, after)
	next := migrations.Migration{App: "cascadehistory", Name: "0002_note", Dependencies: []migrations.MigrationKey{initial.Key()},
		Operations: []migrations.Operation{migrations.AddField{AppLabel: "cascadehistory", ModelName: "child",
			Field: ir.Field{Name: "note", GoName: "Note", Column: "note", Kind: ir.FieldText, Nullable: true}}}}
	_, err := (migrations.Executor{Backend: backend}).Migrate(ctx, postgresUniqueHistory(t, initial, next), migrations.LatestLifecycleRequest())
	if !errors.Is(err, errPostgresMigrationPhysicalDrift) {
		t.Fatal("changed FK timing was accepted as the declared cascade constraint")
	}
	if current := postgresNamedSnapshot(t, backend, namespace, after); !reflect.DeepEqual(snapshot, current) {
		t.Fatal("FK timing drift changed rows, schema or migration history")
	}
}
