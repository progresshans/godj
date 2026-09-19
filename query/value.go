package query

import (
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/internal/temporal"
	"time"
)

type ValueKind string

const (
	ValueNull     ValueKind = "null"
	ValueInteger  ValueKind = "integer"
	ValueString   ValueKind = "string"
	ValueBoolean  ValueKind = "boolean"
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

func String(value string) Value {
	return Value{kind: ValueString, text: value}
}

// Date snapshots a valid calendar day. Invalid literals remain invalid Values
// and are rejected before a query or write reaches the backend.
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
	case ValueDate:
		return v.text, nil
	case ValueDateTime:
		value, _ := v.DateTime()
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
