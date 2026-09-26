package migrationgraph

import (
	"fmt"
	"slices"

	"github.com/progresshans/godj/internal/irresource"
	"github.com/progresshans/godj/schema/ir"
)

// ModelChange describes owned auxiliary storage within the same logical
// operation and transaction. A rename has both sides; it is never represented
// as a drop/create pair, so implementations must preserve retained row IDs.
type ModelChange struct{ Before, After ir.Model }

// AutomaticStorageChanges derives all auxiliary changes from exact logical
// model snapshots. No separately supplied physical definition is authoritative.
func (operation MigrationOperation) AutomaticStorageChanges(app string) ([]ModelChange, error) {
	budget := irresource.New(irresource.Limits{Fields: migrationGraphMaxFields, StringBytes: migrationGraphMaxStringBytes, Nodes: migrationGraphMaxNodes, Bytes: migrationGraphMaxBytes})
	for _, model := range []ir.Model{operation.Before, operation.After} {
		if err := budget.ScanModel("owner", model); err != nil {
			return nil, err
		}
	}
	derive := func(owner ir.Model, field ir.ManyToManyField) (ir.Model, error) {
		if field.Name == "" || field.Through != nil {
			return ir.Model{}, nil
		}
		// Admit concatenated identifiers before allocating. Charge each complete
		// result before deriving the next, so fan-out cannot bypass aggregate bounds.
		if len(owner.Name)+len(field.Name)+1 > migrationGraphMaxStringBytes || len(owner.GoName)+len(field.GoName)+4 > migrationGraphMaxStringBytes || len(owner.DBTable)+len(field.Name)+1 > migrationGraphMaxStringBytes {
			return ir.Model{}, fmt.Errorf("automatic storage identifier exceeds its resource limit")
		}
		model, err := ir.AutomaticThroughModel(app, owner, field)
		if err != nil {
			return ir.Model{}, err
		}
		if err := budget.ScanModel("automatic", model); err != nil {
			return ir.Model{}, err
		}
		return model, nil
	}
	switch operation.Kind {
	case MigrationAlterManyToMany:
		before, after, err := operation.ChangedManyToMany()
		if err != nil {
			return nil, err
		}
		previous, err := derive(operation.Before, before)
		if err != nil {
			return nil, err
		}
		next, err := derive(operation.After, after)
		if err != nil {
			return nil, err
		}
		if previous.Name == "" && next.Name == "" {
			return nil, nil
		}
		return []ModelChange{{Before: previous, After: next}}, nil
	case MigrationCreateModel, MigrationDeleteModel:
		owner := operation.After
		if operation.Kind == MigrationDeleteModel {
			owner = operation.Before
		}
		var changes []ModelChange
		for _, field := range owner.ManyToMany {
			if field.Through != nil {
				continue
			}
			model, err := derive(owner, field)
			if err != nil {
				return nil, err
			}
			change := ModelChange{After: model}
			if operation.Kind == MigrationDeleteModel {
				change = ModelChange{Before: model}
			}
			changes = append(changes, change)
		}
		if operation.Kind == MigrationDeleteModel {
			slices.Reverse(changes)
		}
		return changes, nil
	default:
		if !slices.EqualFunc(operation.Before.ManyToMany, operation.After.ManyToMany, ir.ManyToManyField.Equal) {
			return nil, fmt.Errorf("stored field/constraint operation changes owned relation storage")
		}
		return nil, nil
	}
}

// StorageChanges orders concrete model creation/deletion with its owned link
// tables. Scalar operations are not model changes and return only auxiliaries.
func (operation MigrationOperation) StorageChanges(app string) ([]ModelChange, error) {
	changes, err := operation.AutomaticStorageChanges(app)
	if err != nil {
		return nil, err
	}
	if operation.Kind != MigrationCreateModel && operation.Kind != MigrationDeleteModel {
		return changes, nil
	}
	owner := operation.After
	if operation.Kind == MigrationDeleteModel {
		owner = operation.Before
	}
	models := []ir.Model{owner}
	for _, change := range changes {
		model := change.After
		if operation.Kind == MigrationDeleteModel {
			model = change.Before
		}
		models = append(models, model)
	}
	// An automatic intermediary can itself be a declared relation target.
	// Preserve declaration order among ready models, but create each owned
	// dependency first. A stored owner FK back into its own intermediary needs
	// an authored AddField after creation, just like other inter-table cycles.
	pending := make(map[ir.ModelIdentity]bool, len(models))
	for _, model := range models {
		pending[ir.ModelIdentity{AppLabel: app, ModelName: model.Name}] = true
	}
	ordered := make([]ModelChange, 0, len(models))
	for len(pending) != 0 {
		progress := false
		for _, model := range models {
			id := ir.ModelIdentity{AppLabel: app, ModelName: model.Name}
			if !pending[id] {
				continue
			}
			ready := true
			for _, field := range model.Fields {
				if field.Relation != nil && field.Relation.Target != id && pending[field.Relation.Target] {
					ready = false
					break
				}
			}
			if !ready {
				continue
			}
			change := ModelChange{After: model}
			if operation.Kind == MigrationDeleteModel {
				change = ModelChange{Before: model}
			}
			ordered = append(ordered, change)
			delete(pending, id)
			progress = true
		}
		if !progress {
			return nil, fmt.Errorf("cyclic owned storage requires staged ForeignKey additions")
		}
	}
	if operation.Kind == MigrationDeleteModel {
		slices.Reverse(ordered)
	}
	changes = ordered

	return changes, nil
}

// StorageChanges returns the ordered detached inventory owned by the complete
// step, including transient tables created and removed within that step.
func (plan MigrationGraphPlan) StorageChanges() []ModelChange {
	changes := make([]ModelChange, len(plan.storage))
	for index, change := range plan.storage {
		changes[index] = ModelChange{Before: change.Before.Clone(), After: change.After.Clone()}
	}
	return changes
}
