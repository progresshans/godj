package postgres

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/uniquetest"
	"github.com/progresshans/godj/migrations"
	mb "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func postgresUniqueHistory(t testing.TB, changes ...migrations.Migration) migrations.LoadedDefinitionSet {
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

func assertPostgresUniqueError(t testing.TB, err error) {
	t.Helper()
	if !errors.Is(err, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeUniqueConstraint}) {
		t.Fatalf("expected stable unique error, got %v", err)
	}
	var cause *pgconn.PgError
	if !errors.As(err, &cause) || cause.Code != "23505" {
		t.Fatalf("unique error lost native cause: %v", err)
	}
}

func TestPostgresUniqueReferenceWrites(t *testing.T) {
	url := postgresIntegrationURL(t)
	for _, profile := range uniquetest.Profiles(t, "postgres") {
		t.Run(profile.Name, func(t *testing.T) {
			ctx := t.Context()
			namespace := postgresMigrationIntegrationSchema(t, ctx, url)
			backend := openPostgresMigrationIntegrationBackend(t, ctx, url, namespace)
			model, field := uniquetest.Model(t, profile.Name)
			initial := migrations.Migration{App: "uniqueref", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "uniqueref", Model: model}}}
			loaded := postgresUniqueHistory(t, initial)
			if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
				t.Fatal(err)
			}
			id := query.NewFieldRef("id", "id", query.FieldInteger, false)
			var nullID, valueID int64
			var occupied query.Value
			count := 0
			for _, attempt := range profile.Attempts {
				t.Run(fmt.Sprintf("insert_%02d", attempt.Index), func(t *testing.T) {
					value := uniquetest.Value(t, profile.Name, attempt.Input)
					key, err := backend.Insert(ctx, query.NewInsertPlanReturningKey(model.DBTable, []query.Assignment{query.NewAssignment(field, value)}, id))
					if !attempt.Saved {
						assertPostgresUniqueError(t, err)
						if key != 0 || attempt.SQLState != "23505" {
							t.Fatal("failed insert returned a key or lost reference cause")
						}
						return
					}
					if err != nil || key <= 0 {
						t.Fatalf("reference save rejected: %v", err)
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
			if nullID <= 0 || valueID <= 0 {
				t.Fatal("reference did not exercise NULL and occupied values")
			}
			update := func(key int64) (int64, error) {
				return backend.Update(ctx, query.NewUpdatePlan(model.DBTable, []query.Assignment{query.NewAssignment(field, occupied)}, id, query.Integer(key)))
			}
			if changed, err := update(valueID); err != nil || changed != 1 {
				t.Fatal("unchanged value conflicted with itself", err)
			}
			changed, err := update(nullID)
			assertPostgresUniqueError(t, err)
			if changed != 0 {
				t.Fatal("failed unique update returned a mutation count")
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := backend.Insert(canceled, query.NewInsertPlanReturningKey(model.DBTable, []query.Assignment{query.NewAssignment(field, occupied)}, id)); !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation lost precedence", err)
			}
			if err := backend.Close(); err != nil {
				t.Fatal(err)
			}
			reopened := openPostgresMigrationIntegrationBackend(t, ctx, url, namespace)
			table, err := quoteTable(namespace, model.DBTable)
			if err != nil {
				t.Fatal(err)
			}
			var actual int
			if err := reopened.database.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&actual); err != nil || actual != count {
				t.Fatalf("reopened row count %d, expected %d: %v", actual, count, err)
			}
			var isNull bool
			if err := reopened.database.QueryRowContext(ctx, "SELECT value IS NULL FROM "+table+" WHERE id=$1", nullID).Scan(&isNull); err != nil || !isNull {
				t.Fatal("failed update changed original NULL", err)
			}
			catalog, present, err := loadPostgresMigrationTableCatalog(ctx, reopened.database, namespace, model.DBTable)
			if err != nil || !present {
				t.Fatal("unique table missing after reopen", err)
			}
			if err := assertPostgresMigrationModelCatalog(catalog, namespace, model, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPostgresUniqueConcurrentWritersAndAtomicRollback(t *testing.T) {
	ctx := t.Context()
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, ctx, url)
	first := openPostgresMigrationIntegrationBackend(t, ctx, url, namespace)
	second := openPostgresMigrationIntegrationBackend(t, ctx, url, namespace)
	model, field := uniquetest.Model(t, "char")
	loaded := postgresUniqueHistory(t, migrations.Migration{App: "uniqueref", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "uniqueref", Model: model}}})
	if _, err := (migrations.Executor{Backend: first}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
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
	ready, start, results := make(chan struct{}, 2), make(chan struct{}), make(chan result, 2)
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
		result := <-results
		if result.err == nil {
			if result.key <= 0 {
				t.Fatal("successful concurrent insert has no key")
			}
			saved++
		} else {
			assertPostgresUniqueError(t, result.err)
			if result.key != 0 {
				t.Fatal("rejected concurrent insert has a key")
			}
			rejected++
		}
	}
	if saved != 1 || rejected != 1 {
		t.Fatalf("concurrent writers saved=%d rejected=%d", saved, rejected)
	}
	err := first.Atomic(ctx, func(session db.Session) error {
		if _, err := session.Insert(ctx, plan("before conflict")); err != nil {
			return err
		}
		_, err := session.Insert(ctx, plan("same"))
		return err
	})
	assertPostgresUniqueError(t, err)
	table, err := quoteTable(namespace, model.DBTable)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := first.database.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 1 {
		t.Fatal("failed atomic unique write left a partial mutation", err)
	}
	if _, err := first.Insert(ctx, plan("after rollback")); err != nil {
		t.Fatal("unique failure poisoned later writes", err)
	}
}

func TestPostgresUniqueAlterFailurePreservesRevisionRowsAndInboundFK(t *testing.T) {
	ctx := t.Context()
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, ctx, url)
	writer := openPostgresMigrationIntegrationBackend(t, ctx, url, namespace)
	built, err := schema.Build(schema.Definition{AppLabel: "uniqueref", Models: []schema.Model{
		{Name: "owner", GoName: "Owner", Fields: []schema.Field{schema.TextField("reference", "Reference", schema.Nullable())}},
		{Name: "child", GoName: "Child", Fields: []schema.Field{schema.ForeignKey("owner", "Owner", schema.Target("uniqueref", "owner"), schema.RelatedName("children"), schema.Protect)}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	models := make(map[string]ir.Model)
	for _, model := range built.Models {
		models[model.Name] = model
	}
	before, after := models["owner"], models["owner"].Clone()
	after.Fields[1].Unique = true
	initial := migrations.Migration{App: "uniqueref", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "uniqueref", Model: before}, migrations.CreateModel{AppLabel: "uniqueref", Model: models["child"]}}}
	add := migrations.Migration{App: "uniqueref", Name: "0002_unique", Dependencies: []migrations.MigrationKey{initial.Key()}, Operations: []migrations.Operation{migrations.AlterField{AppLabel: "uniqueref", ModelName: "owner", Before: before.Fields[1], After: after.Fields[1]}}}
	remove := migrations.Migration{App: "uniqueref", Name: "0003_remove", Dependencies: []migrations.MigrationKey{add.Key()}, Operations: []migrations.Operation{migrations.AlterField{AppLabel: "uniqueref", ModelName: "owner", Before: after.Fields[1], After: before.Fields[1]}}}
	loaded := postgresUniqueHistory(t, initial, add, remove)
	executor := migrations.Executor{Backend: writer}
	initialTarget := migrations.TargetedLifecycleRequest(migrations.NamedTarget(initial.Key()))
	uniqueTarget := migrations.TargetedLifecycleRequest(migrations.NamedTarget(add.Key()))
	if _, err := executor.Migrate(ctx, loaded, initialTarget); err != nil {
		t.Fatal(err)
	}
	table, err := quoteTable(namespace, before.DBTable)
	if err != nil {
		t.Fatal(err)
	}
	childTable, err := quoteTable(namespace, models["child"].DBTable)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.database.ExecContext(ctx, "INSERT INTO "+table+` (reference) VALUES ('same'),('same')`); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.database.ExecContext(ctx, "INSERT INTO "+childTable+` (owner_id) VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	for _, reverse := range []bool{false, true} {
		if reverse {
			if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
				t.Fatal(err)
			}
			if _, err := writer.database.ExecContext(ctx, "UPDATE "+table+` SET reference='same' WHERE id=2`); err != nil {
				t.Fatal(err)
			}
		}
		held := postgresMigrationIntegrationSession(t, ctx, writer)
		history, err := held.ReadAppliedMigrations(ctx)
		if err != nil {
			t.Fatal(err)
		}
		catalogBefore, _, err := loadPostgresMigrationTableCatalog(ctx, writer.database, namespace, before.DBTable)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := executor.Migrate(ctx, loaded, uniqueTarget); err == nil {
			t.Fatal("duplicate data acquired uniqueness")
		} else {
			var cause *pgconn.PgError
			if !errors.As(err, &cause) || cause.Code != "23505" || mb.IsCapabilityError(err) || mb.IsRevisionFenceError(err) {
				t.Fatalf("duplicate migration lost execution failure: %v", err)
			}
		}
		catalogAfter, _, err := loadPostgresMigrationTableCatalog(ctx, writer.database, namespace, before.DBTable)
		if err != nil || !reflect.DeepEqual(catalogAfter, catalogBefore) {
			t.Fatal("failed unique alteration changed physical catalog", err)
		}
		transition := mb.HistoryTransition{Kind: mb.HistoryTransitionApply, Migration: mb.AppliedMigration{App: add.App, Name: add.Name}}
		if reverse {
			transition.Kind, transition.Migration.Name = mb.HistoryTransitionUnapply, remove.Name
		}
		tx, err := held.BeginMigration(ctx, transition, mb.MigrationIntent{Operations: []mb.MigrationOperation{{Kind: mb.MigrationAlterField, Before: before, After: after}}})
		if err != nil {
			t.Fatal("failed unique alteration advanced revision", err)
		}
		if err := tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		if err := held.Close(ctx); err != nil {
			t.Fatal(err)
		}
		check := postgresMigrationIntegrationSession(t, ctx, writer)
		current, err := check.ReadAppliedMigrations(ctx)
		if err != nil || !reflect.DeepEqual(current, history) {
			t.Fatal("failed unique alteration changed recorder", err)
		}
		if err := check.Close(ctx); err != nil {
			t.Fatal(err)
		}
		var duplicates, children int
		if err := writer.database.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+` WHERE reference='same'`).Scan(&duplicates); err != nil || duplicates != 2 {
			t.Fatal("failed unique alteration changed rows", err)
		}
		if err := writer.database.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+childTable+` WHERE owner_id=1`).Scan(&children); err != nil || children != 1 {
			t.Fatal("failed unique alteration changed inbound relation", err)
		}
		if _, err := writer.database.ExecContext(ctx, "UPDATE "+table+` SET reference='fixed' WHERE id=2`); err != nil {
			t.Fatal(err)
		}
		if _, err := executor.Migrate(ctx, loaded, uniqueTarget); err != nil {
			t.Fatal("explicit repair and retry failed", err)
		}
		catalog, _, err := loadPostgresMigrationTableCatalog(ctx, writer.database, namespace, before.DBTable)
		if err != nil {
			t.Fatal(err)
		}
		if err := assertPostgresMigrationModelCatalog(catalog, namespace, after, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openPostgresMigrationIntegrationBackend(t, ctx, url, namespace)
	if _, err := (migrations.Executor{Backend: reopened}).Migrate(ctx, loaded, uniqueTarget); err != nil {
		t.Fatal("unique history did not survive reopen", err)
	}
}

func TestPostgresUniqueNullableAddAndForeignKeyReverse(t *testing.T) {
	ctx := t.Context()
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, ctx, url)
	backend := openPostgresMigrationIntegrationBackend(t, ctx, url, namespace)
	parentField, parentValue := uniquetest.Field(t, "char")
	build := func(extended bool) map[string]ir.Model {
		t.Helper()
		parent := schema.Model{Name: "entry", GoName: "Entry", Fields: []schema.Field{parentField}}
		child := schema.Model{Name: "child", GoName: "Child", Fields: []schema.Field{schema.TextField("label", "Label")}}
		if extended {
			parent.Fields = append(parent.Fields, schema.UUIDField("external", "External", schema.Nullable(), schema.Unique()))
			child.Fields = append(child.Fields, schema.ForeignKey("parent", "Parent", schema.Target("uniqueref", "entry"), schema.RelatedName("children"), schema.Protect, schema.Nullable(), schema.Unique()))
		}
		s, err := schema.Build(schema.Definition{AppLabel: "uniqueref", Models: []schema.Model{parent, child}})
		if err != nil {
			t.Fatal(err)
		}
		models := make(map[string]ir.Model)
		for _, model := range s.Models {
			models[model.Name] = model
		}
		return models
	}
	before, after := build(false), build(true)
	initial := migrations.Migration{App: "uniqueref", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "uniqueref", Model: before["entry"]}, migrations.CreateModel{AppLabel: "uniqueref", Model: before["child"]}}}
	addition := migrations.Migration{App: "uniqueref", Name: "0002_fields", Dependencies: []migrations.MigrationKey{initial.Key()}, Operations: []migrations.Operation{
		migrations.AddField{AppLabel: "uniqueref", ModelName: "entry", Field: after["entry"].Fields[2]},
		migrations.AddField{AppLabel: "uniqueref", ModelName: "child", Field: after["child"].Fields[2]},
	}}
	loaded := postgresUniqueHistory(t, initial, addition)
	executor := migrations.Executor{Backend: backend}
	initialTarget := migrations.TargetedLifecycleRequest(migrations.NamedTarget(initial.Key()))
	if _, err := executor.Migrate(ctx, loaded, initialTarget); err != nil {
		t.Fatal(err)
	}
	parentTable, err := quoteTable(namespace, before["entry"].DBTable)
	if err != nil {
		t.Fatal(err)
	}
	childTable, err := quoteTable(namespace, before["child"].DBTable)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.database.ExecContext(ctx, "INSERT INTO "+parentTable+` (value) VALUES ('first'),('second')`); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.database.ExecContext(ctx, "INSERT INTO "+childTable+` (label) VALUES ('one'),('two')`); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{"SELECT COUNT(*) FROM " + parentTable + " WHERE external IS NULL", "SELECT COUNT(*) FROM " + childTable + " WHERE parent_id IS NULL"} {
		var count int
		if err := backend.database.QueryRowContext(ctx, statement).Scan(&count); err != nil || count != 2 {
			t.Fatal("nullable unique addition filled or removed existing rows", err)
		}
	}
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	for _, change := range []struct {
		table string
		field query.FieldRef
		value query.Value
	}{
		{before["entry"].DBTable, query.NewFieldRef("external", "external", query.FieldUUID, true), uniquetest.Value(t, "uuid", []byte(`"12345678-9abc-4def-8123-456789abcdef"`))},
		{before["child"].DBTable, query.NewFieldRef("parent", "parent_id", query.FieldInteger, true), query.Integer(1)},
	} {
		update := func(key int64) (int64, error) {
			return backend.Update(ctx, query.NewUpdatePlan(change.table, []query.Assignment{query.NewAssignment(change.field, change.value)}, id, query.Integer(key)))
		}
		if count, err := update(1); err != nil || count != 1 {
			t.Fatal(err)
		}
		count, err := update(2)
		assertPostgresUniqueError(t, err)
		if count != 0 {
			t.Fatal("duplicate added field changed rows")
		}
	}
	_, err = backend.Update(ctx, query.NewUpdatePlan(before["child"].DBTable, []query.Assignment{query.NewAssignment(query.NewFieldRef("parent", "parent_id", query.FieldInteger, true), query.Integer(999))}, id, query.Integer(2)))
	if !errors.Is(err, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeRelatedObjectMissing}) {
		t.Fatal("unique FK lost referential integrity", err)
	}
	if _, err := executor.Migrate(ctx, loaded, initialTarget); err != nil {
		t.Fatal("reverse unique addition failed", err)
	}
	for _, model := range before {
		catalog, present, err := loadPostgresMigrationTableCatalog(ctx, backend.database, namespace, model.DBTable)
		if err != nil || !present {
			t.Fatal(err)
		}
		if err := assertPostgresMigrationModelCatalog(catalog, namespace, model, nil); err != nil {
			t.Fatal("reverse left a unique index or damaged a retained constraint", err)
		}
	}
	key, err := backend.Insert(ctx, query.NewInsertPlanReturningKey(before["entry"].DBTable, []query.Assignment{query.NewAssignment(parentValue, query.String("third"))}, id))
	if err != nil || key != 3 {
		t.Fatal("unique add/reverse changed sequence high-water", key, err)
	}
	zero := migrations.TargetedLifecycleRequest(migrations.ZeroTarget("uniqueref"))
	if _, err := executor.Migrate(ctx, loaded, zero); err != nil {
		t.Fatal("reverse unique model creation failed", err)
	}
	for _, model := range before {
		assertPostgresMigrationIntegrationTableMissing(t, ctx, backend, namespace, model.DBTable)
	}
	// Exercise unique FK creation directly, independently of the ADD COLUMN
	// path above, then reverse the complete model/constraint/sequence graph.
	fresh := postgresUniqueHistory(t, migrations.Migration{App: "uniqueref", Name: "0001_whole", Operations: []migrations.Operation{
		migrations.CreateModel{AppLabel: "uniqueref", Model: after["entry"]},
		migrations.CreateModel{AppLabel: "uniqueref", Model: after["child"]},
	}})
	if _, err := executor.Migrate(ctx, fresh, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal("unique FK CreateModel failed", err)
	}
	key, err = backend.Insert(ctx, query.NewInsertPlanReturningKey(after["entry"].DBTable, []query.Assignment{query.NewAssignment(parentValue, query.String("fresh"))}, id))
	if err != nil || key != 1 {
		t.Fatal("recreated unique model retained an orphan sequence", key, err)
	}
	childInsert := query.NewInsertPlanReturningKey(after["child"].DBTable, []query.Assignment{
		query.NewAssignment(query.NewFieldRef("label", "label", query.FieldString, false), query.String("child")),
		query.NewAssignment(query.NewFieldRef("parent", "parent_id", query.FieldInteger, true), query.Integer(key)),
	}, id)
	if _, err := backend.Insert(ctx, childInsert); err != nil {
		t.Fatal(err)
	}
	if key, err := backend.Insert(ctx, childInsert); key != 0 || err == nil {
		t.Fatal("created unique FK accepted a duplicate", key, err)
	} else {
		assertPostgresUniqueError(t, err)
	}
	if _, err := executor.Migrate(ctx, fresh, zero); err != nil {
		t.Fatal("reverse unique FK model graph failed", err)
	}
	for _, model := range after {
		assertPostgresMigrationIntegrationTableMissing(t, ctx, backend, namespace, model.DBTable)
	}
}

func TestPostgresUniquePhysicalDriftRejectsBeforeRevisionClaim(t *testing.T) {
	url := postgresIntegrationURL(t)
	for _, variant := range []string{"nulls_not_distinct", "deferrable", "standalone", "included", "extra_index", "index_options"} {
		t.Run(variant, func(t *testing.T) {
			ctx := t.Context()
			namespace := postgresMigrationIntegrationSchema(t, ctx, url)
			backend := openPostgresMigrationIntegrationBackend(t, ctx, url, namespace)
			model, _ := uniquetest.Model(t, "char")
			initial := migrations.Migration{App: "uniqueref", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "uniqueref", Model: model}}}
			change := migrations.Migration{App: "uniqueref", Name: "0002_note", Dependencies: []migrations.MigrationKey{initial.Key()}, Operations: []migrations.Operation{migrations.AddField{AppLabel: "uniqueref", ModelName: "entry", Field: ir.Field{Name: "note", GoName: "Note", Column: "note", Kind: ir.FieldText, Nullable: true}}}}
			loaded := postgresUniqueHistory(t, initial, change)
			executor := migrations.Executor{Backend: backend}
			if _, err := executor.Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(initial.Key()))); err != nil {
				t.Fatal(err)
			}
			table, err := quoteTable(namespace, model.DBTable)
			if err != nil {
				t.Fatal(err)
			}
			name, err := postgresUniqueConstraintName(model.DBTable, "value")
			if err != nil {
				t.Fatal(err)
			}
			constraint, err := quoteIdentifier(name)
			if err != nil {
				t.Fatal(err)
			}
			index, err := quoteTable(namespace, name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := backend.database.ExecContext(ctx, "INSERT INTO "+table+` (value) VALUES ('preserved')`); err != nil {
				t.Fatal(err)
			}
			held := postgresMigrationIntegrationSession(t, ctx, backend)
			history, err := held.ReadAppliedMigrations(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var ddl []string
			switch variant {
			case "extra_index":
				ddl = []string{"CREATE INDEX unexpected_pattern ON " + table + " (value text_pattern_ops)"}
			case "index_options":
				ddl = []string{"ALTER INDEX " + index + " SET (fillfactor=80)"}
			default:
				ddl = []string{"ALTER TABLE " + table + " DROP CONSTRAINT " + constraint}
				suffix := "UNIQUE (value)"
				switch variant {
				case "nulls_not_distinct":
					suffix = "UNIQUE NULLS NOT DISTINCT (value)"
				case "deferrable":
					suffix += " DEFERRABLE INITIALLY IMMEDIATE"
				case "included":
					suffix += " INCLUDE (id)"
				case "standalone":
					ddl = append(ddl, "CREATE UNIQUE INDEX "+constraint+" ON "+table+" (value)")
				}
				if variant != "standalone" {
					ddl = append(ddl, "ALTER TABLE "+table+" ADD CONSTRAINT "+constraint+" "+suffix)
				}
			}
			for _, statement := range ddl {
				if _, err := backend.database.ExecContext(ctx, statement); err != nil {
					t.Fatal("drift fixture failed to mutate schema", err)
				}
			}
			before, _, err := loadPostgresMigrationTableCatalog(ctx, backend.database, namespace, model.DBTable)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); !mb.IsCapabilityError(err) {
				t.Fatalf("physical uniqueness drift accepted: %v", err)
			}
			after, _, err := loadPostgresMigrationTableCatalog(ctx, backend.database, namespace, model.DBTable)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("drift rejection modified schema", err)
			}
			tx, err := held.BeginMigration(ctx, mb.HistoryTransition{Kind: mb.HistoryTransitionApply, Migration: mb.AppliedMigration{App: "uniqueref", Name: "0002_probe"}}, mb.MigrationIntent{Operations: []mb.MigrationOperation{}})
			if err != nil {
				t.Fatal("physical drift advanced revision", err)
			}
			if err := tx.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			if err := held.Close(ctx); err != nil {
				t.Fatal(err)
			}
			check := postgresMigrationIntegrationSession(t, ctx, backend)
			current, err := check.ReadAppliedMigrations(ctx)
			if err != nil || !reflect.DeepEqual(current, history) {
				t.Fatal("physical drift changed recorder", err)
			}
			if err := check.Close(ctx); err != nil {
				t.Fatal(err)
			}
			var value string
			if err := backend.database.QueryRowContext(ctx, "SELECT value FROM "+table).Scan(&value); err != nil || value != "preserved" {
				t.Fatal("physical drift changed data", err)
			}
		})
	}
}
