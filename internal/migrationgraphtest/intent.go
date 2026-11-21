package migrationgraphtest

import (
	"testing"

	"github.com/progresshans/godj/migrations"
	backend "github.com/progresshans/godj/migrations/backend"
)

// TransitiveIntent creates C with exact read-only B -> A -> A authority.
// The target tables are historical inputs, not additional Create operations.
func TransitiveIntent(t *testing.T) (backend.HistoryTransition, backend.MigrationIntent) {
	t.Helper()
	loaded, _ := Definitions(t)
	ops := loaded.Definitions()[0].Operations
	a, b, c := ops[0].(migrations.CreateModel).Model, ops[1].(migrations.CreateModel).Model, ops[2].(migrations.CreateModel).Model
	return backend.HistoryTransition{Migration: backend.AppliedMigration{App: "graph", Name: "0001_c"}, Kind: backend.HistoryTransitionApply},
		backend.MigrationIntent{Operations: []backend.MigrationOperation{{
			OperationIndex: 0, Kind: backend.MigrationCreateModel, After: c,
			Targets:       []backend.MigrationTarget{{SourceField: c.Fields[2], TargetModel: b, TargetKey: b.Fields[0]}},
			RelatedModels: []backend.MigrationModel{{AppLabel: "graph", Model: a}},
		}}}
}
