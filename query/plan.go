// Package query owns GoDj's database-independent query AST. Plan values use
// copy-on-write operations so a derived QuerySet cannot mutate its source.
package query

import "slices"

type FieldKind string

const (
	FieldInteger FieldKind = "integer"
	FieldString  FieldKind = "string"
	FieldBoolean FieldKind = "boolean"
)

type FieldRef struct {
	name     string
	column   string
	kind     FieldKind
	nullable bool
}

func NewFieldRef(name, column string, kind FieldKind, nullable bool) FieldRef {
	return FieldRef{name: name, column: column, kind: kind, nullable: nullable}
}

func (f FieldRef) Name() string              { return f.name }
func (f FieldRef) Column() string            { return f.column }
func (f FieldRef) Kind() FieldKind           { return f.kind }
func (f FieldRef) Nullable() bool            { return f.nullable }
func (f FieldRef) Equal(other FieldRef) bool { return f == other }

type Lookup string

const (
	LookupExact     Lookup = "exact"
	LookupIContains Lookup = "icontains"
	LookupIsNull    Lookup = "isnull"
)

type Condition struct {
	field  FieldRef
	lookup Lookup
	value  Value
}

func NewCondition(field FieldRef, lookup Lookup, value Value) Condition {
	return Condition{field: field, lookup: lookup, value: value}
}

func (c Condition) Field() FieldRef { return c.field }
func (c Condition) Lookup() Lookup  { return c.lookup }
func (c Condition) Value() Value    { return c.value }
func (c Condition) Equal(other Condition) bool {
	return c.field == other.field && c.lookup == other.lookup && c.value == other.value
}

type Direction string

const (
	Ascending  Direction = "asc"
	Descending Direction = "desc"
)

type Ordering struct {
	field     FieldRef
	direction Direction
}

func NewOrdering(field FieldRef, direction Direction) Ordering {
	return Ordering{field: field, direction: direction}
}

func (o Ordering) Field() FieldRef      { return o.field }
func (o Ordering) Direction() Direction { return o.direction }
func (o Ordering) Equal(other Ordering) bool {
	return o == other
}

type Plan struct {
	table      string
	columns    []FieldRef
	conditions []Condition
	orderings  []Ordering
	limit      *int
}

func NewPlan(table string, columns []FieldRef) Plan {
	return Plan{table: table, columns: append([]FieldRef(nil), columns...)}
}

func (p Plan) Table() string {
	return p.table
}

func (p Plan) Columns() []FieldRef {
	return append([]FieldRef(nil), p.columns...)
}

func (p Plan) Conditions() []Condition {
	return append([]Condition(nil), p.conditions...)
}

func (p Plan) Orderings() []Ordering {
	return append([]Ordering(nil), p.orderings...)
}

func (p Plan) Limit() (int, bool) {
	if p.limit == nil {
		return 0, false
	}
	return *p.limit, true
}

func (p Plan) WithConditions(conditions ...Condition) Plan {
	clone := p.clone()
	clone.conditions = append(clone.conditions, conditions...)
	return clone
}

func (p Plan) WithOrderings(orderings ...Ordering) Plan {
	clone := p.clone()
	clone.orderings = append([]Ordering(nil), orderings...)
	return clone
}

func (p Plan) WithLimit(limit int) (Plan, error) {
	if limit < 0 {
		return Plan{}, &Error{Category: CategoryQuery, Code: CodeInvalidLimit, Detail: "limit cannot be negative"}
	}
	clone := p.clone()
	clone.limit = &limit
	return clone, nil
}

func (p Plan) Equal(other Plan) bool {
	if p.table != other.table || !slices.Equal(p.columns, other.columns) {
		return false
	}
	if !slices.EqualFunc(p.conditions, other.conditions, func(left, right Condition) bool { return left.Equal(right) }) {
		return false
	}
	if !slices.EqualFunc(p.orderings, other.orderings, func(left, right Ordering) bool { return left.Equal(right) }) {
		return false
	}
	leftLimit, leftOK := p.Limit()
	rightLimit, rightOK := other.Limit()
	return leftOK == rightOK && (!leftOK || leftLimit == rightLimit)
}

func (p Plan) clone() Plan {
	clone := p
	clone.columns = append([]FieldRef(nil), p.columns...)
	clone.conditions = append([]Condition(nil), p.conditions...)
	clone.orderings = append([]Ordering(nil), p.orderings...)
	if p.limit != nil {
		limit := *p.limit
		clone.limit = &limit
	}
	return clone
}
