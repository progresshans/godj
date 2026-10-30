package domain

import (
	_ "embed"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
)

//go:embed migrations/godj_account_domain_0001_initial.godj.json
var initialDefinition []byte

//go:embed migrations/godj_account_domain_0002_auto_fe206c392ab4.godj.json
var mappingDefinition []byte

func InitialMigrationKey() migrations.MigrationKey {
	return migrations.MigrationKey{App: "godj_account_domain", Name: "0001_initial"}
}

// MigrationSources returns detached immutable history. This graph has no
// dependency on godj_identity.User or the historical system operator adoption.
func MigrationSources() []definition.Source {
	return []definition.Source{
		{SourceID: "account-domain/0001_initial", Document: append([]byte(nil), initialDefinition...)},
		{SourceID: "account-domain/0002_auto_fe206c392ab4", Document: append([]byte(nil), mappingDefinition...)},
	}
}
