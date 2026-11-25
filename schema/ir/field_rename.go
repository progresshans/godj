package ir

import "fmt"

// ValidateFieldRename compares two complete, normalized declarations. A
// separate AlterField is needed to change a default, type or relation policy.
func ValidateFieldRename(before, after Field) error {
	for _, field := range []Field{before, after} {
		normalized, err := NormalizeField(field)
		if err != nil {
			return err
		}
		if !normalized.Equal(field) {
			return fmt.Errorf("RenameField requires exact normalized fields")
		}
	}
	if before.Name == after.Name {
		return fmt.Errorf("RenameField requires different logical names")
	}
	renamed := before
	renamed.Name, renamed.GoName, renamed.Column = after.Name, after.GoName, after.Column
	if !renamed.Equal(after) {
		return fmt.Errorf("RenameField changes field properties outside its identity")
	}
	return nil
}

// RenameThroughField rewrites only explicit through bindings to this field
// of this model. It returns the original value when no binding uses it.
func RenameThroughField(owner Model, through ModelIdentity, from, to string) (Model, bool) {
	changed := false
	for i, many := range owner.ManyToMany {
		link := many.Through
		if link == nil || link.Model != through || (link.SourceField != from && link.TargetField != from) {
			continue
		}
		if !changed {
			owner = owner.Clone()
			changed = true
		}
		link = owner.ManyToMany[i].Through
		if link.SourceField == from {
			link.SourceField = to
		}
		if link.TargetField == from {
			link.TargetField = to
		}
	}
	return owner, changed
}

// RenameModelField preserves declaration order, named unique membership and
// ImageField dimension ownership. The returned model owns all its metadata.
func RenameModelField(app string, model Model, before, after Field) (Model, error) {
	if err := ValidateFieldRename(before, after); err != nil {
		return Model{}, err
	}
	position := -1
	for i, field := range model.Fields {
		if field.Name == before.Name {
			if !field.Equal(before) {
				return Model{}, fmt.Errorf("field %s.%s.%s differs from its rename source", app, model.Name, before.Name)
			}
			position = i
		}
	}
	if position < 0 {
		return Model{}, fmt.Errorf("rename source %s.%s.%s does not exist", app, model.Name, before.Name)
	}
	out, changed := RenameThroughField(model, ModelIdentity{AppLabel: app, ModelName: model.Name}, before.Name, after.Name)
	if !changed {
		out = model.Clone()
	}
	out.Fields[position] = after.Clone()
	for i := range out.Fields {
		f := &out.Fields[i]
		if f.WidthField == before.Name {
			f.WidthField = after.Name
		}
		if f.HeightField == before.Name {
			f.HeightField = after.Name
		}
	}
	for _, constraint := range out.UniqueConstraints {
		for i, name := range constraint.Fields {
			if name == before.Name {
				constraint.Fields[i] = after.Name
			}
		}
	}
	normalized, err := Normalize(Schema{FormatVersion: CurrentFormatVersion, AppLabel: app, Models: []Model{out}})
	if err != nil {
		return Model{}, err
	}
	if !normalized.Models[0].Equal(out) {
		return Model{}, fmt.Errorf("renamed model differs from its normalized declaration")
	}
	return out, nil
}
