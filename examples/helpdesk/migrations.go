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

//go:embed migrations/helpdesk_0010_ticket_elapsed.godj.json
var elapsedMigration []byte

//go:embed migrations/helpdesk_0009_ticket_service_at.godj.json
var serviceAtMigration []byte

//go:embed migrations/helpdesk_0011_ticket_effort.godj.json
var effortMigration []byte

//go:embed migrations/helpdesk_0012_ticket_expected_cost.godj.json
var expectedCostMigration []byte

//go:embed migrations/helpdesk_0013_alter_ticket_expected_cost.godj.json
var expectedCostPrecisionMigration []byte

//go:embed migrations/helpdesk_0014_ticket_external_reference.godj.json
var externalReferenceMigration []byte

//go:embed migrations/helpdesk_0015_ticket_external_payload.godj.json
var externalPayloadMigration []byte

//go:embed migrations/helpdesk_0016_alter_ticket_external_reference.godj.json
var externalReferenceUniqueMigration []byte

//go:embed migrations/helpdesk_0017_service_report.godj.json
var serviceReportMigration []byte

//go:embed migrations/helpdesk_0018_label.godj.json
var labelMigration []byte

//go:embed migrations/helpdesk_0019_ticket_label.godj.json
var ticketLabelMigration []byte

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
		{SourceID: "helpdesk/0010_ticket_elapsed", Document: append([]byte(nil), elapsedMigration...)},
		{SourceID: "helpdesk/0011_ticket_effort", Document: append([]byte(nil), effortMigration...)},
		{SourceID: "helpdesk/0012_ticket_expected_cost", Document: append([]byte(nil), expectedCostMigration...)},
		{SourceID: "helpdesk/0013_alter_ticket_expected_cost", Document: append([]byte(nil), expectedCostPrecisionMigration...)},
		{SourceID: "helpdesk/0014_ticket_external_reference", Document: append([]byte(nil), externalReferenceMigration...)},
		{SourceID: "helpdesk/0015_ticket_external_payload", Document: append([]byte(nil), externalPayloadMigration...)},
		{SourceID: "helpdesk/0016_alter_ticket_external_reference", Document: append([]byte(nil), externalReferenceUniqueMigration...)},
		{SourceID: "helpdesk/0017_service_report", Document: append([]byte(nil), serviceReportMigration...)},
		{SourceID: "helpdesk/0018_label", Document: append([]byte(nil), labelMigration...)},
		{SourceID: "helpdesk/0019_ticket_label", Document: append([]byte(nil), ticketLabelMigration...)},
	}
}
