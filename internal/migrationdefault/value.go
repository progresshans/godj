// Package migrationdefault converts normalized Schema IR constants to the
// same typed values used by ordinary writes. Dialect compilers own SQL and
// storage-range differences; defaults are never persistent database defaults.
package migrationdefault

import (
	"errors"

	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/internal/floatvalue"
	"github.com/progresshans/godj/internal/temporal"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/uuid"
)

// Value requires a default from the caller's fully normalized, sealed field.
// It preserves explicit zero, false, empty text and JSON null as values.
func Value(value ir.Scalar) (query.Value, error) {
	switch value.Kind {
	case ir.ScalarString:
		return query.String(value.String), nil
	case ir.ScalarBoolean:
		return query.Boolean(value.Boolean), nil
	case ir.ScalarInteger:
		return query.Integer(value.Integer), nil
	case ir.ScalarFloat:
		parsed, err := floatvalue.FromBits(value.FloatBits)
		return query.Float(parsed), err
	case ir.ScalarDecimal:
		parsed, err := decimal.Parse(value.Decimal)
		return query.Decimal(parsed), err
	case ir.ScalarUUID:
		parsed, err := uuid.Parse(value.UUID)
		return query.UUID(parsed), err
	case ir.ScalarJSON:
		parsed, err := jsonvalue.Parse([]byte(value.JSON))
		return query.JSON(parsed), err
	case ir.ScalarDuration:
		parsed, err := duration.Parse(value.Duration)
		return query.Duration(parsed), err
	case ir.ScalarTime:
		parsed, err := clock.Parse(value.Time)
		return query.Time(parsed), err
	case ir.ScalarDate:
		parsed, err := calendar.Parse(value.Date)
		return query.Date(parsed), err
	case ir.ScalarDateTime:
		parsed, err := temporal.ParseCanonical(value.DateTime)
		return query.DateTime(parsed), err
	default:
		return query.Value{}, errors.New("unsupported migration default kind")
	}
}
