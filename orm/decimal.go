package orm

import (
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/internal/decimalstorage"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type decimalField[M any] struct{ field[M] }
type DecimalField[M any] struct{ decimalField[M] }
type NullableDecimalField[M any] struct{ decimalField[M] }

func NewDecimalField[M any](metadata ir.Field) DecimalField[M] {
	return DecimalField[M]{decimalField[M]{newField[M](metadata, query.FieldDecimal, ir.FieldDecimal, false)}}
}
func NewNullableDecimalField[M any](metadata ir.Field) NullableDecimalField[M] {
	return NullableDecimalField[M]{decimalField[M]{newField[M](metadata, query.FieldDecimal, ir.FieldDecimal, true)}}
}

func (f decimalField[M]) Exact(value decimal.Decimal) Predicate[M] {
	return f.predicate(query.LookupExact, query.Decimal(value))
}
func (f decimalField[M]) GreaterThan(value decimal.Decimal) Predicate[M] {
	return f.predicate(query.LookupGreaterThan, query.Decimal(value))
}
func (f decimalField[M]) GreaterThanOrEqual(value decimal.Decimal) Predicate[M] {
	return f.predicate(query.LookupGreaterThanOrEqual, query.Decimal(value))
}
func (f decimalField[M]) LessThan(value decimal.Decimal) Predicate[M] {
	return f.predicate(query.LookupLessThan, query.Decimal(value))
}
func (f decimalField[M]) LessThanOrEqual(value decimal.Decimal) Predicate[M] {
	return f.predicate(query.LookupLessThanOrEqual, query.Decimal(value))
}
func (f decimalField[M]) IsNull(value bool) Predicate[M] {
	return f.predicate(query.LookupIsNull, query.Boolean(value))
}
func (f decimalField[M]) Asc() Ordering[M]                        { return f.ordering(query.Ascending) }
func (f decimalField[M]) Desc() Ordering[M]                       { return f.ordering(query.Descending) }
func (f decimalField[M]) writableField(M) (query.FieldRef, error) { return f.reference, f.err }
func (f decimalField[M]) referenceField(M, decimal.Decimal) (query.FieldRef, error) {
	return f.reference, f.err
}
func (f decimalField[M]) ExactField(right FieldReference[M, decimal.Decimal]) Predicate[M] {
	return f.fieldPredicate(query.LookupExact, right.reference, right.err)
}
func (f decimalField[M]) GreaterThanField(right FieldReference[M, decimal.Decimal]) Predicate[M] {
	return f.fieldPredicate(query.LookupGreaterThan, right.reference, right.err)
}
func (f decimalField[M]) GreaterThanOrEqualField(right FieldReference[M, decimal.Decimal]) Predicate[M] {
	return f.fieldPredicate(query.LookupGreaterThanOrEqual, right.reference, right.err)
}
func (f decimalField[M]) LessThanField(right FieldReference[M, decimal.Decimal]) Predicate[M] {
	return f.fieldPredicate(query.LookupLessThan, right.reference, right.err)
}
func (f decimalField[M]) LessThanOrEqualField(right FieldReference[M, decimal.Decimal]) Predicate[M] {
	return f.fieldPredicate(query.LookupLessThanOrEqual, right.reference, right.err)
}

// DecimalScanner accepts a canonical SQLite order key or the exact typed value
// produced by a native backend. Text, float and integer coercion are forbidden.
type DecimalScanner struct {
	Decimal   decimal.Decimal
	precision ir.DecimalSpec
}

func NewDecimalScanner(maxDigits, decimalPlaces int) DecimalScanner {
	return DecimalScanner{precision: ir.DecimalSpec{MaxDigits: maxDigits, DecimalPlaces: decimalPlaces}}
}
func (scanner *DecimalScanner) Scan(raw any) error {
	if scanner == nil {
		return decimal.ErrInvalid
	}
	scanner.Decimal = decimal.Decimal{}
	value, err := scanDecimal(raw, scanner.precision)
	if err != nil {
		return err
	}
	scanner.Decimal = value
	return nil
}

type NullableDecimalScanner struct {
	Decimal   decimal.Decimal
	Valid     bool
	precision ir.DecimalSpec
}

func NewNullableDecimalScanner(maxDigits, decimalPlaces int) NullableDecimalScanner {
	return NullableDecimalScanner{precision: ir.DecimalSpec{MaxDigits: maxDigits, DecimalPlaces: decimalPlaces}}
}
func (scanner *NullableDecimalScanner) Scan(raw any) error {
	if scanner == nil {
		return decimal.ErrInvalid
	}
	scanner.Decimal, scanner.Valid = decimal.Decimal{}, false
	if !scanner.precision.Valid() {
		return decimal.ErrInvalid
	}
	if raw == nil {
		return nil
	}
	value, err := scanDecimal(raw, scanner.precision)
	if err != nil {
		return err
	}
	scanner.Decimal, scanner.Valid = value, true
	return nil
}
func scanDecimal(raw any, precision ir.DecimalSpec) (decimal.Decimal, error) {
	if !precision.Valid() {
		return decimal.Decimal{}, decimal.ErrInvalid
	}
	var value decimal.Decimal
	var err error
	switch source := raw.(type) {
	case decimal.Decimal:
		value, err = source.Canonical()
	case []byte:
		value, err = decimalstorage.Decode(source)
	default:
		return decimal.Decimal{}, decimal.ErrInvalid
	}
	if err != nil {
		return decimal.Decimal{}, err
	}
	if !value.Fits(precision.MaxDigits, precision.DecimalPlaces) {
		return decimal.Decimal{}, decimal.ErrRange
	}
	return value, nil
}

func (f DecimalField[M]) scalarResultField(M, decimal.Decimal) (query.FieldRef, func() scalarCell[decimal.Decimal], error) {
	return f.reference, func() scalarCell[decimal.Decimal] {
		digits, places, _ := f.reference.DecimalPrecision()
		value := NewDecimalScanner(digits, places)
		return scalarCell[decimal.Decimal]{destination: &value, value: func() decimal.Decimal { return value.Decimal }}
	}, f.err
}
func (f NullableDecimalField[M]) scalarResultField(M, *decimal.Decimal) (query.FieldRef, func() scalarCell[*decimal.Decimal], error) {
	return f.reference, func() scalarCell[*decimal.Decimal] {
		digits, places, _ := f.reference.DecimalPrecision()
		value := NewNullableDecimalScanner(digits, places)
		return scalarCell[*decimal.Decimal]{destination: &value, value: func() *decimal.Decimal {
			if !value.Valid {
				return nil
			}
			copy := value.Decimal
			return &copy
		}}
	}, f.err
}
func (f decimalField[M]) scalarOrderedField(M, decimal.Decimal) (query.FieldRef, func() scalarCell[Optional[decimal.Decimal]], error) {
	return f.reference, func() scalarCell[Optional[decimal.Decimal]] {
		digits, places, _ := f.reference.DecimalPrecision()
		value := NewNullableDecimalScanner(digits, places)
		return scalarCell[Optional[decimal.Decimal]]{destination: &value, value: func() Optional[decimal.Decimal] {
			return Optional[decimal.Decimal]{value: value.Decimal, valid: value.Valid}
		}}
	}, f.err
}

func (f decimalField[M]) In(values ...decimal.Decimal) Predicate[M] {
	return membershipPredicate(f.field, values, query.Decimal)
}
