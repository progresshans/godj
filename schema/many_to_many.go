package schema

import "github.com/progresshans/godj/schema/ir"

type ManyToManyOption func(*ir.ManyToManyField)

type ManyToManyField = ir.ManyToManyField

// ManyToMany declares a columnless collection. The default intermediary is
// automatic; Through selects an existing model without rewriting its rows.
// A self relation is symmetric by default; Directed makes its reverse separate.
func ManyToMany(name, goName string, target ModelTarget, reverse ReverseRelation, options ...ManyToManyOption) ir.ManyToManyField {
	field := ir.ManyToManyField{Name: name, GoName: goName, Target: target, Reverse: reverse}
	for _, option := range options {
		if option != nil {
			option(&field)
		}
	}
	return field
}

func Through(model ModelTarget, sourceField, targetField string) ManyToManyOption {
	return func(field *ir.ManyToManyField) {
		field.Through = &ir.ThroughModel{Model: model, SourceField: sourceField, TargetField: targetField}
	}
}

func Directed() ManyToManyOption {
	return func(field *ir.ManyToManyField) { field.Symmetry = ir.ManyToManyDirected }
}
