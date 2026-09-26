// Package identity supplies the model-backed user, group and permission app.
// Migration and credential maintenance are explicit host operations.
package identity

import (
	_ "embed"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
)

//go:embed migrations/0001_initial.godj.json
var initialDefinition []byte

//go:embed migrations/godj_identity_0002_permission_revision.godj.json
var permissionRevisionDefinition []byte

func InitialMigrationKey() migrations.MigrationKey {
	return migrations.MigrationKey{App: "godj_identity", Name: "0001_initial"}
}

func CurrentMigrationKey() migrations.MigrationKey {
	return migrations.MigrationKey{App: "godj_identity", Name: "0002_permission_revision"}
}

// MigrationSources returns detached historical definitions. Reading them never
// modifies the database or adopts an existing operator credential.
func MigrationSources() []definition.Source {
	return []definition.Source{
		{SourceID: "identity/0001_initial", Document: append([]byte(nil), initialDefinition...)},
		{SourceID: "identity/0002_permission_revision", Document: append([]byte(nil), permissionRevisionDefinition...)},
	}
}
