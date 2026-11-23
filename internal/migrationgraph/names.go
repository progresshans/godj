package migrationgraph

import (
	"fmt"

	"github.com/progresshans/godj/schema/ir"
)

// A declared name belongs to itself. A reverse name belongs to the field
// on its source model. All names here have been normalized by Schema IR.
type fieldName struct {
	model ir.ModelIdentity
	name  string
}

type relationNamespace map[fieldName]fieldName

func (m MigrationModel) fieldNames(yield func(fieldName, fieldName) bool) {
	id := m.Identity()
	for _, f := range m.Model.Fields {
		owner := fieldName{id, f.Name}
		if !yield(owner, owner) {
			return
		}
		if r := f.Relation; r != nil && r.Reverse.Name != "" && !yield(fieldName{r.Target, r.Reverse.Name}, owner) {
			return
		}
	}
	for _, f := range m.Model.ManyToMany {
		owner := fieldName{id, f.Name}
		if !yield(owner, owner) {
			return
		}
		if f.Reverse.Name != "" && !yield(fieldName{f.Target, f.Reverse.Name}, owner) {
			return
		}
	}
}

func (names relationNamespace) add(model MigrationModel) error {
	for key, owner := range model.fieldNames {
		if previous, exists := names[key]; exists {
			if key == owner || key == previous {
				return fmt.Errorf("relation graph reverse name %q collides with %s.%s field", key.name, key.model.AppLabel, key.model.ModelName)
			}
			return fmt.Errorf("relation graph reverse name %q is owned by both %s.%s.%s and %s.%s.%s", key.name,
				previous.model.AppLabel, previous.model.ModelName, previous.name, owner.model.AppLabel, owner.model.ModelName, owner.name)
		}
		names[key] = owner
	}
	return nil
}

// Validate the complete visible namespace between operations. A referencing
// model can remain present even when it is outside a later operation's graph.
// Only changed declarations need to be removed and registered again.
func (plan MigrationGraphPlan) checkFieldNames(changes [][]graphChange) error {
	names := make(relationNamespace)
	for _, id := range plan.initial.order {
		if err := names.add(MigrationModel{AppLabel: id.AppLabel, Model: plan.initial.models[id]}); err != nil {
			return err
		}
	}
	for _, step := range changes {
		for _, change := range step {
			before := MigrationModel{AppLabel: change.app, Model: change.Before}
			for key := range before.fieldNames {
				delete(names, key)
			}
		}
		for _, change := range step {
			if err := names.add(MigrationModel{AppLabel: change.app, Model: change.After}); err != nil {
				return err
			}
		}
	}
	return nil
}
