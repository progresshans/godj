package migrationautodetect

import (
	"fmt"
	"sort"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

// nextCandidate selects one dependency-ready app. If every app waits on new
// models, it breaks one concrete cycle by creating an app's models without its
// unresolved cross-app FKs. The next prefix replay detects those missing fields
// again. It never changes model order or guesses a target migration identity.
func nextCandidate(changes map[string]appChange, current, desired migrations.ProjectState, leaves map[string][]migrations.MigrationKey, history []migrations.Migration) (migrations.Migration, error) {
	known := make(map[ir.ModelIdentity]bool)
	for _, app := range current.Apps() {
		schema, _ := current.Schema(app)
		for _, model := range schema.Models {
			known[ir.ModelIdentity{AppLabel: app, ModelName: model.Name}] = true
		}
	}
	wanted := make(map[ir.ModelIdentity]bool)
	for _, app := range desired.Apps() {
		schema, _ := desired.Schema(app)
		for _, model := range schema.Models {
			wanted[ir.ModelIdentity{AppLabel: app, ModelName: model.Name}] = true
		}
	}
	apps := make([]string, 0, len(changes))
	for app := range changes {
		apps = append(apps, app)
	}
	sort.Strings(apps)
	waits := make(map[string][]string, len(changes))
	for _, app := range apps {
		relations := append([]relationReference(nil), changes[app].relations...)
		sort.Slice(relations, func(i, j int) bool {
			a, b := relations[i], relations[j]
			if a.targetApp != b.targetApp {
				return a.targetApp < b.targetApp
			}
			if a.targetModel != b.targetModel {
				return a.targetModel < b.targetModel
			}
			if a.sourceModel != b.sourceModel {
				return a.sourceModel < b.sourceModel
			}
			return a.field < b.field
		})
		unique := make(map[string]bool)
		for _, relation := range relations {
			target := ir.ModelIdentity{AppLabel: relation.targetApp, ModelName: relation.targetModel}
			if known[target] && manyReferenceReady(relation, current) {
				if len(leaves[target.AppLabel]) > 1 {
					return migrations.Migration{}, detectionError(CodeAmbiguousHistory, target.AppLabel, target.ModelName, "", fmt.Errorf("relation target app has multiple migration leaves"))
				}
				if len(leaves[target.AppLabel]) == 0 {
					return migrations.Migration{}, detectionError(CodeInvalidRelation, app, relation.sourceModel, relation.field, fmt.Errorf("historical relation target has no migration authority"))
				}
				continue
			}
			if !wanted[target] {
				return migrations.Migration{}, detectionError(CodeInvalidRelation, app, relation.sourceModel, relation.field, fmt.Errorf("relation target %s.%s does not exist", target.AppLabel, target.ModelName))
			}
			if _, exists := changes[target.AppLabel]; !exists {
				return migrations.Migration{}, detectionError(CodeInvalidRelation, app, relation.sourceModel, relation.field, fmt.Errorf("new relation target has no candidate authority"))
			}
			unique[target.AppLabel] = true
		}
		for target := range unique {
			waits[app] = append(waits[app], target)
		}
		sort.Strings(waits[app])
	}
	selected, breakCycle := "", false
	for _, app := range apps {
		if len(waits[app]) == 0 {
			selected = app
			break
		}
	}
	if selected == "" {
		// Every vertex has an outgoing wait. Following its first sorted edge
		// must reach a cycle, without recursive traversal or transitive cloning.
		seen := make(map[string]int, len(apps))
		var path []string
		cursor := apps[0]
		for {
			if start, exists := seen[cursor]; exists {
				cycle := append([]string(nil), path[start:]...)
				sort.Strings(cycle)
				selected, breakCycle = cycle[0], true
				break
			}
			seen[cursor] = len(path)
			path = append(path, cursor)
			cursor = waits[cursor][0]
		}
	}
	operations, err := candidateOperations(selected, changes[selected], current, desired, known, breakCycle)
	if err != nil {
		return migrations.Migration{}, err
	}
	if len(operations) == 0 || len(operations) > definition.MaxOperationsPerMigration {
		return migrations.Migration{}, detectionError(CodeInvalidGeneratedPlan, selected, "", "", fmt.Errorf("candidate operation count is outside current limits"))
	}
	name, err := nextMigrationName(selected, leaves[selected], operations, history)
	if err != nil {
		return migrations.Migration{}, err
	}
	dependencies := append([]migrations.MigrationKey(nil), leaves[selected]...)
	var references []relationReference
	for _, operation := range operations {
		collectManyOperationReferences(&references, operation)
		switch value := operation.(type) {
		case migrations.CreateModel:
			for _, field := range value.Model.Fields {
				collectRelationReference(&references, selected, value.Model.Name, field)
			}
		case migrations.AddField:
			collectRelationReference(&references, selected, value.ModelName, value.Field)
		}
	}
	for _, reference := range references {
		if !known[ir.ModelIdentity{AppLabel: reference.targetApp, ModelName: reference.targetModel}] || len(leaves[reference.targetApp]) != 1 {
			return migrations.Migration{}, detectionError(CodeInvalidGeneratedPlan, selected, reference.sourceModel, reference.field, fmt.Errorf("candidate retains an unresolved external target"))
		}
		dependencies = append(dependencies, leaves[reference.targetApp][0])
	}
	return migrations.Migration{App: selected, Name: name, Dependencies: canonicalDependencies(dependencies), Operations: operations}, nil
}

func candidateOperations(app string, change appChange, current, desired migrations.ProjectState, known map[ir.ModelIdentity]bool, breakCycle bool) ([]migrations.Operation, error) {
	creators := make(map[string]int)
	for index, operation := range change.operations {
		if create, ok := operation.(migrations.CreateModel); ok {
			creators[create.Model.Name] = index
		}
	}
	unresolvedCross := func(field ir.Field) bool {
		return field.Relation != nil && field.Relation.Target.AppLabel != app && !known[field.Relation.Target]
	}
	var creates, sameAppAdds, existing, removals, manyAdds, manyRemovals []migrations.Operation
	var constraintAdds []migrations.AddConstraint
	for index, operation := range change.operations {
		switch value := operation.(type) {
		case migrations.CreateModel:
			model := value.Model
			model.Fields = make([]ir.Field, 0, len(value.Model.Fields))
			for _, field := range value.Model.Fields {
				if breakCycle && unresolvedCross(field) {
					continue
				}
				if field.Relation != nil && field.Relation.Target.AppLabel == app {
					if targetIndex, exists := creators[field.Relation.Target.ModelName]; exists && targetIndex > index {
						sameAppAdds = append(sameAppAdds, migrations.AddField{AppLabel: app, ModelName: model.Name, Field: field.Clone()})
						continue
					}
				}
				model.Fields = append(model.Fields, field.Clone())
			}
			// A deferred FK also defers each constraint that uses it. Retained
			// fields may own inline constraints; missing members are added only
			// after this candidate has created every referenced local column.
			model.UniqueConstraints = nil
			available := fieldNameSet(model.Fields)
			for _, constraint := range value.Model.UniqueConstraints {
				if constraintMembersPresent(constraint, available) {
					model.UniqueConstraints = append(model.UniqueConstraints, constraint.Clone())
				} else {
					constraintAdds = append(constraintAdds, migrations.AddConstraint{AppLabel: app, ModelName: model.Name, Constraint: constraint.Clone()})
				}
			}
			creates = append(creates, migrations.CreateModel{AppLabel: app, Model: model})
		case migrations.AddField:
			if breakCycle && unresolvedCross(value.Field) {
				continue
			}
			existing = append(existing, value)
		case migrations.AddManyToMany, migrations.RenameManyToMany:
			manyAdds = append(manyAdds, operation)
		case migrations.RemoveManyToMany:
			manyRemovals = append(manyRemovals, operation)
		case migrations.AddConstraint:
			constraintAdds = append(constraintAdds, value)
		case migrations.RemoveConstraint:
			removals = append(removals, value)
		case migrations.AlterField:
			existing = append(existing, value)
		default:
			return nil, detectionError(CodeInvalidGeneratedPlan, app, "", "", fmt.Errorf("unsupported candidate operation %T", operation))
		}
	}
	if breakCycle && len(creates) == 0 {
		return nil, detectionError(CodeInvalidGeneratedPlan, app, "", "", fmt.Errorf("cycle has no model creator"))
	}
	operations := append(manyRemovals, creates...)
	operations = append(operations, removals...)
	operations = append(operations, sameAppAdds...)
	operations = append(operations, existing...)
	// Anchors refer only to fields already present at this exact operation.
	// Deferring an earlier FK therefore cannot move a following scalar field.
	available := make(map[string]map[string]bool)
	before, _ := current.Schema(app)
	for _, model := range before.Models {
		available[model.Name] = fieldNameSet(model.Fields)
	}
	after, _ := desired.Schema(app)
	models := make(map[string]ir.Model, len(after.Models))
	for _, model := range after.Models {
		models[model.Name] = model
	}
	for index, operation := range operations {
		switch value := operation.(type) {
		case migrations.CreateModel:
			available[value.Model.Name] = fieldNameSet(value.Model.Fields)
		case migrations.AddField:
			fields, exists := available[value.ModelName]
			if !exists {
				return nil, detectionError(CodeInvalidGeneratedPlan, app, value.ModelName, value.Field.Name, fmt.Errorf("AddField source is not yet available"))
			}
			found := false
			for _, field := range models[value.ModelName].Fields {
				if field.Name == value.Field.Name {
					found = true
					continue
				}
				if found && fields[field.Name] {
					value.BeforeField = field.Name
					break
				}
			}
			if !found {
				return nil, detectionError(CodeInvalidGeneratedPlan, app, value.ModelName, value.Field.Name, fmt.Errorf("AddField lacks desired declaration"))
			}
			fields[value.Field.Name] = true
			operations[index] = value
		}
	}
	for _, operation := range constraintAdds {
		if constraintMembersPresent(operation.Constraint, available[operation.ModelName]) {
			operations = append(operations, operation)
		} else if !breakCycle {
			return nil, detectionError(CodeInvalidGeneratedPlan, app, operation.ModelName, "", fmt.Errorf("constraint members are not yet available"))
		}
		// An unresolved cross-app FK and its constraints are rediscovered
		// from the next durable prefix, retaining deterministic recovery.
	}
	deferred := map[string]bool{}
	for _, operation := range manyAdds {
		if value, ok := operation.(migrations.AddManyToMany); ok {
			if !manyCandidateReady(value, app, current, available) || value.BeforeField != "" && deferred[value.ModelName+"."+value.BeforeField] {
				deferred[value.ModelName+"."+value.Field.Name] = true
				continue
			}
		}
		operations = append(operations, operation)
	}
	return operations, nil
}

func fieldNameSet(fields []ir.Field) map[string]bool {
	set := make(map[string]bool, len(fields))
	for _, field := range fields {
		set[field.Name] = true
	}
	return set
}
