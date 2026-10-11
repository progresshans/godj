package backend

import (
	"fmt"

	"github.com/progresshans/godj/internal/migrationgraph"
)

type MigrationGraphPlan = migrationgraph.MigrationGraphPlan

// ResolveMigrationGraphPlan adapts the revision-fenced transition to the pure
// chronological graph. Physical deltas and catalog checks remain DB-owned.
func ResolveMigrationGraphPlan(transition HistoryTransition, intent MigrationIntent) (MigrationGraphPlan, error) {
	if transition.Kind != HistoryTransitionApply && transition.Kind != HistoryTransitionUnapply {
		return MigrationGraphPlan{}, fmt.Errorf("migration graph transition is invalid")
	}
	return migrationgraph.ResolveMigrationGraphPlan(transition.Migration.App, transition.Migration.Name,
		transition.Kind == HistoryTransitionUnapply, intent)
}
