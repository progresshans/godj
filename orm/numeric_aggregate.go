package orm

import (
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/query"
)

// SumField and AvgField are sealed numeric capabilities. The value parameter
// names the result type, not the operand type: Avg of int64 produces float64.
type SumField[M, V any] interface {
	scalarSumField(M, V) (query.ResultExpression, func() scalarCell[Optional[V]], error)
}
type AvgField[M, V any] interface {
	scalarAvgField(M, V) (query.ResultExpression, func() scalarCell[Optional[V]], error)
}

func Sum[M, V any](field SumField[M, V]) AggregateExpression[M, Optional[V]] {
	if interfaceIsNil(field) {
		return AggregateExpression[M, Optional[V]]{err: invalidResultBuilder("SUM field is nil")}
	}
	operand, cell, err := field.scalarSumField(*new(M), *new(V))
	if err == nil {
		operand, err = query.SumResult(operand)
	}
	return AggregateExpression[M, Optional[V]]{expression: operand, newCell: cell, err: err}
}

func Avg[M, V any](field AvgField[M, V]) AggregateExpression[M, Optional[V]] {
	if interfaceIsNil(field) {
		return AggregateExpression[M, Optional[V]]{err: invalidResultBuilder("AVG field is nil")}
	}
	operand, cell, err := field.scalarAvgField(*new(M), *new(V))
	if err == nil {
		operand, err = query.AvgResult(operand)
	}
	return AggregateExpression[M, Optional[V]]{expression: operand, newCell: cell, err: err}
}

func optionalNumericCell[V any](factory func() scalarCell[*V]) func() scalarCell[Optional[V]] {
	return func() scalarCell[Optional[V]] {
		cell := factory()
		return scalarCell[Optional[V]]{destination: cell.destination, value: func() Optional[V] {
			value := cell.value()
			if value == nil {
				return Optional[V]{}
			}
			return Some(*value)
		}}
	}
}

// Aggregate precision belongs to the result's global Decimal domain. Field
// precision is checked on each operand by the backend, not on this result.
type aggregateDecimalScanner struct {
	value decimal.Decimal
	valid bool
}

func (s *aggregateDecimalScanner) Scan(raw any) error {
	if s == nil {
		return decimal.ErrInvalid
	}
	s.value, s.valid = decimal.Decimal{}, false
	if raw == nil {
		return nil
	}
	value, err := scanDecimalValue(raw)
	if err != nil {
		return err
	}
	s.value, s.valid = value, true
	return nil
}
func nullableAggregateDecimalCell() scalarCell[*decimal.Decimal] {
	var scanner aggregateDecimalScanner
	return scalarCell[*decimal.Decimal]{destination: &scanner, value: func() *decimal.Decimal {
		if !scanner.valid {
			return nil
		}
		value := scanner.value
		return &value
	}}
}

func (f integerField[M]) scalarSumField(M, int64) (query.ResultExpression, func() scalarCell[Optional[int64]], error) {
	return query.FieldResult(f.reference), optionalNumericCell(nullableIntegerResultCell), f.err
}

func (f RelatedIntegerField[M]) scalarSumField(M, int64) (query.ResultExpression, func() scalarCell[Optional[int64]], error) {
	expression, err := relatedResult(f.path, f.valid, f.configurationErr)
	return expression, optionalNumericCell(nullableIntegerResultCell), err
}

func (f integerField[M]) scalarAvgField(M, float64) (query.ResultExpression, func() scalarCell[Optional[float64]], error) {
	return query.FieldResult(f.reference), optionalNumericCell(nullableFloatResultCell), f.err
}

func (f RelatedIntegerField[M]) scalarAvgField(M, float64) (query.ResultExpression, func() scalarCell[Optional[float64]], error) {
	expression, err := relatedResult(f.path, f.valid, f.configurationErr)
	return expression, optionalNumericCell(nullableFloatResultCell), err
}

func (f floatField[M]) scalarSumField(M, float64) (query.ResultExpression, func() scalarCell[Optional[float64]], error) {
	return query.FieldResult(f.reference), optionalNumericCell(nullableFloatResultCell), f.err
}

func (f RelatedFloatField[M]) scalarSumField(M, float64) (query.ResultExpression, func() scalarCell[Optional[float64]], error) {
	expression, err := relatedResult(f.path, f.valid, f.configurationErr)
	return expression, optionalNumericCell(nullableFloatResultCell), err
}

func (f floatField[M]) scalarAvgField(M, float64) (query.ResultExpression, func() scalarCell[Optional[float64]], error) {
	return query.FieldResult(f.reference), optionalNumericCell(nullableFloatResultCell), f.err
}

func (f RelatedFloatField[M]) scalarAvgField(M, float64) (query.ResultExpression, func() scalarCell[Optional[float64]], error) {
	expression, err := relatedResult(f.path, f.valid, f.configurationErr)
	return expression, optionalNumericCell(nullableFloatResultCell), err
}

func (f decimalField[M]) scalarSumField(M, decimal.Decimal) (query.ResultExpression, func() scalarCell[Optional[decimal.Decimal]], error) {
	return query.FieldResult(f.reference), optionalNumericCell(nullableAggregateDecimalCell), f.err
}

func (f RelatedDecimalField[M]) scalarSumField(M, decimal.Decimal) (query.ResultExpression, func() scalarCell[Optional[decimal.Decimal]], error) {
	expression, err := relatedResult(f.path, f.valid, f.configurationErr)
	return expression, optionalNumericCell(nullableAggregateDecimalCell), err
}

func (f decimalField[M]) scalarAvgField(M, decimal.Decimal) (query.ResultExpression, func() scalarCell[Optional[decimal.Decimal]], error) {
	return query.FieldResult(f.reference), optionalNumericCell(nullableAggregateDecimalCell), f.err
}

func (f RelatedDecimalField[M]) scalarAvgField(M, decimal.Decimal) (query.ResultExpression, func() scalarCell[Optional[decimal.Decimal]], error) {
	expression, err := relatedResult(f.path, f.valid, f.configurationErr)
	return expression, optionalNumericCell(nullableAggregateDecimalCell), err
}

func (f durationField[M]) scalarSumField(M, duration.Duration) (query.ResultExpression, func() scalarCell[Optional[duration.Duration]], error) {
	return query.FieldResult(f.reference), optionalNumericCell(nullableDurationResultCell), f.err
}

func (f RelatedDurationField[M]) scalarSumField(M, duration.Duration) (query.ResultExpression, func() scalarCell[Optional[duration.Duration]], error) {
	expression, err := relatedResult(f.path, f.valid, f.configurationErr)
	return expression, optionalNumericCell(nullableDurationResultCell), err
}

func (f durationField[M]) scalarAvgField(M, duration.Duration) (query.ResultExpression, func() scalarCell[Optional[duration.Duration]], error) {
	return query.FieldResult(f.reference), optionalNumericCell(nullableAggregateDurationCell), f.err
}

func (f RelatedDurationField[M]) scalarAvgField(M, duration.Duration) (query.ResultExpression, func() scalarCell[Optional[duration.Duration]], error) {
	expression, err := relatedResult(f.path, f.valid, f.configurationErr)
	return expression, optionalNumericCell(nullableAggregateDurationCell), err
}
