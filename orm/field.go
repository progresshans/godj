package orm

import (
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type field[M any] struct {
	reference query.FieldRef
	err       error
	marker    [0]func(M)
}

// AutoField exposes integer reads without the writable update-mask capability.
type AutoField[M any] struct{ integerField[M] }

// IntegerField and NullableIntegerField are ordinary writable signed scalars.
type IntegerField[M any] struct{ integerField[M] }
type NullableIntegerField[M any] struct{ integerField[M] }

// Integer comparisons share a value type even when reads are nullable.
type integerField[M any] struct{ field[M] }
type StringField[M any] struct{ field[M] }
type NullableStringField[M any] struct{ field[M] }
type BooleanField[M any] struct{ field[M] }

// WritableField is the sealed, model-specific field capability accepted by
// Save update masks. The private method prevents arbitrary external field
// implementations, and its M argument prevents fields for different models
// from satisfying the same instantiated interface. AutoField deliberately does
// not implement this interface: primary keys are never writable through update_fields.
type WritableField[M any] interface {
	writableField(M) (query.FieldRef, error)
}

type Predicate[M any] struct {
	expression query.Expression
	err        error
	marker     [0]func(M)
}

type Ordering[M any] struct {
	ordering query.Ordering
	err      error
	marker   [0]func(M)
}

func NewAutoField[M any](metadata ir.Field) AutoField[M] {
	return AutoField[M]{integerField[M]{newField[M](metadata, query.FieldInteger, ir.FieldAuto, false)}}
}

func NewIntegerField[M any](metadata ir.Field) IntegerField[M] {
	return IntegerField[M]{integerField[M]{newField[M](metadata, query.FieldInteger, ir.FieldInteger, false)}}
}

func NewNullableIntegerField[M any](metadata ir.Field) NullableIntegerField[M] {
	return NullableIntegerField[M]{integerField[M]{newField[M](metadata, query.FieldInteger, ir.FieldInteger, true)}}
}

func NewStringField[M any](metadata ir.Field) StringField[M] {
	return StringField[M]{field: newStringField[M](metadata, false)}
}

func NewNullableStringField[M any](metadata ir.Field) NullableStringField[M] {
	return NullableStringField[M]{field: newStringField[M](metadata, true)}
}

func newStringField[M any](metadata ir.Field, nullable bool) field[M] {
	kind := ir.FieldChar
	if metadata.Kind == ir.FieldText {
		kind = ir.FieldText
	}
	return newField[M](metadata, query.FieldString, kind, nullable)
}

func NewBooleanField[M any](metadata ir.Field) BooleanField[M] {
	return BooleanField[M]{field: newField[M](metadata, query.FieldBoolean, ir.FieldBoolean, false)}
}

func (f integerField[M]) Exact(value int64) Predicate[M] {
	return f.field.predicate(query.LookupExact, query.Integer(value))
}

func (f integerField[M]) GreaterThan(value int64) Predicate[M] {
	return f.field.predicate(query.LookupGreaterThan, query.Integer(value))
}

func (f integerField[M]) GreaterThanOrEqual(value int64) Predicate[M] {
	return f.field.predicate(query.LookupGreaterThanOrEqual, query.Integer(value))
}

func (f integerField[M]) LessThan(value int64) Predicate[M] {
	return f.field.predicate(query.LookupLessThan, query.Integer(value))
}

func (f integerField[M]) LessThanOrEqual(value int64) Predicate[M] {
	return f.field.predicate(query.LookupLessThanOrEqual, query.Integer(value))
}

func (f integerField[M]) IsNull(value bool) Predicate[M] {
	return f.field.predicate(query.LookupIsNull, query.Boolean(value))
}

func (f integerField[M]) Asc() Ordering[M]  { return f.field.ordering(query.Ascending) }
func (f integerField[M]) Desc() Ordering[M] { return f.field.ordering(query.Descending) }

func (f StringField[M]) Exact(value string) Predicate[M] {
	return f.field.predicate(query.LookupExact, query.String(value))
}

func (f StringField[M]) GreaterThan(value string) Predicate[M] {
	return f.field.predicate(query.LookupGreaterThan, query.String(value))
}

func (f StringField[M]) GreaterThanOrEqual(value string) Predicate[M] {
	return f.field.predicate(query.LookupGreaterThanOrEqual, query.String(value))
}

func (f StringField[M]) LessThan(value string) Predicate[M] {
	return f.field.predicate(query.LookupLessThan, query.String(value))
}

func (f StringField[M]) LessThanOrEqual(value string) Predicate[M] {
	return f.field.predicate(query.LookupLessThanOrEqual, query.String(value))
}

func (f StringField[M]) IContains(value string) Predicate[M] {
	return f.field.predicate(query.LookupIContains, query.String(value))
}

func (f StringField[M]) IsNull(value bool) Predicate[M] {
	return f.field.predicate(query.LookupIsNull, query.Boolean(value))
}

func (f StringField[M]) Asc() Ordering[M]  { return f.field.ordering(query.Ascending) }
func (f StringField[M]) Desc() Ordering[M] { return f.field.ordering(query.Descending) }

func (f NullableStringField[M]) Exact(value string) Predicate[M] {
	return f.field.predicate(query.LookupExact, query.String(value))
}

func (f NullableStringField[M]) GreaterThan(value string) Predicate[M] {
	return f.field.predicate(query.LookupGreaterThan, query.String(value))
}

func (f NullableStringField[M]) GreaterThanOrEqual(value string) Predicate[M] {
	return f.field.predicate(query.LookupGreaterThanOrEqual, query.String(value))
}

func (f NullableStringField[M]) LessThan(value string) Predicate[M] {
	return f.field.predicate(query.LookupLessThan, query.String(value))
}

func (f NullableStringField[M]) LessThanOrEqual(value string) Predicate[M] {
	return f.field.predicate(query.LookupLessThanOrEqual, query.String(value))
}

func (f NullableStringField[M]) IContains(value string) Predicate[M] {
	return f.field.predicate(query.LookupIContains, query.String(value))
}

func (f NullableStringField[M]) IsNull(value bool) Predicate[M] {
	return f.field.predicate(query.LookupIsNull, query.Boolean(value))
}

func (f NullableStringField[M]) Asc() Ordering[M] {
	return f.field.ordering(query.Ascending)
}

func (f NullableStringField[M]) Desc() Ordering[M] {
	return f.field.ordering(query.Descending)
}

func (f BooleanField[M]) Exact(value bool) Predicate[M] {
	return f.field.predicate(query.LookupExact, query.Boolean(value))
}

func (f BooleanField[M]) IsNull(value bool) Predicate[M] {
	return f.field.predicate(query.LookupIsNull, query.Boolean(value))
}

func (f BooleanField[M]) Asc() Ordering[M]  { return f.field.ordering(query.Ascending) }
func (f BooleanField[M]) Desc() Ordering[M] { return f.field.ordering(query.Descending) }

func (f IntegerField[M]) writableField(M) (query.FieldRef, error) {
	return f.reference, f.err
}

func (f NullableIntegerField[M]) writableField(M) (query.FieldRef, error) {
	return f.reference, f.err
}

func (f StringField[M]) writableField(M) (query.FieldRef, error) {
	return f.reference, f.err
}

func (f NullableStringField[M]) writableField(M) (query.FieldRef, error) {
	return f.reference, f.err
}

func (f BooleanField[M]) writableField(M) (query.FieldRef, error) {
	return f.reference, f.err
}

func newField[M any](metadata ir.Field, kind query.FieldKind, expectedKind ir.FieldKind, expectedNullable bool) field[M] {
	result := field[M]{reference: query.NewFieldRef(metadata.Name, metadata.Column, kind, metadata.Nullable)}
	if metadata.Kind != expectedKind || metadata.Nullable != expectedNullable || metadata.Name == "" || metadata.Column == "" {
		result.err = &query.Error{
			Category: query.CategoryQuery,
			Code:     query.CodeInvalidPlan,
			Field:    metadata.Name,
			Detail:   "typed field constructor does not match normalized field metadata",
		}
	}
	return result
}

func (f field[M]) predicate(lookup query.Lookup, value query.Value) Predicate[M] {
	return predicateFromCondition[M](query.NewCondition(f.reference, lookup, value), f.err)
}

func (f field[M]) fieldPredicate(lookup query.Lookup, right query.FieldRef, rightErr error) Predicate[M] {
	if f.err != nil {
		return Predicate[M]{err: f.err}
	}
	if rightErr != nil {
		return Predicate[M]{err: rightErr}
	}
	condition, err := query.NewFieldCondition(f.reference, lookup, right)
	return predicateFromCondition[M](condition, err)
}

func (f field[M]) ordering(direction query.Direction) Ordering[M] {
	return Ordering[M]{ordering: query.NewOrdering(f.reference, direction), err: f.err}
}
