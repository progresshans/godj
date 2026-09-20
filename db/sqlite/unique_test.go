package sqlite

import (
	"errors"
	"testing"

	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

func TestSQLiteUniqueMigrationRefusesBeforeSchemaOrHistoryWrites(t *testing.T) {
	ctx := t.Context()
	database, err := OpenMemory(ctx, "unique-capability-boundary")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	})
	model, _, _ := sqliteRelationTestModels()
	model.Fields[1].Unique = true
	wire, err := definition.Encode(definition.Producer{Name: "unique-test", Version: "1"}, migrations.Migration{App: "news", Name: "0001_unique", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "news", Model: model}}})
	if err != nil {
		t.Fatal(err)
	}
	loaded, _, err := definition.Load(definition.Source{SourceID: "unique", Document: wire})
	if err != nil {
		t.Fatal(err)
	}
	_, err = (migrations.Executor{Backend: database}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
	var capability *migrationbackend.CapabilityError
	if !errors.As(err, &capability) {
		t.Fatalf("expected unsupported unique migration, got %v", err)
	}
	var objects int
	if err := database.database.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%'`).Scan(&objects); err != nil || objects != 0 {
		t.Fatalf("unsupported migration wrote schema/history: %d, %v", objects, err)
	}
}

func TestSQLiteUniqueMetadataCannotEscapeTheUnsupportedBoundary(t *testing.T) {
	if (&Backend{}).MigrationCapabilities().UniqueConstraints {
		t.Fatal("unimplemented uniqueness advertised")
	}
	plain, _, _ := sqliteRelationTestModels()
	unique := plain.Clone()
	unique.Fields[1].Unique = true
	added := plain.Clone()
	field := ir.Field{Name: "reference", GoName: "Reference", Column: "reference", Kind: ir.FieldUUID, Nullable: true, Unique: true}
	added.Fields = append(added.Fields, field)
	for _, operation := range []migrationbackend.MigrationOperation{
		{Kind: migrationbackend.MigrationCreateModel, After: unique},
		{Kind: migrationbackend.MigrationAddField, Before: plain, After: added},
		{Kind: migrationbackend.MigrationAlterField, Before: plain, After: unique},
		{Kind: migrationbackend.MigrationAlterField, Before: unique, After: plain},
	} {
		request := migrationbackend.ForwardMigrationSQLRequest{App: "news", Name: "0002_unique", Intent: migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{operation}}}
		statements, err := NewMigrationSQLRenderer().RenderForwardMigrationSQL(t.Context(), request)
		if !migrationbackend.IsCapabilityError(err) || statements != nil {
			t.Fatalf("unique produced incomplete SQL: %v %v", statements, err)
		}
	}
	for _, compile := range []func() (string, error){
		func() (string, error) { return compileMigrationCreateModel(unique) },
		func() (string, error) { return compileMigrationAddField(plain, field) },
		func() (string, error) { return compileSQLiteRelationCreateModel(unique, nil) },
	} {
		statement, err := compile()
		if !migrationbackend.IsCapabilityError(err) || statement != "" {
			t.Fatalf("direct compiler discarded unique: %q %v", statement, err)
		}
	}
	target, source, relation := sqliteRelationTestModels()
	source.Fields[len(source.Fields)-1].Unique, relation.Unique = true, true
	intent := sqliteRelationApplyIntent(target, source, relation)
	if _, err := NewMigrationSQLRenderer().RenderForwardMigrationSQL(t.Context(), migrationbackend.ForwardMigrationSQLRequest{App: "news", Name: "0001_unique_fk", Intent: intent}); !migrationbackend.IsCapabilityError(err) {
		t.Fatalf("unique FK silently weakened: %v", err)
	}
}

func TestSQLiteIntentSealIncludesUnique(t *testing.T) {
	model, _, _ := sqliteRelationTestModels()
	intent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{Kind: migrationbackend.MigrationCreateModel, After: model}}}
	seal, err := validateAndSealSQLiteRelationIntent(migrationbackend.HistoryTransition{Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_initial"}, Kind: migrationbackend.HistoryTransitionApply}, intent)
	if err != nil {
		t.Fatal(err)
	}
	seal.intent.Operations[0].After.Fields[1].Unique = true
	if err := verifySQLiteRelationIntentSeal(&seal); err == nil {
		t.Fatal("unique mutation did not invalidate the intent seal")
	}
}
