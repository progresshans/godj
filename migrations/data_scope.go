package migrations

import (
	"context"
	"fmt"
	"slices"

	"github.com/progresshans/godj/internal/migrationgraph"
)

// Data callbacks are replayable from their own dependency closure. An
// unrelated applied branch must never supply a newer field or model to one.
func containsRunData(migration Migration) bool {
	return slices.ContainsFunc(migration.Operations, func(op Operation) bool { _, ok := operationValue(op).(RunData); return ok })
}

func (r loadedStateReconstructor) dataScopeBuilder(ctx context.Context, migration Migration, direction Direction) (*loadedStateBuilder, error) {
	if !containsRunData(migration) {
		return nil, nil
	}
	state := newLoadedStateBuilder()
	if len(migration.Dependencies) != 0 {
		steps, err := r.targetProjection(migration.Dependencies)
		if err != nil {
			return nil, err
		}
		for _, step := range steps {
			if err := r.applyLoadedMigrationContext(ctx, state, r.definitions[step.Key], DirectionForward); err != nil {
				return nil, err
			}
		}
	}
	if direction == DirectionBackward {
		if err := r.applyLoadedMigrationContext(ctx, state, migration, DirectionForward); err != nil {
			return nil, err
		}
	}
	return state, nil
}

func dataScopeModels(ctx context.Context, historical, applied *loadedStateBuilder, op RunData) ([]migrationgraph.MigrationModel, error) {
	roots := make([]migrationgraph.MigrationModel, 0, len(op.Models))
	for _, id := range op.Models {
		model, found := historical.storageModel(loadedModelIdentity{app: id.AppLabel, model: id.ModelName})
		if !found {
			return nil, fmt.Errorf("historical model %s.%s is missing at this operation in its dependency closure", id.AppLabel, id.ModelName)
		}
		roots = append(roots, migrationgraph.MigrationModel{AppLabel: id.AppLabel, Model: model})
	}
	closure, err := historicalModelClosure(ctx, historical, roots)
	if err != nil {
		return nil, err
	}
	result := make([]migrationgraph.MigrationModel, 0, len(closure))
	for id, model := range closure {
		result = append(result, migrationgraph.MigrationModel{AppLabel: id.AppLabel, Model: model})
	}
	slices.SortFunc(result, func(a, b migrationgraph.MigrationModel) int { return compareDataModel(a.Identity(), b.Identity()) })
	for _, member := range result {
		actual, found := applied.storageModel(loadedModelIdentity{app: member.AppLabel, model: member.Model.Name})
		if !found || !actual.Equal(member.Model) {
			return nil, fmt.Errorf("applied model %s.%s differs from the data operation's declared history", member.AppLabel, member.Model.Name)
		}
	}
	return result, nil
}
