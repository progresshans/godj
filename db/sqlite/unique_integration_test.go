package sqlite

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/uniquetest"
	"github.com/progresshans/godj/migrations"
	mb "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
	modernsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

func sqliteUniqueHistory(t testing.TB, changes ...migrations.Migration) migrations.LoadedDefinitionSet {
	t.Helper()
	var sources []definition.Source
	for _, change := range changes {
		wire, err := definition.Encode(definition.Producer{Name: "unique-test", Version: "1"}, change)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, definition.Source{SourceID: change.Name, Document: wire})
	}
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

func assertSQLiteUniqueError(t testing.TB, err error) {
	t.Helper()
	if !errors.Is(err, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeUniqueConstraint}) {
		t.Fatalf("missing stable unique error: %v", err)
	}
	assertSQLiteNativeUniqueError(t, err)
}

func assertSQLiteNativeUniqueError(t testing.TB, err error) {
	t.Helper()
	var cause *modernsqlite.Error
	if !errors.As(err, &cause) || cause.Code() != sqlite3.SQLITE_CONSTRAINT_UNIQUE {
		t.Fatalf("unique failure lost native cause: %v", err)
	}
}

func sqliteUniqueExec(t testing.TB, database *Backend, statement string, args ...any) {
	t.Helper()
	if _, err := database.ExecContext(context.Background(), statement, args...); err != nil {
		t.Fatal(err)
	}
}

func sqliteUniqueCount(t testing.TB, database *Backend, statement string, args ...any) int64 {
	t.Helper()
	var value int64
	if err := database.database.QueryRowContext(context.Background(), statement, args...).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

type sqliteUniqueSnapshot struct {
	revision                   migrationRevisionSnapshot
	schema, sequence, rows     []string
	schemaVersion, foreignKeys int64
}

func sqliteUniqueReadSnapshot(t testing.TB, database *Backend, models ...ir.Model) sqliteUniqueSnapshot {
	t.Helper()
	ctx := context.Background()
	revision, err := readAtomicMigrationRevisionSnapshot(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := sqliteUniqueSnapshot{revision: revision}
	read := func(statement string) []string {
		rows, err := database.database.QueryContext(ctx, statement)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		var result []string
		for rows.Next() {
			values := make([]any, len(columns))
			dest := make([]any, len(columns))
			for i := range values {
				dest[i] = &values[i]
			}
			if err := rows.Scan(dest...); err != nil {
				t.Fatal(err)
			}
			result = append(result, fmt.Sprintf("%#v", values))
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			t.Fatal(err)
		}
		return result
	}
	snapshot.schema = read(`SELECT type,name,tbl_name,rootpage,sql FROM main.sqlite_schema ORDER BY type,name`)
	if sqliteUniqueCount(t, database, `SELECT COUNT(*) FROM main.sqlite_schema WHERE name='sqlite_sequence'`) > 0 {
		snapshot.sequence = read(`SELECT typeof(name),name,typeof(seq),seq FROM main.sqlite_sequence ORDER BY name`)
	}
	for _, model := range models {
		table, err := quoteIdentifier(model.DBTable)
		if err != nil {
			t.Fatal(err)
		}
		snapshot.rows = append(snapshot.rows, model.DBTable)
		snapshot.rows = append(snapshot.rows, read("SELECT * FROM "+table+" ORDER BY id")...)
	}
	snapshot.schemaVersion = sqliteUniqueCount(t, database, "PRAGMA schema_version")
	snapshot.foreignKeys = sqliteUniqueCount(t, database, "PRAGMA foreign_keys")
	return snapshot
}

func TestSQLiteUniqueReferenceWrites(t *testing.T) {
	profiles := uniquetest.Profiles(t, "sqlite")
	var canonical uniquetest.Profile
	for _, profile := range profiles {
		if profile.Name == "json_canonical" {
			canonical = profile
		}
	}
	for _, profile := range profiles {
		t.Run(profile.Name, func(t *testing.T) {
			ctx := t.Context()
			path := filepath.Join(t.TempDir(), "unique.sqlite")
			backend := openMigrationHistoryFileBackend(t, path)
			model, field := uniquetest.Model(t, profile.Name)
			initial := migrations.Migration{App: "uniqueref", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "uniqueref", Model: model}}}
			loaded := sqliteUniqueHistory(t, initial)
			if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
				t.Fatal(err)
			}
			id := query.NewFieldRef("id", "id", query.FieldInteger, false)
			var nullID, valueID int64
			var occupied query.Value
			count, deviations := 0, 0
			for _, attempt := range profile.Attempts {
				t.Run(fmt.Sprintf("insert_%02d", attempt.Index), func(t *testing.T) {
					value := uniquetest.Value(t, profile.Name, attempt.Input)
					key, err := backend.Insert(ctx, query.NewInsertPlanReturningKey(model.DBTable, []query.Assignment{query.NewAssignment(field, value)}, id))
					if number, ok := value.Float(); ok && math.IsNaN(number) {
						// Existing SQLite policy rejects NaN before I/O instead
						// of silently storing the reference driver's SQL NULL.
						if !attempt.Saved || err == nil || key != 0 {
							t.Fatal("NaN rejection policy changed", err)
						}
						var native *modernsqlite.Error
						if errors.As(err, &native) {
							t.Fatal("NaN reached native SQLite")
						}
						deviations++
						return
					}
					saved := attempt.Saved
					if profile.Name == "json" {
						expected := canonical.Attempts[attempt.Index]
						if !reflect.DeepEqual(uniquetest.Value(t, "json_canonical", expected.Input), value) {
							t.Fatal("canonical reference uses different logical input")
						}
						saved = expected.Saved
						if saved != attempt.Saved {
							deviations++
						}
					}
					if !saved {
						assertSQLiteUniqueError(t, err)
						if key != 0 {
							t.Fatal("rejected unique insert returned a key")
						}
						return
					}
					if err != nil || key <= 0 {
						t.Fatal("reference save rejected", key, err)
					}
					count++
					if value.Kind() == query.ValueNull {
						nullID = key
					} else if valueID == 0 {
						valueID, occupied = key, value
					}
				})
			}
			if t.Failed() {
				return
			}
			wantDeviations := 0
			if profile.Name == "float" {
				wantDeviations = 2
			} else if profile.Name == "json" {
				wantDeviations = 1
			}
			if deviations != wantDeviations {
				t.Fatal("SQLite uniqueness policy deviation roster changed", deviations)
			}
			if nullID <= 0 || valueID <= 0 {
				t.Fatal("reference lost nullable and occupied rows")
			}
			update := func(key int64) (int64, error) {
				return backend.Update(ctx, query.NewUpdatePlan(model.DBTable, []query.Assignment{query.NewAssignment(field, occupied)}, id, query.Integer(key)))
			}
			if changed, err := update(valueID); err != nil || changed != 1 {
				t.Fatal("self update conflicted", err)
			}
			changed, err := update(nullID)
			assertSQLiteUniqueError(t, err)
			if changed != 0 {
				t.Fatal("failed update returned row count")
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := backend.Insert(canceled, query.NewInsertPlanReturningKey(model.DBTable, []query.Assignment{query.NewAssignment(field, occupied)}, id)); !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation lost precedence", err)
			}
			if err := backend.Close(); err != nil {
				t.Fatal(err)
			}
			reopened := openMigrationHistoryFileBackend(t, path)
			if actual := sqliteUniqueCount(t, reopened, "SELECT COUNT(*) FROM "+model.DBTable); actual != int64(count) {
				t.Fatal("reopen lost rows", actual, count)
			}
			if actual := sqliteUniqueCount(t, reopened, "SELECT value IS NULL FROM "+model.DBTable+" WHERE id=?", nullID); actual != 1 {
				t.Fatal("failed update changed original NULL")
			}
			if err := assertSQLiteUniqueIndexes(ctx, reopened.database, model, model.Fields); err != nil {
				t.Fatal(err)
			}
			if _, err := (migrations.Executor{Backend: reopened}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
				t.Fatal("reopened history failed", err)
			}
		})
	}
}

func TestSQLiteUniqueConcurrentWritesAndAtomicRollback(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "concurrent.sqlite")
	first := openLifecycleFileBackend(t, path, "&_pragma=busy_timeout(5000)")
	second := openLifecycleFileBackend(t, path, "&_pragma=busy_timeout(5000)")
	for _, writer := range []*Backend{first, second} {
		if got := sqliteUniqueCount(t, writer, "PRAGMA busy_timeout"); got != 5000 {
			t.Fatal("concurrent writer timeout not configured", got)
		}
	}
	model, field := uniquetest.Model(t, "char")
	initial := migrations.Migration{App: "uniqueref", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "uniqueref", Model: model}}}
	if _, err := (migrations.Executor{Backend: first}).Migrate(ctx, sqliteUniqueHistory(t, initial), migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	plan := func(value string) query.InsertPlan {
		return query.NewInsertPlanReturningKey(model.DBTable, []query.Assignment{query.NewAssignment(field, query.String(value))}, id)
	}
	type result struct {
		key int64
		err error
	}
	start := make(chan struct{})
	ready := make(chan struct{}, 2)
	results := make(chan result, 2)
	for _, writer := range []*Backend{first, second} {
		go func() {
			ready <- struct{}{}
			<-start
			key, err := writer.Insert(ctx, plan("same"))
			results <- result{key, err}
		}()
	}
	<-ready
	<-ready
	close(start)
	saved, rejected := 0, 0
	for range 2 {
		r := <-results
		if r.err == nil {
			if r.key <= 0 {
				t.Fatal("saved insert has no key")
			}
			saved++
		} else {
			assertSQLiteUniqueError(t, r.err)
			if r.key != 0 {
				t.Fatal("rejected insert has key")
			}
			rejected++
		}
	}
	if saved != 1 || rejected != 1 {
		t.Fatal("concurrent duplicate survived", saved, rejected)
	}
	for _, scope := range []string{"atomic", "relation"} {
		for _, operation := range []string{"insert", "update"} {
			t.Run(scope+"/"+operation, func(t *testing.T) {
				callback := func(session db.Session) error {
					key, err := session.Insert(ctx, plan("before conflict"))
					if err != nil {
						return err
					}
					if operation == "insert" {
						_, err = session.Insert(ctx, plan("same"))
					} else {
						_, err = session.Update(ctx, query.NewUpdatePlan(model.DBTable, []query.Assignment{query.NewAssignment(field, query.String("same"))}, id, query.Integer(key)))
					}
					return err
				}
				var err error
				if scope == "relation" {
					err = first.AtomicRelation(ctx, func(session db.RelationSession) error { return callback(session) })
				} else {
					err = first.Atomic(ctx, callback)
				}
				assertSQLiteUniqueError(t, err)
				if count := sqliteUniqueCount(t, first, "SELECT COUNT(*) FROM "+model.DBTable); count != 1 {
					t.Fatal("atomic failure left partial mutation", count)
				}
			})
		}
	}
	if _, err := first.Insert(ctx, plan("after rollback")); err != nil {
		t.Fatal("failed atomic poisoned later writes", err)
	}
}

func TestSQLiteUniqueAlterFailurePreservesRevisionRowsAndInboundFK(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "alter.sqlite")
	backend := openMigrationHistoryFileBackend(t, path)
	built, err := schema.Build(schema.Definition{AppLabel: "uniqueref", Models: []schema.Model{
		{Name: "owner", GoName: "Owner", Fields: []schema.Field{schema.TextField("reference", "Reference", schema.Nullable())}},
		{Name: "child", GoName: "Child", Fields: []schema.Field{schema.ForeignKey("owner", "Owner", schema.Target("uniqueref", "owner"), schema.RelatedName("children"), schema.Protect)}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	models := map[string]ir.Model{}
	for _, m := range built.Models {
		models[m.Name] = m
	}
	before, after := models["owner"], models["owner"].Clone()
	after.Fields[1].Unique = true
	initial := migrations.Migration{App: "uniqueref", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "uniqueref", Model: before}, migrations.CreateModel{AppLabel: "uniqueref", Model: models["child"]}}}
	add := migrations.Migration{App: "uniqueref", Name: "0002_unique", Dependencies: []migrations.MigrationKey{initial.Key()}, Operations: []migrations.Operation{migrations.AlterField{AppLabel: "uniqueref", ModelName: "owner", Before: before.Fields[1], After: after.Fields[1]}}}
	remove := migrations.Migration{App: "uniqueref", Name: "0003_remove", Dependencies: []migrations.MigrationKey{add.Key()}, Operations: []migrations.Operation{migrations.AlterField{AppLabel: "uniqueref", ModelName: "owner", Before: after.Fields[1], After: before.Fields[1]}}}
	loaded := sqliteUniqueHistory(t, initial, add, remove)
	executor := migrations.Executor{Backend: backend}
	initialTarget := migrations.TargetedLifecycleRequest(migrations.NamedTarget(initial.Key()))
	uniqueTarget := migrations.TargetedLifecycleRequest(migrations.NamedTarget(add.Key()))
	if _, err := executor.Migrate(ctx, loaded, initialTarget); err != nil {
		t.Fatal(err)
	}
	sqliteUniqueExec(t, backend, `INSERT INTO uniqueref_owner (reference) VALUES ('same'),('same')`)
	sqliteUniqueExec(t, backend, `INSERT INTO uniqueref_child (owner_id) VALUES (1)`)
	for _, reverse := range []bool{false, true} {
		if reverse {
			if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
				t.Fatal(err)
			}
			sqliteUniqueExec(t, backend, `UPDATE uniqueref_owner SET reference='same' WHERE id=2`)
		}
		held := openLifecycleSession(t, backend)
		if _, err := held.ReadAppliedMigrations(ctx); err != nil {
			t.Fatal(err)
		}
		original := sqliteUniqueReadSnapshot(t, backend, before, models["child"])
		_, err := executor.Migrate(ctx, loaded, uniqueTarget)
		assertSQLiteNativeUniqueError(t, err)
		if mb.IsCapabilityError(err) || mb.IsRevisionFenceError(err) {
			t.Fatal("duplicate migration lost execution ownership", err)
		}
		if current := sqliteUniqueReadSnapshot(t, backend, before, models["child"]); !reflect.DeepEqual(current, original) {
			t.Fatal("failed unique alteration changed catalog, data, sequence, revision or FK state")
		}
		transition := mb.HistoryTransition{Kind: mb.HistoryTransitionApply, Migration: mb.AppliedMigration{App: add.App, Name: add.Name}}
		if reverse {
			transition.Kind, transition.Migration.Name = mb.HistoryTransitionUnapply, remove.Name
		}
		tx, err := held.BeginMigration(ctx, transition, mb.MigrationIntent{Operations: []mb.MigrationOperation{{Kind: mb.MigrationAlterField, Before: before, After: after}}})
		if err != nil {
			t.Fatal("failed unique alteration invalidated held revision", err)
		}
		if err := tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		if err := held.Close(ctx); err != nil {
			t.Fatal(err)
		}
		sqliteUniqueExec(t, backend, `UPDATE uniqueref_owner SET reference='fixed' WHERE id=2`)
		if _, err := executor.Migrate(ctx, loaded, uniqueTarget); err != nil {
			t.Fatal("explicit repair and retry failed", err)
		}
		if err := assertSQLiteUniqueIndexes(ctx, backend.database, after, after.Fields); err != nil {
			t.Fatal(err)
		}
		if n := sqliteUniqueCount(t, backend, `SELECT COUNT(*) FROM uniqueref_child WHERE owner_id=1`); n != 1 {
			t.Fatal("inbound FK row lost")
		}
	}
	_, err = backend.ExecContext(ctx, `INSERT INTO uniqueref_child (owner_id) VALUES (999)`)
	var cause *modernsqlite.Error
	if !errors.As(err, &cause) || cause.Code() != sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY {
		t.Fatal("inbound FK lost enforcement", err)
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openMigrationHistoryFileBackend(t, path)
	if _, err := (migrations.Executor{Backend: reopened}).Migrate(ctx, loaded, uniqueTarget); err != nil {
		t.Fatal("unique history lost on reopen", err)
	}
}

func TestSQLiteUniqueNullableAddAndRetainedIndexesThroughForeignKeyRemake(t *testing.T) {
	ctx := t.Context()
	backend := openMigrationHistoryFileBackend(t, filepath.Join(t.TempDir(), "remake.sqlite"))
	parentField, parentValue := uniquetest.Field(t, "char")
	build := func(extended bool) map[string]ir.Model {
		t.Helper()
		parent := schema.Model{Name: "entry", GoName: "Entry", Fields: []schema.Field{parentField}}
		child := schema.Model{Name: "child", GoName: "Child", Fields: []schema.Field{
			schema.TextField("label", "Label", schema.Unique()),
			schema.ForeignKey("keeper", "Keeper", schema.Target("uniqueref", "entry"), schema.RelatedName("keepers"), schema.Protect, schema.Nullable(), schema.Unique()),
		}}
		if extended {
			parent.Fields = append(parent.Fields, schema.UUIDField("external", "External", schema.Nullable(), schema.Unique()))
			child.Fields = append(child.Fields, schema.ForeignKey("parent", "Parent", schema.Target("uniqueref", "entry"), schema.RelatedName("children"), schema.Protect, schema.Nullable(), schema.Unique()))
		}
		built, err := schema.Build(schema.Definition{AppLabel: "uniqueref", Models: []schema.Model{parent, child}})
		if err != nil {
			t.Fatal(err)
		}
		models := map[string]ir.Model{}
		for _, m := range built.Models {
			models[m.Name] = m
		}
		return models
	}
	before, after := build(false), build(true)
	initial := migrations.Migration{App: "uniqueref", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "uniqueref", Model: before["entry"]}, migrations.CreateModel{AppLabel: "uniqueref", Model: before["child"]}}}
	addition := migrations.Migration{App: "uniqueref", Name: "0002_fields", Dependencies: []migrations.MigrationKey{initial.Key()}, Operations: []migrations.Operation{
		migrations.AddField{AppLabel: "uniqueref", ModelName: "entry", Field: after["entry"].Fields[2]},
		migrations.AddField{AppLabel: "uniqueref", ModelName: "child", Field: after["child"].Fields[3]},
	}}
	loaded := sqliteUniqueHistory(t, initial, addition)
	executor := migrations.Executor{Backend: backend}
	initialTarget := migrations.TargetedLifecycleRequest(migrations.NamedTarget(initial.Key()))
	if _, err := executor.Migrate(ctx, loaded, initialTarget); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`INSERT INTO uniqueref_entry (value) VALUES ('first'),('second')`,
		`INSERT INTO uniqueref_entry (id,value) VALUES (90,'deleted')`,
		`DELETE FROM uniqueref_entry WHERE id=90`,
		`INSERT INTO uniqueref_child (label,keeper_id) VALUES ('one',1),('two',2)`,
		`INSERT INTO uniqueref_child (id,label) VALUES (70,'deleted')`,
		`DELETE FROM uniqueref_child WHERE id=70`,
	} {
		sqliteUniqueExec(t, backend, stmt)
	}
	if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{`SELECT COUNT(*) FROM uniqueref_entry WHERE external IS NULL`, `SELECT COUNT(*) FROM uniqueref_child WHERE parent_id IS NULL`} {
		if count := sqliteUniqueCount(t, backend, stmt); count != 2 {
			t.Fatal("nullable unique addition changed existing rows", count)
		}
	}
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	for _, change := range []struct {
		table string
		field query.FieldRef
		value query.Value
	}{
		{"uniqueref_entry", query.NewFieldRef("external", "external", query.FieldUUID, true), uniquetest.Value(t, "uuid", []byte(`"12345678-9abc-4def-8123-456789abcdef"`))},
		{"uniqueref_child", query.NewFieldRef("parent", "parent_id", query.FieldInteger, true), query.Integer(1)},
	} {
		update := func(key int64) (int64, error) {
			return backend.Update(ctx, query.NewUpdatePlan(change.table, []query.Assignment{query.NewAssignment(change.field, change.value)}, id, query.Integer(key)))
		}
		if count, err := update(1); err != nil || count != 1 {
			t.Fatal(err)
		}
		count, err := update(2)
		assertSQLiteUniqueError(t, err)
		if count != 0 {
			t.Fatal("duplicate changed a row")
		}
	}
	_, err := backend.Update(ctx, query.NewUpdatePlan("uniqueref_child", []query.Assignment{query.NewAssignment(query.NewFieldRef("parent", "parent_id", query.FieldInteger, true), query.Integer(999))}, id, query.Integer(2)))
	var cause *modernsqlite.Error
	if !errors.As(err, &cause) || cause.Code() != sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY {
		t.Fatal("unique FK lost referential enforcement", err)
	}
	if _, err := executor.Migrate(ctx, loaded, initialTarget); err != nil {
		t.Fatal("reverse unique additions failed", err)
	}
	for _, model := range before {
		if err := assertSQLiteUniqueIndexes(ctx, backend.database, model, model.Fields); err != nil {
			t.Fatal("retained indexes lost during remake", err)
		}
	}
	if n := sqliteUniqueCount(t, backend, `SELECT COUNT(*) FROM uniqueref_child WHERE (id=1 AND label='one' AND keeper_id=1) OR (id=2 AND label='two' AND keeper_id=2)`); n != 2 {
		t.Fatal("remake changed retained rows or FK values")
	}
	_, err = backend.ExecContext(ctx, `UPDATE uniqueref_child SET label='one' WHERE id=2`)
	assertSQLiteNativeUniqueError(t, err)
	_, err = backend.ExecContext(ctx, `UPDATE uniqueref_child SET keeper_id=1 WHERE id=2`)
	assertSQLiteNativeUniqueError(t, err)
	key, err := backend.Insert(ctx, query.NewInsertPlanReturningKey("uniqueref_entry", []query.Assignment{query.NewAssignment(parentValue, query.String("third"))}, id))
	if err != nil || key != 91 {
		t.Fatal("parent sequence high-water lost", key, err)
	}
	key, err = backend.Insert(ctx, query.NewInsertPlanReturningKey("uniqueref_child", []query.Assignment{query.NewAssignment(query.NewFieldRef("label", "label", query.FieldString, false), query.String("three"))}, id))
	if err != nil || key != 71 {
		t.Fatal("remade child sequence high-water lost", key, err)
	}
	if _, err := executor.Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.ZeroTarget("uniqueref"))); err != nil {
		t.Fatal("unique graph reverse failed", err)
	}
	if count := sqliteUniqueCount(t, backend, `SELECT COUNT(*) FROM sqlite_schema WHERE name LIKE 'godj_uq_%'`); count != 0 {
		t.Fatal("deleted models left owned indexes")
	}
	if count := sqliteUniqueCount(t, backend, `SELECT COUNT(*) FROM sqlite_sequence WHERE name IN ('uniqueref_entry','uniqueref_child')`); count != 0 {
		t.Fatal("deleted models left sequences")
	}
	if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal("unique graph recreation failed", err)
	}
}

func TestSQLiteUniqueChangesBeforeRemakeUseOperationState(t *testing.T) {
	for _, initialUnique := range []bool{false, true} {
		t.Run(fmt.Sprint(initialUnique), func(t *testing.T) {
			ctx := t.Context()
			backend := openMigrationTestBackend(t)
			parent, child, fk := sqliteRelationTestModels()
			// Start with a scalar table, add its FK, then change a retained
			// column's uniqueness. Reverse changes the index before the remake.
			child.Fields[1].Unique = initialUnique
			without := child.Clone()
			without.Fields = without.Fields[:len(without.Fields)-1]
			altered := child.Clone()
			altered.Fields[1].Unique = !initialUnique
			initial := migrations.Migration{App: "news", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "news", Model: parent}, migrations.CreateModel{AppLabel: "news", Model: without}}}
			fk.Nullable = true
			child.Fields[len(child.Fields)-1] = fk
			altered.Fields[len(altered.Fields)-1] = fk
			added := migrations.Migration{App: "news", Name: "0002_mixed", Dependencies: []migrations.MigrationKey{initial.Key()}, Operations: []migrations.Operation{
				migrations.AddField{AppLabel: "news", ModelName: child.Name, Field: fk},
				migrations.AlterField{AppLabel: "news", ModelName: child.Name, Before: child.Fields[1], After: altered.Fields[1]},
			}}
			loaded := sqliteUniqueHistory(t, initial, added)
			executor := migrations.Executor{Backend: backend}
			target := migrations.TargetedLifecycleRequest(migrations.NamedTarget(initial.Key()))
			if _, err := executor.Migrate(ctx, loaded, target); err != nil {
				t.Fatal(err)
			}
			sqliteUniqueExec(t, backend, `INSERT INTO news_article (id,title) VALUES (7,'one'),(80,'deleted')`)
			sqliteUniqueExec(t, backend, `DELETE FROM news_article WHERE id=80`)
			for _, request := range []migrations.LifecycleRequest{migrations.LatestLifecycleRequest(), target} {
				if _, err := executor.Migrate(ctx, loaded, request); err != nil {
					t.Fatal("mixed unique/remake failed", err)
				}
			}
			if err := assertSQLiteUniqueIndexes(ctx, backend.database, without, without.Fields); err != nil {
				t.Fatal(err)
			}
			if n := sqliteUniqueCount(t, backend, `SELECT COUNT(*) FROM news_article WHERE id=7 AND title='one'`); n != 1 {
				t.Fatal("mixed operation lost rows")
			}
			if high := sqliteUniqueCount(t, backend, `SELECT seq FROM sqlite_sequence WHERE name='news_article'`); high != 80 {
				t.Fatal("mixed operation lost high-water", high)
			}
		})
	}
}

func TestSQLiteUniqueCatalogDriftFailsBeforeRevisionClaim(t *testing.T) {
	for _, mode := range []string{"missing", "nonunique", "column", "descending", "collation", "compound", "expression", "partial", "extra", "spelling", "other owner"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			backend := openMigrationTestBackend(t)
			model, _ := uniquetest.Model(t, "char")
			initial := migrations.Migration{App: "uniqueref", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "uniqueref", Model: model}}}
			addition := migrations.Migration{App: "uniqueref", Name: "0002_note", Dependencies: []migrations.MigrationKey{initial.Key()}, Operations: []migrations.Operation{migrations.AddField{AppLabel: "uniqueref", ModelName: model.Name, Field: ir.Field{Name: "note", GoName: "Note", Column: "note", Kind: ir.FieldText, Nullable: true}}}}
			loaded := sqliteUniqueHistory(t, initial, addition)
			executor := migrations.Executor{Backend: backend}
			if _, err := executor.Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(initial.Key()))); err != nil {
				t.Fatal(err)
			}
			sqliteUniqueExec(t, backend, `INSERT INTO uniqueref_entry (value) VALUES ('one'),('two')`)
			name, err := sqliteUniqueIndexName(model.DBTable, model.Fields[1].Column)
			if err != nil {
				t.Fatal(err)
			}
			quoted, _ := quoteIdentifier(name)
			if mode != "extra" {
				sqliteUniqueExec(t, backend, "DROP INDEX "+quoted)
			}
			declaration := "CREATE UNIQUE INDEX " + quoted + " ON uniqueref_entry (value)"
			switch mode {
			case "missing":
				declaration = ""
			case "nonunique":
				declaration = "CREATE INDEX " + quoted + " ON uniqueref_entry (value)"
			case "column":
				declaration = "CREATE UNIQUE INDEX " + quoted + " ON uniqueref_entry (id)"
			case "descending":
				declaration = "CREATE UNIQUE INDEX " + quoted + " ON uniqueref_entry (value DESC)"
			case "collation":
				declaration = "CREATE UNIQUE INDEX " + quoted + " ON uniqueref_entry (value COLLATE NOCASE)"
			case "compound":
				declaration = "CREATE UNIQUE INDEX " + quoted + " ON uniqueref_entry (value,id)"
			case "expression":
				declaration = "CREATE UNIQUE INDEX " + quoted + " ON uniqueref_entry (lower(value))"
			case "partial":
				declaration += " WHERE value IS NOT NULL"
			case "extra":
				declaration = "CREATE INDEX extra_index ON uniqueref_entry (value)"
			case "spelling":
				declaration = "CREATE UNIQUE INDEX " + strings.ToUpper(quoted) + " ON uniqueref_entry (value)"
			case "other owner":
				sqliteUniqueExec(t, backend, `CREATE TABLE outsider (value TEXT)`)
				declaration = "CREATE UNIQUE INDEX " + quoted + " ON outsider (value)"
			}
			if declaration != "" {
				sqliteUniqueExec(t, backend, declaration)
			}
			before := sqliteUniqueReadSnapshot(t, backend, model)
			if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); !errors.Is(err, errSQLiteRelationPhysicalDrift) {
				t.Fatal("physical uniqueness drift accepted or lost preclaim classification", err)
			}
			if after := sqliteUniqueReadSnapshot(t, backend, model); !reflect.DeepEqual(before, after) {
				t.Fatal("preclaim rejection changed stored state")
			}
		})
	}
}

func TestSQLiteUniqueFutureIndexNamespaceRejectsMainAndTempObjects(t *testing.T) {
	for _, namespace := range []string{"main", "temp"} {
		t.Run(namespace, func(t *testing.T) {
			ctx := t.Context()
			backend := openMigrationTestBackend(t)
			model, _ := uniquetest.Model(t, "char")
			name, err := sqliteUniqueIndexName(model.DBTable, model.Fields[1].Column)
			if err != nil {
				t.Fatal(err)
			}
			quoted, _ := quoteIdentifier(name)
			sqliteUniqueExec(t, backend, "CREATE TABLE "+namespace+"."+quoted+" (id INTEGER)")
			initial := migrations.Migration{App: "uniqueref", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "uniqueref", Model: model}}}
			before := sqliteUniqueReadSnapshot(t, backend)
			_, err = (migrations.Executor{Backend: backend}).Migrate(ctx, sqliteUniqueHistory(t, initial), migrations.LatestLifecycleRequest())
			if !errors.Is(err, errSQLiteRelationPhysicalDrift) {
				t.Fatal("future unique name conflict was not rejected before claim", err)
			}
			if after := sqliteUniqueReadSnapshot(t, backend); !reflect.DeepEqual(before, after) {
				t.Fatal("name conflict mutated schema/history")
			}
			if count := sqliteUniqueCount(t, backend, "SELECT COUNT(*) FROM "+namespace+".sqlite_schema WHERE name=?", name); count != 1 {
				t.Fatal("name conflict deleted foreign object")
			}
		})
	}
}

func TestSQLiteUniqueTransitiveTargetPhysicalValidation(t *testing.T) {
	ctx := t.Context()
	backend := openMigrationTestBackend(t)
	built, err := schema.Build(schema.Definition{AppLabel: "uniqueref", Models: []schema.Model{
		{Name: "owner", GoName: "Owner", Fields: []schema.Field{schema.TextField("reference", "Reference", schema.Unique())}},
		{Name: "group", GoName: "Group", Fields: []schema.Field{schema.ForeignKey("owner", "Owner", schema.Target("uniqueref", "owner"), schema.RelatedName("groups"), schema.Protect)}},
		{Name: "entry", GoName: "Entry", Fields: []schema.Field{schema.ForeignKey("group", "Group", schema.Target("uniqueref", "group"), schema.RelatedName("entries"), schema.Protect)}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	models := map[string]ir.Model{}
	for _, model := range built.Models {
		models[model.Name] = model
	}
	initial := migrations.Migration{App: "uniqueref", Name: "0001_initial"}
	for _, name := range []string{"owner", "group", "entry"} {
		initial.Operations = append(initial.Operations, migrations.CreateModel{AppLabel: "uniqueref", Model: models[name]})
	}
	addition := migrations.Migration{App: "uniqueref", Name: "0002_note", Dependencies: []migrations.MigrationKey{initial.Key()}, Operations: []migrations.Operation{migrations.AddField{AppLabel: "uniqueref", ModelName: "entry", Field: ir.Field{Name: "note", GoName: "Note", Column: "note", Kind: ir.FieldText, Nullable: true}}}}
	loaded := sqliteUniqueHistory(t, initial, addition)
	executor := migrations.Executor{Backend: backend}
	if _, err := executor.Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(initial.Key()))); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{`INSERT INTO uniqueref_owner (reference) VALUES ('one')`, `INSERT INTO uniqueref_group (owner_id) VALUES (1)`, `INSERT INTO uniqueref_entry (group_id) VALUES (1)`} {
		sqliteUniqueExec(t, backend, stmt)
	}
	name, _ := sqliteUniqueIndexName(models["owner"].DBTable, "reference")
	quoted, _ := quoteIdentifier(name)
	sqliteUniqueExec(t, backend, "DROP INDEX "+quoted)
	before := sqliteUniqueReadSnapshot(t, backend, models["owner"], models["group"], models["entry"])
	if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); !errors.Is(err, errSQLiteRelationPhysicalDrift) {
		t.Fatal("transitive target unique drift escaped validation", err)
	}
	if after := sqliteUniqueReadSnapshot(t, backend, models["owner"], models["group"], models["entry"]); !reflect.DeepEqual(before, after) {
		t.Fatal("target drift rejection changed data/history")
	}
}

func TestSQLiteUniqueStatementFailureRollsBackCreatedTableAndBootstrap(t *testing.T) {
	ctx := t.Context()
	backend := openMigrationTestBackend(t)
	model, _ := uniquetest.Model(t, "char")
	before := sqliteUniqueReadSnapshot(t, backend)
	session := openLifecycleSession(t, backend)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	transition := mb.HistoryTransition{Kind: mb.HistoryTransitionApply, Migration: mb.AppliedMigration{App: "uniqueref", Name: "0001_initial"}}
	tx := beginLifecycleTransaction(t, session, transition, createModelMigrationIntent(model))
	concrete := tx.(*sqliteRevisionFencedTransaction)
	name, _ := sqliteUniqueIndexName(model.DBTable, model.Fields[1].Column)
	quoted, _ := quoteIdentifier(name)
	// Inject a deterministic native failure after preflight. The table body
	// succeeds, then its unique-index body collides. Commit must roll back both
	// the table and the already-claimed bootstrap metadata, even if attempted.
	if _, err := concrete.connection.ExecContext(ctx, "CREATE TABLE "+quoted+" (id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	failure := tx.CreateModel(ctx, model)
	if failure == nil {
		t.Fatal("late index failure was ignored")
	}
	if err := tx.RecordApplied(ctx, "uniqueref", "0001_initial"); !errors.Is(err, failure) {
		t.Fatal("failed stream allowed recorder publication", err)
	}
	outcome, err := tx.CommitFenced(ctx)
	if !errors.Is(err, failure) || outcome.Durability != mb.CommitRolledBack {
		t.Fatal("failed SQL group committed or lost cause", outcome, err)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if after := sqliteUniqueReadSnapshot(t, backend); !reflect.DeepEqual(before, after) {
		t.Fatal("failed SQL group left table/index/bootstrap mutation")
	}
}

func TestSQLiteUniqueRemoveLateFailureKeepsExecutionOwnershipAndRollsBack(t *testing.T) {
	for _, test := range []struct {
		name, method, contains string
		cause                  error
		remainingColumns       int
	}{
		{"drop_column_busy", "exec", "DROP COLUMN", sqliteUniqueBusyError{}, 1},
		{"final_catalog_busy", "query", "FROM main.sqlite_schema", sqliteUniqueBusyError{}, 0},
		{"final_catalog_drift", "query", "FROM main.sqlite_schema", relationPhysicalDrift("injected final catalog drift"), 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := t.Context()
			backend := openMigrationTestBackend(t)
			model, _ := uniquetest.Model(t, "char")
			field := ir.Field{Name: "note", GoName: "Note", Column: "note", Kind: ir.FieldText, Nullable: true, Unique: true}
			withNote := model.Clone()
			withNote.Fields = append(withNote.Fields, field)
			initial := migrations.Migration{App: "uniqueref", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "uniqueref", Model: model}}}
			added := migrations.Migration{App: "uniqueref", Name: "0002_note", Dependencies: []migrations.MigrationKey{initial.Key()}, Operations: []migrations.Operation{migrations.AddField{AppLabel: "uniqueref", ModelName: model.Name, Field: field}}}
			loaded := sqliteUniqueHistory(t, initial, added)
			executor := migrations.Executor{Backend: backend}
			if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
				t.Fatal(err)
			}
			sqliteUniqueExec(t, backend, `INSERT INTO uniqueref_entry (id,value,note) VALUES (4,'one','alpha'),(99,'deleted','beta')`)
			sqliteUniqueExec(t, backend, `DELETE FROM uniqueref_entry WHERE id=99`)
			before := sqliteUniqueReadSnapshot(t, backend, withNote)
			session := openLifecycleSession(t, backend)
			if _, err := session.ReadAppliedMigrations(ctx); err != nil {
				t.Fatal(err)
			}
			transition := mb.HistoryTransition{Kind: mb.HistoryTransitionUnapply, Migration: mb.AppliedMigration{App: added.App, Name: added.Name}}
			intent := mb.MigrationIntent{Operations: []mb.MigrationOperation{{Kind: mb.MigrationRemoveField, Before: withNote, After: model}}}
			tx := beginLifecycleTransaction(t, session, transition, intent)
			concrete := tx.(*sqliteRevisionFencedTransaction)
			fault := &sqliteRelationBeginFaultConnection{migrationPinnedConnection: concrete.connection, method: test.method, contains: test.contains, remaining: 1, faultErr: test.cause}
			concrete.connection = fault
			failure := tx.RemoveField(ctx, withNote, field)
			if !errors.Is(failure, test.cause) || fault.remaining != 0 || mb.IsCapabilityError(failure) || mb.IsRevisionFenceError(failure) {
				t.Fatal("late removal failure escaped its operation owner", failure)
			}
			// Observe the real intermediate DDL before rollback: the owned index
			// is gone, and final verification runs only after the column is gone.
			name, err := sqliteUniqueIndexName(withNote.DBTable, field.Column)
			if err != nil {
				t.Fatal(err)
			}
			var indexes, columns int
			if err := concrete.connection.QueryRowContext(ctx, `SELECT COUNT(*) FROM main.sqlite_schema WHERE type='index' AND name=?`, name).Scan(&indexes); err != nil || indexes != 0 {
				t.Fatal("fault did not follow index removal", indexes, err)
			}
			if err := concrete.connection.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_xinfo('uniqueref_entry') WHERE name='note'`).Scan(&columns); err != nil || columns != test.remainingColumns {
				t.Fatal("fault did not occur at the intended removal stage", columns, err)
			}
			if err := tx.RecordUnapplied(ctx, added.App, added.Name); !errors.Is(err, failure) {
				t.Fatal("failed removal recorded success", err)
			}
			outcome, err := tx.CommitFenced(ctx)
			if !errors.Is(err, failure) || outcome.Durability != mb.CommitRolledBack {
				t.Fatal("failed removal committed or lost its cause", outcome, err)
			}
			if err := session.Close(ctx); err != nil {
				t.Fatal(err)
			}
			if after := sqliteUniqueReadSnapshot(t, backend, withNote); !reflect.DeepEqual(before, after) {
				t.Fatal("failed removal changed rows, indexes, sequence or revision")
			}
			if _, err := executor.Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(initial.Key()))); err != nil {
				t.Fatal("failed removal prevented a clean retry", err)
			}
		})
	}
}

func TestSQLiteUniqueRemakeIndexFailureRestoresOriginalRowsIndexesSequenceAndFK(t *testing.T) {
	ctx := t.Context()
	backend := openMigrationTestBackend(t)
	parent, withFK, fk := sqliteRelationTestModels()
	withFK.Fields[1].Unique = true
	without := withFK.Clone()
	without.Fields = without.Fields[:len(without.Fields)-1]
	initial := migrations.Migration{App: "news", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "news", Model: parent}, migrations.CreateModel{AppLabel: "news", Model: without}}}
	added := migrations.Migration{App: "news", Name: "0002_relation", Dependencies: []migrations.MigrationKey{initial.Key()}, Operations: []migrations.Operation{migrations.AddField{AppLabel: "news", ModelName: withFK.Name, Field: fk}}}
	loaded := sqliteUniqueHistory(t, initial, added)
	executor := migrations.Executor{Backend: backend}
	if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{`INSERT INTO news_author (name) VALUES ('author')`, `INSERT INTO news_article (id,title,author_id) VALUES (4,'one',1),(99,'deleted',1)`, `DELETE FROM news_article WHERE id=99`} {
		sqliteUniqueExec(t, backend, stmt)
	}
	original := sqliteUniqueReadSnapshot(t, backend, parent, withFK)
	session := openLifecycleSession(t, backend)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	transition := mb.HistoryTransition{Kind: mb.HistoryTransitionUnapply, Migration: mb.AppliedMigration{App: added.App, Name: added.Name}}
	intent := mb.MigrationIntent{Operations: []mb.MigrationOperation{{Kind: mb.MigrationRemoveField, Before: withFK, After: without, Targets: []mb.MigrationTarget{{SourceField: fk, TargetModel: parent, TargetKey: parent.Fields[0]}}}}}
	tx := beginLifecycleTransaction(t, session, transition, intent)
	concrete := tx.(*sqliteRevisionFencedTransaction)
	name, _ := sqliteUniqueIndexName(withFK.DBTable, withFK.Fields[1].Column)
	quoted, _ := quoteIdentifier(name)
	// Force a failure specifically when the retained index is recreated, after
	// row copy, table replacement and sequence restoration have all executed.
	for _, stmt := range []string{"DROP INDEX " + quoted, "CREATE TABLE " + quoted + " (id INTEGER)"} {
		if _, err := concrete.connection.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	failure := tx.RemoveField(ctx, withFK, fk)
	if failure == nil {
		t.Fatal("late unique-index restoration failure ignored")
	}
	if err := tx.RecordUnapplied(ctx, added.App, added.Name); !errors.Is(err, failure) {
		t.Fatal("failed remake recorded success", err)
	}
	outcome, err := tx.CommitFenced(ctx)
	if !errors.Is(err, failure) || outcome.Durability != mb.CommitRolledBack {
		t.Fatal("failed remake did not roll back", outcome, err)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if current := sqliteUniqueReadSnapshot(t, backend, parent, withFK); !reflect.DeepEqual(original, current) {
		t.Fatal("failed remake changed rows, indexes, sequence, revision or FK setting")
	}
	if _, err := executor.Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(initial.Key()))); err != nil {
		t.Fatal("rollback did not permit clean retry", err)
	}
	if err := assertSQLiteUniqueIndexes(ctx, backend.database, without, without.Fields); err != nil {
		t.Fatal("retry lost retained uniqueness", err)
	}
}

func TestSQLiteUniqueErrorClassificationKeepsCauseAndCancellation(t *testing.T) {
	ctx := t.Context()
	backend := openMigrationTestBackend(t)
	sqliteUniqueExec(t, backend, `CREATE TABLE error_probe (id INTEGER PRIMARY KEY, value TEXT UNIQUE)`)
	sqliteUniqueExec(t, backend, `INSERT INTO error_probe (value) VALUES ('private-duplicate-value')`)
	_, native := backend.ExecContext(ctx, `INSERT INTO error_probe (value) VALUES ('private-duplicate-value')`)
	assertSQLiteNativeUniqueError(t, native)
	for _, operation := range []string{"insert", "update", "delete"} {
		wrapped := fmt.Errorf("wrapped: %w", native)
		classified := classifySQLiteWriteError(ctx, operation, wrapped)
		if !errors.Is(classified, wrapped) {
			t.Fatal("write classification lost cause")
		}
		if operation == "delete" {
			if errors.Is(classified, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeUniqueConstraint}) {
				t.Fatal("unrelated operation overclassified")
			}
			continue
		}
		assertSQLiteUniqueError(t, classified)
		if strings.Contains(classified.Error(), "private-duplicate-value") {
			t.Fatal("unique detail exposed rejected value")
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if err := classifySQLiteWriteError(canceled, operation, wrapped); !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation lost precedence", err)
		}
	}
	lookalike := errors.New("UNIQUE constraint failed: error_probe.value (2067)")
	if err := classifySQLiteWriteError(ctx, "update", lookalike); errors.Is(err, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeUniqueConstraint}) || !errors.Is(err, lookalike) {
		t.Fatal("message-only unique classification", err)
	}
}

func TestSQLiteUniqueForeignKeyAlterAndReverse(t *testing.T) {
	ctx := t.Context()
	backend := openMigrationTestBackend(t)
	parent, child, fk := sqliteRelationTestModels()
	fk.Nullable = true
	child.Fields[len(child.Fields)-1] = fk
	unique := fk.Clone()
	unique.Unique = true
	initial := migrations.Migration{App: "news", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "news", Model: parent}, migrations.CreateModel{AppLabel: "news", Model: child}}}
	added := migrations.Migration{App: "news", Name: "0002_unique_fk", Dependencies: []migrations.MigrationKey{initial.Key()}, Operations: []migrations.Operation{migrations.AlterField{AppLabel: "news", ModelName: child.Name, Before: fk, After: unique}}}
	loaded := sqliteUniqueHistory(t, initial, added)
	executor := migrations.Executor{Backend: backend}
	target := migrations.TargetedLifecycleRequest(migrations.NamedTarget(initial.Key()))
	if _, err := executor.Migrate(ctx, loaded, target); err != nil {
		t.Fatal(err)
	}
	sqliteUniqueExec(t, backend, `INSERT INTO news_author (name) VALUES ('author')`)
	sqliteUniqueExec(t, backend, `INSERT INTO news_article (title,author_id) VALUES ('one',1),('two',1)`)
	before := sqliteUniqueReadSnapshot(t, backend, parent, child)
	_, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
	assertSQLiteNativeUniqueError(t, err)
	if after := sqliteUniqueReadSnapshot(t, backend, parent, child); !reflect.DeepEqual(before, after) {
		t.Fatal("duplicate FK alteration changed durable state")
	}
	sqliteUniqueExec(t, backend, `UPDATE news_article SET author_id=NULL WHERE id=2`)
	if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	_, err = backend.ExecContext(ctx, `INSERT INTO news_article (title,author_id) VALUES ('duplicate',1)`)
	assertSQLiteNativeUniqueError(t, err)
	sqliteUniqueExec(t, backend, `INSERT INTO news_article (title,author_id) VALUES ('null',NULL)`)
	if _, err := executor.Migrate(ctx, loaded, target); err != nil {
		t.Fatal("unique FK reverse failed", err)
	}
	sqliteUniqueExec(t, backend, `INSERT INTO news_article (title,author_id) VALUES ('duplicate allowed',1)`)
	if n := sqliteUniqueCount(t, backend, `SELECT COUNT(*) FROM news_article WHERE author_id=1`); n != 2 {
		t.Fatal("unique removal changed FK cardinality")
	}
}

func TestSQLiteUniqueIndexNamespacePreservesUnrelatedSameNameTrigger(t *testing.T) {
	ctx := t.Context()
	backend := openMigrationTestBackend(t)
	model, _ := uniquetest.Model(t, "char")
	name, err := sqliteUniqueIndexName(model.DBTable, model.Fields[1].Column)
	if err != nil {
		t.Fatal(err)
	}
	quoted, _ := quoteIdentifier(name)
	sqliteUniqueExec(t, backend, `CREATE TABLE outsider (id INTEGER)`)
	sqliteUniqueExec(t, backend, "CREATE TRIGGER "+quoted+" AFTER INSERT ON outsider BEGIN UPDATE outsider SET id=id; END")
	initial := migrations.Migration{App: "uniqueref", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "uniqueref", Model: model}}}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, sqliteUniqueHistory(t, initial), migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal("unrelated trigger falsely shadowed unique index", err)
	}
	if count := sqliteUniqueCount(t, backend, `SELECT COUNT(*) FROM sqlite_schema WHERE name=? AND type IN ('index','trigger')`, name); count != 2 {
		t.Fatal("separate trigger namespace was modified")
	}
}
