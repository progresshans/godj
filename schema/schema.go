// Package schema provides the declarative Go DSL used to build GoDj's schema
// IR. The DSL deliberately contains data only: it never imports generated
// model packages.
package schema

import (
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/internal/floatvalue"
	"github.com/progresshans/godj/internal/temporal"
	"github.com/progresshans/godj/schema/ir"
	"time"
)

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
	Decimal   *ir.DecimalSpec
	Default   *ir.Scalar
	Choices   []ir.Choice
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
// types keep defaults explicit while preserving zero, false and empty string
// as present values in the current Schema IR.
type DefaultScalar interface {
	string | bool | int64 | float64 | time.Time | calendar.Date | clock.Time | duration.Duration | decimal.Decimal
}

func Default[T DefaultScalar](value T) FieldOption {
	return func(field *Field) {
		switch typed := any(value).(type) {
		case string:
			field.Default = &ir.Scalar{Kind: ir.ScalarString, String: typed}
		case bool:
			field.Default = &ir.Scalar{Kind: ir.ScalarBoolean, Boolean: typed}
		case decimal.Decimal:
			field.Default = &ir.Scalar{Kind: ir.ScalarDecimal}
			if typed.Valid() {
				field.Default.Decimal = typed.String()
			}
		case duration.Duration:
			field.Default = &ir.Scalar{Kind: ir.ScalarDuration}
			if typed.Valid() {
				field.Default.Duration = typed.String()
			}
		case clock.Time:
			field.Default = &ir.Scalar{Kind: ir.ScalarTime}
			if typed.Valid() {
				field.Default.Time = typed.String()
			}
		case calendar.Date:
			field.Default = &ir.Scalar{Kind: ir.ScalarDate}
			if typed.Valid() {
				field.Default.Date = typed.String()
			}
		case time.Time:
			canonical, err := temporal.Canonical(typed)
			field.Default = &ir.Scalar{Kind: ir.ScalarDateTime}
			if err == nil {
				field.Default.DateTime = temporal.Format(canonical)
			}
		case float64:
			field.Default = &ir.Scalar{Kind: ir.ScalarFloat, FloatBits: floatvalue.Bits(typed)}
		case int64:
			field.Default = &ir.Scalar{Kind: ir.ScalarInteger, Integer: typed}
		}
	}
}

func CharField(name, goName string, maxLength int, options ...FieldOption) Field {
	return newField(name, goName, ir.FieldChar, maxLength, options)
}

// TextField stores a Unicode string without a declared storage length limit.
// HTTP and form input budgets remain explicit application choices.
func TextField(name, goName string, options ...FieldOption) Field {
	return newField(name, goName, ir.FieldText, 0, options)
}

// DecimalField stores finite exact values within an explicit precision/scale.
func DecimalField(name, goName string, maxDigits, decimalPlaces int, options ...FieldOption) Field {
	field := Field{Name: name, GoName: goName, Kind: ir.FieldDecimal, Decimal: &ir.DecimalSpec{MaxDigits: maxDigits, DecimalPlaces: decimalPlaces}}
	for _, option := range options {
		if option != nil {
			option(&field)
		}
	}
	return field
}

// FloatField stores a binary64 value. Each backend owns its storage limits.
func FloatField(name, goName string, options ...FieldOption) Field {
	return newField(name, goName, ir.FieldFloat, 0, options)
}

// DurationField stores a normalized elapsed value with microsecond precision.
func DurationField(name, goName string, options ...FieldOption) Field {
	return newField(name, goName, ir.FieldDuration, 0, options)
}

// TimeField stores microsecond clock components without a date or time zone.
func TimeField(name, goName string, options ...FieldOption) Field {
	return newField(name, goName, ir.FieldTime, 0, options)
}

// DateField stores a Gregorian calendar day without a clock or time zone.
func DateField(name, goName string, options ...FieldOption) Field {
	return newField(name, goName, ir.FieldDate, 0, options)
}

// DateTimeField stores an instant as UTC microseconds; zero time is a value.
func DateTimeField(name, goName string, options ...FieldOption) Field {
	return newField(name, goName, ir.FieldDateTime, 0, options)
}

func BooleanField(name, goName string, options ...FieldOption) Field {
	return newField(name, goName, ir.FieldBoolean, 0, options)
}

func AutoField(name, goName string, options ...FieldOption) Field {
	return newField(name, goName, ir.FieldAuto, 0, options)
}

// IntegerField stores a signed 64-bit integer, independently of the Go target
// architecture. Unlike AutoField, it is an ordinary writable, optionally
// nullable scalar and may have an explicit int64 application default.
func IntegerField(name, goName string, options ...FieldOption) Field {
	return newField(name, goName, ir.FieldInteger, 0, options)
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
	result := ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
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
			var defaultValue *ir.Scalar
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
				Decimal:    field.Decimal,
				Default:    defaultValue,
				Choices:    field.Choices,
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
