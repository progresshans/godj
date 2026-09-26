package query

import (
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/internal/floatvalue"
	"github.com/progresshans/godj/internal/temporal"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/uuid"
	"math"
	"time"
)

type ValueKind string

const (
	ValueNull     ValueKind = "null"
	ValueInteger  ValueKind = "integer"
	ValueFloat    ValueKind = "float"
	ValueDecimal  ValueKind = "decimal"
	ValueUUID     ValueKind = "uuid"
	ValueJSON     ValueKind = "json"
	ValueString   ValueKind = "string"
	ValueBoolean  ValueKind = "boolean"
	ValueDuration ValueKind = "duration"
	ValueTime     ValueKind = "time"
	ValueDate     ValueKind = "date"
	ValueDateTime ValueKind = "datetime"
)

// Value is a deliberately small tagged scalar. Null is an explicit tagged
// value for mutation assignments; read isnull predicates continue to carry a
// Boolean value so query construction cannot confuse NULL with an untyped nil.
type Value struct {
	kind    ValueKind
	integer int64
	text    string
	boolean bool
}

func Null() Value {
	return Value{kind: ValueNull}
}

func Integer(value int64) Value {
	return Value{kind: ValueInteger, integer: value}
}

// UUID snapshots the canonical value without retaining caller-owned bytes.
func UUID(value uuid.UUID) Value { return Value{kind: ValueUUID, text: value.String()} }

// JSON snapshots a validated document. It does not convert SQL NULL into JSON
// null, or a JSON document into a string-typed query value.
func JSON(value jsonvalue.Value) Value {
	canonical, err := value.Canonical()
	if err != nil {
		return Value{}
	}
	return Value{kind: ValueJSON, text: canonical.Text}
}

func (v Value) JSON() (jsonvalue.Value, bool) {
	if v.kind != ValueJSON {
		return jsonvalue.Value{}, false
	}
	value, err := jsonvalue.Parse([]byte(v.text))
	return value, err == nil
}
func (v Value) UUID() (uuid.UUID, bool) {
	if v.kind != ValueUUID {
		return uuid.UUID{}, false
	}
	value, err := uuid.Parse(v.text)
	return value, err == nil
}

// Decimal snapshots a canonical finite value. Invalid literals stay invalid
// and declared write precision is validated separately from comparison values.
func Decimal(value decimal.Decimal) Value {
	canonical, err := value.Canonical()
	if err != nil {
		return Value{}
	}
	return Value{kind: ValueDecimal, text: canonical.String()}
}
func (v Value) Decimal() (decimal.Decimal, bool) {
	if v.kind != ValueDecimal {
		return decimal.Decimal{}, false
	}
	value, err := decimal.Parse(v.text)
	return value, err == nil
}

// Float preserves finite binary64 bits and signed zero, and canonicalizes NaN.
func Float(value float64) Value {
	return Value{kind: ValueFloat, integer: int64(floatvalue.CanonicalBits(value))}
}
func (v Value) Float() (float64, bool) {
	return math.Float64frombits(uint64(v.integer)), v.kind == ValueFloat
}

func String(value string) Value {
	return Value{kind: ValueString, text: value}
}

// Duration snapshots a normalized elapsed value. Invalid model literals are
// rejected before I/O; each backend validates its own storage range.
func Duration(value duration.Duration) Value {
	if !value.Valid() {
		return Value{}
	}
	return Value{kind: ValueDuration, text: value.String()}
}
func (v Value) Duration() (duration.Duration, bool) {
	if v.kind != ValueDuration {
		return duration.Duration{}, false
	}
	value, err := duration.Parse(v.text)
	return value, err == nil
}

// Time snapshots valid clock components, including midnight. Invalid literals
// are rejected before a query or write reaches the backend.
func Time(value clock.Time) Value {
	if !value.Valid() {
		return Value{}
	}
	return Value{kind: ValueTime, text: value.String()}
}
func (v Value) Time() (clock.Time, bool) {
	if v.kind != ValueTime {
		return clock.Time{}, false
	}
	value, err := clock.Parse(v.text)
	return value, err == nil
}

// Date snapshots a valid calendar day. Invalid literals remain invalid Values.
func Date(value calendar.Date) Value {
	if !value.Valid() {
		return Value{}
	}
	return Value{kind: ValueDate, text: value.String()}
}
func (v Value) Date() (calendar.Date, bool) {
	if v.kind != ValueDate {
		return calendar.Date{}, false
	}
	value, err := calendar.Parse(v.text)
	return value, err == nil
}

// DateTime canonicalizes an instant to UTC microseconds. An out-of-range
// instant produces an invalid value, rejected by plan/write validation.
func DateTime(value time.Time) Value {
	canonical, err := temporal.Canonical(value)
	if err != nil {
		return Value{}
	}
	return Value{kind: ValueDateTime, integer: canonical.UnixMicro()}
}

func (v Value) DateTime() (time.Time, bool) {
	if v.kind != ValueDateTime {
		return time.Time{}, false
	}
	return time.UnixMicro(v.integer).UTC(), true
}

func Boolean(value bool) Value {
	return Value{kind: ValueBoolean, boolean: value}
}

func (v Value) Kind() ValueKind {
	return v.kind
}

func (v Value) Integer() (int64, bool) {
	return v.integer, v.kind == ValueInteger
}

func (v Value) String() (string, bool) {
	return v.text, v.kind == ValueString
}

func (v Value) Boolean() (bool, bool) {
	return v.boolean, v.kind == ValueBoolean
}

func (v Value) IsNull() bool {
	return v.kind == ValueNull
}

func (v Value) Equal(other Value) bool {
	return v == other
}

func (v Value) DatabaseValue() (any, error) {
	switch v.kind {
	case ValueNull:
		return nil, nil
	case ValueDuration:
		return v.text, nil
	case ValueTime:
		return v.text, nil
	case ValueDate:
		return v.text, nil
	case ValueDateTime:
		value, _ := v.DateTime()
		return value, nil
	case ValueJSON:
		value, ok := v.JSON()
		if !ok {
			return nil, &Error{Category: CategoryField, Code: CodeInvalidValue, Detail: "invalid JSON value"}
		}
		return value, nil
	case ValueUUID:
		value, ok := v.UUID()
		if !ok {
			return nil, &Error{Category: CategoryField, Code: CodeInvalidValue, Detail: "invalid UUID value"}
		}
		return value, nil
	case ValueDecimal:
		value, ok := v.Decimal()
		if !ok {
			return nil, &Error{Category: CategoryField, Code: CodeInvalidValue, Detail: "invalid decimal value"}
		}
		return value, nil
	case ValueFloat:
		value, _ := v.Float()
		return value, nil
	case ValueInteger:
		return v.integer, nil
	case ValueString:
		return v.text, nil
	case ValueBoolean:
		return v.boolean, nil
	default:
		return nil, &Error{Category: CategoryQuery, Code: CodeInvalidPlan, Detail: "unknown scalar value kind"}
	}
}
