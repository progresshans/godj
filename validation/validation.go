// Package validation defines stable, presentation-independent validation
// diagnostics shared by forms and higher-level product packages.
package validation

import "slices"

// Field identifies the input field that owns a violation. NonField identifies
// a violation that belongs to the whole input rather than one field.
type Field string

const NonField Field = "__all__"

// Code is a stable machine-readable violation identifier. Human-readable
// messages intentionally do not live in this package.
type Code string

// Param is one ordered, presentation-independent diagnostic parameter.
// Its fields are private so callers cannot mutate a published violation.
type Param struct {
	key   string
	value string
}

// NewParam returns an immutable diagnostic parameter.
func NewParam(key, value string) Param {
	return Param{key: key, value: value}
}

func (p Param) Key() string   { return p.key }
func (p Param) Value() string { return p.value }

// Violation is an immutable validation diagnostic. Parameter order is part of
// the machine contract; display text is deliberately left to renderers.
type Violation struct {
	field  Field
	code   Code
	params []Param
}

// New returns a violation detached from the caller's parameter slice.
func New(field Field, code Code, params ...Param) Violation {
	return Violation{
		field:  field,
		code:   code,
		params: append([]Param(nil), params...),
	}
}

func (v Violation) Field() Field { return v.field }
func (v Violation) Code() Code   { return v.code }

// Params returns a detached copy in stable declaration order.
func (v Violation) Params() []Param {
	return append([]Param(nil), v.params...)
}

// Errors is an immutable ordered collection of violations. Its zero value is
// a valid empty collection.
type Errors struct {
	items []Violation
}

// NewErrors snapshots the caller's slice. Each Violation already owns its
// immutable parameters, which can be shared between collections.
func NewErrors(items ...Violation) Errors {
	return Errors{items: slices.Clone(items)}
}

func (e Errors) Len() int    { return len(e.items) }
func (e Errors) Empty() bool { return len(e.items) == 0 }

// At returns an immutable violation and false when index is outside the
// collection.
func (e Errors) At(index int) (Violation, bool) {
	if index < 0 || index >= len(e.items) {
		return Violation{}, false
	}
	return e.items[index], true
}

// All returns a detached copy in stable insertion order.
func (e Errors) All() []Violation {
	items := make([]Violation, len(e.items))
	copy(items, e.items)
	return items
}

// ByField returns matching violations in original insertion order.
func (e Errors) ByField(field Field) Errors {
	items := make([]Violation, 0)
	for _, item := range e.items {
		if item.field == field {
			items = append(items, item)
		}
	}
	return Errors{items: items}
}

// Append returns a new collection without changing either operand.
func (e Errors) Append(other Errors) Errors {
	return Join(e, other)
}

// Join concatenates immutable collections in argument order with at most one
// new item slice. Accumulate groups and join once instead of repeatedly copying
// a growing error prefix. Empty groups do not force a copy.
func Join(groups ...Errors) Errors {
	var only Errors
	count, nonempty := 0, 0
	for _, group := range groups {
		if !group.Empty() {
			count += len(group.items)
			nonempty++
			only = group
		}
	}
	if nonempty < 2 {
		return only
	}
	items := make([]Violation, 0, count)
	for _, group := range groups {
		items = append(items, group.items...)
	}
	return Errors{items: items}
}
