package orm

import (
	"database/sql"
	"time"

	"github.com/progresshans/godj/binaryvalue"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/uuid"
)

// NumericExpression exposes numeric aggregate capabilities only for the
// int64/float64 expression domain. AVG returns float64 for either operand.
// The embedded value remains usable by the same typed write API.
type NumericExpression[M any, N ArithmeticNumber] struct{ ScalarExpression[M, N] }

func Numeric[M any, N ArithmeticNumber](operand ScalarOperand[M, N]) NumericExpression[M, N] {
	expression, err := operandExpression(operand)
	return NumericExpression[M, N]{ScalarExpression[M, N]{expression: expression, err: err}}
}

func (value ScalarExpression[M, V]) scalarResultField(M, Optional[V]) (query.ResultExpression, func() scalarCell[Optional[V]], error) {
	if value.err != nil {
		return query.ResultExpression{}, nil, value.err
	}
	expression, err := query.ScalarResult(value.expression)
	if err != nil {
		return query.ResultExpression{}, nil, err
	}
	factory, err := computedCell[V](expression.ResultValueKind(), expression.ResultNullable())
	return expression, factory, err
}

func (reference FieldReference[M, V]) scalarResultField(M, Optional[V]) (query.ResultExpression, func() scalarCell[Optional[V]], error) {
	expression, err := operandExpression[M, V](reference)
	return (ScalarExpression[M, V]{expression: expression, err: err}).scalarResultField(*new(M), Optional[V]{})
}

func (value NumericExpression[M, N]) scalarSumField(M, N) (query.ResultExpression, func() scalarCell[Optional[N]], error) {
	expression, _, err := value.scalarResultField(*new(M), Optional[N]{})
	if err != nil {
		return query.ResultExpression{}, nil, err
	}
	factory, err := computedCell[N](expression.ResultValueKind(), true)
	return expression, factory, err
}
func (value NumericExpression[M, N]) scalarAvgField(M, float64) (query.ResultExpression, func() scalarCell[Optional[float64]], error) {
	expression, _, err := value.scalarResultField(*new(M), Optional[N]{})
	return expression, optionalNumericCell(nullableFloatResultCell), err
}
func (value NumericExpression[M, N]) scalarOrderedField(M, N) (query.ResultExpression, func() scalarCell[Optional[N]], error) {
	return value.scalarSumField(*new(M), *new(N))
}

func (value ScalarExpression[M, V]) Asc() Ordering[M]  { return value.order(query.Ascending) }
func (value ScalarExpression[M, V]) Desc() Ordering[M] { return value.order(query.Descending) }
func (value ScalarExpression[M, V]) order(direction query.Direction) Ordering[M] {
	expression, _, err := value.scalarResultField(*new(M), Optional[V]{})
	return resultOrdering[M](expression, err, direction)
}

func (value ScalarExpression[M, V]) comparison(lookup query.Lookup, literal any) Predicate[M] {
	if value.err != nil {
		return Predicate[M]{err: value.err}
	}
	right, err := scalarValue(literal)
	if err != nil {
		return Predicate[M]{err: err}
	}
	condition, err := query.NewScalarCondition(value.expression, lookup, right)
	return predicateFromCondition[M](condition, err)
}
func (value ScalarExpression[M, V]) Exact(literal V) Predicate[M] {
	return value.comparison(query.LookupExact, literal)
}
func (value ScalarExpression[M, V]) GreaterThan(literal V) Predicate[M] {
	return value.comparison(query.LookupGreaterThan, literal)
}
func (value ScalarExpression[M, V]) GreaterThanOrEqual(literal V) Predicate[M] {
	return value.comparison(query.LookupGreaterThanOrEqual, literal)
}
func (value ScalarExpression[M, V]) LessThan(literal V) Predicate[M] {
	return value.comparison(query.LookupLessThan, literal)
}
func (value ScalarExpression[M, V]) LessThanOrEqual(literal V) Predicate[M] {
	return value.comparison(query.LookupLessThanOrEqual, literal)
}
func (value ScalarExpression[M, V]) IsNull(isNull bool) Predicate[M] {
	return value.comparison(query.LookupIsNull, isNull)
}

func scalarKind[V any]() query.FieldKind {
	switch any(*new(V)).(type) {
	case int, int64:
		return query.FieldInteger
	case float64:
		return query.FieldFloat
	case string:
		return query.FieldString
	case bool:
		return query.FieldBoolean
	case decimal.Decimal:
		return query.FieldDecimal
	case duration.Duration:
		return query.FieldDuration
	case time.Time:
		return query.FieldDateTime
	case calendar.Date:
		return query.FieldDate
	case clock.Time:
		return query.FieldTime
	case uuid.UUID:
		return query.FieldUUID
	case binaryvalue.Value:
		return query.FieldBinary
	case jsonvalue.Value:
		return query.FieldJSON
	default:
		return ""
	}
}

// A computed result has its own domain. Source precision and native operand
// validity are enforced inside SQL, before grouping/filtering can hide them.
// No DTO is built until every driver value has passed this typed boundary.
func computedCell[V any](kind query.FieldKind, nullable bool) (func() scalarCell[Optional[V]], error) {
	if scalarKind[V]() != kind || kind == "" {
		return nil, invalidResultBuilder("computed result type differs from its scalar domain")
	}
	switch any(*new(V)).(type) {
	case int:
		return convertComputedCell[int, V](func() scalarCell[*int] {
			var value sql.Null[int]
			return scalarCell[*int]{destination: &value, value: func() *int {
				if !value.Valid {
					return nil
				}
				copy := value.V
				return &copy
			}}
		}, nullable), nil
	case int64:
		return convertComputedCell[int64, V](nullableIntegerResultCell, nullable), nil
	case float64:
		return convertComputedCell[float64, V](nullableFloatResultCell, nullable), nil
	case string:
		return convertComputedCell[string, V](nullableStringResultCell, nullable), nil
	case bool:
		return convertComputedCell[bool, V](nullableBooleanResultCell, nullable), nil
	case decimal.Decimal:
		return convertComputedCell[decimal.Decimal, V](nullableAggregateDecimalCell, nullable), nil
	case duration.Duration:
		return convertComputedCell[duration.Duration, V](nullableDurationResultCell, nullable), nil
	case time.Time:
		return convertComputedCell[time.Time, V](nullableDateTimeResultCell, nullable), nil
	case calendar.Date:
		return convertComputedCell[calendar.Date, V](nullableDateResultCell, nullable), nil
	case clock.Time:
		return convertComputedCell[clock.Time, V](nullableTimeResultCell, nullable), nil
	case uuid.UUID:
		return convertComputedCell[uuid.UUID, V](nullableUUIDResultCell, nullable), nil
	case binaryvalue.Value:
		return convertComputedCell[binaryvalue.Value, V](nullableBinaryResultCell, nullable), nil
	case jsonvalue.Value:
		return convertComputedCell[jsonvalue.Value, V](nullableJSONResultCell, nullable), nil
	default:
		return nil, invalidResultBuilder("computed result has no typed scalar decoder")
	}
}

func convertComputedCell[A, V any](factory func() scalarCell[*A], nullable bool) func() scalarCell[Optional[V]] {
	return func() scalarCell[Optional[V]] {
		cell := factory()
		var value Optional[V]
		scanner := &groupValueScanner{scan: func(raw any) error {
			value = Optional[V]{}
			if raw == nil && !nullable {
				return invalidResultBuilder("required computed result is NULL")
			}
			if err := scanGroupValue(cell.destination, raw); err != nil {
				return err
			}
			if present := cell.value(); present != nil {
				typed, ok := any(*present).(V)
				if !ok {
					return invalidResultBuilder("computed decoder returned a different scalar type")
				}
				value = Some(typed)
			}
			return nil
		}}
		return scalarCell[Optional[V]]{destination: scanner, value: func() Optional[V] { return value }}
	}
}
