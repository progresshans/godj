package query

import (
	"errors"
	"github.com/progresshans/godj/binaryvalue"

	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/internal/floatvalue"
	"github.com/progresshans/godj/internal/temporal"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/uuid"
)

// ValueFromScalar converts a normalized Schema IR constant without losing
// explicit zero, false, empty text or JSON null. Field constraints belong to
// the owning model; dialect compilers own storage-range differences.
func ValueFromScalar(value ir.Scalar) (Value, error) {
	switch value.Kind {
	case ir.ScalarBinary:
		parsed, err := binaryvalue.Parse(value.Binary)
		return Binary(parsed), err
	case ir.ScalarString:
		return String(value.String), nil
	case ir.ScalarBoolean:
		return Boolean(value.Boolean), nil
	case ir.ScalarInteger:
		return Integer(value.Integer), nil
	case ir.ScalarFloat:
		parsed, err := floatvalue.FromBits(value.FloatBits)
		return Float(parsed), err
	case ir.ScalarDecimal:
		parsed, err := decimal.Parse(value.Decimal)
		return Decimal(parsed), err
	case ir.ScalarUUID:
		parsed, err := uuid.Parse(value.UUID)
		return UUID(parsed), err
	case ir.ScalarJSON:
		parsed, err := jsonvalue.Parse([]byte(value.JSON))
		return JSON(parsed), err
	case ir.ScalarDuration:
		parsed, err := duration.Parse(value.Duration)
		return Duration(parsed), err
	case ir.ScalarTime:
		parsed, err := clock.Parse(value.Time)
		return Time(parsed), err
	case ir.ScalarDate:
		parsed, err := calendar.Parse(value.Date)
		return Date(parsed), err
	case ir.ScalarDateTime:
		parsed, err := temporal.ParseCanonical(value.DateTime)
		return DateTime(parsed), err
	default:
		return Value{}, errors.New("unsupported schema scalar kind")
	}
}
