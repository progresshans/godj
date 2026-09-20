package orm

import (
	"math"

	"github.com/progresshans/godj/internal/floatvalue"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type floatField[M any] struct{ field[M] }
type FloatField[M any] struct{ floatField[M] }
type NullableFloatField[M any] struct{ floatField[M] }

func NewFloatField[M any](metadata ir.Field) FloatField[M] {
	return FloatField[M]{floatField[M]{newField[M](metadata, query.FieldFloat, ir.FieldFloat, false)}}
}
func NewNullableFloatField[M any](metadata ir.Field) NullableFloatField[M] {
	return NullableFloatField[M]{floatField[M]{newField[M](metadata, query.FieldFloat, ir.FieldFloat, true)}}
}

func (f floatField[M]) Exact(value float64) Predicate[M] {
	return f.predicate(query.LookupExact, query.Float(value))
}
func (f floatField[M]) GreaterThan(value float64) Predicate[M] {
	return f.predicate(query.LookupGreaterThan, query.Float(value))
}
func (f floatField[M]) GreaterThanOrEqual(value float64) Predicate[M] {
	return f.predicate(query.LookupGreaterThanOrEqual, query.Float(value))
}
func (f floatField[M]) LessThan(value float64) Predicate[M] {
	return f.predicate(query.LookupLessThan, query.Float(value))
}
func (f floatField[M]) LessThanOrEqual(value float64) Predicate[M] {
	return f.predicate(query.LookupLessThanOrEqual, query.Float(value))
}
func (f floatField[M]) IsNull(value bool) Predicate[M] {
	return f.predicate(query.LookupIsNull, query.Boolean(value))
}
func (f floatField[M]) Asc() Ordering[M]                        { return f.ordering(query.Ascending) }
func (f floatField[M]) Desc() Ordering[M]                       { return f.ordering(query.Descending) }
func (f floatField[M]) writableField(M) (query.FieldRef, error) { return f.reference, f.err }
func (f floatField[M]) referenceField(M, float64) (query.FieldRef, error) {
	return f.reference, f.err
}
func (f floatField[M]) ExactField(right FieldReference[M, float64]) Predicate[M] {
	return f.fieldPredicate(query.LookupExact, right.reference, right.err)
}
func (f floatField[M]) GreaterThanField(right FieldReference[M, float64]) Predicate[M] {
	return f.fieldPredicate(query.LookupGreaterThan, right.reference, right.err)
}
func (f floatField[M]) GreaterThanOrEqualField(right FieldReference[M, float64]) Predicate[M] {
	return f.fieldPredicate(query.LookupGreaterThanOrEqual, right.reference, right.err)
}
func (f floatField[M]) LessThanField(right FieldReference[M, float64]) Predicate[M] {
	return f.fieldPredicate(query.LookupLessThan, right.reference, right.err)
}
func (f floatField[M]) LessThanOrEqualField(right FieldReference[M, float64]) Predicate[M] {
	return f.fieldPredicate(query.LookupLessThanOrEqual, right.reference, right.err)
}

// FloatScanner accepts native numeric driver values and canonicalizes NaN.
// Text and NULL cannot silently become a present zero value.
type FloatScanner struct{ Float float64 }

func (scanner *FloatScanner) Scan(raw any) error {
	if scanner == nil {
		return floatvalue.ErrInvalid
	}
	scanner.Float = 0
	value, err := scanFloat(raw)
	if err != nil {
		return err
	}
	scanner.Float = value
	return nil
}

type NullableFloatScanner struct {
	Float float64
	Valid bool
}

func (scanner *NullableFloatScanner) Scan(raw any) error {
	if scanner == nil {
		return floatvalue.ErrInvalid
	}
	scanner.Float, scanner.Valid = 0, false
	if raw == nil {
		return nil
	}
	value, err := scanFloat(raw)
	if err != nil {
		return err
	}
	scanner.Float, scanner.Valid = value, true
	return nil
}
func scanFloat(raw any) (float64, error) {
	var value float64
	switch typed := raw.(type) {
	case float64:
		value = typed
	case int64:
		value = float64(typed)
	default:
		return 0, floatvalue.ErrInvalid
	}
	return math.Float64frombits(floatvalue.CanonicalBits(value)), nil
}

func (f FloatField[M]) scalarResultField(M, float64) (query.FieldRef, func() scalarCell[float64], error) {
	return f.reference, func() scalarCell[float64] {
		var value FloatScanner
		return scalarCell[float64]{destination: &value, value: func() float64 { return value.Float }}
	}, f.err
}
func (f NullableFloatField[M]) scalarResultField(M, *float64) (query.FieldRef, func() scalarCell[*float64], error) {
	return f.reference, func() scalarCell[*float64] {
		var value NullableFloatScanner
		return scalarCell[*float64]{destination: &value, value: func() *float64 {
			if !value.Valid {
				return nil
			}
			copy := value.Float
			return &copy
		}}
	}, f.err
}
func (f floatField[M]) scalarOrderedField(M, float64) (query.FieldRef, func() scalarCell[Optional[float64]], error) {
	return f.reference, func() scalarCell[Optional[float64]] {
		var value NullableFloatScanner
		return scalarCell[Optional[float64]]{destination: &value, value: func() Optional[float64] {
			return Optional[float64]{value: value.Float, valid: value.Valid}
		}}
	}, f.err
}

func (f floatField[M]) In(values ...float64) Predicate[M] {
	return membershipPredicate(f.field, values, query.Float)
}
