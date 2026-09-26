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

func InitialMigrationKey() migrations.MigrationKey {
	return migrations.MigrationKey{App: "godj_identity", Name: "0001_initial"}
}

// MigrationSources returns detached historical definitions. Reading them never
// modifies the database or adopts an existing operator credential.
func MigrationSources() []definition.Source {
	return []definition.Source{{SourceID: "identity/0001_initial", Document: append([]byte(nil), initialDefinition...)}}
}
