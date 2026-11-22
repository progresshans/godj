package migrationautodetect

import (
	"fmt"
	"maps"
	"slices"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

type modelDeletionGraph struct {
	models   map[ir.ModelIdentity]ir.Model
	order    map[string][]string
	pending  map[ir.ModelIdentity]bool
	owners   map[ir.ModelIdentity]ir.ModelIdentity
	incoming map[ir.ModelIdentity]map[ir.ModelIdentity]bool
	outgoing map[ir.ModelIdentity][]ir.ModelIdentity
}

func newModelDeletionGraph(current migrations.ProjectState, changes map[string]appChange) modelDeletionGraph {
	g := modelDeletionGraph{
		models: make(map[ir.ModelIdentity]ir.Model), order: make(map[string][]string),
		pending: make(map[ir.ModelIdentity]bool), owners: make(map[ir.ModelIdentity]ir.ModelIdentity),
		incoming: make(map[ir.ModelIdentity]map[ir.ModelIdentity]bool), outgoing: make(map[ir.ModelIdentity][]ir.ModelIdentity),
	}
	for app, change := range changes {
		for _, model := range change.deletions {
			g.pending[ir.ModelIdentity{AppLabel: app, ModelName: model.Name}] = true
		}
	}
	for _, app := range current.Apps() {
		s, _ := current.Schema(app)
		for _, model := range s.Models {
			id := ir.ModelIdentity{AppLabel: app, ModelName: model.Name}
			g.models[id] = model
			g.order[app] = append(g.order[app], model.Name)
			for _, many := range model.ManyToMany {
				if many.Through == nil {
					g.owners[many.StorageThrough(id).Model] = id
				}
			}
		}
	}
	for id, model := range g.models {
		edge := func(target ir.ModelIdentity) {
			if target == id {
				return // The table itself owns its self-reference.
			}
			// A reference to this model's automatic intermediary still needs
			// releasing: its physical table is dropped before the owner.
			target = g.owner(target)
			if g.incoming[target][id] {
				return
			}
			if g.incoming[target] == nil {
				g.incoming[target] = make(map[ir.ModelIdentity]bool)
			}
			g.incoming[target][id] = true
			g.outgoing[id] = append(g.outgoing[id], target)
		}
		for _, field := range model.Fields {
			if field.Relation != nil {
				edge(field.Relation.Target)
			}
		}
		for _, field := range model.ManyToMany {
			edge(field.Target)
			if field.Through != nil {
				edge(field.Through.Model)
			}
		}
	}
	return g
}

func (g modelDeletionGraph) owner(id ir.ModelIdentity) ir.ModelIdentity {
	if owner, derived := g.owners[id]; derived {
		return owner
	}
	return id
}

func nextModelDeletion(changes map[string]appChange, current migrations.ProjectState, leaves map[string][]migrations.MigrationKey, history []migrations.Migration) (migrations.Migration, error) {
	g := newModelDeletionGraph(current, changes)
	apps := current.Apps()
	for _, app := range apps {
		queue := []ir.ModelIdentity{}
		order := g.order[app]
		for i := len(order) - 1; i >= 0; i-- {
			id := ir.ModelIdentity{AppLabel: app, ModelName: order[i]}
			if g.pending[id] && len(g.incoming[id]) == 0 {
				queue = append(queue, id)
			}
		}
		if len(queue) == 0 {
			continue
		}
		var ops []migrations.Operation
		for i := 0; i < len(queue) && len(ops) < definition.MaxOperationsPerMigration; i++ {
			id := queue[i]
			pos := slices.Index(order, id.ModelName)
			before := ""
			if pos+1 < len(order) {
				before = order[pos+1]
			}
			ops = append(ops, migrations.DeleteModel{AppLabel: app, Model: g.models[id], BeforeModel: before})
			order = slices.Delete(order, pos, pos+1)
			for _, target := range g.outgoing[id] {
				delete(g.incoming[target], id)
				if target.AppLabel == app && g.pending[target] && len(g.incoming[target]) == 0 {
					queue = append(queue, target)
				}
			}
		}
		return g.candidate(app, ops, leaves, history)
	}
	// Every remaining table has an incoming edge. A surviving owner cannot
	// authorize its target's deletion. Within a cycle of retired models,
	// release explicit through metadata, then FKs, then automatic links.
	for _, app := range apps {
		for _, name := range g.order[app] {
			owner := ir.ModelIdentity{AppLabel: app, ModelName: name}
			if g.pending[owner] {
				continue
			}
			for _, target := range g.outgoing[owner] {
				if g.pending[target] {
					return migrations.Migration{}, detectionError(CodeInvalidRelation, target.AppLabel, target.ModelName, "", fmt.Errorf("retained model %s.%s still references the deletion", app, name))
				}
			}
		}
	}
	for _, release := range []string{"explicit", "foreign_key", "automatic"} {
		for _, app := range apps {
			for _, name := range g.order[app] {
				id := ir.ModelIdentity{AppLabel: app, ModelName: name}
				if !g.pending[id] {
					continue
				}
				if ops := g.releaseCycleEdge(id, release); len(ops) != 0 {
					return g.candidate(app, ops, leaves, history)
				}
			}
		}
	}
	return migrations.Migration{}, detectionError(CodeInvalidGeneratedPlan, "", "", "", fmt.Errorf("model deletion graph made no progress"))
}

func (g modelDeletionGraph) releaseCycleEdge(id ir.ModelIdentity, release string) []migrations.Operation {
	model := g.models[id]
	pendingTarget := func(target ir.ModelIdentity) bool { return target != id && g.pending[g.owner(target)] }
	if release == "foreign_key" {
		for i, field := range model.Fields {
			if field.Relation == nil || !pendingTarget(field.Relation.Target) {
				continue
			}
			var ops []migrations.Operation
			for _, constraint := range model.UniqueConstraints {
				if slices.Contains(constraint.Fields, field.Name) {
					ops = append(ops, migrations.RemoveConstraint{AppLabel: id.AppLabel, ModelName: id.ModelName, Constraint: constraint})
				}
			}
			before := ""
			if i+1 < len(model.Fields) {
				before = model.Fields[i+1].Name
			}
			return append(ops, migrations.RemoveField{AppLabel: id.AppLabel, ModelName: id.ModelName, Field: field, BeforeField: before})
		}
		return nil
	}
	for i, field := range model.ManyToMany {
		if (field.Through != nil) != (release == "explicit") {
			continue
		}
		if !pendingTarget(field.Target) && (field.Through == nil || !pendingTarget(field.Through.Model)) {
			continue
		}
		before := ""
		if i+1 < len(model.ManyToMany) {
			before = model.ManyToMany[i+1].Name
		}
		return []migrations.Operation{migrations.RemoveManyToMany{AppLabel: id.AppLabel, ModelName: id.ModelName, Field: field, BeforeField: before}}
	}
	return nil
}

func (g modelDeletionGraph) candidate(app string, ops []migrations.Operation, leaves map[string][]migrations.MigrationKey, history []migrations.Migration) (migrations.Migration, error) {
	change := migrations.Migration{App: app, Operations: ops}
	name, err := nextMigrationName(app, leaves[app], ops, history)
	if err != nil {
		return migrations.Migration{}, err
	}
	change.Name = name
	apps := map[string]bool{app: true}
	refs, err := change.ReferencedModels()
	if err != nil {
		return migrations.Migration{}, err
	}
	for _, ref := range refs {
		apps[ref.AppLabel] = true
	}
	deleted := make(map[ir.ModelIdentity]bool)
	for _, op := range ops {
		if drop, ok := op.(migrations.DeleteModel); ok {
			deleted[ir.ModelIdentity{AppLabel: app, ModelName: drop.Model.Name}] = true
		}
	}
	// Bind retired incoming references too. Their tables/fields may already
	// be absent from current state, but reversing this migration must precede
	// restoring those owners, including from another app or through model.
	if len(deleted) != 0 {
		for _, prior := range history {
			if apps[prior.App] {
				continue
			}
			refs, err := prior.ReferencedModels()
			if err != nil {
				return migrations.Migration{}, err
			}
			for _, ref := range refs {
				if deleted[g.owner(ref)] {
					apps[prior.App] = true
					break
				}
			}
		}
	}
	for _, dependency := range slices.Sorted(maps.Keys(apps)) {
		if len(leaves[dependency]) != 1 {
			return migrations.Migration{}, detectionError(CodeAmbiguousHistory, dependency, "", "", fmt.Errorf("model deletion needs a unique migration leaf"))
		}
		change.Dependencies = append(change.Dependencies, leaves[dependency][0])
	}
	change.Dependencies = canonicalDependencies(change.Dependencies)
	return change, nil
}
