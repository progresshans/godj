package postgres

import (
	"context"
	"errors"
	"github.com/progresshans/godj/internal/uniquetest"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"reflect"
	"testing"

	"github.com/progresshans/godj/migrations"
	mb "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

func TestPostgresColumnIndexStoredScalarProfiles(t *testing.T) {
	url := postgresIntegrationURL(t)
	for _, profile := range uniquetest.Profiles(t, "postgres") {
		t.Run(profile.Name, func(t *testing.T) {
			ctx := t.Context()
			namespace := postgresMigrationIntegrationSchema(t, ctx, url)
			backend := openPostgresMigrationIntegrationBackend(t, ctx, url, namespace)
			model, field := uniquetest.Model(t, profile.Name)
			model.Fields[0].DBIndex = true
			model.Fields[1].Unique, model.Fields[1].DBIndex = false, true
			initial := migrations.Migration{App: "uniqueref", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "uniqueref", Model: model}}}
			loaded := postgresUniqueHistory(t, initial)
			executor := migrations.Executor{Backend: backend}
			if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
				t.Fatal(err)
			}
			value := uniquetest.Value(t, profile.Name, profile.Attempts[0].Input)
			for _, current := range []query.Value{value, value, query.Null()} {
				if _, err := backend.Insert(ctx, query.NewInsertPlanReturningKey(model.DBTable, []query.Assignment{query.NewAssignment(field, current)}, query.NewFieldRef("id", "id", query.FieldInteger, false))); err != nil {
					t.Fatal("ordinary scalar index rejected duplicate/null storage", err)
				}
			}
			manager := orm.NewManager[uniquetest.Record](uniquetest.Descriptor{Model: model})
			if count, err := manager.Using(backend).Count(ctx); err != nil || count != 3 {
				t.Fatal("indexed scalar rows lost", count, err)
			}
			catalog, found, err := loadPostgresMigrationTableCatalog(ctx, backend.database, namespace, model.DBTable)
			if err != nil || !found {
				t.Fatal("indexed scalar catalog missing", err)
			}
			if err := assertPostgresMigrationModelCatalog(catalog, namespace, model, nil); err != nil {
				t.Fatal(err)
			}
			if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
				t.Fatal("scalar index catalog changed after writes", err)
			}
			if _, err := executor.Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.ZeroTarget(initial.App))); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPostgresColumnIndexPhysicalDriftRejectsBeforeRevisionClaim(t *testing.T) {
	url := postgresIntegrationURL(t)
	for _, variant := range []string{"missing", "unique", "included", "partial", "column", "descending", "collation", "expression", "hash", "pattern", "extra", "options", "other_owner"} {
		t.Run(variant, func(t *testing.T) {
			ctx := t.Context()
			namespace := postgresMigrationIntegrationSchema(t, ctx, url)
			database := openPostgresMigrationIntegrationBackend(t, ctx, url, namespace)
			model := postgresIndexTestModel(t)
			initial := migrations.Migration{App: "indexref", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "indexref", Model: model}}}
			change := migrations.Migration{App: initial.App, Name: "0002_note", Dependencies: []migrations.MigrationKey{initial.Key()}, Operations: []migrations.Operation{migrations.AddField{AppLabel: initial.App, ModelName: model.Name, Field: ir.Field{Name: "note", GoName: "Note", Column: "note", Kind: ir.FieldText, Nullable: true}}}}
			loaded := postgresUniqueHistory(t, initial, change)
			executor := migrations.Executor{Backend: database}
			if _, err := executor.Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(initial.Key()))); err != nil {
				t.Fatal(err)
			}
			table, err := quoteTable(namespace, model.DBTable)
			if err != nil {
				t.Fatal(err)
			}
			name, err := postgresColumnIndexName(model.DBTable, "address")
			if err != nil {
				t.Fatal(err)
			}
			quoted, _ := quoteIdentifier(name)
			index, _ := quoteTable(namespace, name)
			exec := func(sql string) {
				t.Helper()
				if _, err := database.database.ExecContext(ctx, sql); err != nil {
					t.Fatal("fixture DDL failed", err)
				}
			}
			exec("INSERT INTO " + table + ` (address) VALUES ('preserved')`)
			held := postgresMigrationIntegrationSession(t, ctx, database)
			history, err := held.ReadAppliedMigrations(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if variant != "extra" && variant != "options" {
				exec("DROP INDEX " + index)
			}
			ddl := "CREATE INDEX " + quoted + " ON " + table + " (address)"
			switch variant {
			case "missing":
				ddl = ""
			case "unique":
				ddl = "CREATE UNIQUE INDEX " + quoted + " ON " + table + " (address)"
			case "included":
				ddl += " INCLUDE (id)"
			case "partial":
				ddl += " WHERE address IS NOT NULL"
			case "column":
				ddl = "CREATE INDEX " + quoted + " ON " + table + " (id)"
			case "descending":
				ddl = "CREATE INDEX " + quoted + " ON " + table + " (address DESC)"
			case "collation":
				ddl = "CREATE INDEX " + quoted + " ON " + table + ` (address COLLATE "POSIX")`
			case "expression":
				ddl = "CREATE INDEX " + quoted + " ON " + table + " (lower(address))"
			case "hash":
				ddl = "CREATE INDEX " + quoted + " ON " + table + " USING hash (address)"
			case "pattern":
				ddl = "CREATE INDEX " + quoted + " ON " + table + " (address text_pattern_ops)"
			case "extra":
				ddl = "CREATE INDEX unexpected_index ON " + table + " (address)"
			case "options":
				ddl = "ALTER INDEX " + index + " SET (fillfactor=80)"
			case "other_owner":
				outsider, _ := quoteTable(namespace, "outsider")
				exec("CREATE TABLE " + outsider + " (address TEXT)")
				ddl = "CREATE INDEX " + quoted + " ON " + outsider + " (address)"
			}
			if ddl != "" {
				exec(ddl)
			}
			before, _, err := loadPostgresMigrationTableCatalog(ctx, database.database, namespace, model.DBTable)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); !mb.IsCapabilityError(err) {
				t.Fatal("forged ordinary index accepted", err)
			}
			after, _, err := loadPostgresMigrationTableCatalog(ctx, database.database, namespace, model.DBTable)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("rejected index drift changed catalog", err)
			}
			tx, err := held.BeginMigration(ctx, mb.HistoryTransition{Kind: mb.HistoryTransitionApply, Migration: mb.AppliedMigration{App: initial.App, Name: "0002_probe"}}, mb.MigrationIntent{Operations: []mb.MigrationOperation{}})
			if err != nil {
				t.Fatal("index drift rejection advanced held revision", err)
			}
			if err := tx.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			if err := held.Close(ctx); err != nil {
				t.Fatal(err)
			}
			check := postgresMigrationIntegrationSession(t, ctx, database)
			current, err := check.ReadAppliedMigrations(ctx)
			if err != nil || !reflect.DeepEqual(current, history) {
				t.Fatal("index drift rejection changed recorder", err)
			}
			if err := check.Close(ctx); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := database.database.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+` WHERE address='preserved'`).Scan(&count); err != nil || count != 1 {
				t.Fatal("index drift rejection changed rows", err)
			}
		})
	}
}

func TestPostgresColumnIndexAddDefaultReverseAndReopen(t *testing.T) {
	ctx := t.Context()
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, ctx, url)
	database := openPostgresMigrationIntegrationBackend(t, ctx, url, namespace)
	model := postgresIndexTestModel(t)
	field := ir.Field{Name: "alias", GoName: "Alias", Column: "alias", Kind: ir.FieldSlug, MaxLength: 50, DBIndex: true, AllowUnicode: true, Default: &ir.Scalar{Kind: ir.ScalarString, String: "Ready_Value"}}
	extended := model.Clone()
	extended.Fields = append(extended.Fields, field)
	initial := migrations.Migration{App: "indexref", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "indexref", Model: model}}}
	addition := migrations.Migration{App: initial.App, Name: "0002_alias", Dependencies: []migrations.MigrationKey{initial.Key()}, Operations: []migrations.Operation{migrations.AddField{AppLabel: initial.App, ModelName: model.Name, Field: field}}}
	loaded := postgresUniqueHistory(t, initial, addition)
	initialTarget := migrations.TargetedLifecycleRequest(migrations.NamedTarget(initial.Key()))
	migrate := func(request migrations.LifecycleRequest) {
		t.Helper()
		if _, err := (migrations.Executor{Backend: database}).Migrate(ctx, loaded, request); err != nil {
			t.Fatal(err)
		}
	}
	migrate(initialTarget)
	table, err := quoteTable(namespace, model.DBTable)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.database.ExecContext(ctx, "INSERT INTO "+table+` (address) VALUES ('Old slug!'),('Old slug!'),(NULL)`); err != nil {
		t.Fatal(err)
	}
	migrate(migrations.LatestLifecycleRequest())
	assertCatalog := func(want ir.Model) {
		t.Helper()
		catalog, present, err := loadPostgresMigrationTableCatalog(ctx, database.database, namespace, model.DBTable)
		if err != nil || !present {
			t.Fatal(err)
		}
		if err := assertPostgresMigrationModelCatalog(catalog, namespace, want, nil); err != nil {
			t.Fatal(err)
		}
	}
	assertCatalog(extended)
	count := func(sql string, want int) {
		t.Helper()
		var got int
		if err := database.database.QueryRowContext(ctx, sql).Scan(&got); err != nil || got != want {
			t.Fatal("indexed default migration changed rows", got, want, err)
		}
	}
	count("SELECT COUNT(*) FROM "+table+` WHERE alias='Ready_Value'`, 3)
	count("SELECT COUNT(*) FROM "+table+` WHERE address='Old slug!'`, 2)
	if _, err := database.database.ExecContext(ctx, "INSERT INTO "+table+` (address) VALUES ('no persistent default')`); err == nil {
		t.Fatal("temporary index backfill leaked a database default")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := (migrations.Executor{Backend: database}).Migrate(canceled, loaded, initialTarget); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled indexed removal lost cancellation", err)
	}
	assertCatalog(extended)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database = openPostgresMigrationIntegrationBackend(t, ctx, url, namespace)
	migrate(initialTarget)
	assertCatalog(model)
	count("SELECT COUNT(*) FROM "+table, 3)
	migrate(migrations.LatestLifecycleRequest())
	assertCatalog(extended)
	count("SELECT COUNT(*) FROM "+table+` WHERE alias='Ready_Value'`, 3)
	migrate(migrations.TargetedLifecycleRequest(migrations.ZeroTarget(initial.App)))
	count(`SELECT COUNT(*) FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='`+namespace+`' AND c.relname LIKE 'godj_ix_%'`, 0)
}
