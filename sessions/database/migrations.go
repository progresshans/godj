package database

import (
	_ "embed"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
)

//go:embed migrations/godj_sessions_0001_initial.godj.json
var initialDefinition []byte

func InitialMigrationKey() migrations.MigrationKey {
	return migrations.MigrationKey{App: "godj_sessions", Name: "0001_initial"}
}

// MigrationSources returns detached history. Applying this schema never
// installs an account model or moves historical systemstate session rows.
func MigrationSources() []definition.Source {
	return []definition.Source{{SourceID: "sessions-database/0001_initial", Document: append([]byte(nil), initialDefinition...)}}
}
