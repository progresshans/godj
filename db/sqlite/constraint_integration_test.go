package sqlite

import (
	"errors"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/uniquetest"
	"github.com/progresshans/godj/migrations"
	mb "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSQLiteNamedConstraintAdvisoryMatchesScalarReference(t *testing.T) {
	for _, profile := range uniquetest.Profiles(t, "sqlite") {
		t.Run(profile.Name, func(t *testing.T) {
			backend := openMigrationHistoryFileBackend(t, filepath.Join(t.TempDir(), "advisory.sqlite"))
			model, _, _ := uniquetest.CompositeModel(t, profile.Name, true)
			initial := migrations.Migration{App: "composite", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "composite", Model: model}}}
			if _, err := (migrations.Executor{Backend: backend}).Migrate(t.Context(), sqliteUniqueHistory(t, initial), migrations.LatestLifecycleRequest()); err != nil {
				t.Fatal(err)
			}
			uniquetest.CheckCompositeValidation(t, backend, model, profile, "sqlite")
		})
	}
}

func TestSQLiteNamedConstraintNativeScalarTuples(t *testing.T) {
	for _, profile := range uniquetest.Profiles(t, "sqlite") {
		t.Run(profile.Name, func(t *testing.T) {
			ctx := t.Context()
			path := filepath.Join(t.TempDir(), "composite.sqlite")
			backend := openMigrationHistoryFileBackend(t, path)
			model, bucket, value := uniquetest.CompositeModel(t, profile.Name, true)
			sample := uniquetest.CompositeSample(t, profile)
			initial := migrations.Migration{App: "composite", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "composite", Model: model}}}
			loaded := sqliteUniqueHistory(t, initial)
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
			assertSQLiteUniqueError(t, err)
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
			assertSQLiteUniqueError(t, err)
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
			assertSQLiteUniqueError(t, err)
			if got := sqliteUniqueCount(t, backend, "SELECT COUNT(*) FROM "+model.DBTable); got != 8 {
				t.Fatal("native rollback changed rows", got)
			}
			if err := backend.Close(); err != nil {
				t.Fatal(err)
			}
			reopened := openMigrationHistoryFileBackend(t, path)
			if err := assertSQLiteUniqueIndexes(ctx, reopened.database, model, model.Fields); err != nil {
				t.Fatal("reopened composite catalog", err)
			}
			if _, err := (migrations.Executor{Backend: reopened}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSQLiteNamedConstraintFailureRollsBackEarlierDDLAndCanReverseRetry(t *testing.T) {
	ctx := t.Context()
	backend := openMigrationHistoryFileBackend(t, filepath.Join(t.TempDir(), "rollback.sqlite"))
	model, _, _ := uniquetest.CompositeModel(t, "char", false)
	constraint := ir.UniqueConstraint{Name: "within_bucket", Fields: []string{"bucket", "value"}}
	initial := migrations.Migration{App: "composite", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "composite", Model: model}}}
	add := migrations.Migration{App: "composite", Name: "0002_scope", Dependencies: []migrations.MigrationKey{initial.Key()}, Operations: []migrations.Operation{
		migrations.AddField{AppLabel: "composite", ModelName: "entry", Field: ir.Field{Name: "note", GoName: "Note", Column: "note", Kind: ir.FieldText, Nullable: true}},
		migrations.AddConstraint{AppLabel: "composite", ModelName: "entry", Constraint: constraint},
	}}
	remove := migrations.Migration{App: "composite", Name: "0003_remove", Dependencies: []migrations.MigrationKey{add.Key()}, Operations: []migrations.Operation{migrations.RemoveConstraint{AppLabel: "composite", ModelName: "entry", Constraint: constraint}}}
	loaded := sqliteUniqueHistory(t, initial, add, remove)
	executor := migrations.Executor{Backend: backend}
	if _, err := executor.Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(initial.Key()))); err != nil {
		t.Fatal(err)
	}
	table := model.DBTable
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
		before := sqliteUniqueReadSnapshot(t, backend, model)
		_, err := executor.Migrate(ctx, loaded, target)
		assertSQLiteNativeUniqueError(t, err)
		if mb.IsCapabilityError(err) || mb.IsRevisionFenceError(err) {
			t.Fatal("native data conflict lost execution ownership", err)
		}
		if after := sqliteUniqueReadSnapshot(t, backend, model); !reflect.DeepEqual(before, after) {
			t.Fatal("failed composite change modified rows, catalog, sequence or history", reverse)
		}
		exec("UPDATE " + table + " SET scope_key=2 WHERE id=2")
		if _, err := executor.Migrate(ctx, loaded, target); err != nil {
			t.Fatal("explicit data repair did not permit retry", err)
		}
		_, err = backend.database.ExecContext(ctx, "INSERT INTO "+table+" (scope_key,value) VALUES (1,'same')")
		assertSQLiteNativeUniqueError(t, err)
	}
	if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal("constraint removal", err)
	}
	exec("INSERT INTO " + table + " (scope_key,value) VALUES (1,'same')")
}

func TestSQLiteNamedConstraintDriftChecksLaterKeysBeforeRevisionClaim(t *testing.T) {
	for _, mode := range []string{"missing", "second_member", "order", "second_desc", "second_collation", "expression", "partial", "nonunique", "extra_index"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			backend := openMigrationTestBackend(t)
			model, _, _ := uniquetest.CompositeModel(t, "char", true)
			initial := migrations.Migration{App: "composite", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "composite", Model: model}}}
			next := migrations.Migration{App: "composite", Name: "0002_note", Dependencies: []migrations.MigrationKey{initial.Key()}, Operations: []migrations.Operation{migrations.AddField{AppLabel: "composite", ModelName: "entry", Field: ir.Field{Name: "note", GoName: "Note", Column: "note", Kind: ir.FieldText, Nullable: true}}}}
			loaded := sqliteUniqueHistory(t, initial, next)
			executor := migrations.Executor{Backend: backend}
			if _, err := executor.Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(initial.Key()))); err != nil {
				t.Fatal(err)
			}
			sqliteUniqueExec(t, backend, `INSERT INTO composite_entry (scope_key,value) VALUES (1,'preserved')`)
			name, err := sqliteNamedUniqueIndexName(model.DBTable, "within_bucket")
			if err != nil {
				t.Fatal(err)
			}
			quoted, _ := quoteIdentifier(name)
			if mode != "extra_index" {
				sqliteUniqueExec(t, backend, "DROP INDEX "+quoted)
			}
			ddl := "CREATE UNIQUE INDEX " + quoted + " ON composite_entry (scope_key,value)"
			switch mode {
			case "missing":
				ddl = ""
			case "second_member":
				ddl = "CREATE UNIQUE INDEX " + quoted + " ON composite_entry (scope_key,id)"
			case "order":
				ddl = "CREATE UNIQUE INDEX " + quoted + " ON composite_entry (value,scope_key)"
			case "second_desc":
				ddl = "CREATE UNIQUE INDEX " + quoted + " ON composite_entry (scope_key,value DESC)"
			case "second_collation":
				ddl = "CREATE UNIQUE INDEX " + quoted + " ON composite_entry (scope_key,value COLLATE NOCASE)"
			case "expression":
				ddl = "CREATE UNIQUE INDEX " + quoted + " ON composite_entry (scope_key,lower(value))"
			case "partial":
				ddl += " WHERE value IS NOT NULL"
			case "nonunique":
				ddl = strings.Replace(ddl, "UNIQUE ", "", 1)
			case "extra_index":
				ddl = "CREATE INDEX unowned ON composite_entry (value)"
			}
			if ddl != "" {
				sqliteUniqueExec(t, backend, ddl)
			}
			before := sqliteUniqueReadSnapshot(t, backend, model)
			if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); !errors.Is(err, errSQLiteRelationPhysicalDrift) {
				t.Fatal("later-key drift was admitted", err)
			}
			if after := sqliteUniqueReadSnapshot(t, backend, model); !reflect.DeepEqual(before, after) {
				t.Fatal("rejection changed rows, catalog or revision")
			}
		})
	}
}

func TestSQLiteNamedConstraintReplacementKeepsIndependentColumnAndNamedOwners(t *testing.T) {
	ctx := t.Context()
	backend := openMigrationTestBackend(t)
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
	loaded := sqliteUniqueHistory(t, initial, withoutSingle, withoutColumn, replacement, rename)
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
	assertSQLiteUniqueError(t, err)
	target(withoutColumn.Key())
	if _, err := insert(2); err != nil {
		t.Fatal("removing independent owners retained unintended global uniqueness", err)
	}
	for _, step := range []migrations.MigrationKey{replacement.Key(), rename.Key(), withoutColumn.Key()} {
		target(step)
		if _, err := insert(2); err == nil {
			t.Fatal("constraint replacement or reversal dropped tuple enforcement")
		} else {
			assertSQLiteUniqueError(t, err)
		}
	}
}

func TestSQLiteNamedConstraintRemakeFailureRestoresAndRetriesRetainedTuple(t *testing.T) {
	ctx := t.Context()
	backend := openMigrationTestBackend(t)
	parent, withFK, fk := sqliteRelationTestModels()
	withFK.UniqueConstraints = []ir.UniqueConstraint{{Name: "identity_title", Fields: []string{"id", "title"}}}
	without := withFK.Clone()
	without.Fields = without.Fields[:len(without.Fields)-1]
	initial := migrations.Migration{App: "news", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "news", Model: parent}, migrations.CreateModel{AppLabel: "news", Model: without}}}
	dependent := ir.UniqueConstraint{Name: "author_title", Fields: []string{fk.Name, "title"}}
	added := migrations.Migration{App: "news", Name: "0002_relation", Dependencies: []migrations.MigrationKey{initial.Key()}, Operations: []migrations.Operation{migrations.AddField{AppLabel: "news", ModelName: withFK.Name, Field: fk}, migrations.AddConstraint{AppLabel: "news", ModelName: withFK.Name, Constraint: dependent}}}
	loaded := sqliteUniqueHistory(t, initial, added)
	executor := migrations.Executor{Backend: backend}
	if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{`INSERT INTO news_author (name) VALUES ('owner')`, `INSERT INTO news_article (id,title,author_id) VALUES (4,'kept',1),(99,'deleted',1)`, `DELETE FROM news_article WHERE id=99`} {
		sqliteUniqueExec(t, backend, statement)
	}
	full := withFK.Clone()
	full.UniqueConstraints = append([]ir.UniqueConstraint{dependent}, full.UniqueConstraints...)
	before := sqliteUniqueReadSnapshot(t, backend, parent, full)
	session := openLifecycleSession(t, backend)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	target := mb.MigrationTarget{SourceField: fk, TargetModel: parent, TargetKey: parent.Fields[0]}
	intent := mb.MigrationIntent{Operations: []mb.MigrationOperation{
		{OperationIndex: 1, Kind: mb.MigrationRemoveConstraint, Before: full, After: withFK, Targets: []mb.MigrationTarget{target}},
		{OperationIndex: 0, Kind: mb.MigrationRemoveField, Before: withFK, After: without, Targets: []mb.MigrationTarget{target}},
	}}
	transition := mb.HistoryTransition{Kind: mb.HistoryTransitionUnapply, Migration: mb.AppliedMigration{App: added.App, Name: added.Name}}
	tx := beginLifecycleTransaction(t, session, transition, intent)
	if err := tx.RemoveConstraint(ctx, full, dependent); err != nil {
		t.Fatal(err)
	}
	concrete := tx.(*sqliteRevisionFencedTransaction)
	name, err := sqliteNamedUniqueIndexName(withFK.DBTable, "identity_title")
	if err != nil {
		t.Fatal(err)
	}
	quoted, _ := quoteIdentifier(name)
	// Force the retained named index to fail after row copy, table replacement
	// and sequence restoration. The prior constraint removal must roll back too.
	for _, statement := range []string{"DROP INDEX " + quoted, "CREATE TABLE " + quoted + " (id INTEGER)"} {
		if _, err := concrete.connection.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	failure := tx.RemoveField(ctx, withFK, fk)
	if failure == nil {
		t.Fatal("late named-index recreation unexpectedly succeeded")
	}
	if err := tx.RecordUnapplied(ctx, added.App, added.Name); !errors.Is(err, failure) {
		t.Fatal("failed remake published recorder", err)
	}
	outcome, err := tx.CommitFenced(ctx)
	if !errors.Is(err, failure) || outcome.Durability != mb.CommitRolledBack {
		t.Fatal("late composite failure committed", outcome, err)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if after := sqliteUniqueReadSnapshot(t, backend, parent, full); !reflect.DeepEqual(before, after) {
		t.Fatal("failed remake changed original rows, indexes, sequence, revision or FK state")
	}
	if _, err := executor.Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(initial.Key()))); err != nil {
		t.Fatal("ordered removal/remake retry failed", err)
	}
	if err := assertSQLiteUniqueIndexes(ctx, backend.database, without, without.Fields); err != nil {
		t.Fatal("remake dropped retained compound index", err)
	}
	if got := sqliteUniqueCount(t, backend, `SELECT seq FROM sqlite_sequence WHERE name='news_article'`); got != 99 {
		t.Fatal("remake lost sequence high water", got)
	}
	if got := sqliteUniqueCount(t, backend, `SELECT COUNT(*) FROM news_article WHERE id=4 AND title='kept'`); got != 1 {
		t.Fatal("remake lost original row")
	}
}

func TestSQLiteNamedConstraintConcurrentWritersHaveOneNativeWinner(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "race.sqlite")
	first := openLifecycleFileBackend(t, path, "&_pragma=busy_timeout(5000)")
	second := openLifecycleFileBackend(t, path, "&_pragma=busy_timeout(5000)")
	for _, writer := range []*Backend{first, second} {
		if sqliteUniqueCount(t, writer, "PRAGMA busy_timeout") != 5000 {
			t.Fatal("writer wait budget not configured")
		}
	}
	model, bucket, value := uniquetest.CompositeModel(t, "char", true)
	initial := migrations.Migration{App: "composite", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "composite", Model: model}}}
	if _, err := (migrations.Executor{Backend: first}).Migrate(ctx, sqliteUniqueHistory(t, initial), migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	plan := func(scope int64) query.InsertPlan {
		return query.NewInsertPlanReturningKey(model.DBTable, []query.Assignment{query.NewAssignment(bucket, query.Integer(scope)), query.NewAssignment(value, query.String("same"))}, id)
	}
	manager := orm.NewManager[uniquetest.Record](uniquetest.Descriptor{Model: model})
	// Both advisory reads finish before either actual writer starts.
	for _, writer := range []*Backend{first, second} {
		diagnostics, err := manager.ValidateUniqueCreate(ctx, writer, uniquetest.CompositeInput{Model: model, Bucket: query.Integer(1), Value: query.String("same")})
		if err != nil || !diagnostics.Empty() {
			t.Fatal("fresh tuple preflight", diagnostics.All(), err)
		}
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
			assertSQLiteUniqueError(t, value.err)
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

func TestSQLiteNamedConstraintTransitiveTargetCannotHideLaterKeyDrift(t *testing.T) {
	ctx := t.Context()
	backend := openMigrationTestBackend(t)
	models := uniquetest.CompositeTargets(t)
	initial := migrations.Migration{App: "composite", Name: "0001_initial"}
	for _, model := range models {
		initial.Operations = append(initial.Operations, migrations.CreateModel{AppLabel: "composite", Model: model})
	}
	next := migrations.Migration{App: "composite", Name: "0002_leaf", Dependencies: []migrations.MigrationKey{initial.Key()}, Operations: []migrations.Operation{migrations.AddField{AppLabel: "composite", ModelName: "leaf", Field: ir.Field{Name: "note", GoName: "Note", Column: "note", Kind: ir.FieldText, Nullable: true}}}}
	loaded := sqliteUniqueHistory(t, initial, next)
	executor := migrations.Executor{Backend: backend}
	if _, err := executor.Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(initial.Key()))); err != nil {
		t.Fatal("valid composite FK graph", err)
	}
	name, err := sqliteNamedUniqueIndexName(models[0].DBTable, "within_bucket")
	if err != nil {
		t.Fatal(err)
	}
	quoted, _ := quoteIdentifier(name)
	sqliteUniqueExec(t, backend, "DROP INDEX "+quoted)
	sqliteUniqueExec(t, backend, "CREATE UNIQUE INDEX "+quoted+" ON composite_entry (scope_key,id)")
	before := sqliteUniqueReadSnapshot(t, backend, models...)
	if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); !errors.Is(err, errSQLiteRelationPhysicalDrift) {
		t.Fatal("transitive named constraint drift was ignored", err)
	}
	if after := sqliteUniqueReadSnapshot(t, backend, models...); !reflect.DeepEqual(before, after) {
		t.Fatal("target rejection changed data, catalog or revision")
	}
}
