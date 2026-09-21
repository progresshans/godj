package postgres

import (
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/uniquetest"
	"github.com/progresshans/godj/migrations"
	mb "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"reflect"
	"testing"
)

func TestPostgresNamedConstraintNativeScalarTuples(t *testing.T) {
	for _, profile := range uniquetest.Profiles(t, "postgres") {
		t.Run(profile.Name, func(t *testing.T) {
			ctx := t.Context()
			url := postgresIntegrationURL(t)
			namespace := postgresMigrationIntegrationSchema(t, ctx, url)
			backend := openPostgresMigrationIntegrationBackend(t, ctx, url, namespace)
			model, bucket, value := uniquetest.CompositeModel(t, profile.Name, true)
			sample := uniquetest.CompositeSample(t, profile)
			initial := migrations.Migration{App: "composite", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "composite", Model: model}}}
			loaded := postgresUniqueHistory(t, initial)
			if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
				t.Fatal(err)
			}
			id := query.NewFieldRef("id", "id", query.FieldInteger, false)
			plan := func(scope, input query.Value) query.InsertPlan {
				return query.NewInsertPlanReturningKey(model.DBTable, []query.Assignment{query.NewAssignment(bucket, scope), query.NewAssignment(value, input)}, id)
			}
			first, err := backend.Insert(ctx, plan(query.Integer(1), sample))
			if err != nil || first <= 0 {
				t.Fatal("initial tuple", err)
			}
			key, err := backend.Insert(ctx, plan(query.Integer(1), sample))
			assertPostgresUniqueError(t, err)
			if key != 0 {
				t.Fatal("rejected tuple returned a key")
			}
			other, err := backend.Insert(ctx, plan(query.Integer(2), sample))
			if err != nil || other <= first {
				t.Fatal("other bucket was treated as duplicate", err)
			}
			for _, pair := range [][2]query.Value{{query.Null(), sample}, {query.Integer(1), query.Null()}, {query.Null(), query.Null()}} {
				for repeat := 0; repeat < 2; repeat++ {
					if _, err := backend.Insert(ctx, plan(pair[0], pair[1])); err != nil {
						t.Fatal("SQL NULL tuple was not distinct", err)
					}
				}
			}
			if count, err := backend.Update(ctx, query.NewUpdatePlan(model.DBTable, []query.Assignment{query.NewAssignment(value, sample)}, id, query.Integer(first))); err != nil || count != 1 {
				t.Fatal("self update conflicted", err)
			}
			count, err := backend.Update(ctx, query.NewUpdatePlan(model.DBTable, []query.Assignment{query.NewAssignment(bucket, query.Integer(1))}, id, query.Integer(other)))
			assertPostgresUniqueError(t, err)
			if count != 0 {
				t.Fatal("failed composite update returned a changed row")
			}
			err = backend.Atomic(ctx, func(session db.Session) error {
				if _, err := session.Insert(ctx, plan(query.Integer(9), sample)); err != nil {
					return err
				}
				_, err := session.Insert(ctx, plan(query.Integer(1), sample))
				return err
			})
			assertPostgresUniqueError(t, err)
			table, err := quoteTable(namespace, model.DBTable)
			if err != nil {
				t.Fatal(err)
			}
			var storedCount int
			if err := backend.database.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&storedCount); err != nil || storedCount != 8 {
				t.Fatal("native rollback changed rows", storedCount, err)
			}
			catalog, exists, err := loadPostgresMigrationTableCatalog(ctx, backend.database, namespace, model.DBTable)
			if err != nil || !exists {
				t.Fatal("composite catalog missing", err)
			}
			if err := assertPostgresMigrationModelCatalog(catalog, namespace, model, nil); err != nil {
				t.Fatal("full composite catalog", err)
			}
		})
	}
}

func TestPostgresNamedConstraintFailureRollsBackEarlierDDLAndCanReverseRetry(t *testing.T) {
	ctx := t.Context()
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, ctx, url)
	backend := openPostgresMigrationIntegrationBackend(t, ctx, url, namespace)
	model, _, _ := uniquetest.CompositeModel(t, "char", false)
	constraint := ir.UniqueConstraint{Name: "within_bucket", Fields: []string{"bucket", "value"}}
	initial := migrations.Migration{App: "composite", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "composite", Model: model}}}
	add := migrations.Migration{App: "composite", Name: "0002_scope", Dependencies: []migrations.MigrationKey{initial.Key()}, Operations: []migrations.Operation{
		migrations.AddField{AppLabel: "composite", ModelName: "entry", Field: ir.Field{Name: "note", GoName: "Note", Column: "note", Kind: ir.FieldText, Nullable: true}},
		migrations.AddConstraint{AppLabel: "composite", ModelName: "entry", Constraint: constraint},
	}}
	remove := migrations.Migration{App: "composite", Name: "0003_remove", Dependencies: []migrations.MigrationKey{add.Key()}, Operations: []migrations.Operation{migrations.RemoveConstraint{AppLabel: "composite", ModelName: "entry", Constraint: constraint}}}
	loaded := postgresUniqueHistory(t, initial, add, remove)
	executor := migrations.Executor{Backend: backend}
	if _, err := executor.Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(initial.Key()))); err != nil {
		t.Fatal(err)
	}
	table, err := quoteTable(namespace, model.DBTable)
	if err != nil {
		t.Fatal(err)
	}
	exec := func(statement string) {
		t.Helper()
		if _, err := backend.database.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	exec("INSERT INTO " + table + " (scope_key,value) VALUES (1,'same'),(1,'same')")
	target := migrations.TargetedLifecycleRequest(migrations.NamedTarget(add.Key()))
	for _, reverse := range []bool{false, true} {
		if reverse {
			if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
				t.Fatal(err)
			}
			exec("UPDATE " + table + " SET scope_key=1 WHERE id=2")
		}
		before := postgresNamedSnapshot(t, backend, namespace, model)
		_, err := executor.Migrate(ctx, loaded, target)
		{
			var native *pgconn.PgError
			if !errors.As(err, &native) || native.Code != "23505" {
				t.Fatal("composite failure lost native unique cause", err)
			}
		}
		if mb.IsCapabilityError(err) || mb.IsRevisionFenceError(err) {
			t.Fatal("native data conflict lost execution ownership", err)
		}
		if after := postgresNamedSnapshot(t, backend, namespace, model); !reflect.DeepEqual(before, after) {
			t.Fatal("failed composite change modified rows, catalog, sequence or history", reverse)
		}
		exec("UPDATE " + table + " SET scope_key=2 WHERE id=2")
		if _, err := executor.Migrate(ctx, loaded, target); err != nil {
			t.Fatal("explicit data repair did not permit retry", err)
		}
		_, err = backend.database.ExecContext(ctx, "INSERT INTO "+table+" (scope_key,value) VALUES (1,'same')")
		{
			var native *pgconn.PgError
			if !errors.As(err, &native) || native.Code != "23505" {
				t.Fatal("composite failure lost native unique cause", err)
			}
		}
	}
	if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal("constraint removal", err)
	}
	exec("INSERT INTO " + table + " (scope_key,value) VALUES (1,'same')")
}

func postgresNamedSnapshot(t *testing.T, backend *Backend, namespace string, model ir.Model) any {
	t.Helper()
	ctx := t.Context()
	var snapshot struct {
		history  postgresMigrationRevisionSnapshot
		catalog  postgresMigrationTableCatalog
		rows     string
		sequence int64
		called   bool
	}
	var err error
	snapshot.history, err = readAtomicPostgresMigrationSnapshot(ctx, backend)
	if err != nil {
		t.Fatal(err)
	}
	var exists bool
	snapshot.catalog, exists, err = loadPostgresMigrationTableCatalog(ctx, backend.database, namespace, model.DBTable)
	if err != nil || !exists {
		t.Fatal("missing catalog snapshot", err)
	}
	table, err := quoteTable(namespace, model.DBTable)
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.database.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb("entry") ORDER BY "id")::text, '[]') FROM `+table+` AS "entry"`).Scan(&snapshot.rows); err != nil {
		t.Fatal(err)
	}
	name, err := postgresIdentitySequenceName(model.DBTable, "id")
	if err != nil {
		t.Fatal(err)
	}
	sequence, err := quoteTable(namespace, name)
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.database.QueryRowContext(ctx, "SELECT last_value,is_called FROM "+sequence).Scan(&snapshot.sequence, &snapshot.called); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestPostgresNamedConstraintDriftChecksLaterKeysBeforeRevisionClaim(t *testing.T) {
	url := postgresIntegrationURL(t)
	for _, mode := range []string{"second_member", "order", "nulls_not_distinct", "deferrable", "included", "second_opclass", "extra_index"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			namespace := postgresMigrationIntegrationSchema(t, ctx, url)
			backend := openPostgresMigrationIntegrationBackend(t, ctx, url, namespace)
			model, _, _ := uniquetest.CompositeModel(t, "char", true)
			initial := migrations.Migration{App: "composite", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "composite", Model: model}}}
			next := migrations.Migration{App: "composite", Name: "0002_note", Dependencies: []migrations.MigrationKey{initial.Key()}, Operations: []migrations.Operation{migrations.AddField{AppLabel: "composite", ModelName: "entry", Field: ir.Field{Name: "note", GoName: "Note", Column: "note", Kind: ir.FieldText, Nullable: true}}}}
			loaded := postgresUniqueHistory(t, initial, next)
			executor := migrations.Executor{Backend: backend}
			if _, err := executor.Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(initial.Key()))); err != nil {
				t.Fatal(err)
			}
			table, err := quoteTable(namespace, model.DBTable)
			if err != nil {
				t.Fatal(err)
			}
			exec := func(statement string) {
				t.Helper()
				if _, err := backend.database.ExecContext(ctx, statement); err != nil {
					t.Fatal(err)
				}
			}
			exec("INSERT INTO " + table + " (scope_key,value) VALUES (1,'preserved')")
			name, err := postgresNamedUniqueConstraintName(model.DBTable, "within_bucket")
			if err != nil {
				t.Fatal(err)
			}
			quoted, _ := quoteIdentifier(name)
			if mode != "extra_index" {
				exec("ALTER TABLE " + table + " DROP CONSTRAINT " + quoted)
			}
			suffix := "UNIQUE (scope_key,value)"
			switch mode {
			case "second_member":
				suffix = "UNIQUE (scope_key,id)"
			case "order":
				suffix = "UNIQUE (value,scope_key)"
			case "nulls_not_distinct":
				suffix = "UNIQUE NULLS NOT DISTINCT (scope_key,value)"
			case "deferrable":
				suffix += " DEFERRABLE INITIALLY IMMEDIATE"
			case "included":
				suffix += " INCLUDE (id)"
			case "second_opclass":
				exec("CREATE UNIQUE INDEX " + quoted + " ON " + table + " (scope_key,value text_pattern_ops)")
			case "extra_index":
				exec("CREATE INDEX unowned ON " + table + " (value)")
			}
			if mode != "second_opclass" && mode != "extra_index" {
				exec("ALTER TABLE " + table + " ADD CONSTRAINT " + quoted + " " + suffix)
			}
			before := postgresNamedSnapshot(t, backend, namespace, model)
			if mode == "second_opclass" {
				catalog, _, err := loadPostgresMigrationTableCatalog(ctx, backend.database, namespace, model.DBTable)
				if err != nil {
					t.Fatal(err)
				}
				observed := false
				for _, index := range catalog.indexes {
					if index.name == name {
						observed = len(index.keys) == 2 && index.keys[0].operatorClassName == "int8_ops" && index.keys[1].operatorClassName == "text_pattern_ops"
					}
				}
				if !observed {
					t.Fatal("catalog reader omitted the second native operator class")
				}
			}
			if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); !errors.Is(err, errPostgresMigrationPhysicalDrift) {
				t.Fatal("later-key drift was admitted", err)
			}
			if after := postgresNamedSnapshot(t, backend, namespace, model); !reflect.DeepEqual(before, after) {
				t.Fatal("rejection changed rows, catalog or revision")
			}
		})
	}
}

func TestPostgresNamedConstraintReplacementKeepsIndependentColumnAndNamedOwners(t *testing.T) {
	ctx := t.Context()
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, ctx, url)
	backend := openPostgresMigrationIntegrationBackend(t, ctx, url, namespace)
	model, bucket, value := uniquetest.CompositeModel(t, "char", true)
	model.Fields[2].Unique = true
	single := ir.UniqueConstraint{Name: "single_value", Fields: []string{"value"}}
	model.UniqueConstraints = append(model.UniqueConstraints[:1], append([]ir.UniqueConstraint{single}, model.UniqueConstraints[1:]...)...)
	initial := migrations.Migration{App: "composite", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "composite", Model: model}}}
	withoutSingle := migrations.Migration{App: "composite", Name: "0002_remove_single", Dependencies: []migrations.MigrationKey{initial.Key()}, Operations: []migrations.Operation{migrations.RemoveConstraint{AppLabel: "composite", ModelName: "entry", Constraint: single}}}
	before := model.Fields[2].Clone()
	after := before.Clone()
	after.Unique = false
	withoutColumn := migrations.Migration{App: "composite", Name: "0003_remove_column", Dependencies: []migrations.MigrationKey{withoutSingle.Key()}, Operations: []migrations.Operation{migrations.AlterField{AppLabel: "composite", ModelName: "entry", Before: before, After: after}}}
	old := ir.UniqueConstraint{Name: "within_bucket", Fields: []string{"bucket", "value"}}
	changed := old.Clone()
	changed.Fields = []string{"value", "bucket"}
	replacement := migrations.Migration{App: "composite", Name: "0004_reorder", Dependencies: []migrations.MigrationKey{withoutColumn.Key()}, Operations: []migrations.Operation{migrations.RemoveConstraint{AppLabel: "composite", ModelName: "entry", Constraint: old}, migrations.AddConstraint{AppLabel: "composite", ModelName: "entry", Constraint: changed}}}
	renamed := changed.Clone()
	renamed.Name = "renamed_bucket"
	rename := migrations.Migration{App: "composite", Name: "0005_rename", Dependencies: []migrations.MigrationKey{replacement.Key()}, Operations: []migrations.Operation{migrations.RemoveConstraint{AppLabel: "composite", ModelName: "entry", Constraint: changed}, migrations.AddConstraint{AppLabel: "composite", ModelName: "entry", Constraint: renamed}}}
	loaded := postgresUniqueHistory(t, initial, withoutSingle, withoutColumn, replacement, rename)
	executor := migrations.Executor{Backend: backend}
	target := func(key migrations.MigrationKey) {
		t.Helper()
		if _, err := executor.Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(key))); err != nil {
			t.Fatal(err)
		}
	}
	target(initial.Key())
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	insert := func(scope int64) (int64, error) {
		return backend.Insert(ctx, query.NewInsertPlanReturningKey(model.DBTable, []query.Assignment{query.NewAssignment(bucket, query.Integer(scope)), query.NewAssignment(value, query.String("same"))}, id))
	}
	if _, err := insert(1); err != nil {
		t.Fatal(err)
	}
	target(withoutSingle.Key())
	_, err := insert(2)
	assertPostgresUniqueError(t, err)
	target(withoutColumn.Key())
	if _, err := insert(2); err != nil {
		t.Fatal("removing independent owners retained unintended global uniqueness", err)
	}
	for _, step := range []migrations.MigrationKey{replacement.Key(), rename.Key(), withoutColumn.Key()} {
		target(step)
		if _, err := insert(2); err == nil {
			t.Fatal("constraint replacement or reversal dropped tuple enforcement")
		} else {
			assertPostgresUniqueError(t, err)
		}
	}
}

func TestPostgresNamedConstraintConcurrentWritersHaveOneNativeWinner(t *testing.T) {
	ctx := t.Context()
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, ctx, url)
	first := openPostgresMigrationIntegrationBackend(t, ctx, url, namespace)
	second := openPostgresMigrationIntegrationBackend(t, ctx, url, namespace)
	model, bucket, value := uniquetest.CompositeModel(t, "char", true)
	initial := migrations.Migration{App: "composite", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "composite", Model: model}}}
	if _, err := (migrations.Executor{Backend: first}).Migrate(ctx, postgresUniqueHistory(t, initial), migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	plan := func(scope int64) query.InsertPlan {
		return query.NewInsertPlanReturningKey(model.DBTable, []query.Assignment{query.NewAssignment(bucket, query.Integer(scope)), query.NewAssignment(value, query.String("same"))}, id)
	}
	type result struct {
		key int64
		err error
	}
	ready, start, results := make(chan struct{}, 2), make(chan struct{}), make(chan result, 2)
	for _, writer := range []*Backend{first, second} {
		go func() {
			ready <- struct{}{}
			<-start
			key, err := writer.Insert(ctx, plan(1))
			results <- result{key, err}
		}()
	}
	<-ready
	<-ready
	close(start)
	saved, rejected := 0, 0
	for range 2 {
		value := <-results
		if value.err == nil {
			if value.key <= 0 {
				t.Fatal("winner has no key")
			}
			saved++
		} else {
			assertPostgresUniqueError(t, value.err)
			var native *pgconn.PgError
			expected, _ := postgresNamedUniqueConstraintName(model.DBTable, "within_bucket")
			if !errors.As(value.err, &native) || native.ConstraintName != expected {
				t.Fatal("another constraint owned the tuple conflict", value.err)
			}
			if value.key != 0 {
				t.Fatal("loser exposed a key")
			}
			rejected++
		}
	}
	if saved != 1 || rejected != 1 {
		t.Fatal("concurrent tuple invariant", saved, rejected)
	}
	if _, err := first.Insert(ctx, plan(2)); err != nil {
		t.Fatal("another bucket was incorrectly locked out", err)
	}
}

func TestPostgresNamedConstraintCreateLateFailureRollsBackEveryStatementAndBootstrap(t *testing.T) {
	ctx := t.Context()
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, ctx, url)
	backend := openPostgresMigrationIntegrationBackend(t, ctx, url, namespace)
	model, _, _ := uniquetest.CompositeModel(t, "char", true)
	before, err := readAtomicPostgresMigrationSnapshot(ctx, backend)
	if err != nil {
		t.Fatal(err)
	}
	session := postgresMigrationIntegrationSession(t, ctx, backend)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	transition := mb.HistoryTransition{Kind: mb.HistoryTransitionApply, Migration: mb.AppliedMigration{App: "composite", Name: "0001_initial"}}
	intent := mb.MigrationIntent{Operations: []mb.MigrationOperation{{Kind: mb.MigrationCreateModel, After: model}}}
	tx, err := session.BeginMigration(ctx, transition, intent)
	if err != nil {
		t.Fatal(err)
	}
	concrete := tx.(*postgresRevisionFencedTransaction)
	name, err := postgresNamedUniqueConstraintName(model.DBTable, "within_bucket")
	if err != nil {
		t.Fatal(err)
	}
	collision, err := quoteTable(namespace, name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := concrete.connection.ExecContext(ctx, "CREATE TABLE "+collision+" (id BIGINT)"); err != nil {
		t.Fatal(err)
	}
	failure := tx.CreateModel(ctx, model)
	if failure == nil {
		t.Fatal("second named constraint did not see the native collision")
	}
	if err := tx.RecordApplied(ctx, "composite", "0001_initial"); !errors.Is(err, failure) {
		t.Fatal("failed SQL group recorded success", err)
	}
	outcome, err := tx.CommitFenced(ctx)
	if !errors.Is(err, failure) || outcome.Durability != mb.CommitRolledBack {
		t.Fatal("failed SQL group committed", outcome, err)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := readAtomicPostgresMigrationSnapshot(ctx, backend)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("failed SQL group changed migration bootstrap", err)
	}
	var relations int
	if err := backend.database.QueryRowContext(ctx, `SELECT COUNT(*) FROM pg_class AS c JOIN pg_namespace AS n ON n.oid=c.relnamespace WHERE n.nspname=$1`, namespace).Scan(&relations); err != nil || relations != 0 {
		t.Fatal("table, constraint index, sequence or injected collision survived rollback", relations, err)
	}
	initial := migrations.Migration{App: "composite", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "composite", Model: model}}}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, postgresUniqueHistory(t, initial), migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal("create group could not retry", err)
	}
}

func TestPostgresNamedConstraintTransitiveTargetCannotHideLaterKeyDrift(t *testing.T) {
	ctx := t.Context()
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, ctx, url)
	backend := openPostgresMigrationIntegrationBackend(t, ctx, url, namespace)
	models := uniquetest.CompositeTargets(t)
	initial := migrations.Migration{App: "composite", Name: "0001_initial"}
	for _, model := range models {
		initial.Operations = append(initial.Operations, migrations.CreateModel{AppLabel: "composite", Model: model})
	}
	next := migrations.Migration{App: "composite", Name: "0002_leaf", Dependencies: []migrations.MigrationKey{initial.Key()}, Operations: []migrations.Operation{migrations.AddField{AppLabel: "composite", ModelName: "leaf", Field: ir.Field{Name: "note", GoName: "Note", Column: "note", Kind: ir.FieldText, Nullable: true}}}}
	loaded := postgresUniqueHistory(t, initial, next)
	executor := migrations.Executor{Backend: backend}
	if _, err := executor.Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(initial.Key()))); err != nil {
		t.Fatal("valid composite FK graph", err)
	}
	table, err := quoteTable(namespace, models[0].DBTable)
	if err != nil {
		t.Fatal(err)
	}
	name, err := postgresNamedUniqueConstraintName(models[0].DBTable, "within_bucket")
	if err != nil {
		t.Fatal(err)
	}
	quoted, _ := quoteIdentifier(name)
	for _, statement := range []string{"ALTER TABLE " + table + " DROP CONSTRAINT " + quoted, "ALTER TABLE " + table + " ADD CONSTRAINT " + quoted + " UNIQUE (scope_key,id)"} {
		if _, err := backend.database.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	before := postgresNamedSnapshot(t, backend, namespace, models[0])
	if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); !errors.Is(err, errPostgresMigrationPhysicalDrift) {
		t.Fatal("transitive named constraint drift was ignored", err)
	}
	if after := postgresNamedSnapshot(t, backend, namespace, models[0]); !reflect.DeepEqual(before, after) {
		t.Fatal("target rejection changed data, catalog or revision")
	}
}
