package sqlite

import (
	"context"
	"errors"
	"github.com/progresshans/godj/internal/uniquetest"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSQLiteColumnIndexStoredScalarProfiles(t *testing.T) {
	for _, profile := range uniquetest.Profiles(t, "sqlite") {
		t.Run(profile.Name, func(t *testing.T) {
			ctx := t.Context()
			backend := openMigrationTestBackend(t)
			model, field := uniquetest.Model(t, profile.Name)
			model.Fields[0].DBIndex = true // A primary key owns its own index.
			model.Fields[1].Unique, model.Fields[1].DBIndex = false, true
			initial := migrations.Migration{App: "uniqueref", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "uniqueref", Model: model}}}
			loaded := sqliteUniqueHistory(t, initial)
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
			if err := assertSQLiteIndexes(ctx, backend.database, model, model.Fields); err != nil {
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

func TestSQLiteColumnIndexCatalogDriftFailsBeforeRevisionClaim(t *testing.T) {
	for _, mode := range []string{"missing", "unique", "column", "descending", "collation", "compound", "expression", "partial", "extra", "spelling", "other owner"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			backend := openMigrationTestBackend(t)
			model := sqliteIndexTestModel(t)
			initial := migrations.Migration{App: "indexref", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "indexref", Model: model}}}
			addition := migrations.Migration{App: "indexref", Name: "0002_note", Dependencies: []migrations.MigrationKey{initial.Key()}, Operations: []migrations.Operation{migrations.AddField{AppLabel: "indexref", ModelName: model.Name, Field: ir.Field{Name: "note", GoName: "Note", Column: "note", Kind: ir.FieldText, Nullable: true}}}}
			loaded := sqliteUniqueHistory(t, initial, addition)
			executor := migrations.Executor{Backend: backend}
			if _, err := executor.Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(initial.Key()))); err != nil {
				t.Fatal(err)
			}
			sqliteUniqueExec(t, backend, `INSERT INTO indexref_entry (address) VALUES ('one'),('two')`)
			name, err := sqliteColumnIndexName(model.DBTable, model.Fields[1].Column)
			if err != nil {
				t.Fatal(err)
			}
			quoted, _ := quoteIdentifier(name)
			if mode != "extra" {
				sqliteUniqueExec(t, backend, "DROP INDEX "+quoted)
			}
			declaration := "CREATE INDEX " + quoted + " ON indexref_entry (address)"
			switch mode {
			case "missing":
				declaration = ""
			case "unique":
				declaration = "CREATE UNIQUE INDEX " + quoted + " ON indexref_entry (address)"
			case "column":
				declaration = "CREATE INDEX " + quoted + " ON indexref_entry (id)"
			case "descending":
				declaration = "CREATE INDEX " + quoted + " ON indexref_entry (address DESC)"
			case "collation":
				declaration = "CREATE INDEX " + quoted + " ON indexref_entry (address COLLATE NOCASE)"
			case "compound":
				declaration = "CREATE INDEX " + quoted + " ON indexref_entry (address,id)"
			case "expression":
				declaration = "CREATE INDEX " + quoted + " ON indexref_entry (lower(address))"
			case "partial":
				declaration += " WHERE address IS NOT NULL"
			case "extra":
				declaration = "CREATE INDEX extra_index ON indexref_entry (address)"
			case "spelling":
				declaration = "CREATE INDEX " + strings.ToUpper(quoted) + " ON indexref_entry (address)"
			case "other owner":
				sqliteUniqueExec(t, backend, `CREATE TABLE outsider (address TEXT)`)
				declaration = "CREATE INDEX " + quoted + " ON outsider (address)"
			}
			if declaration != "" {
				sqliteUniqueExec(t, backend, declaration)
			}
			before := sqliteUniqueReadSnapshot(t, backend, model)
			if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); !errors.Is(err, errSQLiteRelationPhysicalDrift) {
				t.Fatal("physical ordinary index drift accepted or lost preclaim classification", err)
			}
			if after := sqliteUniqueReadSnapshot(t, backend, model); !reflect.DeepEqual(before, after) {
				t.Fatal("preclaim rejection changed stored state")
			}
		})
	}
}

func TestSQLiteColumnIndexFutureIndexNamespaceRejectsMainAndTempObjects(t *testing.T) {
	for _, namespace := range []string{"main", "temp"} {
		t.Run(namespace, func(t *testing.T) {
			ctx := t.Context()
			backend := openMigrationTestBackend(t)
			model := sqliteIndexTestModel(t)
			name, err := sqliteColumnIndexName(model.DBTable, model.Fields[1].Column)
			if err != nil {
				t.Fatal(err)
			}
			quoted, _ := quoteIdentifier(name)
			sqliteUniqueExec(t, backend, "CREATE TABLE "+namespace+"."+quoted+" (id INTEGER)")
			initial := migrations.Migration{App: "indexref", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "indexref", Model: model}}}
			before := sqliteUniqueReadSnapshot(t, backend)
			_, err = (migrations.Executor{Backend: backend}).Migrate(ctx, sqliteUniqueHistory(t, initial), migrations.LatestLifecycleRequest())
			if !errors.Is(err, errSQLiteRelationPhysicalDrift) {
				t.Fatal("future index name conflict was not rejected before claim", err)
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

func TestSQLiteColumnIndexAddDefaultReverseAndReopen(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "indexed-default.sqlite")
	database := openMigrationHistoryFileBackend(t, path)
	model := sqliteIndexTestModel(t)
	field := ir.Field{Name: "alias", GoName: "Alias", Column: "alias", Kind: ir.FieldSlug, MaxLength: 50, DBIndex: true, AllowUnicode: true, Default: &ir.Scalar{Kind: ir.ScalarString, String: "Ready_Value"}}
	extended := model.Clone()
	extended.Fields = append(extended.Fields, field)
	initial := migrations.Migration{App: "indexref", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "indexref", Model: model}}}
	addition := migrations.Migration{App: initial.App, Name: "0002_alias", Dependencies: []migrations.MigrationKey{initial.Key()}, Operations: []migrations.Operation{migrations.AddField{AppLabel: initial.App, ModelName: model.Name, Field: field}}}
	loaded := sqliteUniqueHistory(t, initial, addition)
	initialTarget := migrations.TargetedLifecycleRequest(migrations.NamedTarget(initial.Key()))
	migrate := func(request migrations.LifecycleRequest) {
		t.Helper()
		if _, err := (migrations.Executor{Backend: database}).Migrate(ctx, loaded, request); err != nil {
			t.Fatal(err)
		}
	}
	migrate(initialTarget)
	table, err := quoteIdentifier(model.DBTable)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.database.ExecContext(ctx, "INSERT INTO "+table+` (address) VALUES ('Old slug!'),('Old slug!'),(NULL)`); err != nil {
		t.Fatal(err)
	}
	migrate(migrations.LatestLifecycleRequest())
	assertCatalog := func(want ir.Model) {
		t.Helper()
		if err := assertSQLiteIndexes(ctx, database.database, want, want.Fields); err != nil {
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
	database = openMigrationHistoryFileBackend(t, path)
	migrate(initialTarget)
	assertCatalog(model)
	count("SELECT COUNT(*) FROM "+table, 3)
	migrate(migrations.LatestLifecycleRequest())
	assertCatalog(extended)
	count("SELECT COUNT(*) FROM "+table+` WHERE alias='Ready_Value'`, 3)
	migrate(migrations.TargetedLifecycleRequest(migrations.ZeroTarget(initial.App)))
	count(`SELECT COUNT(*) FROM main.sqlite_schema WHERE name LIKE 'godj_ix_%'`, 0)
}
