package migrations

import (
	"github.com/progresshans/godj/schema/ir"
)

// storageModel reads either a declared historical model or an exact derived
// intermediary. Mutation methods continue to address logical models only.
func (builder *loadedStateBuilder) storageModel(identity loadedModelIdentity) (ir.Model, bool) {
	if model, exists := builder.model(identity); exists {
		return model.value, true
	}
	app, exists := builder.apps[identity.app]
	if !exists {
		return ir.Model{}, false
	}
	for _, name := range app.order {
		owner := app.models[name].value
		for _, field := range owner.ManyToMany {
			if field.Through != nil || field.StorageThrough(ir.ModelIdentity{AppLabel: identity.app, ModelName: name}).Model.ModelName != identity.model {
				continue
			}
			model, err := ir.AutomaticThroughModel(identity.app, owner, field)
			return model, err == nil
		}
	}
	return ir.Model{}, false
}

// A derived name may be removed and later recreated by a different declaration
// operation. Each use needs a visible creation authority; replay independently
// proves that the exact storage is still present at that chronological point.
func collectAutomaticCreators(graph *plannerGraph, definitions map[MigrationKey]Migration) map[loadedModelIdentity][]loadedModelCreator {
	result := make(map[loadedModelIdentity][]loadedModelCreator)
	for _, key := range graph.nodes {
		for index, operation := range definitions[key].Operations {
			app, name := operationSourceModel(operation)
			var fields []ir.ManyToManyField
			switch op := operation.(type) {
			case CreateModel:
				fields = op.Model.ManyToMany
			case AddManyToMany:
				fields = []ir.ManyToManyField{op.Field}
			case RenameManyToMany:
				fields = []ir.ManyToManyField{op.After}
			}
			for _, field := range fields {
				if field.Through != nil {
					continue
				}
				id := field.StorageThrough(ir.ModelIdentity{AppLabel: app, ModelName: name}).Model
				target := loadedModelIdentity{app: id.AppLabel, model: id.ModelName}
				result[target] = append(result[target], loadedModelCreator{key: key, operationIndex: index})
			}
		}
	}
	return result
}

func loadedManyRequirements(model ir.Model) loadedRelationRequirements {
	var requirements loadedRelationRequirements
	for _, field := range model.ManyToMany {
		if field.Through == nil {
			requirements |= loadedRequiresAutomaticManyToMany
		} else {
			requirements |= loadedRequiresExplicitManyToMany
		}
	}
	return requirements
}
