package orm

import (
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"strings"
)

type timeField[M any] struct{ field[M] }
type TimeField[M any] struct{ timeField[M] }
type NullableTimeField[M any] struct{ timeField[M] }

func NewTimeField[M any](metadata ir.Field) TimeField[M] {
	return TimeField[M]{timeField[M]{newField[M](metadata, query.FieldTime, ir.FieldTime, false)}}
}
func NewNullableTimeField[M any](metadata ir.Field) NullableTimeField[M] {
	return NullableTimeField[M]{timeField[M]{newField[M](metadata, query.FieldTime, ir.FieldTime, true)}}
}

func (f timeField[M]) Exact(value clock.Time) Predicate[M] {
	return f.predicate(query.LookupExact, query.Time(value))
}
func (f timeField[M]) GreaterThan(value clock.Time) Predicate[M] {
	return f.predicate(query.LookupGreaterThan, query.Time(value))
}
func (f timeField[M]) GreaterThanOrEqual(value clock.Time) Predicate[M] {
	return f.predicate(query.LookupGreaterThanOrEqual, query.Time(value))
}
func (f timeField[M]) LessThan(value clock.Time) Predicate[M] {
	return f.predicate(query.LookupLessThan, query.Time(value))
}
func (f timeField[M]) LessThanOrEqual(value clock.Time) Predicate[M] {
	return f.predicate(query.LookupLessThanOrEqual, query.Time(value))
}
func (f timeField[M]) IsNull(value bool) Predicate[M] {
	return f.predicate(query.LookupIsNull, query.Boolean(value))
}
func (f timeField[M]) Asc() Ordering[M]                        { return f.ordering(query.Ascending) }
func (f timeField[M]) Desc() Ordering[M]                       { return f.ordering(query.Descending) }
func (f timeField[M]) writableField(M) (query.FieldRef, error) { return f.reference, f.err }
func (f timeField[M]) referenceField(M, clock.Time) (query.FieldRef, error) {
	return f.reference, f.err
}
func (f timeField[M]) ExactField(right FieldReference[M, clock.Time]) Predicate[M] {
	return f.fieldPredicate(query.LookupExact, right.reference, right.err)
}
func (f timeField[M]) GreaterThanField(right FieldReference[M, clock.Time]) Predicate[M] {
	return f.fieldPredicate(query.LookupGreaterThan, right.reference, right.err)
}
func (f timeField[M]) GreaterThanOrEqualField(right FieldReference[M, clock.Time]) Predicate[M] {
	return f.fieldPredicate(query.LookupGreaterThanOrEqual, right.reference, right.err)
}
func (f timeField[M]) LessThanField(right FieldReference[M, clock.Time]) Predicate[M] {
	return f.fieldPredicate(query.LookupLessThan, right.reference, right.err)
}
func (f timeField[M]) LessThanOrEqualField(right FieldReference[M, clock.Time]) Predicate[M] {
	return f.fieldPredicate(query.LookupLessThanOrEqual, right.reference, right.err)
}

// TimeScanner accepts database TIME text with seconds and up to six
// fractional digits. Driver text is validated without a date or UTC conversion.
type TimeScanner struct{ Time clock.Time }

func (scanner *TimeScanner) Scan(raw any) error {
	if scanner == nil {
		return clock.ErrInvalid
	}
	value, err := scanTime(raw)
	scanner.Time = value
	return err
}

type NullableTimeScanner struct {
	Time  clock.Time
	Valid bool
}

func (scanner *NullableTimeScanner) Scan(raw any) error {
	if scanner == nil {
		return clock.ErrInvalid
	}
	scanner.Time, scanner.Valid = clock.Time{}, false
	if raw == nil {
		return nil
	}
	value, err := scanTime(raw)
	if err != nil {
		return err
	}
	scanner.Time, scanner.Valid = value, true
	return nil
}

func scanTime(raw any) (clock.Time, error) {
	var text string
	switch value := raw.(type) {
	case string:
		text = value
	case []byte:
		text = string(value)
	default:
		return clock.Time{}, clock.ErrInvalid
	}
	// PostgreSQL's database/sql codec emits six digits even at whole seconds.
	// It may also return trimmed text. Both represent the same SQL TIME value.
	if len(text) > 8 && len(text) <= 15 && text[8] == '.' {
		fraction := text[9:]
		for _, digit := range fraction {
			if digit < '0' || digit > '9' {
				return clock.Time{}, clock.ErrInvalid
			}
		}
		if fraction == "" {
			return clock.Time{}, clock.ErrInvalid
		}
		fraction += strings.Repeat("0", 6-len(fraction))
		if fraction == "000000" {
			text = text[:8]
		} else {
			text = text[:9] + fraction
		}
	}
	return clock.Parse(text)
}
func (f TimeField[M]) scalarResultField(M, clock.Time) (query.ResultExpression, func() scalarCell[clock.Time], error) {
	return query.FieldResult(f.reference), func() scalarCell[clock.Time] {
		var value TimeScanner
		return scalarCell[clock.Time]{destination: &value, value: func() clock.Time { return value.Time }}
	}, f.err
}
func (f NullableTimeField[M]) scalarResultField(M, *clock.Time) (query.ResultExpression, func() scalarCell[*clock.Time], error) {
	return query.FieldResult(f.reference), nullableTimeResultCell, f.err
}

func nullableTimeResultCell() scalarCell[*clock.Time] {
	var value NullableTimeScanner
	return scalarCell[*clock.Time]{destination: &value, value: func() *clock.Time {
		if !value.Valid {
			return nil
		}
		copy := value.Time
		return &copy
	}}
}
func (f timeField[M]) scalarOrderedField(M, clock.Time) (query.FieldRef, func() scalarCell[Optional[clock.Time]], error) {
	return f.reference, func() scalarCell[Optional[clock.Time]] {
		var value NullableTimeScanner
		return scalarCell[Optional[clock.Time]]{destination: &value, value: func() Optional[clock.Time] { return Optional[clock.Time]{value: value.Time, valid: value.Valid} }}
	}, f.err
}

func (f timeField[M]) In(values ...clock.Time) Predicate[M] {
	return membershipPredicate(f.field, values, query.Time)
}
