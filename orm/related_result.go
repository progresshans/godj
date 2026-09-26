package orm

import (
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/uuid"
	"time"
)

// Related scalar selection uses the same nullable cell as a root projection.
// A route may contain an optional ancestor independently of target nullability;
// all bound related fields therefore expose *V without fabricating metadata.
func relatedResult(path query.RelationPath, valid bool, cause error) (query.ResultExpression, error) {
	if cause != nil {
		return query.ResultExpression{}, cause
	}
	if !valid {
		return query.ResultExpression{}, relationInvalidPlan("related scalar field is unbound")
	}
	return query.RelatedFieldResult(path)
}

func (f RelatedIntegerField[M]) scalarResultField(M, *int64) (query.ResultExpression, func() scalarCell[*int64], error) {
	expression, err := relatedResult(f.path, f.valid, f.configurationErr)
	return expression, nullableIntegerResultCell, err
}

func (f RelatedStringField[M]) scalarResultField(M, *string) (query.ResultExpression, func() scalarCell[*string], error) {
	expression, err := relatedResult(f.path, f.valid, f.configurationErr)
	return expression, nullableStringResultCell, err
}

func (f RelatedBooleanField[M]) scalarResultField(M, *bool) (query.ResultExpression, func() scalarCell[*bool], error) {
	expression, err := relatedResult(f.path, f.valid, f.configurationErr)
	return expression, nullableBooleanResultCell, err
}

func (f RelatedFloatField[M]) scalarResultField(M, *float64) (query.ResultExpression, func() scalarCell[*float64], error) {
	expression, err := relatedResult(f.path, f.valid, f.configurationErr)
	return expression, nullableFloatResultCell, err
}

func (f RelatedDecimalField[M]) scalarResultField(M, *decimal.Decimal) (query.ResultExpression, func() scalarCell[*decimal.Decimal], error) {
	expression, err := relatedResult(f.path, f.valid, f.configurationErr)
	return expression, func() scalarCell[*decimal.Decimal] { return nullableDecimalResultCell(f.path.Terminal()) }, err
}

func (f RelatedDateTimeField[M]) scalarResultField(M, *time.Time) (query.ResultExpression, func() scalarCell[*time.Time], error) {
	expression, err := relatedResult(f.path, f.valid, f.configurationErr)
	return expression, nullableDateTimeResultCell, err
}

func (f RelatedDateField[M]) scalarResultField(M, *calendar.Date) (query.ResultExpression, func() scalarCell[*calendar.Date], error) {
	expression, err := relatedResult(f.path, f.valid, f.configurationErr)
	return expression, nullableDateResultCell, err
}

func (f RelatedTimeField[M]) scalarResultField(M, *clock.Time) (query.ResultExpression, func() scalarCell[*clock.Time], error) {
	expression, err := relatedResult(f.path, f.valid, f.configurationErr)
	return expression, nullableTimeResultCell, err
}

func (f RelatedDurationField[M]) scalarResultField(M, *duration.Duration) (query.ResultExpression, func() scalarCell[*duration.Duration], error) {
	expression, err := relatedResult(f.path, f.valid, f.configurationErr)
	return expression, nullableDurationResultCell, err
}

func (f RelatedUUIDField[M]) scalarResultField(M, *uuid.UUID) (query.ResultExpression, func() scalarCell[*uuid.UUID], error) {
	expression, err := relatedResult(f.path, f.valid, f.configurationErr)
	return expression, nullableUUIDResultCell, err
}

func (f RelatedJSONField[M]) scalarResultField(M, *jsonvalue.Value) (query.ResultExpression, func() scalarCell[*jsonvalue.Value], error) {
	expression, err := relatedResult(f.path, f.valid, f.configurationErr)
	return expression, nullableJSONResultCell, err
}
