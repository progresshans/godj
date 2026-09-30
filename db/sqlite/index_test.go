package sqlite

import (
	"strings"
	"testing"

	mb "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func sqliteIndexTestModel(t *testing.T) ir.Model {
	t.Helper()
	s, err := schema.Build(schema.Definition{AppLabel: "indexref", Models: []schema.Model{{Name: "entry", GoName: "Entry", Fields: []schema.Field{schema.SlugField("address", "Address", schema.Nullable())}}}})
	if err != nil {
		t.Fatal(err)
	}
	return s.Models[0]
}

func TestSQLiteColumnIndexSQLAndNamesOwnExactTransitions(t *testing.T) {
	indexed := sqliteIndexTestModel(t)
	plain, unique, unicode := indexed.Clone(), indexed.Clone(), indexed.Clone()
	plain.Fields[1].Kind, plain.Fields[1].DBIndex = ir.FieldChar, false
	unique.Fields[1].Unique, unicode.Fields[1].AllowUnicode = true, true
	for _, test := range []struct {
		before, after ir.Model
		prefixes      []string
	}{
		{plain, indexed, []string{`CREATE INDEX "main".`}},
		{indexed, plain, []string{`DROP INDEX "main".`}},
		{indexed, unique, []string{`DROP INDEX "main".`, `CREATE UNIQUE INDEX "main".`}},
		{unique, indexed, []string{`DROP INDEX "main".`, `CREATE INDEX "main".`}},
		{indexed, unicode, nil},
	} {
		groups, err := NewMigrationSQLRenderer().RenderForwardMigrationSQL(t.Context(), mb.ForwardMigrationSQLRequest{App: "indexref", Name: "0002_change", Intent: mb.MigrationIntent{Operations: []mb.MigrationOperation{{Kind: mb.MigrationAlterField, Before: test.before, After: test.after}}}})
		if err != nil || len(groups) != 1 || len(groups[0]) != len(test.prefixes) {
			t.Fatal("index transition lost physical operation group", groups, err)
		}
		for i, prefix := range test.prefixes {
			if !strings.HasPrefix(groups[0][i], prefix) {
				t.Fatal("wrong index transition order", groups)
			}
		}
	}
	for _, model := range []ir.Model{indexed, unique} {
		statements, err := compileSQLiteCreateModelStatements(model, nil)
		if err != nil || len(statements) != 2 {
			t.Fatal("index omitted or redundantly added beside uniqueness", statements, err)
		}
	}
	names := map[string]bool{}
	for _, pair := range [][2]string{{"a_b", "c"}, {"a", "b_c"}, {"a", "c"}, {"b", "c"}} {
		name, err := sqliteColumnIndexName(pair[0], pair[1])
		uq, uqErr := sqliteUniqueIndexName(pair[0], pair[1])
		again, againErr := sqliteColumnIndexName(pair[0], pair[1])
		if err != nil || uqErr != nil || againErr != nil || name != again || len(name) != 56 || name == uq || names[name] {
			t.Fatal("index identities collide", name, err)
		}
		names[name] = true
	}
	collision := plain.Clone()
	collision.Name, collision.GoName = "collision", "Collision"
	var err error
	collision.DBTable, err = sqliteColumnIndexName(indexed.DBTable, indexed.Fields[1].Column)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := NewMigrationSQLRenderer().RenderForwardMigrationSQL(t.Context(), mb.ForwardMigrationSQLRequest{App: "indexref", Name: "0001_collision", Intent: mb.MigrationIntent{Operations: []mb.MigrationOperation{{Kind: mb.MigrationCreateModel, After: indexed}, {OperationIndex: 1, Kind: mb.MigrationCreateModel, After: collision}}}}); err == nil || result != nil {
		t.Fatal("declared table captured an index name")
	}
}

func TestSQLiteColumnIndexPolicyIsSealedAndLegacyEditorFailsClosed(t *testing.T) {
	model := sqliteIndexTestModel(t)
	for _, mutate := range []func(*ir.Field){func(f *ir.Field) { f.DBIndex = false }, func(f *ir.Field) { f.AllowUnicode = true }} {
		seal, err := validateAndSealSQLiteRelationIntent(mb.HistoryTransition{Migration: mb.AppliedMigration{App: "indexref", Name: "0001_initial"}, Kind: mb.HistoryTransitionApply}, mb.MigrationIntent{Operations: []mb.MigrationOperation{{Kind: mb.MigrationCreateModel, After: model}}})
		if err != nil {
			t.Fatal(err)
		}
		mutate(&seal.intent.Operations[0].After.Fields[1])
		if err := verifySQLiteRelationIntentSeal(&seal); err == nil {
			t.Fatal("index/slug policy changed after sealing")
		}
	}
	database := openMigrationTestBackend(t)
	if !database.MigrationCapabilities().ColumnIndexes {
		t.Fatal("index capability missing")
	}
	tx, err := database.BeginMigration(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range []func() error{
		func() error { return tx.CreateModel(t.Context(), model) },
		func() error { return tx.AddField(t.Context(), model, model.Fields[1]) },
		func() error { return tx.RemoveField(t.Context(), model, model.Fields[1]) },
		func() error { return tx.DeleteModel(t.Context(), model) },
	} {
		if err := call(); !mb.IsCapabilityError(err) {
			t.Fatal("legacy editor bypassed index ownership", err)
		}
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n := sqliteUniqueCount(t, database, `SELECT COUNT(*) FROM main.sqlite_schema`); n != 0 {
		t.Fatal("legacy index operation mutated schema")
	}
}
