package orm

import (
	"database/sql"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// NullableBooleanField reads *bool while comparisons use bool. A nil model
// pointer represents SQL NULL; false remains an ordinary selected value.
type NullableBooleanField[M any] struct{ field[M] }

func NewNullableBooleanField[M any](metadata ir.Field) NullableBooleanField[M] {
	return NullableBooleanField[M]{field: newField[M](metadata, query.FieldBoolean, ir.FieldBoolean, true)}
}

func (f NullableBooleanField[M]) Exact(value bool) Predicate[M] {
	return f.field.predicate(query.LookupExact, query.Boolean(value))
}

func (f NullableBooleanField[M]) IsNull(value bool) Predicate[M] {
	return f.field.predicate(query.LookupIsNull, query.Boolean(value))
}

func (f NullableBooleanField[M]) In(values ...bool) Predicate[M] {
	return membershipPredicate(f.field, values, query.Boolean)
}

func (f NullableBooleanField[M]) Asc() Ordering[M]  { return f.field.ordering(query.Ascending) }
func (f NullableBooleanField[M]) Desc() Ordering[M] { return f.field.ordering(query.Descending) }

func (f NullableBooleanField[M]) writableField(M) (query.FieldRef, error) {
	return f.reference, f.err
}

// BooleanLookupField is the sealed Boolean source capability for forward
// relation lookups. It does not grant unsupported Boolean F comparisons.
type BooleanLookupField[M any] interface {
	booleanLookupField(M) (query.FieldRef, error)
}

func (f NullableBooleanField[M]) booleanLookupField(M) (query.FieldRef, error) {
	return f.reference, f.err
}

func (f BooleanField[M]) booleanLookupField(M) (query.FieldRef, error) {
	return f.reference, f.err
}

func (f NullableBooleanField[M]) scalarResultField(M, *bool) (query.FieldRef, func() scalarCell[*bool], error) {
	return f.reference, func() scalarCell[*bool] {
		var value sql.NullBool
		return scalarCell[*bool]{
			destination: &value,
			value: func() *bool {
				if !value.Valid {
					return nil
				}
				copy := value.Bool
				return &copy
			},
		}
	}, f.err
}
