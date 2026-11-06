package audit

import (
	_ "embed"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
)

//go:embed migrations/godj_account_audit_0001_initial.godj.json
var initialDefinition []byte

func InitialMigrationKey() migrations.MigrationKey {
	return migrations.MigrationKey{App: "godj_account_audit", Name: "0001_initial"}
}

// MigrationSources returns detached history, without selecting or installing a
// default account or changing the historical systemstate audit table.
func MigrationSources() []definition.Source {
	return []definition.Source{{SourceID: "account-audit/0001_initial", Document: append([]byte(nil), initialDefinition...)}}
}
