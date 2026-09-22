package ir

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/progresshans/godj/internal/identifiers"
)

type ManyToManySymmetry string

const (
	ManyToManyDirected    ManyToManySymmetry = "directed"
	ManyToManySymmetrical ManyToManySymmetry = "symmetrical"
)

// ThroughModel selects the two logical FK fields of an explicit intermediary.
// Selection is explicit even when the model contains only two foreign keys;
// adding another FK must not silently change the relation's meaning.
type ThroughModel struct {
	Model       ModelIdentity `json:"model"`
	SourceField string        `json:"source_field"`
	TargetField string        `json:"target_field"`
}

// ManyToManyField is columnless: it is separate from a model's stored Fields.
// A nil Through derives owned storage; an explicit Through preserves the named
// model's storage and constraints. Zero symmetry defaults to symmetric for a
// self target and directed otherwise. Normalize makes that choice explicit.
type ManyToManyField struct {
	Name     string             `json:"name"`
	GoName   string             `json:"go_name"`
	Target   ModelIdentity      `json:"target"`
	Reverse  ReverseRelation    `json:"reverse"`
	Symmetry ManyToManySymmetry `json:"symmetry"`
	Through  *ThroughModel      `json:"through,omitempty"`
}

func (field ManyToManyField) Clone() ManyToManyField {
	if field.Through != nil {
		value := *field.Through
		field.Through = &value
	}
	return field
}

func (field ManyToManyField) Equal(other ManyToManyField) bool {
	return field.Name == other.Name && field.GoName == other.GoName && field.Target == other.Target &&
		field.Reverse == other.Reverse && field.Symmetry == other.Symmetry &&
		(field.Through == nil && other.Through == nil || field.Through != nil && other.Through != nil && *field.Through == *other.Through)
}

// ManyToManyRename reports an exact name-only change. Renaming never changes
// relation direction, endpoint selection, target or explicit storage ownership.
func ManyToManyRename(before, after ManyToManyField) bool {
	if before.Name == after.Name && before.GoName == after.GoName {
		return false
	}
	before.Name, before.GoName = after.Name, after.GoName
	return before.Equal(after)
}

// NormalizeManyToManyField normalizes a declaration independently of an owner
// field list. Normalize additionally checks the owner's full field namespace.
func NormalizeManyToManyField(app, owner string, field ManyToManyField) (ManyToManyField, error) {
	if err := validateManyToManyIdentity(ModelIdentity{AppLabel: app, ModelName: owner}, "owner"); err != nil {
		return ManyToManyField{}, err
	}
	model := Model{Name: owner, ManyToMany: []ManyToManyField{field.Clone()}}
	if err := normalizeManyToMany(app, &model, "model"); err != nil {
		return ManyToManyField{}, err
	}
	return model.ManyToMany[0], nil
}

func normalizeManyToMany(app string, model *Model, path string) error {
	if len(model.ManyToMany) == 0 {
		model.ManyToMany = nil
		return nil
	}
	names, goNames := make(map[string]struct{}), make(map[string]struct{})
	for _, field := range model.Fields {
		names[field.Name] = struct{}{}
		goNames[field.GoName] = struct{}{}
	}
	for index := range model.ManyToMany {
		field := &model.ManyToMany[index]
		fieldPath := fmt.Sprintf("%s.many_to_many[%d]", path, index)
		if !identifiers.SQL(field.Name) {
			return validation(fieldPath+".name", "invalid_identifier", field.Name)
		}
		if !identifiers.ExportedGo(field.GoName) {
			return validation(fieldPath+".go_name", "invalid_go_identifier", field.GoName)
		}
		if duplicate(names, field.Name) {
			return validation(fieldPath+".name", "duplicate", field.Name)
		}
		if duplicate(goNames, field.GoName) {
			return validation(fieldPath+".go_name", "duplicate", field.GoName)
		}
		if err := validateManyToManyIdentity(field.Target, fieldPath+".target"); err != nil {
			return err
		}
		self := field.Target == (ModelIdentity{AppLabel: app, ModelName: model.Name})
		if field.Symmetry == "" {
			field.Symmetry = ManyToManyDirected
			if self {
				field.Symmetry = ManyToManySymmetrical
			}
		}
		if field.Symmetry != ManyToManyDirected && field.Symmetry != ManyToManySymmetrical {
			return validation(fieldPath+".symmetry", "unsupported", string(field.Symmetry))
		}
		if field.Symmetry == ManyToManySymmetrical && !self {
			return validation(fieldPath+".symmetry", "invalid", "symmetry requires a self target")
		}
		if field.Reverse == (ReverseRelation{}) {
			if field.Symmetry == ManyToManySymmetrical {
				field.Reverse.Disabled = true
			} else {
				field.Reverse.Name = model.Name + "_set"
			}
		}
		if field.Reverse.Disabled == (field.Reverse.Name != "") {
			return validation(fieldPath+".reverse", "invalid", "choose a name or disable the reverse relation")
		}
		if field.Reverse.Name != "" && !identifiers.SQL(field.Reverse.Name) {
			return validation(fieldPath+".reverse.name", "invalid_identifier", field.Reverse.Name)
		}
		if field.Symmetry == ManyToManySymmetrical && !field.Reverse.Disabled {
			return validation(fieldPath+".reverse", "invalid", "a symmetric self relation has no separate reverse manager")
		}
		if through := field.Through; through != nil {
			if err := validateManyToManyIdentity(through.Model, fieldPath+".through.model"); err != nil {
				return err
			}
			if !identifiers.SQL(through.SourceField) {
				return validation(fieldPath+".through.source_field", "invalid_identifier", through.SourceField)
			}
			if !identifiers.SQL(through.TargetField) {
				return validation(fieldPath+".through.target_field", "invalid_identifier", through.TargetField)
			}
			if through.SourceField == through.TargetField {
				return validation(fieldPath+".through", "duplicate", "source and target fields must differ")
			}
		}
	}
	return nil
}

func validateManyToManyIdentity(identity ModelIdentity, path string) error {
	if !identifiers.SQL(identity.AppLabel) {
		return validation(path+".app_label", "invalid_identifier", identity.AppLabel)
	}
	if !identifiers.SQL(identity.ModelName) {
		return validation(path+".model_name", "invalid_identifier", identity.ModelName)
	}
	return nil
}

// StorageSchema projects a logical schema into its model storage inventory.
// The logical owners retain their columnless metadata; automatic intermediary
// models are appended in declaration order. It never mutates its input and
// rejects all collisions with declared models, Go names or tables. Its output
// is a derived view, not a new declaration input.
func StorageSchema(input Schema) (Schema, error) {
	schema, err := Normalize(input)
	if err != nil {
		return Schema{}, err
	}
	var automatic []Model
	for _, model := range schema.Models {
		for _, field := range model.ManyToMany {
			if field.Through == nil {
				automatic = append(automatic, automaticThroughModel(schema.AppLabel, model, field))
			}
		}
	}
	if len(automatic) == 0 {
		return schema, nil
	}
	schema.Models = append(schema.Models, automatic...)
	return Normalize(schema)
}

func automaticThroughModel(app string, owner Model, field ManyToManyField) Model {
	fk := func(name, goName string, target ModelIdentity) Field {
		return Field{Name: name, GoName: goName, Column: name + "_id", Kind: FieldForeignKey,
			Relation: &ForeignKeyRelation{Target: target, Cardinality: RelationManyToOne, Reverse: ReverseRelation{Disabled: true}, OnDelete: DeleteCascade}}
	}
	return Model{Name: owner.Name + "_" + field.Name, GoName: owner.GoName + field.GoName + "Link", DBTable: owner.DBTable + "_" + field.Name,
		Fields:            []Field{fk("source", "SourceID", ModelIdentity{AppLabel: app, ModelName: owner.Name}), fk("target", "TargetID", field.Target)},
		UniqueConstraints: []UniqueConstraint{{Name: "relation_pair", Fields: []string{"source", "target"}}}}
}

// ManyToManyBinding is a resolved coordinate in the derived storage inventory.
// It contains no loader, query, mutation capability or mutable containers.
type ManyToManyBinding struct {
	Source    ModelIdentity
	Field     string
	Target    ModelIdentity
	Reverse   ReverseRelation
	Symmetry  ManyToManySymmetry
	Through   ThroughModel
	Automatic bool
}

// ResolveManyToMany validates project targets and intermediary field selection
// without requiring the intermediary to own pair uniqueness. Explicit Django
// through models may have additional fields or permit repeated pairs; the
// mutation runtime must respect that model's actual constraints.
func ResolveManyToMany(schemas ...Schema) ([]ManyToManyBinding, error) {
	models := make(map[ModelIdentity]Model)
	apps := make(map[string]struct{})
	var bindings []ManyToManyBinding
	for _, input := range schemas {
		schema, err := StorageSchema(input)
		if err != nil {
			return nil, err
		}
		if duplicate(apps, schema.AppLabel) {
			return nil, validation("apps", "duplicate", schema.AppLabel)
		}
		for _, model := range schema.Models {
			source := ModelIdentity{AppLabel: schema.AppLabel, ModelName: model.Name}
			models[source] = model
			for _, field := range model.ManyToMany {
				binding := ManyToManyBinding{Source: source, Field: field.Name, Target: field.Target, Reverse: field.Reverse, Symmetry: field.Symmetry, Automatic: field.Through == nil}
				if field.Through == nil {
					binding.Through = ThroughModel{Model: ModelIdentity{AppLabel: schema.AppLabel, ModelName: model.Name + "_" + field.Name}, SourceField: "source", TargetField: "target"}
				} else {
					binding.Through = *field.Through
				}
				bindings = append(bindings, binding)
			}
		}
	}
	slices.SortFunc(bindings, func(left, right ManyToManyBinding) int {
		for _, pair := range [][2]string{{left.Source.AppLabel, right.Source.AppLabel}, {left.Source.ModelName, right.Source.ModelName}, {left.Field, right.Field}} {
			if value := cmp.Compare(pair[0], pair[1]); value != 0 {
				return value
			}
		}
		return 0
	})
	for _, binding := range bindings {
		path := "apps." + binding.Source.AppLabel + ".models." + binding.Source.ModelName + ".many_to_many." + binding.Field
		if _, ok := models[binding.Target]; !ok {
			return nil, validation(path+".target", "unresolved_target", binding.Target.AppLabel+"."+binding.Target.ModelName)
		}
		through, ok := models[binding.Through.Model]
		if !ok {
			return nil, validation(path+".through.model", "unresolved_target", binding.Through.Model.AppLabel+"."+binding.Through.Model.ModelName)
		}
		for _, endpoint := range []struct {
			name   string
			target ModelIdentity
		}{{binding.Through.SourceField, binding.Source}, {binding.Through.TargetField, binding.Target}} {
			found := false
			for _, field := range through.Fields {
				if field.Name != endpoint.name {
					continue
				}
				found = field.Kind == FieldForeignKey && field.Relation != nil && field.Relation.Target == endpoint.target
				break
			}
			if !found {
				return nil, validation(path+".through."+endpoint.name, "invalid_relation", "intermediary field must reference the selected endpoint")
			}
		}
	}
	if len(bindings) != 0 {
		type namespace struct {
			model ModelIdentity
			name  string
		}
		names := make(map[namespace]bool)
		for _, model := range models {
			for _, field := range model.Fields {
				if field.Relation != nil && !field.Relation.Reverse.Disabled {
					names[namespace{field.Relation.Target, field.Relation.Reverse.Name}] = true
				}
			}
		}
		for _, binding := range bindings {
			if binding.Reverse.Disabled {
				continue
			}
			key := namespace{binding.Target, binding.Reverse.Name}
			model := models[binding.Target]
			collision := names[key]
			for _, field := range model.Fields {
				collision = collision || field.Name == key.name
			}
			for _, field := range model.ManyToMany {
				collision = collision || field.Name == key.name
			}
			if collision {
				return nil, validation("many_to_many."+binding.Source.AppLabel+"."+binding.Source.ModelName+"."+binding.Field+".reverse", "reverse_name_collision", key.name)
			}
			names[key] = true
		}
	}
	return bindings, nil
}
