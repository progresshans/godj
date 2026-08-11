// Package query owns GoDj's database-independent query AST. Plan values use
// copy-on-write operations so a derived QuerySet cannot mutate its source.
package query

import (
	"slices"
	"strings"
)

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
	LookupIn        Lookup = "in"
)

type Condition struct {
	field        FieldRef
	lookup       Lookup
	value        Value
	values       *conditionValues
	relationPath *RelationPath
}

type conditionValues struct {
	values []Value
}

func NewCondition(field FieldRef, lookup Lookup, value Value) Condition {
	return Condition{field: field, lookup: lookup, value: value}
}

// NewInCondition constructs one immutable scalar-list membership condition.
// The values are copied so later caller mutation cannot alter the condition or
// a query plan that contains it.
func NewInCondition(field FieldRef, values []Value) (Condition, error) {
	if !validInValues(field, values) {
		return Condition{}, &Error{
			Category: CategoryQuery,
			Code:     CodeInvalidPlan,
			Detail:   "IN requires a supported field and a non-empty same-kind non-NULL value list",
		}
	}
	return Condition{
		field:  field,
		lookup: LookupIn,
		values: &conditionValues{values: append([]Value(nil), values...)},
	}, nil
}

// NewRelatedCondition constructs a condition over the terminal field of a
// relation path. The path is copied so callers cannot retain aliases into a
// query plan.
func NewRelatedCondition(path RelationPath, lookup Lookup, value Value) Condition {
	cloned := path.clone()
	return Condition{
		field:        cloned.Terminal(),
		lookup:       lookup,
		value:        value,
		relationPath: &cloned,
	}
}

func (c Condition) Field() FieldRef { return c.field }
func (c Condition) Lookup() Lookup  { return c.lookup }
func (c Condition) Value() Value {
	if c.lookup == LookupIn {
		return Value{}
	}
	return c.value
}
func (c Condition) Values() ([]Value, bool) {
	if c.lookup != LookupIn || c.values == nil || c.relationPath != nil ||
		!validInValues(c.field, c.values.values) {
		return nil, false
	}
	return append([]Value(nil), c.values.values...), true
}
func (c Condition) RelationPath() (RelationPath, bool) {
	if c.relationPath == nil {
		return RelationPath{}, false
	}
	return c.relationPath.clone(), true
}
func (c Condition) Equal(other Condition) bool {
	if c.field != other.field || c.lookup != other.lookup || c.value != other.value {
		return false
	}
	if (c.values == nil) != (other.values == nil) {
		return false
	}
	if c.values != nil && !slices.EqualFunc(c.values.values, other.values.values, func(left, right Value) bool {
		return left.Equal(right)
	}) {
		return false
	}
	leftPath, leftOK := c.RelationPath()
	rightPath, rightOK := other.RelationPath()
	return leftOK == rightOK && (!leftOK || leftPath.Equal(rightPath))
}

func (c Condition) clone() Condition {
	clone := c
	if c.values != nil {
		clone.values = &conditionValues{values: append([]Value(nil), c.values.values...)}
	}
	if c.relationPath != nil {
		path := c.relationPath.clone()
		clone.relationPath = &path
	}
	return clone
}

func validInValues(field FieldRef, values []Value) bool {
	if field.name == "" || field.column == "" ||
		strings.ContainsRune(field.name, '\x00') || strings.ContainsRune(field.column, '\x00') ||
		len(values) == 0 {
		return false
	}

	var expected ValueKind
	switch field.kind {
	case FieldInteger:
		expected = ValueInteger
	case FieldString:
		expected = ValueString
	case FieldBoolean:
		expected = ValueBoolean
	default:
		return false
	}
	for _, value := range values {
		if value.IsNull() || value.Kind() != expected {
			return false
		}
	}
	return true
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
	table              string
	columns            []FieldRef
	conditions         []Condition
	orderings          []Ordering
	limit              *int
	relationProjection *RelationProjection
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
	return cloneConditions(p.conditions)
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

// RelationProjection returns a detached copy of the singular eager relation
// projection carried by this plan. Plans without eager selection retain the
// exact pre-projection behavior and report false.
func (p Plan) RelationProjection() (RelationProjection, bool) {
	if p.relationProjection == nil {
		return RelationProjection{}, false
	}
	return p.relationProjection.clone(), true
}

// WithRelationProjection derives a plan with exactly one immutable forward
// relation projection. A projection is singular by contract: callers cannot
// overwrite or extend one that is already present.
func (p Plan) WithRelationProjection(projection RelationProjection) (Plan, error) {
	if p.relationProjection != nil {
		return Plan{}, invalidPlanError("query plan already contains a relation projection")
	}
	if err := projection.validate(); err != nil {
		return Plan{}, err
	}
	clone := p.clone()
	value := projection.clone()
	clone.relationProjection = &value
	return clone, nil
}

func (p Plan) WithConditions(conditions ...Condition) Plan {
	clone := p.clone()
	clone.conditions = append(clone.conditions, cloneConditions(conditions)...)
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
	if leftOK != rightOK || (leftOK && leftLimit != rightLimit) {
		return false
	}
	leftProjection, leftOK := p.RelationProjection()
	rightProjection, rightOK := other.RelationProjection()
	return leftOK == rightOK && (!leftOK || leftProjection.Equal(rightProjection))
}

func (p Plan) clone() Plan {
	clone := p
	clone.columns = append([]FieldRef(nil), p.columns...)
	clone.conditions = cloneConditions(p.conditions)
	clone.orderings = append([]Ordering(nil), p.orderings...)
	if p.limit != nil {
		limit := *p.limit
		clone.limit = &limit
	}
	if p.relationProjection != nil {
		projection := p.relationProjection.clone()
		clone.relationProjection = &projection
	}
	return clone
}

func cloneConditions(conditions []Condition) []Condition {
	if conditions == nil {
		return nil
	}
	clone := make([]Condition, len(conditions))
	for index := range conditions {
		clone[index] = conditions[index].clone()
	}
	return clone
}
