package serializers

import (
	"slices"
	"strings"
	"unicode/utf8"
)

// ValueKind identifies the closed JSON value set. Floating-point numbers are
// deliberately absent so application adapters never lose numeric precision.
type ValueKind uint8

const (
	ValueNull ValueKind = iota + 1
	ValueString
	ValueBoolean
	ValueInteger
	ValueList
	ValueObject
)

// Value is an immutable closed JSON value. Its zero value is invalid rather
// than silently meaning null.
type Value struct {
	kind    ValueKind
	string  string
	boolean bool
	integer int64
	list    []Value
	object  Object
	valid   bool
}

func Null() Value               { return Value{kind: ValueNull, valid: true} }
func String(value string) Value { return Value{kind: ValueString, string: value, valid: true} }
func Boolean(value bool) Value  { return Value{kind: ValueBoolean, boolean: value, valid: true} }
func Integer(value int64) Value { return Value{kind: ValueInteger, integer: value, valid: true} }

// NewList snapshots the input slice and shares its immutable child values.
func NewList(values ...Value) (Value, error) {
	cloned := make([]Value, len(values))
	for index := range values {
		if !values[index].validValue() {
			return Value{}, invalidValue("list", "list contains an invalid value")
		}
		cloned[index] = values[index]
	}
	return Value{kind: ValueList, list: cloned, valid: true}, nil
}

// Member is an immutable object name/value pair. MemberOf only stages a pair;
// NewObject performs duplicate/name/value validation before publication.
type Member struct {
	name  string
	value Value
}

func MemberOf(name string, value Value) Member { return Member{name: name, value: value} }
func (m Member) Name() string                  { return m.name }
func (m Member) Value() Value                  { return m.value }

// Object is an immutable ordered JSON object with indexed lookup. Member
// declaration order is retained for deterministic rendering and validation.
type Object struct {
	members []Member
	index   map[string]int
	valid   bool
}

// NewObject validates and snapshots ordered members, sharing immutable values.
func NewObject(members ...Member) (Object, error) {
	result := Object{
		members: make([]Member, len(members)),
		index:   make(map[string]int, len(members)),
		valid:   true,
	}
	for index := range members {
		member := members[index]
		if !validMemberName(member.name) {
			return Object{}, invalidValue("object.name", "object member name is empty or invalid UTF-8 text")
		}
		if !member.value.validValue() {
			return Object{}, invalidValue("object."+member.name, "object member contains an invalid value")
		}
		if _, duplicate := result.index[member.name]; duplicate {
			return Object{}, invalidValue("object."+member.name, "object member name is duplicated")
		}
		result.index[member.name] = index
		result.members[index] = Member{name: member.name, value: member.value}
	}
	return result, nil
}

func validMemberName(name string) bool {
	return name != "" && utf8.ValidString(name) && !strings.ContainsRune(name, 0)
}

func (o Object) Valid() bool { return o.valid }
func (o Object) Len() int {
	if !o.valid {
		return 0
	}
	return len(o.members)
}

// Get returns the immutable value for name.
func (o Object) Get(name string) (Value, bool) {
	if !o.valid {
		return Value{}, false
	}
	index, ok := o.index[name]
	if !ok {
		return Value{}, false
	}
	return o.members[index].value, true
}

// Members returns a detached slice of immutable ordered members.
func (o Object) Members() []Member {
	if !o.valid {
		return nil
	}
	return slices.Clone(o.members)
}

// Value returns this immutable object as a closed JSON value.
func (o Object) Value() Value {
	if !o.valid {
		return Value{}
	}
	return Value{kind: ValueObject, object: o, valid: true}
}

func (v Value) Kind() ValueKind {
	if !v.valid {
		return 0
	}
	return v.kind
}

func (v Value) IsNull() bool { return v.valid && v.kind == ValueNull }

func (v Value) AsString() (string, bool) {
	return v.string, v.valid && v.kind == ValueString
}

func (v Value) AsBoolean() (bool, bool) {
	return v.boolean, v.valid && v.kind == ValueBoolean
}

func (v Value) AsInteger() (int64, bool) {
	return v.integer, v.valid && v.kind == ValueInteger
}

func (v Value) AsList() ([]Value, bool) {
	if !v.valid || v.kind != ValueList {
		return nil, false
	}
	return slices.Clone(v.list), true
}

func (v Value) AsObject() (Object, bool) {
	if !v.valid || v.kind != ValueObject {
		return Object{}, false
	}
	return v.object, true
}

func (v Value) validValue() bool {
	if !v.valid {
		return false
	}
	switch v.kind {
	case ValueNull, ValueBoolean, ValueInteger, ValueList:
		// NewList validates and snapshots its children before publication.
		// Opaque immutable containers never need a second descendant walk.
		return true
	case ValueString:
		return utf8.ValidString(v.string) && !strings.ContainsRune(v.string, 0)
	case ValueObject:
		return v.object.valid
	default:
		return false
	}
}

func invalidValue(field, detail string) error {
	return &Error{Code: CodeInvalidValue, Field: field, Detail: detail}
}
