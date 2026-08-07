// Package schema provides the declarative Go DSL used to build GoDj's schema
// IR. The DSL deliberately contains data only: it never imports generated
// model packages.
package schema

import "github.com/progresshans/godj/schema/ir"

type Definition struct {
	AppLabel string
	Models   []Model
}

type Model struct {
	Name    string
	GoName  string
	DBTable string
	Fields  []Field
}

type Field struct {
	Name      string
	GoName    string
	Column    string
	Kind      ir.FieldKind
	Nullable  bool
	MaxLength int
}

type FieldOption func(*Field)

func Nullable() FieldOption {
	return func(field *Field) {
		field.Nullable = true
	}
}

func Column(name string) FieldOption {
	return func(field *Field) {
		field.Column = name
	}
}

func CharField(name, goName string, maxLength int, options ...FieldOption) Field {
	return newField(name, goName, ir.FieldChar, maxLength, options)
}

func BooleanField(name, goName string, options ...FieldOption) Field {
	return newField(name, goName, ir.FieldBoolean, 0, options)
}

func AutoField(name, goName string, options ...FieldOption) Field {
	return newField(name, goName, ir.FieldAuto, 0, options)
}

func Build(definition Definition) (ir.Schema, error) {
	result := ir.Schema{
		FormatVersion: ir.FormatVersion,
		AppLabel:      definition.AppLabel,
		Models:        make([]ir.Model, len(definition.Models)),
	}
	for modelIndex, model := range definition.Models {
		result.Models[modelIndex] = ir.Model{
			Name:    model.Name,
			GoName:  model.GoName,
			DBTable: model.DBTable,
			Fields:  make([]ir.Field, len(model.Fields)),
		}
		for fieldIndex, field := range model.Fields {
			result.Models[modelIndex].Fields[fieldIndex] = ir.Field{
				Name:       field.Name,
				GoName:     field.GoName,
				Column:     field.Column,
				Kind:       field.Kind,
				PrimaryKey: field.Kind == ir.FieldAuto,
				Nullable:   field.Nullable,
				MaxLength:  field.MaxLength,
			}
		}
	}
	return ir.Normalize(result)
}

func newField(name, goName string, kind ir.FieldKind, maxLength int, options []FieldOption) Field {
	field := Field{Name: name, GoName: goName, Kind: kind, MaxLength: maxLength}
	for _, option := range options {
		if option != nil {
			option(&field)
		}
	}
	return field
}
