package orm

import (
	"time"

	"github.com/progresshans/godj/binaryvalue"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/uuid"
)

func (f RelatedIntegerField[M]) countAggregateField(M) (query.ResultExpression, error) {
	return relatedAggregateResult(query.ResultCount, f.path, f.valid, f.configurationErr)
}
func (f RelatedStringField[M]) countAggregateField(M) (query.ResultExpression, error) {
	return relatedAggregateResult(query.ResultCount, f.path, f.valid, f.configurationErr)
}
func (f RelatedBooleanField[M]) countAggregateField(M) (query.ResultExpression, error) {
	return relatedAggregateResult(query.ResultCount, f.path, f.valid, f.configurationErr)
}
func (f RelatedFloatField[M]) countAggregateField(M) (query.ResultExpression, error) {
	return relatedAggregateResult(query.ResultCount, f.path, f.valid, f.configurationErr)
}
func (f RelatedDecimalField[M]) countAggregateField(M) (query.ResultExpression, error) {
	return relatedAggregateResult(query.ResultCount, f.path, f.valid, f.configurationErr)
}
func (f RelatedDateTimeField[M]) countAggregateField(M) (query.ResultExpression, error) {
	return relatedAggregateResult(query.ResultCount, f.path, f.valid, f.configurationErr)
}
func (f RelatedDateField[M]) countAggregateField(M) (query.ResultExpression, error) {
	return relatedAggregateResult(query.ResultCount, f.path, f.valid, f.configurationErr)
}
func (f RelatedTimeField[M]) countAggregateField(M) (query.ResultExpression, error) {
	return relatedAggregateResult(query.ResultCount, f.path, f.valid, f.configurationErr)
}
func (f RelatedDurationField[M]) countAggregateField(M) (query.ResultExpression, error) {
	return relatedAggregateResult(query.ResultCount, f.path, f.valid, f.configurationErr)
}
func (f RelatedBinaryField[M]) countAggregateField(M) (query.ResultExpression, error) {
	return relatedAggregateResult(query.ResultCount, f.path, f.valid, f.configurationErr)
}
func (f RelatedUUIDField[M]) countAggregateField(M) (query.ResultExpression, error) {
	return relatedAggregateResult(query.ResultCount, f.path, f.valid, f.configurationErr)
}
func (f RelatedJSONField[M]) countAggregateField(M) (query.ResultExpression, error) {
	return relatedAggregateResult(query.ResultCount, f.path, f.valid, f.configurationErr)
}

func (f RelatedIntegerField[M]) scalarOrderedField(_ M, _ int64, kind query.ResultExpressionKind) (query.ResultExpression, func() scalarCell[Optional[int64]], error) {
	expression, err := relatedAggregateResult(kind, f.path, f.valid, f.configurationErr)
	return expression, optionalResultCell(nullableIntegerResultCell), err
}
func (f RelatedStringField[M]) scalarOrderedField(_ M, _ string, kind query.ResultExpressionKind) (query.ResultExpression, func() scalarCell[Optional[string]], error) {
	expression, err := relatedAggregateResult(kind, f.path, f.valid, f.configurationErr)
	return expression, optionalResultCell(nullableStringResultCell), err
}
func (f RelatedFloatField[M]) scalarOrderedField(_ M, _ float64, kind query.ResultExpressionKind) (query.ResultExpression, func() scalarCell[Optional[float64]], error) {
	expression, err := relatedAggregateResult(kind, f.path, f.valid, f.configurationErr)
	return expression, optionalResultCell(nullableFloatResultCell), err
}
func (f RelatedDecimalField[M]) scalarOrderedField(_ M, _ decimal.Decimal, kind query.ResultExpressionKind) (query.ResultExpression, func() scalarCell[Optional[decimal.Decimal]], error) {
	expression, err := relatedAggregateResult(kind, f.path, f.valid, f.configurationErr)
	return expression, optionalResultCell(func() scalarCell[*decimal.Decimal] { return nullableDecimalResultCell(f.path.Terminal()) }), err
}
func (f RelatedDateTimeField[M]) scalarOrderedField(_ M, _ time.Time, kind query.ResultExpressionKind) (query.ResultExpression, func() scalarCell[Optional[time.Time]], error) {
	expression, err := relatedAggregateResult(kind, f.path, f.valid, f.configurationErr)
	return expression, optionalResultCell(nullableDateTimeResultCell), err
}
func (f RelatedDateField[M]) scalarOrderedField(_ M, _ calendar.Date, kind query.ResultExpressionKind) (query.ResultExpression, func() scalarCell[Optional[calendar.Date]], error) {
	expression, err := relatedAggregateResult(kind, f.path, f.valid, f.configurationErr)
	return expression, optionalResultCell(nullableDateResultCell), err
}
func (f RelatedTimeField[M]) scalarOrderedField(_ M, _ clock.Time, kind query.ResultExpressionKind) (query.ResultExpression, func() scalarCell[Optional[clock.Time]], error) {
	expression, err := relatedAggregateResult(kind, f.path, f.valid, f.configurationErr)
	return expression, optionalResultCell(nullableTimeResultCell), err
}
func (f RelatedDurationField[M]) scalarOrderedField(_ M, _ duration.Duration, kind query.ResultExpressionKind) (query.ResultExpression, func() scalarCell[Optional[duration.Duration]], error) {
	expression, err := relatedAggregateResult(kind, f.path, f.valid, f.configurationErr)
	return expression, optionalResultCell(nullableDurationResultCell), err
}
func (f RelatedBinaryField[M]) scalarOrderedField(_ M, _ binaryvalue.Value, kind query.ResultExpressionKind) (query.ResultExpression, func() scalarCell[Optional[binaryvalue.Value]], error) {
	expression, err := relatedAggregateResult(kind, f.path, f.valid, f.configurationErr)
	return expression, optionalResultCell(nullableBinaryResultCell), err
}
func (f RelatedUUIDField[M]) scalarOrderedField(_ M, _ uuid.UUID, kind query.ResultExpressionKind) (query.ResultExpression, func() scalarCell[Optional[uuid.UUID]], error) {
	expression, err := relatedAggregateResult(kind, f.path, f.valid, f.configurationErr)
	return expression, optionalResultCell(nullableUUIDResultCell), err
}
