// Package templates implements a bounded, startup-compiled template language
// over a closed immutable value algebra. Template text cannot call arbitrary Go
// functions or methods and never receives raw application values.
package templates

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// ValueKind identifies every value the template resolver can observe.
type ValueKind uint8

const (
	ValueNull ValueKind = iota
	ValueString
	ValueBoolean
	ValueInteger
	ValueList
	ValueObject
	ValueSafeHTML
)

// Value is a closed immutable template value. Composite constructors detach
// all nested storage from their callers.
type Value struct {
	kind    ValueKind
	text    string
	boolean bool
	integer int64
	list    []Value
	object  map[string]Value
}

func Null() Value               { return Value{kind: ValueNull} }
func String(value string) Value { return Value{kind: ValueString, text: value} }
func Bool(value bool) Value     { return Value{kind: ValueBoolean, boolean: value} }
func Integer(value int64) Value { return Value{kind: ValueInteger, integer: value} }

// TrustedHTML is the only opt-in escape bypass. Callers must pass a literal or
// HTML constructed by trusted framework code, never untrusted request data.
func TrustedHTML(value string) Value { return Value{kind: ValueSafeHTML, text: value} }

func List(values ...Value) Value {
	clone := make([]Value, len(values))
	for index := range values {
		clone[index] = values[index].clone()
	}
	return Value{kind: ValueList, list: clone}
}

// Object copies a closed string-to-Value object. Private/underscore names are
// rejected before template publication.
func Object(values map[string]Value) (Value, error) {
	clone := make(map[string]Value, len(values))
	for name, value := range values {
		if !validIdentifier(name) {
			return Value{}, &ValueError{Path: name, Code: "invalid_key"}
		}
		clone[name] = value.clone()
	}
	return Value{kind: ValueObject, object: clone}, nil
}

func (v Value) Kind() ValueKind { return v.kind }
func (v Value) IsNull() bool    { return v.kind == ValueNull }

func (v Value) AsString() (string, bool) {
	return v.text, v.kind == ValueString || v.kind == ValueSafeHTML
}

func (v Value) AsBool() (bool, bool) {
	return v.boolean, v.kind == ValueBoolean
}

func (v Value) AsInteger() (int64, bool) {
	return v.integer, v.kind == ValueInteger
}

func (v Value) Items() ([]Value, bool) {
	if v.kind != ValueList {
		return nil, false
	}
	items := make([]Value, len(v.list))
	for index := range v.list {
		items[index] = v.list[index].clone()
	}
	return items, true
}

// Member returns a detached object member.
func (v Value) Member(name string) (Value, bool) {
	if v.kind != ValueObject || !validIdentifier(name) {
		return Value{}, false
	}
	value, ok := v.object[name]
	return value.clone(), ok
}

// Members returns object entries sorted by name so no map iteration order leaks
// into a public result.
func (v Value) Members() ([]Member, bool) {
	if v.kind != ValueObject {
		return nil, false
	}
	names := make([]string, 0, len(v.object))
	for name := range v.object {
		names = append(names, name)
	}
	sort.Strings(names)
	items := make([]Member, len(names))
	for index, name := range names {
		items[index] = Member{name: name, value: v.object[name].clone()}
	}
	return items, true
}

type Member struct {
	name  string
	value Value
}

func (m Member) Name() string { return m.name }
func (m Member) Value() Value { return m.value.clone() }

func (v Value) clone() Value {
	clone := v
	if v.list != nil {
		clone.list = make([]Value, len(v.list))
		for index := range v.list {
			clone.list[index] = v.list[index].clone()
		}
	}
	if v.object != nil {
		clone.object = make(map[string]Value, len(v.object))
		for name, value := range v.object {
			clone.object[name] = value.clone()
		}
	}
	return clone
}

func (v Value) truth() bool {
	switch v.kind {
	case ValueNull:
		return false
	case ValueString, ValueSafeHTML:
		return v.text != ""
	case ValueBoolean:
		return v.boolean
	case ValueInteger:
		return v.integer != 0
	case ValueList:
		return len(v.list) != 0
	case ValueObject:
		return len(v.object) != 0
	default:
		return false
	}
}

// ValueError reports an invalid closed-value construction.
type ValueError struct {
	Path string
	Code string
}

func (e *ValueError) Error() string {
	return fmt.Sprintf("templates: value %q: %s", e.Path, e.Code)
}

// Context is an immutable top-level name resolver.
type Context struct {
	values map[string]Value
}

func NewContext(values map[string]Value) (Context, error) {
	clone := make(map[string]Value, len(values))
	for name, value := range values {
		if !validIdentifier(name) {
			return Context{}, &ValueError{Path: name, Code: "invalid_context_name"}
		}
		clone[name] = value.clone()
	}
	return Context{values: clone}, nil
}

func (c Context) Get(name string) (Value, bool) {
	if !validIdentifier(name) {
		return Value{}, false
	}
	value, ok := c.values[name]
	return value.clone(), ok
}

func (c Context) with(name string, value Value) Context {
	values := make(map[string]Value, len(c.values)+1)
	for current, item := range c.values {
		values[current] = item
	}
	values[name] = value
	return Context{values: values}
}

func validIdentifier(name string) bool {
	if name == "" || strings.HasPrefix(name, "_") || !utf8.ValidString(name) || strings.ContainsRune(name, 0) {
		return false
	}
	for index, r := range name {
		if r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || index > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}
