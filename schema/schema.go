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
	Default   *ir.ScalarDefault
	Relation  *ir.ForeignKeyRelation
}

type ModelTarget = ir.ModelIdentity
type ReverseRelation = ir.ReverseRelation
type DeletePolicy = ir.DeletePolicy

const (
	Protect = ir.DeleteProtect
	SetNull = ir.DeleteSetNull
)

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

// Default records an explicitly typed application default. Exact scalar
// types keep the declaration surface small for the M2 field subset while
// preserving false and empty string as present values in Schema IR v2.
type DefaultScalar interface {
	string | bool | int64
}

func Default[T DefaultScalar](value T) FieldOption {
	return func(field *Field) {
		switch typed := any(value).(type) {
		case string:
			field.Default = &ir.ScalarDefault{Kind: ir.ScalarString, String: typed}
		case bool:
			field.Default = &ir.ScalarDefault{Kind: ir.ScalarBoolean, Boolean: typed}
		case int64:
			field.Default = &ir.ScalarDefault{Kind: ir.ScalarInteger, Integer: typed}
		}
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

func Target(appLabel, modelName string) ModelTarget {
	return ModelTarget{AppLabel: appLabel, ModelName: modelName}
}

func RelatedName(name string) ReverseRelation {
	return ReverseRelation{Name: name}
}

func NoReverse() ReverseRelation {
	return ReverseRelation{Disabled: true}
}

func ForeignKey(
	name, goName string,
	target ModelTarget,
	reverse ReverseRelation,
	onDelete DeletePolicy,
	options ...FieldOption,
) Field {
	field := Field{
		Name:   name,
		GoName: goName,
		Column: name + "_id",
		Kind:   ir.FieldForeignKey,
		Relation: &ir.ForeignKeyRelation{
			Target:      target,
			Cardinality: ir.RelationManyToOne,
			Reverse:     reverse,
			OnDelete:    onDelete,
		},
	}
	for _, option := range options {
		if option != nil {
			option(&field)
		}
	}
	return field
}

func Build(definition Definition) (ir.Schema, error) {
	formatVersion := ir.FormatVersion
	for _, model := range definition.Models {
		for _, field := range model.Fields {
			if field.Kind == ir.FieldForeignKey || field.Relation != nil {
				formatVersion = ir.RelationFormatVersion
			}
		}
	}
	result := ir.Schema{
		FormatVersion: formatVersion,
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
			var defaultValue *ir.ScalarDefault
			if field.Default != nil {
				copy := *field.Default
				defaultValue = &copy
			}
			var relation *ir.ForeignKeyRelation
			if field.Relation != nil {
				copy := *field.Relation
				relation = &copy
			}
			result.Models[modelIndex].Fields[fieldIndex] = ir.Field{
				Name:       field.Name,
				GoName:     field.GoName,
				Column:     field.Column,
				Kind:       field.Kind,
				PrimaryKey: field.Kind == ir.FieldAuto,
				Nullable:   field.Nullable,
				MaxLength:  field.MaxLength,
				Default:    defaultValue,
				Relation:   relation,
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
