package orm

import (
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type durationField[M any] struct{ field[M] }
type DurationField[M any] struct{ durationField[M] }
type NullableDurationField[M any] struct{ durationField[M] }

func NewDurationField[M any](metadata ir.Field) DurationField[M] {
	return DurationField[M]{durationField[M]{newField[M](metadata, query.FieldDuration, ir.FieldDuration, false)}}
}
func NewNullableDurationField[M any](metadata ir.Field) NullableDurationField[M] {
	return NullableDurationField[M]{durationField[M]{newField[M](metadata, query.FieldDuration, ir.FieldDuration, true)}}
}

func (f durationField[M]) Exact(value duration.Duration) Predicate[M] {
	return f.predicate(query.LookupExact, query.Duration(value))
}
func (f durationField[M]) GreaterThan(value duration.Duration) Predicate[M] {
	return f.predicate(query.LookupGreaterThan, query.Duration(value))
}
func (f durationField[M]) GreaterThanOrEqual(value duration.Duration) Predicate[M] {
	return f.predicate(query.LookupGreaterThanOrEqual, query.Duration(value))
}
func (f durationField[M]) LessThan(value duration.Duration) Predicate[M] {
	return f.predicate(query.LookupLessThan, query.Duration(value))
}
func (f durationField[M]) LessThanOrEqual(value duration.Duration) Predicate[M] {
	return f.predicate(query.LookupLessThanOrEqual, query.Duration(value))
}
func (f durationField[M]) IsNull(value bool) Predicate[M] {
	return f.predicate(query.LookupIsNull, query.Boolean(value))
}
func (f durationField[M]) Asc() Ordering[M]                        { return f.ordering(query.Ascending) }
func (f durationField[M]) Desc() Ordering[M]                       { return f.ordering(query.Descending) }
func (f durationField[M]) writableField(M) (query.FieldRef, error) { return f.reference, f.err }
func (f durationField[M]) referenceField(M, duration.Duration) (query.FieldRef, error) {
	return f.reference, f.err
}
func (f durationField[M]) ExactField(right FieldReference[M, duration.Duration]) Predicate[M] {
	return f.fieldPredicate(query.LookupExact, right.reference, right.err)
}
func (f durationField[M]) GreaterThanField(right FieldReference[M, duration.Duration]) Predicate[M] {
	return f.fieldPredicate(query.LookupGreaterThan, right.reference, right.err)
}
func (f durationField[M]) GreaterThanOrEqualField(right FieldReference[M, duration.Duration]) Predicate[M] {
	return f.fieldPredicate(query.LookupGreaterThanOrEqual, right.reference, right.err)
}
func (f durationField[M]) LessThanField(right FieldReference[M, duration.Duration]) Predicate[M] {
	return f.fieldPredicate(query.LookupLessThan, right.reference, right.err)
}
func (f durationField[M]) LessThanOrEqualField(right FieldReference[M, duration.Duration]) Predicate[M] {
	return f.fieldPredicate(query.LookupLessThanOrEqual, right.reference, right.err)
}

// DurationScanner accepts signed int64 microseconds or canonical duration
// text. The backend owns conversion from any native database representation.
type DurationScanner struct{ Duration duration.Duration }

func (scanner *DurationScanner) Scan(raw any) error {
	if scanner == nil {
		return duration.ErrInvalid
	}
	value, err := scanDuration(raw)
	scanner.Duration = value
	return err
}

type NullableDurationScanner struct {
	Duration duration.Duration
	Valid    bool
}

func (scanner *NullableDurationScanner) Scan(raw any) error {
	if scanner == nil {
		return duration.ErrInvalid
	}
	scanner.Duration, scanner.Valid = duration.Duration{}, false
	if raw == nil {
		return nil
	}
	value, err := scanDuration(raw)
	if err != nil {
		return err
	}
	scanner.Duration, scanner.Valid = value, true
	return nil
}

func scanDuration(raw any) (duration.Duration, error) {
	switch value := raw.(type) {
	case int64:
		return duration.FromMicroseconds(value), nil
	case string:
		return duration.Parse(value)
	case []byte:
		return duration.Parse(string(value))
	default:
		return duration.Duration{}, duration.ErrInvalid
	}
}

func (f DurationField[M]) scalarResultField(M, duration.Duration) (query.ResultExpression, func() scalarCell[duration.Duration], error) {
	return query.FieldResult(f.reference), func() scalarCell[duration.Duration] {
		var value DurationScanner
		return scalarCell[duration.Duration]{destination: &value, value: func() duration.Duration { return value.Duration }}
	}, f.err
}
func (f NullableDurationField[M]) scalarResultField(M, *duration.Duration) (query.ResultExpression, func() scalarCell[*duration.Duration], error) {
	return query.FieldResult(f.reference), func() scalarCell[*duration.Duration] {
		var value NullableDurationScanner
		return scalarCell[*duration.Duration]{destination: &value, value: func() *duration.Duration {
			if !value.Valid {
				return nil
			}
			copy := value.Duration
			return &copy
		}}
	}, f.err
}
func (f durationField[M]) scalarOrderedField(M, duration.Duration) (query.FieldRef, func() scalarCell[Optional[duration.Duration]], error) {
	return f.reference, func() scalarCell[Optional[duration.Duration]] {
		var value NullableDurationScanner
		return scalarCell[Optional[duration.Duration]]{destination: &value, value: func() Optional[duration.Duration] {
			return Optional[duration.Duration]{value: value.Duration, valid: value.Valid}
		}}
	}, f.err
}

func (f durationField[M]) In(values ...duration.Duration) Predicate[M] {
	return membershipPredicate(f.field, values, query.Duration)
}
