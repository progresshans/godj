package postgres

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/conflicttest"
	"github.com/progresshans/godj/query"
)

func TestPostgresConflictInsertCompiler(t *testing.T) {
	conflicttest.CheckCompiler(t, func(plan query.ConflictInsertPlan) (string, []any, error) { return compileConflictInsert("app", plan) },
		`INSERT INTO "app"."conflict_link" ("owner_id", "label_id", "token") VALUES ($1, $2, $3) ON CONFLICT ("owner_id", "label_id") DO NOTHING`)
	field := query.NewFieldRef("name", "name", query.FieldString, false)
	statement, arguments, err := compileConflictInsert("app", query.NewConflictInsertPlan("labels", []query.Assignment{query.NewAssignment(field, query.String("x'); DROP TABLE links; --"))}, []query.FieldRef{field}))
	if err != nil || statement != `INSERT INTO "app"."labels" ("name") VALUES ($1) ON CONFLICT ("name") DO NOTHING` ||
		!reflect.DeepEqual(arguments, []any{"x'); DROP TABLE links; --"}) {
		t.Fatal(statement, arguments, err)
	}
	if statement, arguments, err := compileConflictInsert(`a"schema`, conflicttest.Plan(1, 1, 10)); statement != "" || arguments != nil || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
		t.Fatal("invalid schema accepted", statement, arguments, err)
	}
	if statement, arguments, err := compileConflictInsert("app", query.NewConflictInsertPlan(`a"table`, conflicttest.Plan(1, 1, 10).Assignments(), conflicttest.Plan(1, 1, 10).Target())); statement != "" || arguments != nil || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
		t.Fatal("invalid table accepted", statement, arguments, err)
	}
}

func TestPostgresConflictInsertProduct(t *testing.T) {
	url := postgresIntegrationURL(t)
	ctx := t.Context()
	namespace := postgresMigrationIntegrationSchema(t, ctx, url)
	backend := openPostgresMigrationIntegrationBackend(t, ctx, url, namespace)
	second := openPostgresMigrationIntegrationBackend(t, ctx, url, namespace)
	parent, err := quoteTable(namespace, "conflict_parent")
	if err != nil {
		t.Fatal(err)
	}
	link, err := quoteTable(namespace, "conflict_link")
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"CREATE TABLE " + parent + " (id BIGINT PRIMARY KEY)",
		"INSERT INTO " + parent + " VALUES (1),(2),(3),(4),(5),(6)",
		"CREATE TABLE " + link + " (id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, owner_id BIGINT NOT NULL REFERENCES " + parent + "(id) DEFERRABLE INITIALLY DEFERRED, label_id BIGINT NOT NULL REFERENCES " + parent + "(id), token BIGINT NOT NULL UNIQUE CHECK(token > 0), UNIQUE(owner_id,label_id))",
	} {
		if _, err := backend.database.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	conflicttest.Check(t, backend, second, backend.database, link, parent)
	t.Run("zero_rows_is_not_membership_proof", func(t *testing.T) {
		function, err := quoteTable(namespace, "skip_conflict")
		if err != nil {
			t.Fatal(err)
		}
		for _, statement := range []string{
			"CREATE FUNCTION " + function + "() RETURNS trigger LANGUAGE plpgsql AS 'BEGIN IF NEW.token = 90909 THEN RETURN NULL; END IF; RETURN NEW; END'",
			"CREATE TRIGGER skip_conflict BEFORE INSERT ON " + link + " FOR EACH ROW EXECUTE FUNCTION " + function + "()",
		} {
			if _, err := backend.database.ExecContext(ctx, statement); err != nil {
				t.Fatal(err)
			}
		}
		plan := conflicttest.Plan(4, 4, 90909)
		if inserted, err := backend.InsertOnConflict(ctx, plan); inserted || err != nil {
			t.Fatal(inserted, err)
		}
		var count int
		if err := backend.database.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+link+" WHERE token=90909").Scan(&count); err != nil || count != 0 {
			t.Fatal(count, err)
		}
		if key, err := backend.Insert(ctx, query.NewInsertPlanReturningKey("conflict_link", plan.Assignments(), query.NewFieldRef("id", "id", query.FieldInteger, false))); key != 0 || !errors.Is(err, &query.Error{Code: query.CodeUnexpectedRows}) {
			t.Fatal("ordinary insert contract changed", key, err)
		}
	})
	if err := errors.Join(backend.Close(), second.Close()); err != nil {
		t.Fatal(err)
	}
	reopened := openPostgresMigrationIntegrationBackend(t, ctx, url, namespace)
	var count int
	if err := reopened.database.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+link).Scan(&count); err != nil || count != 1 {
		t.Fatal("reopened link state", count, err)
	}
}

func TestPostgresConflictInsertUncertainTransactionOwnership(t *testing.T) {
	for _, outcome := range []string{"commit", "rollback", "invalid"} {
		t.Run(outcome, func(t *testing.T) {
			failure := errors.New("injected native failure")
			callbackFailure := errors.New("rollback after insert")
			state := &transactionTestState{}
			if outcome == "commit" {
				state.setCommitError(failure)
			}
			if outcome == "rollback" {
				state.setRollbackError(failure)
			}
			backend := newTransactionTestBackend(state)
			t.Cleanup(func() { _ = backend.Close() })
			calls := 0
			err := backend.Atomic(context.Background(), func(session db.Session) error {
				calls++
				plan := conflicttest.Plan(1, 1, 10)
				if outcome == "invalid" {
					plan = query.ConflictInsertPlan{}
				}
				inserted, err := session.(db.ConflictInserter).InsertOnConflict(context.Background(), plan)
				if outcome == "invalid" {
					if inserted || err == nil {
						t.Fatal(inserted, err)
					}
					return err
				}
				if !inserted || err != nil {
					t.Fatal(inserted, err)
				}
				if outcome == "rollback" {
					return callbackFailure
				}
				return nil
			})
			if calls != 1 {
				t.Fatal("callback retried", calls)
			}
			if outcome == "invalid" {
				if !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) || state.snapshot().execs != 0 {
					t.Fatal("invalid plan reached I/O", err)
				}
			} else if !errors.Is(err, failure) || state.snapshot().execs != 1 {
				t.Fatal("native cause or exactly once execution lost", err)
			}
			if outcome == "commit" && !errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) {
				t.Fatal("commit uncertainty lost", err)
			}
			if outcome == "rollback" && (!errors.Is(err, callbackFailure) || !errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown})) {
				t.Fatal("rollback uncertainty lost", err)
			}
		})
	}
}
