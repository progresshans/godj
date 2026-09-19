package orm

import (
	"github.com/progresshans/godj/internal/temporal"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"time"
)

type dateTimeField[M any] struct{ field[M] }
type DateTimeField[M any] struct{ dateTimeField[M] }
type NullableDateTimeField[M any] struct{ dateTimeField[M] }

func NewDateTimeField[M any](metadata ir.Field) DateTimeField[M] {
	return DateTimeField[M]{dateTimeField[M]{newField[M](metadata, query.FieldDateTime, ir.FieldDateTime, false)}}
}
func NewNullableDateTimeField[M any](metadata ir.Field) NullableDateTimeField[M] {
	return NullableDateTimeField[M]{dateTimeField[M]{newField[M](metadata, query.FieldDateTime, ir.FieldDateTime, true)}}
}

func (f dateTimeField[M]) Exact(value time.Time) Predicate[M] {
	return f.predicate(query.LookupExact, query.DateTime(value))
}
func (f dateTimeField[M]) GreaterThan(value time.Time) Predicate[M] {
	return f.predicate(query.LookupGreaterThan, query.DateTime(value))
}
func (f dateTimeField[M]) GreaterThanOrEqual(value time.Time) Predicate[M] {
	return f.predicate(query.LookupGreaterThanOrEqual, query.DateTime(value))
}
func (f dateTimeField[M]) LessThan(value time.Time) Predicate[M] {
	return f.predicate(query.LookupLessThan, query.DateTime(value))
}
func (f dateTimeField[M]) LessThanOrEqual(value time.Time) Predicate[M] {
	return f.predicate(query.LookupLessThanOrEqual, query.DateTime(value))
}
func (f dateTimeField[M]) IsNull(value bool) Predicate[M] {
	return f.predicate(query.LookupIsNull, query.Boolean(value))
}
func (f dateTimeField[M]) Asc() Ordering[M]                        { return f.ordering(query.Ascending) }
func (f dateTimeField[M]) Desc() Ordering[M]                       { return f.ordering(query.Descending) }
func (f dateTimeField[M]) writableField(M) (query.FieldRef, error) { return f.reference, f.err }
func (f dateTimeField[M]) referenceField(M, time.Time) (query.FieldRef, error) {
	return f.reference, f.err
}
func (f dateTimeField[M]) ExactField(right FieldReference[M, time.Time]) Predicate[M] {
	return f.fieldPredicate(query.LookupExact, right.reference, right.err)
}
func (f dateTimeField[M]) GreaterThanField(right FieldReference[M, time.Time]) Predicate[M] {
	return f.fieldPredicate(query.LookupGreaterThan, right.reference, right.err)
}
func (f dateTimeField[M]) GreaterThanOrEqualField(right FieldReference[M, time.Time]) Predicate[M] {
	return f.fieldPredicate(query.LookupGreaterThanOrEqual, right.reference, right.err)
}
func (f dateTimeField[M]) LessThanField(right FieldReference[M, time.Time]) Predicate[M] {
	return f.fieldPredicate(query.LookupLessThan, right.reference, right.err)
}
func (f dateTimeField[M]) LessThanOrEqualField(right FieldReference[M, time.Time]) Predicate[M] {
	return f.fieldPredicate(query.LookupLessThanOrEqual, right.reference, right.err)
}

// DateTimeScanner is shared by generated descriptors and typed projections.
// It accepts native driver times and SQLite's text-valued MIN/MAX results.
// NULL is invalid here; NullableDateTimeScanner preserves it explicitly.
type DateTimeScanner struct{ Time time.Time }

func (scanner *DateTimeScanner) Scan(raw any) error {
	if scanner == nil {
		return temporal.ErrInvalid
	}
	value, err := temporal.FromDatabase(raw)
	scanner.Time = value
	return err
}

type NullableDateTimeScanner struct {
	Time  time.Time
	Valid bool
}

func (scanner *NullableDateTimeScanner) Scan(raw any) error {
	if scanner == nil {
		return temporal.ErrInvalid
	}
	scanner.Time, scanner.Valid = time.Time{}, false
	if raw == nil {
		return nil
	}
	value, err := temporal.FromDatabase(raw)
	if err != nil {
		return err
	}
	scanner.Time, scanner.Valid = value, true
	return nil
}
func (f DateTimeField[M]) scalarResultField(M, time.Time) (query.FieldRef, func() scalarCell[time.Time], error) {
	return f.reference, func() scalarCell[time.Time] {
		var value DateTimeScanner
		return scalarCell[time.Time]{destination: &value, value: func() time.Time { return value.Time }}
	}, f.err
}
func (f NullableDateTimeField[M]) scalarResultField(M, *time.Time) (query.FieldRef, func() scalarCell[*time.Time], error) {
	return f.reference, func() scalarCell[*time.Time] {
		var value NullableDateTimeScanner
		return scalarCell[*time.Time]{destination: &value, value: func() *time.Time {
			if !value.Valid {
				return nil
			}
			copy := value.Time
			return &copy
		}}
	}, f.err
}
func (f dateTimeField[M]) scalarOrderedField(M, time.Time) (query.FieldRef, func() scalarCell[Optional[time.Time]], error) {
	return f.reference, func() scalarCell[Optional[time.Time]] {
		var value NullableDateTimeScanner
		return scalarCell[Optional[time.Time]]{destination: &value, value: func() Optional[time.Time] { return Optional[time.Time]{value: value.Time, valid: value.Valid} }}
	}, f.err
}
