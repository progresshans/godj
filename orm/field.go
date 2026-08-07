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

type IntegerField[M any] struct{ field[M] }
type StringField[M any] struct{ field[M] }
type NullableStringField[M any] struct{ field[M] }
type BooleanField[M any] struct{ field[M] }

// WritableField is the sealed, model-specific field capability accepted by
// Save update masks. The private method prevents arbitrary external field
// implementations, and its M argument prevents fields for different models
// from satisfying the same instantiated interface. IntegerField deliberately
// does not implement this interface because the current AutoField primary key
// is never writable through update_fields.
type WritableField[M any] interface {
	writableField(M) (query.FieldRef, error)
}

type Predicate[M any] struct {
	condition query.Condition
	err       error
	marker    [0]func(M)
}

type Ordering[M any] struct {
	ordering query.Ordering
	err      error
	marker   [0]func(M)
}

func NewIntegerField[M any](metadata ir.Field) IntegerField[M] {
	return IntegerField[M]{field: newField[M](metadata, query.FieldInteger, ir.FieldAuto, false)}
}

func NewStringField[M any](metadata ir.Field) StringField[M] {
	return StringField[M]{field: newField[M](metadata, query.FieldString, ir.FieldChar, false)}
}

func NewNullableStringField[M any](metadata ir.Field) NullableStringField[M] {
	return NullableStringField[M]{field: newField[M](metadata, query.FieldString, ir.FieldChar, true)}
}

func NewBooleanField[M any](metadata ir.Field) BooleanField[M] {
	return BooleanField[M]{field: newField[M](metadata, query.FieldBoolean, ir.FieldBoolean, false)}
}

func (f IntegerField[M]) Exact(value int64) Predicate[M] {
	return f.field.predicate(query.LookupExact, query.Integer(value))
}

func (f IntegerField[M]) IsNull(value bool) Predicate[M] {
	return f.field.predicate(query.LookupIsNull, query.Boolean(value))
}

func (f IntegerField[M]) Asc() Ordering[M]  { return f.field.ordering(query.Ascending) }
func (f IntegerField[M]) Desc() Ordering[M] { return f.field.ordering(query.Descending) }

func (f StringField[M]) Exact(value string) Predicate[M] {
	return f.field.predicate(query.LookupExact, query.String(value))
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
	return Predicate[M]{condition: query.NewCondition(f.reference, lookup, value), err: f.err}
}

func (f field[M]) ordering(direction query.Direction) Ordering[M] {
	return Ordering[M]{ordering: query.NewOrdering(f.reference, direction), err: f.err}
}
