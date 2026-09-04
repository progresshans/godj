package serializers

import (
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

// NewList returns an immutable list detached recursively from its input.
func NewList(values ...Value) (Value, error) {
	cloned := make([]Value, len(values))
	for index := range values {
		if !values[index].validValue() {
			return Value{}, invalidValue("list", "list contains an invalid value")
		}
		cloned[index] = values[index].clone()
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
func (m Member) Value() Value                  { return m.value.clone() }

// Object is an immutable ordered JSON object with indexed lookup. Member
// declaration order is retained for deterministic rendering and validation.
type Object struct {
	members []Member
	index   map[string]int
	valid   bool
}

// NewObject validates and recursively snapshots ordered members.
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
		result.members[index] = Member{name: member.name, value: member.value.clone()}
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

// Get returns a detached value for name.
func (o Object) Get(name string) (Value, bool) {
	if !o.valid {
		return Value{}, false
	}
	index, ok := o.index[name]
	if !ok {
		return Value{}, false
	}
	return o.members[index].value.clone(), true
}

// Members returns a recursively detached ordered snapshot.
func (o Object) Members() []Member {
	if !o.valid {
		return nil
	}
	members := make([]Member, len(o.members))
	for index := range o.members {
		members[index] = Member{name: o.members[index].name, value: o.members[index].value.clone()}
	}
	return members
}

// Value returns this object as a detached closed JSON value.
func (o Object) Value() Value {
	if !o.valid {
		return Value{}
	}
	return Value{kind: ValueObject, object: o.clone(), valid: true}
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
	values := make([]Value, len(v.list))
	for index := range v.list {
		values[index] = v.list[index].clone()
	}
	return values, true
}

func (v Value) AsObject() (Object, bool) {
	if !v.valid || v.kind != ValueObject {
		return Object{}, false
	}
	return v.object.clone(), true
}

func (v Value) clone() Value {
	clone := v
	if !v.valid {
		return Value{}
	}
	if v.kind == ValueList {
		clone.list = make([]Value, len(v.list))
		for index := range v.list {
			clone.list[index] = v.list[index].clone()
		}
	}
	if v.kind == ValueObject {
		clone.object = v.object.clone()
	}
	return clone
}

func (v Value) validValue() bool {
	if !v.valid {
		return false
	}
	switch v.kind {
	case ValueNull, ValueBoolean, ValueInteger:
		return true
	case ValueString:
		return utf8.ValidString(v.string) && !strings.ContainsRune(v.string, 0)
	case ValueList:
		for index := range v.list {
			if !v.list[index].validValue() {
				return false
			}
		}
		return true
	case ValueObject:
		return v.object.validObject()
	default:
		return false
	}
}

func (o Object) validObject() bool {
	if !o.valid || len(o.members) != len(o.index) {
		return false
	}
	for index := range o.members {
		member := o.members[index]
		if !validMemberName(member.name) || !member.value.validValue() || o.index[member.name] != index {
			return false
		}
	}
	return true
}

func (o Object) clone() Object {
	if !o.valid {
		return Object{}
	}
	clone := Object{
		members: make([]Member, len(o.members)),
		index:   make(map[string]int, len(o.index)),
		valid:   true,
	}
	for index := range o.members {
		member := o.members[index]
		clone.members[index] = Member{name: member.name, value: member.value.clone()}
		clone.index[member.name] = index
	}
	return clone
}

func invalidValue(field, detail string) error {
	return &Error{Code: CodeInvalidValue, Field: field, Detail: detail}
}

func invalidValueCause(field, detail string, cause error) error {
	return &Error{Code: CodeInvalidValue, Field: field, Detail: detail, Cause: cause}
}
