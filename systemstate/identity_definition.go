package systemstate

import (
	_ "embed"

	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

const identityMigrationName = "0002_identity_transition"
const identityTransitionTable = "godj_system_identity_transition"

//go:embed testdata/0002_identity_transition.godj.json
var identityTransitionDocument []byte

// IdentityMigrationSources supplies the explicit system/identity graph. It
// neither applies migrations nor transfers or provisions any credential.
// The original 0001 definition remains unchanged historical data.
func IdentityMigrationSources() []definition.Source {
	sources := []definition.Source{InitialDefinitionSource()}
	sources = append(sources, identity.MigrationSources()...)
	return append(sources, definition.Source{SourceID: "systemstate/godj_system.0002_identity_transition", Document: append([]byte(nil), identityTransitionDocument...)})
}

func identityTransitionSchema() (ir.Schema, error) {
	return schema.Build(schema.Definition{AppLabel: initialMigrationApp, Models: []schema.Model{{Name: "identity_transition", GoName: "IdentityTransition", Fields: []schema.Field{
		schema.CharField("source_kind", "SourceKind", 16),
		schema.IntegerField("source_id", "SourceID"),
		schema.CharField("principal_id", "PrincipalID", 128, schema.Unique()),
		schema.IntegerField("user_id", "UserID", schema.Unique()),
		schema.BooleanField("staff", "Staff"),
		schema.BooleanField("superuser", "Superuser"),
		schema.DateTimeField("transitioned_at", "TransitionedAt"),
		schema.CharField("source_fingerprint", "SourceFingerprint", 64),
	}}}})
}

func identityTransitionMigration() (migrations.Migration, error) {
	normalized, err := identityTransitionSchema()
	if err != nil {
		return migrations.Migration{}, err
	}
	return migrations.Migration{App: initialMigrationApp, Name: identityMigrationName, Dependencies: []migrations.MigrationKey{InitialMigrationKey(), identity.InitialMigrationKey()}, Operations: []migrations.Operation{migrations.CreateModel{AppLabel: initialMigrationApp, Model: normalized.Models[0]}}}, nil
}
