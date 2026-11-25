package ir

import "fmt"

// ModelRename maps the identities of one declared model and all of its
// automatic intermediaries. Physical column and through-field names are stable.
type ModelRename struct {
	targets map[ModelIdentity]ModelIdentity
	tables  map[string]bool
}

// NewModelRename validates a pair of normalized historical declarations.
// A source-derived reverse name can follow its owner or retain the explicit
// old spelling. The authored destination resolves that choice.
func NewModelRename(app string, before, after Model) (ModelRename, error) {
	if before.Name == after.Name || len(before.Fields) != len(after.Fields) || len(before.ManyToMany) != len(after.ManyToMany) {
		return ModelRename{}, fmt.Errorf("model rename requires a new logical name and the same fields")
	}
	for _, model := range []Model{before, after} {
		s, err := Normalize(Schema{FormatVersion: CurrentFormatVersion, AppLabel: app, Models: []Model{model}})
		if err != nil {
			return ModelRename{}, err
		}
		if !s.Models[0].Equal(model) {
			return ModelRename{}, fmt.Errorf("model rename requires exact normalized declarations")
		}
	}
	old := ModelIdentity{AppLabel: app, ModelName: before.Name}
	next := ModelIdentity{AppLabel: app, ModelName: after.Name}
	change := ModelRename{targets: map[ModelIdentity]ModelIdentity{old: next}, tables: map[string]bool{after.DBTable: true}}
	for _, field := range before.ManyToMany {
		if field.Through == nil {
			change.targets[field.StorageThrough(old).Model] = field.StorageThrough(next).Model
			newTable := automaticTableName(after, field)
			change.tables[newTable] = true
		}
	}
	if before.DBTable != after.DBTable {
		if err := change.CheckOtherStorage(before); err != nil {
			return ModelRename{}, err
		}
		change.tables[before.DBTable] = true
		for _, f := range before.ManyToMany {
			if f.Through == nil {
				change.tables[automaticTableName(before, f)] = true
			}
		}
	}
	want := before.Clone()
	change.rebind(&want)
	want.Name, want.GoName, want.DBTable = after.Name, after.GoName, after.DBTable
	for i, f := range want.Fields {
		other := after.Fields[i].Relation
		if f.Relation != nil && other != nil && f.Relation.Cardinality == RelationOneToOne &&
			f.Relation.Reverse == (ReverseRelation{Name: before.Name}) && other.Reverse == (ReverseRelation{Name: after.Name}) {
			want.Fields[i].Relation.Reverse = other.Reverse
		}
	}
	oldReverse, nextReverse := defaultManyReverse(before.Name, ManyToManyDirected), defaultManyReverse(after.Name, ManyToManyDirected)
	for i, field := range want.ManyToMany {
		if field.Symmetry == ManyToManyDirected && field.Reverse == oldReverse && after.ManyToMany[i].Reverse == nextReverse {
			want.ManyToMany[i].Reverse = after.ManyToMany[i].Reverse
		}
	}
	if !want.Equal(after) {
		return ModelRename{}, fmt.Errorf("model rename changes unrelated fields, relationships or constraints")
	}
	return change, nil
}

// CheckOtherStorage rejects a shared source or destination in any installed
// app, including an automatic intermediary whose declaration is not the root.
func (change ModelRename) CheckOtherStorage(owner Model) error {
	if change.tables[owner.DBTable] {
		return fmt.Errorf("model rename table %q has overlapping ownership", owner.DBTable)
	}
	for _, f := range owner.ManyToMany {
		if f.Through == nil {
			name := automaticTableName(owner, f)
			if change.tables[name] {
				return fmt.Errorf("model rename table %q has overlapping intermediary ownership", name)
			}
		}
	}
	return nil
}

// Target returns the identity after this rename, including unchanged targets.
func (change ModelRename) Target(id ModelIdentity) ModelIdentity {
	if next, found := change.targets[id]; found {
		return next
	}
	return id
}

// RebindReferences returns an owned copy only when this model refers to a
// renamed identity. A declared through model keeps the same endpoint fields.
func (change ModelRename) RebindReferences(model Model) (Model, bool) {
	for _, f := range model.Fields {
		if f.Relation != nil && change.Target(f.Relation.Target) != f.Relation.Target {
			model = model.Clone()
			change.rebind(&model)
			return model, true
		}
	}
	for _, f := range model.ManyToMany {
		if change.Target(f.Target) != f.Target || f.Through != nil && change.Target(f.Through.Model) != f.Through.Model {
			model = model.Clone()
			change.rebind(&model)
			return model, true
		}
	}
	return model, false
}

func (change ModelRename) rebind(model *Model) {
	for _, f := range model.Fields {
		if f.Relation != nil {
			f.Relation.Target = change.Target(f.Relation.Target)
		}
	}
	for i := range model.ManyToMany {
		f := &model.ManyToMany[i]
		f.Target = change.Target(f.Target)
		if f.Through != nil {
			f.Through.Model = change.Target(f.Through.Model)
		}
	}
}
