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

//go:embed migrations/helpdesk_0004_ticket_due_at.godj.json
var dueAtMigration []byte

//go:embed migrations/helpdesk_0005_alter_ticket_priority.godj.json
var priorityChoicesMigration []byte

//go:embed migrations/helpdesk_0006_alter_ticket_priority.godj.json
var priorityLabelsMigration []byte

//go:embed migrations/helpdesk_0007_ticket_reviewed.godj.json
var reviewedMigration []byte

//go:embed migrations/helpdesk_0008_ticket_service_on.godj.json
var serviceOnMigration []byte

//go:embed migrations/helpdesk_0009_ticket_service_at.godj.json
var serviceAtMigration []byte

// MigrationSources returns detached historical definitions in declaration
// order. Host setup loads them alongside its system-state migration. Existing
// definitions are preserved when the current model grows.
func MigrationSources() []definition.Source {
	return []definition.Source{
		{SourceID: "helpdesk/0001_initial", Document: append([]byte(nil), initialMigration...)},
		{SourceID: "helpdesk/0002_ticket_priority", Document: append([]byte(nil), priorityMigration...)},
		{SourceID: "helpdesk/0003_ticket_resolution", Document: append([]byte(nil), resolutionMigration...)},
		{SourceID: "helpdesk/0004_ticket_due_at", Document: append([]byte(nil), dueAtMigration...)},
		{SourceID: "helpdesk/0005_alter_ticket_priority", Document: append([]byte(nil), priorityChoicesMigration...)},
		{SourceID: "helpdesk/0006_alter_ticket_priority", Document: append([]byte(nil), priorityLabelsMigration...)},
		{SourceID: "helpdesk/0007_ticket_reviewed", Document: append([]byte(nil), reviewedMigration...)},
		{SourceID: "helpdesk/0008_ticket_service_on", Document: append([]byte(nil), serviceOnMigration...)},
		{SourceID: "helpdesk/0009_ticket_service_at", Document: append([]byte(nil), serviceAtMigration...)},
	}
}
