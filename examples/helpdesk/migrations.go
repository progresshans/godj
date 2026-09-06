package helpdesk

import (
	_ "embed"

	"github.com/progresshans/godj/migrations/definition"
)

//go:embed migrations/0001_initial.godj.json
var initialMigration []byte

// InitialMigrationSource returns the fixed historical Category/Ticket schema.
// Host setup explicitly loads/applies it alongside the system-state migration.
// Later model changes must add migrations rather than rewrite this source.
func InitialMigrationSource() definition.Source {
	return definition.Source{SourceID: "helpdesk/0001_initial", Document: append([]byte(nil), initialMigration...)}
}
