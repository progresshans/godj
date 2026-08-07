package query

type ValueKind string

const (
	ValueInteger ValueKind = "integer"
	ValueString  ValueKind = "string"
	ValueBoolean ValueKind = "boolean"
)

// Value is a deliberately small tagged scalar. SQL null is represented by an
// isnull condition, not by an untyped nil value.
type Value struct {
	kind    ValueKind
	integer int64
	text    string
	boolean bool
}

func Integer(value int64) Value {
	return Value{kind: ValueInteger, integer: value}
}

func String(value string) Value {
	return Value{kind: ValueString, text: value}
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

func (v Value) DatabaseValue() (any, error) {
	switch v.kind {
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
