package orm

import (
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"time"
)

type dateField[M any] struct{ field[M] }
type DateField[M any] struct{ dateField[M] }
type NullableDateField[M any] struct{ dateField[M] }

func NewDateField[M any](metadata ir.Field) DateField[M] {
	return DateField[M]{dateField[M]{newField[M](metadata, query.FieldDate, ir.FieldDate, false)}}
}
func NewNullableDateField[M any](metadata ir.Field) NullableDateField[M] {
	return NullableDateField[M]{dateField[M]{newField[M](metadata, query.FieldDate, ir.FieldDate, true)}}
}

func (f dateField[M]) Exact(value calendar.Date) Predicate[M] {
	return f.predicate(query.LookupExact, query.Date(value))
}
func (f dateField[M]) GreaterThan(value calendar.Date) Predicate[M] {
	return f.predicate(query.LookupGreaterThan, query.Date(value))
}
func (f dateField[M]) GreaterThanOrEqual(value calendar.Date) Predicate[M] {
	return f.predicate(query.LookupGreaterThanOrEqual, query.Date(value))
}
func (f dateField[M]) LessThan(value calendar.Date) Predicate[M] {
	return f.predicate(query.LookupLessThan, query.Date(value))
}
func (f dateField[M]) LessThanOrEqual(value calendar.Date) Predicate[M] {
	return f.predicate(query.LookupLessThanOrEqual, query.Date(value))
}
func (f dateField[M]) IsNull(value bool) Predicate[M] {
	return f.predicate(query.LookupIsNull, query.Boolean(value))
}
func (f dateField[M]) Asc() Ordering[M]                        { return f.ordering(query.Ascending) }
func (f dateField[M]) Desc() Ordering[M]                       { return f.ordering(query.Descending) }
func (f dateField[M]) writableField(M) (query.FieldRef, error) { return f.reference, f.err }
func (f dateField[M]) referenceField(M, calendar.Date) (query.FieldRef, error) {
	return f.reference, f.err
}
func (f dateField[M]) ExactField(right FieldReference[M, calendar.Date]) Predicate[M] {
	return f.fieldPredicate(query.LookupExact, right.reference, right.err)
}
func (f dateField[M]) GreaterThanField(right FieldReference[M, calendar.Date]) Predicate[M] {
	return f.fieldPredicate(query.LookupGreaterThan, right.reference, right.err)
}
func (f dateField[M]) GreaterThanOrEqualField(right FieldReference[M, calendar.Date]) Predicate[M] {
	return f.fieldPredicate(query.LookupGreaterThanOrEqual, right.reference, right.err)
}
func (f dateField[M]) LessThanField(right FieldReference[M, calendar.Date]) Predicate[M] {
	return f.fieldPredicate(query.LookupLessThan, right.reference, right.err)
}
func (f dateField[M]) LessThanOrEqualField(right FieldReference[M, calendar.Date]) Predicate[M] {
	return f.fieldPredicate(query.LookupLessThanOrEqual, right.reference, right.err)
}

// DateScanner accepts a canonical database date or a native driver's midnight
// time. The driver's calendar components are preserved without a time zone
// conversion; a clock value is rejected instead of being silently discarded.
type DateScanner struct{ Date calendar.Date }

func (scanner *DateScanner) Scan(raw any) error {
	if scanner == nil {
		return calendar.ErrInvalid
	}
	value, err := scanDate(raw)
	scanner.Date = value
	return err
}

type NullableDateScanner struct {
	Date  calendar.Date
	Valid bool
}

func (scanner *NullableDateScanner) Scan(raw any) error {
	if scanner == nil {
		return calendar.ErrInvalid
	}
	scanner.Date, scanner.Valid = calendar.Date{}, false
	if raw == nil {
		return nil
	}
	value, err := scanDate(raw)
	if err != nil {
		return err
	}
	scanner.Date, scanner.Valid = value, true
	return nil
}

func scanDate(raw any) (calendar.Date, error) {
	switch value := raw.(type) {
	case string:
		return calendar.Parse(value)
	case []byte:
		return calendar.Parse(string(value))
	case time.Time:
		if hour, minute, second := value.Clock(); hour != 0 || minute != 0 || second != 0 || value.Nanosecond() != 0 {
			return calendar.Date{}, calendar.ErrInvalid
		}
		year, month, day := value.Date()
		return calendar.New(year, month, day)
	default:
		return calendar.Date{}, calendar.ErrInvalid
	}
}
func (f DateField[M]) scalarResultField(M, calendar.Date) (query.ResultExpression, func() scalarCell[calendar.Date], error) {
	return query.FieldResult(f.reference), func() scalarCell[calendar.Date] {
		var value DateScanner
		return scalarCell[calendar.Date]{destination: &value, value: func() calendar.Date { return value.Date }}
	}, f.err
}
func (f NullableDateField[M]) scalarResultField(M, *calendar.Date) (query.ResultExpression, func() scalarCell[*calendar.Date], error) {
	return query.FieldResult(f.reference), func() scalarCell[*calendar.Date] {
		var value NullableDateScanner
		return scalarCell[*calendar.Date]{destination: &value, value: func() *calendar.Date {
			if !value.Valid {
				return nil
			}
			copy := value.Date
			return &copy
		}}
	}, f.err
}
func (f dateField[M]) scalarOrderedField(M, calendar.Date) (query.FieldRef, func() scalarCell[Optional[calendar.Date]], error) {
	return f.reference, func() scalarCell[Optional[calendar.Date]] {
		var value NullableDateScanner
		return scalarCell[Optional[calendar.Date]]{destination: &value, value: func() Optional[calendar.Date] { return Optional[calendar.Date]{value: value.Date, valid: value.Valid} }}
	}, f.err
}

func (f dateField[M]) In(values ...calendar.Date) Predicate[M] {
	return membershipPredicate(f.field, values, query.Date)
}
