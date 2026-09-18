package helpdesk

import (
	_ "embed"

	"github.com/progresshans/godj/migrations/definition"
)

//go:embed migrations/0001_initial.godj.json
var initialMigration []byte

//go:embed migrations/helpdesk_0002_ticket_priority.godj.json
var priorityMigration []byte

//go:embed migrations/helpdesk_0003_ticket_resolution.godj.json
var resolutionMigration []byte

// MigrationSources returns detached historical definitions in declaration
// order. Host setup loads them alongside its system-state migration. Existing
// definitions are preserved when the current model grows.
func MigrationSources() []definition.Source {
	return []definition.Source{
		{SourceID: "helpdesk/0001_initial", Document: append([]byte(nil), initialMigration...)},
		{SourceID: "helpdesk/0002_ticket_priority", Document: append([]byte(nil), priorityMigration...)},
		{SourceID: "helpdesk/0003_ticket_resolution", Document: append([]byte(nil), resolutionMigration...)},
	}
}
