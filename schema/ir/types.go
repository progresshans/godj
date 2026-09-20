// Package ir defines GoDj's normalized, versioned schema intermediate
// representation. It is the canonical input to code generation and runtime
// metadata; declaration packages and generated packages do not import each
// other.
package ir

import (
	"github.com/progresshans/godj/decimal"
	"slices"
)

// CurrentFormatVersion is the only accepted format in current development.
// Scalar and relation-bearing schemas use the same
// normalized representation; relation presence is a field property, not a
// format generation.
const CurrentFormatVersion = 1

type Schema struct {
	FormatVersion int     `json:"format_version"`
	AppLabel      string  `json:"app_label"`
	Models        []Model `json:"models"`
}

type Model struct {
	Name    string  `json:"name"`
	GoName  string  `json:"go_name"`
	DBTable string  `json:"db_table"`
	Fields  []Field `json:"fields"`
}

type FieldKind string

const (
	FieldAuto       FieldKind = "auto"
	FieldInteger    FieldKind = "integer"
	FieldFloat      FieldKind = "float"
	FieldDecimal    FieldKind = "decimal"
	FieldUUID       FieldKind = "uuid"
	FieldChar       FieldKind = "char"
	FieldText       FieldKind = "text"
	FieldDuration   FieldKind = "duration"
	FieldTime       FieldKind = "time"
	FieldDate       FieldKind = "date"
	FieldDateTime   FieldKind = "datetime"
	FieldBoolean    FieldKind = "boolean"
	FieldForeignKey FieldKind = "foreign_key"
)

type ModelIdentity struct {
	AppLabel  string `json:"app_label"`
	ModelName string `json:"model_name"`
}

type RelationCardinality string

const (
	RelationManyToOne RelationCardinality = "many_to_one"
	RelationOneToMany RelationCardinality = "one_to_many"
)

type DeletePolicy string

const (
	DeleteProtect DeletePolicy = "protect"
	DeleteSetNull DeletePolicy = "set_null"
)

type ReverseRelation struct {
	Name     string `json:"name,omitempty"`
	Disabled bool   `json:"disabled,omitempty"`
}

type ForeignKeyRelation struct {
	Target      ModelIdentity       `json:"target"`
	Cardinality RelationCardinality `json:"cardinality"`
	Reverse     ReverseRelation     `json:"reverse"`
	OnDelete    DeletePolicy        `json:"on_delete"`
}

// ScalarKind identifies a concrete value in defaults and choice metadata.
// An enclosing pointer records default presence; false and "" are values.
type ScalarKind string

const (
	ScalarString   ScalarKind = "string"
	ScalarBoolean  ScalarKind = "boolean"
	ScalarInteger  ScalarKind = "integer"
	ScalarFloat    ScalarKind = "float"
	ScalarDecimal  ScalarKind = "decimal"
	ScalarUUID     ScalarKind = "uuid"
	ScalarDuration ScalarKind = "duration"
	ScalarTime     ScalarKind = "time"
	ScalarDate     ScalarKind = "date"
	ScalarDateTime ScalarKind = "datetime"
)

type Scalar struct {
	Kind      ScalarKind `json:"kind"`
	String    string     `json:"string,omitempty"`
	Date      string     `json:"date,omitempty"`
	Time      string     `json:"time,omitempty"`
	Duration  string     `json:"duration,omitempty"`
	DateTime  string     `json:"datetime,omitempty"`
	Boolean   bool       `json:"boolean,omitempty"`
	Integer   int64      `json:"integer,omitempty"`
	FloatBits string     `json:"float_bits,omitempty"`
	Decimal   string     `json:"decimal,omitempty"`
	UUID      string     `json:"uuid,omitempty"`
}

// Choice pairs a stored scalar with its presentation label. Declaration order
// is significant for form options and historical schema identity.
type Choice struct {
	Value Scalar `json:"value"`
	Label string `json:"label"`
}

// DecimalSpec is the declared exact storage precision, independent of value
// normalization and the transport's submitted coefficient/exponent.
type DecimalSpec struct {
	MaxDigits     int `json:"max_digits"`
	DecimalPlaces int `json:"decimal_places"`
}

func (spec DecimalSpec) Valid() bool {
	return spec.MaxDigits >= 1 && spec.MaxDigits <= decimal.MaxDigits && spec.DecimalPlaces >= 0 && spec.DecimalPlaces <= spec.MaxDigits
}

type Field struct {
	Name       string              `json:"name"`
	GoName     string              `json:"go_name"`
	Column     string              `json:"column"`
	Kind       FieldKind           `json:"kind"`
	PrimaryKey bool                `json:"primary_key"`
	Nullable   bool                `json:"nullable"`
	MaxLength  int                 `json:"max_length,omitempty"`
	Decimal    *DecimalSpec        `json:"decimal,omitempty"`
	Default    *Scalar             `json:"default,omitempty"`
	Choices    []Choice            `json:"choices,omitempty"`
	Relation   *ForeignKeyRelation `json:"relation,omitempty"`
}

func (s Schema) Clone() Schema {
	clone := s
	clone.Models = make([]Model, len(s.Models))
	for index := range s.Models {
		clone.Models[index] = s.Models[index].Clone()
	}
	return clone
}

func (m Model) Clone() Model {
	clone := m
	clone.Fields = append([]Field(nil), m.Fields...)
	for index := range m.Fields {
		clone.Fields[index] = m.Fields[index].Clone()
	}
	return clone
}

func (f Field) Clone() Field {
	clone := f
	if f.Decimal != nil {
		value := *f.Decimal
		clone.Decimal = &value
	}
	if f.Choices != nil {
		clone.Choices = make([]Choice, len(f.Choices))
		copy(clone.Choices, f.Choices)
	}
	if f.Default != nil {
		value := *f.Default
		clone.Default = &value
	}
	if f.Relation != nil {
		value := *f.Relation
		clone.Relation = &value
	}
	return clone
}

// Equal compares metadata values, including the ordered choices, rather than
// treating independently owned default/relation pointers as different fields.
func (f Field) Equal(other Field) bool {
	return f.Name == other.Name && f.GoName == other.GoName && f.Column == other.Column &&
		f.Kind == other.Kind && f.PrimaryKey == other.PrimaryKey && f.Nullable == other.Nullable && f.MaxLength == other.MaxLength &&
		equalOptional(f.Decimal, other.Decimal) && equalOptional(f.Default, other.Default) && equalOptional(f.Relation, other.Relation) &&
		(f.Choices == nil) == (other.Choices == nil) && slices.Equal(f.Choices, other.Choices)
}

func equalOptional[T comparable](left, right *T) bool {
	return left == right || left != nil && right != nil && *left == *right
}
