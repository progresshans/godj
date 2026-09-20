package postgres

import (
	"testing"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

func TestPostgresUniqueMetadataCannotEscapeTheUnsupportedBoundary(t *testing.T) {
	if (&Backend{}).MigrationCapabilities().UniqueConstraints {
		t.Fatal("unimplemented uniqueness advertised")
	}
	plain := postgresMigrationTestPostModel(false)
	unique := plain.Clone()
	unique.Fields[1].Unique = true
	field := ir.Field{Name: "reference", GoName: "Reference", Column: "reference", Kind: ir.FieldUUID, Nullable: true, Unique: true}
	added := plain.Clone()
	added.Fields = append(added.Fields, field)
	for _, operation := range []migrationbackend.MigrationOperation{
		{Kind: migrationbackend.MigrationCreateModel, After: unique},
		{Kind: migrationbackend.MigrationAddField, Before: plain, After: added},
		{Kind: migrationbackend.MigrationAlterField, Before: plain, After: unique},
		{Kind: migrationbackend.MigrationAlterField, Before: unique, After: plain},
	} {
		request := migrationbackend.ForwardMigrationSQLRequest{App: "blog", Name: "0002_unique", Intent: migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{operation}}}
		statements, err := NewMigrationSQLRenderer(MigrationSQLConfig{Schema: "public"}).RenderForwardMigrationSQL(t.Context(), request)
		if !migrationbackend.IsCapabilityError(err) || statements != nil {
			t.Fatalf("unique produced incomplete SQL: %v %v", statements, err)
		}
	}
	for _, compile := range []func() (string, error){
		func() (string, error) { return compilePostgresMigrationCreateModel("public", unique, nil) },
		func() (string, error) { return compilePostgresMigrationAddField("public", plain, field, nil) },
	} {
		statement, err := compile()
		if !migrationbackend.IsCapabilityError(err) || statement != "" {
			t.Fatalf("direct compiler discarded unique: %q %v", statement, err)
		}
	}
}

func TestPostgresIntentSealIncludesUnique(t *testing.T) {
	model := postgresMigrationTestPostModel(false)
	intent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{Kind: migrationbackend.MigrationCreateModel, After: model}}}
	schema, err := newPostgresMigrationSchema(postgresMigrationTestTransition(), intent)
	if err != nil {
		t.Fatal(err)
	}
	schema.intent.Operations[0].After.Fields[1].Unique = true
	assertPostgresMigrationIntegrity(t, schema.verifySeal())
}
