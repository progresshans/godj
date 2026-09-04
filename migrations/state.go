// Package migrations implements GoDj's backend-neutral historical schema
// state, typed operations, and atomic executor.
package migrations

import (
	"fmt"
	"reflect"
	"sort"

	"github.com/progresshans/godj/schema/ir"
)

const StateFormatVersion = 1

// ProjectState is an immutable snapshot of normalized Schema IR grouped by
// app label. Accessors return deep clones so callers cannot mutate history.
// The zero value is a valid empty state.
type ProjectState struct {
	formatVersion int
	apps          map[string]ir.Schema
}

func EmptyProjectState() ProjectState {
	return ProjectState{formatVersion: StateFormatVersion, apps: make(map[string]ir.Schema)}
}

func NewProjectState(schemas ...ir.Schema) (ProjectState, error) {
	state := EmptyProjectState()
	for index, schema := range schemas {
		normalized, err := ir.Normalize(schema)
		if err != nil {
			return ProjectState{}, fmt.Errorf("normalize project schema %d: %w", index, err)
		}
		if normalized.FormatVersion != ir.CurrentFormatVersion {
			return ProjectState{}, fmt.Errorf(
				"project schema %d uses Schema IR version %d; migration state requires version %d",
				index,
				normalized.FormatVersion,
				ir.CurrentFormatVersion,
			)
		}
		if _, exists := state.apps[normalized.AppLabel]; exists {
			return ProjectState{}, fmt.Errorf("duplicate project app %q", normalized.AppLabel)
		}
		state.apps[normalized.AppLabel] = normalized.Clone()
	}
	return state, nil
}

func (s ProjectState) FormatVersion() int {
	if s.formatVersion == 0 {
		return StateFormatVersion
	}
	return s.formatVersion
}

func (s ProjectState) Clone() ProjectState {
	clone := EmptyProjectState()
	clone.formatVersion = s.FormatVersion()
	for app, schema := range s.apps {
		clone.apps[app] = schema.Clone()
	}
	return clone
}

func (s ProjectState) Apps() []string {
	apps := make([]string, 0, len(s.apps))
	for app := range s.apps {
		apps = append(apps, app)
	}
	sort.Strings(apps)
	return apps
}

func (s ProjectState) Schema(app string) (ir.Schema, bool) {
	schema, exists := s.apps[app]
	if !exists {
		return ir.Schema{}, false
	}
	return schema.Clone(), true
}

func (s ProjectState) Model(app, name string) (ir.Model, bool) {
	schema, exists := s.apps[app]
	if !exists {
		return ir.Model{}, false
	}
	for _, model := range schema.Models {
		if model.Name == name {
			return model.Clone(), true
		}
	}
	return ir.Model{}, false
}

func (s ProjectState) Equal(other ProjectState) bool {
	return reflect.DeepEqual(s.Clone(), other.Clone())
}

func (s ProjectState) validate() error {
	if s.FormatVersion() != StateFormatVersion {
		return fmt.Errorf("unsupported project state version %d", s.FormatVersion())
	}
	expectedIRVersion := ir.CurrentFormatVersion
	for app, schema := range s.apps {
		if schema.AppLabel != app {
			return fmt.Errorf("project app key %q does not match schema app label %q", app, schema.AppLabel)
		}
		if schema.FormatVersion != expectedIRVersion {
			return fmt.Errorf(
				"project app %s uses Schema IR version %d; migration state requires version %d",
				app,
				schema.FormatVersion,
				expectedIRVersion,
			)
		}
		normalized, err := ir.Normalize(schema)
		if err != nil {
			return fmt.Errorf("normalize project app %s: %w", app, err)
		}
		if !reflect.DeepEqual(normalized, schema) {
			return fmt.Errorf("project app %s is not normalized", app)
		}
	}
	return nil
}

func (s ProjectState) withSchema(schema ir.Schema) ProjectState {
	next := s.Clone()
	next.apps[schema.AppLabel] = schema.Clone()
	return next
}

func (s ProjectState) withoutApp(app string) ProjectState {
	next := s.Clone()
	delete(next.apps, app)
	return next
}

func normalizedSingleModel(app string, model ir.Model) (ir.Model, error) {
	schema, err := ir.Normalize(ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      app,
		Models:        []ir.Model{model.Clone()},
	})
	if err != nil {
		return ir.Model{}, err
	}
	return schema.Models[0].Clone(), nil
}

func firstProjectStateRelation(value ProjectState) (app, model, field string, exists bool) {
	apps := value.Apps()
	for _, app = range apps {
		schema := value.apps[app]
		models := append([]ir.Model(nil), schema.Models...)
		sort.Slice(models, func(left, right int) bool { return models[left].Name < models[right].Name })
		for _, candidate := range models {
			fields := append([]ir.Field(nil), candidate.Fields...)
			sort.Slice(fields, func(left, right int) bool { return fields[left].Name < fields[right].Name })
			for _, candidateField := range fields {
				if fieldContainsRelation(candidateField) {
					return app, candidate.Name, candidateField.Name, true
				}
			}
		}
	}
	return "", "", "", false
}

func projectStateRequiresRelationLifecycle(value ProjectState) bool {
	_, _, _, exists := firstProjectStateRelation(value)
	return exists
}

func modelEqual(left, right ir.Model) bool {
	return reflect.DeepEqual(left, right)
}

func fieldEqual(left, right ir.Field) bool {
	return reflect.DeepEqual(left, right)
}
