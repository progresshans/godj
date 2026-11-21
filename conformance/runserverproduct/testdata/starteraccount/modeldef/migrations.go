package modeldef

import (
	"github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/migrations/definition"
	sessiondb "github.com/progresshans/godj/sessions/database"
)

func MigrationSources() []definition.Source {
	sources := append(domain.MigrationSources(), audit.MigrationSources()...)
	return append(sources, sessiondb.MigrationSources()...)
}
