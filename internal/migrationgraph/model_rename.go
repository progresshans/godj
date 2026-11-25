package migrationgraph

import (
	"fmt"

	"github.com/progresshans/godj/internal/irresource"
	"github.com/progresshans/godj/schema/ir"
)

// NewModelRename admits both model snapshots and expanded intermediary names
// at every migration boundary before Schema IR builds its identity map.
func NewModelRename(app string, before, after ir.Model) (ir.ModelRename, error) {
	budget := irresource.New(irresource.Limits{Fields: migrationGraphMaxFields, StringBytes: migrationGraphMaxStringBytes, Nodes: migrationGraphMaxNodes, Bytes: migrationGraphMaxBytes})
	if err := budget.ConsumeString("rename.app", app); err != nil {
		return ir.ModelRename{}, err
	}
	for _, m := range []ir.Model{before, after} {
		if err := budget.ScanModel("rename", m); err != nil {
			return ir.ModelRename{}, err
		}
		for _, f := range m.ManyToMany {
			if f.Through != nil {
				continue
			}
			for _, n := range []int{len(m.Name) + 1 + len(f.Name), len(m.DBTable) + 1 + len(f.Name), len(m.GoName) + len(f.GoName) + 4} {
				if err := budget.ConsumeStringBytes("rename.intermediary", n); err != nil {
					return ir.ModelRename{}, err
				}
			}
		}
	}
	return ir.NewModelRename(app, before, after)
}

type RenamedTable struct {
	ModelChange
	// Incoming maps the pre-rename owner table to its authorized FK columns.
	Incoming map[string]map[string]bool
}

// RenamedTables pairs physical storage with its incoming keys from the
// validated historical graph. All checks run against the old catalog before
// the first rename, including references between two owned tables.
func (op MigrationOperation) RenamedTables(app string, graph RelationGraph) ([]RenamedTable, error) {
	id := ir.ModelIdentity{AppLabel: app, ModelName: op.After.Name}
	if op.Kind != MigrationRenameModel || graph.root != id || !graph.models[id].Equal(op.After) {
		return nil, fmt.Errorf("model rename does not match its resolved graph")
	}
	changes, err := op.StorageChanges(app)
	if err != nil {
		return nil, err
	}
	tables := make([]RenamedTable, len(changes))
	positions := make(map[ir.ModelIdentity]int, len(changes))
	for i, change := range changes {
		tables[i] = RenamedTable{ModelChange: change, Incoming: make(map[string]map[string]bool)}
		positions[ir.ModelIdentity{AppLabel: app, ModelName: change.After.Name}] = i
	}
	for _, owner := range graph.order {
		model := graph.models[owner]
		table := model.DBTable
		if i, moved := positions[owner]; moved {
			table = tables[i].Before.DBTable
		}
		for _, field := range model.Fields {
			if field.Relation == nil {
				continue
			}
			if pos, moved := positions[field.Relation.Target]; moved {
				incoming := tables[pos].Incoming
				if incoming[table] == nil {
					incoming[table] = make(map[string]bool)
				}
				incoming[table][field.Column] = true
			}
		}
	}
	return tables, nil
}

// Targets and related models describe the graph after the operation, in its
// direction of travel. Its inverse gives exact initial snapshots for every
// referencing model, including intermediaries owned by a different app.
func (op MigrationOperation) modelRenameReferences(app string, owned []ModelChange) ([]graphChange, error) {
	inverse, err := NewModelRename(app, op.After, op.Before)
	if err != nil {
		return nil, err
	}
	seen := map[ir.ModelIdentity]bool{{AppLabel: app, ModelName: op.After.Name}: true}
	for _, c := range owned {
		seen[ir.ModelIdentity{AppLabel: app, ModelName: c.After.Name}] = true
	}
	var changes []graphChange
	add := func(id ir.ModelIdentity, model ir.Model) {
		if seen[id] {
			return
		}
		seen[id] = true
		if before, changed := inverse.RebindReferences(model); changed {
			changes = append(changes, graphChange{id.AppLabel, ModelChange{Before: before, After: model}})
		}
	}
	for _, target := range op.Targets {
		if target.SourceField.Relation == nil {
			return nil, fmt.Errorf("model rename contains a target without a foreign key")
		}
		add(target.SourceField.Relation.Target, target.TargetModel)
	}
	for _, related := range op.RelatedModels {
		add(related.Identity(), related.Model)
	}
	return changes, nil
}
