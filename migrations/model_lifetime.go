package migrations

import (
	"fmt"
	"slices"
	"sort"

	"github.com/progresshans/godj/schema/ir"
)

// Events were collected in forward plan order. A reused identity needs a
// causal delete between creations, even if sibling branches happened to be
// replayed in that order when reconstructing the full catalog.
func (r loadedStateReconstructor) validateModelLifetimes() error {
	var changed []loadedModelIdentity
	for id, events := range r.modelEvents {
		if len(events) > 1 || events[0].deleted {
			changed = append(changed, id)
		}
	}
	sort.Slice(changed, func(i, j int) bool { return loadedIdentityLess(changed[i], changed[j]) })
	for _, id := range changed {
		events := r.modelEvents[id]
		for i, event := range events {
			var err error
			switch {
			case i == 0 && event.deleted:
				err = fmt.Errorf("model %s.%s has no historical creator", id.app, id.model)
			case i > 0 && !event.deleted && !events[i-1].deleted:
				err = fmt.Errorf("model %s.%s has multiple historical creators without an intervening deletion", id.app, id.model)
			case i > 0 && (event.deleted == events[i-1].deleted || !r.modelEventBefore(events[i-1], event)):
				err = fmt.Errorf("model %s.%s creation and deletion must alternate in dependency order", id.app, id.model)
			}
			if err != nil {
				migration := r.definitions[event.key]
				return invalidLoadedState(migration, event.operationIndex, operationKindAt(migration, event.operationIndex), err)
			}
		}
	}
	return nil
}

func (r loadedStateReconstructor) modelEventBefore(left, right loadedModelEvent) bool {
	if left.key == right.key {
		return left.operationIndex < right.operationIndex
	}
	return r.isAncestor(left.key, right.key)
}

// A validated lifetime is a single causal chain. Every use must be ordered
// with its next change of lifetime. The deletion itself uses the old model.
func (r loadedStateReconstructor) liveCreatorAt(events []loadedModelEvent, at loadedModelEvent) (loadedModelEvent, bool) {
	end := sort.Search(len(events), func(i int) bool {
		e := events[i]
		return !(e.key == at.key && e.operationIndex == at.operationIndex) && !r.modelEventBefore(e, at)
	})
	if end == 0 || end < len(events) && !r.modelEventBefore(at, events[end]) {
		return loadedModelEvent{}, false
	}
	last := events[end-1]
	if last.deleted {
		if last.key != at.key || last.operationIndex != at.operationIndex || end < 2 {
			return loadedModelEvent{}, false
		}
		last = events[end-2]
	}
	return last, true
}

func (r loadedStateReconstructor) creatorAt(id loadedModelIdentity, declaration loadedRelationDeclaration) (loadedModelEvent, bool) {
	events := r.modelEvents[id]
	if len(events) == 1 {
		// The existing single-creator chronology check also distinguishes
		// a later same-migration operation from missing dependency ancestry.
		return events[0], !events[0].deleted
	}
	return r.liveCreatorAt(events, loadedModelEvent{key: declaration.key, operationIndex: declaration.operationIndex})
}

// A derived table belongs to the instance of its owner model that created it.
// A later owner with the same name cannot lend its predecessor's authority.
func (r loadedStateReconstructor) automaticOwnerAt(creators []loadedModelEvent, at loadedModelEvent) bool {
	for _, creation := range creators {
		if creation != at && !r.modelEventBefore(creation, at) {
			continue
		}
		op := r.definitions[creation.key].Operations[creation.operationIndex]
		app, name := operationSourceModel(op)
		events := r.modelEvents[loadedModelIdentity{app: app, model: name}]
		origin, existed := r.liveCreatorAt(events, creation)
		current, live := r.liveCreatorAt(events, at)
		if existed && live && origin == current {
			return true
		}
	}
	return false
}

func (r loadedStateReconstructor) validateLifetimeReferences(key MigrationKey, index int, op Operation) error {
	at := loadedModelEvent{key: key, operationIndex: index}
	check := func(id ir.ModelIdentity, require bool) error {
		model := loadedModelIdentity{app: id.AppLabel, model: id.ModelName}
		events := r.modelEvents[model]
		if creators := r.automaticCreators[model]; !require && len(events) == 0 && len(creators) != 0 {
			if r.automaticOwnerAt(creators, at) {
				return nil
			}
		} else if !require && len(events) < 2 {
			return nil
		} else if _, visible := r.liveCreatorAt(events, at); visible {
			return nil
		}
		return invalidLoadedState(r.definitions[key], index, op.Kind(), fmt.Errorf("model %s.%s has no unambiguous live declaration at this operation", id.AppLabel, id.ModelName))
	}
	if drop, ok := op.(DeleteModel); ok && drop.BeforeModel != "" {
		if err := check(ir.ModelIdentity{AppLabel: drop.AppLabel, ModelName: drop.BeforeModel}, true); err != nil {
			return err
		}
	}
	return visitOperationModels(op, func(id ir.ModelIdentity) error { return check(id, false) })
}

// ReferencedModels returns the logical model identities named by the migration
// in deterministic order, including relation targets, through models and data scopes.
func (m Migration) ReferencedModels() ([]ir.ModelIdentity, error) {
	seen := make(map[ir.ModelIdentity]bool)
	for _, op := range m.Operations {
		if err := visitOperationModels(operationValue(op), func(id ir.ModelIdentity) error {
			seen[id] = true
			return nil
		}); err != nil {
			return nil, err
		}
	}
	ids := make([]ir.ModelIdentity, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, compareDataModel)
	return ids, nil
}

func visitOperationModels(op Operation, visit func(ir.ModelIdentity) error) error {
	if isNilOperation(op) || loadedOperationWireKind(op) == "" {
		return fmt.Errorf("unknown migration operation %T", op)
	}
	app, name := operationSourceModel(op)
	if name != "" {
		if err := visit(ir.ModelIdentity{AppLabel: app, ModelName: name}); err != nil {
			return err
		}
	}
	var fields []ir.Field
	var many []ir.ManyToManyField
	switch value := op.(type) {
	case CreateModel:
		fields, many = value.Model.Fields, value.Model.ManyToMany
	case DeleteModel:
		fields, many = value.Model.Fields, value.Model.ManyToMany
		if value.BeforeModel != "" {
			if err := visit(ir.ModelIdentity{AppLabel: app, ModelName: value.BeforeModel}); err != nil {
				return err
			}
		}
	case AddField:
		fields = []ir.Field{value.Field}
	case RemoveField:
		fields = []ir.Field{value.Field}
	case AlterField:
		fields = []ir.Field{value.Before, value.After}
	case RenameField:
		fields = []ir.Field{value.Before, value.After}
	case AddManyToMany:
		many = []ir.ManyToManyField{value.Field}
	case RemoveManyToMany:
		many = []ir.ManyToManyField{value.Field}
	case RenameManyToMany:
		many = []ir.ManyToManyField{value.Before, value.After}
	case AlterManyToMany:
		many = []ir.ManyToManyField{value.Before, value.After}
	case RunData:
		for _, id := range value.Models {
			if err := visit(id); err != nil {
				return err
			}
		}
	}
	for _, field := range fields {
		if field.Relation != nil {
			if err := visit(field.Relation.Target); err != nil {
				return err
			}
		}
	}
	for _, field := range many {
		if err := visit(field.Target); err != nil {
			return err
		}
		if field.Through != nil {
			if err := visit(field.Through.Model); err != nil {
				return err
			}
		}
	}
	return nil
}
