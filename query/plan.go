// Package query owns GoDj's database-independent query AST. Plan values use
// copy-on-write operations so a derived QuerySet cannot mutate its source.
package query

import (
	"math"
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
	sourceFields       []FieldRef
	where              Expression
	orderings          []Ordering
	limit              *int
	offset             *int
	distinct           bool
	result             ResultShape
	relationProjection *RelationProjection
}

func NewPlan(table string, sourceFields []FieldRef) Plan {
	return Plan{
		table:        table,
		sourceFields: append([]FieldRef(nil), sourceFields...),
		result:       modelResult(),
	}
}

func (p Plan) Table() string {
	return p.table
}

func (p Plan) SourceFields() []FieldRef {
	return append([]FieldRef(nil), p.sourceFields...)
}

// Where returns the one authoritative immutable Boolean expression tree.
// False means that the plan has no predicate; there is no empty Boolean
// constant in the query AST.
func (p Plan) Where() (Expression, bool) {
	if p.where.node == nil {
		return Expression{}, false
	}
	return p.where, true
}

// Conditions returns a detached ordered DFS leaf inventory for diagnostics
// and compatibility tests. It is computed from Where on every call and is not
// an authoritative query representation; connector and negation semantics
// are intentionally absent from this view.
func (p Plan) Conditions() []Condition {
	return expressionConditions(p.where)
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

func (p Plan) Offset() (int, bool) {
	if p.offset == nil {
		return 0, false
	}
	return *p.offset, true
}

func (p Plan) Distinct() bool { return p.distinct }

func (p Plan) ResultShape() ResultShape { return p.result.clone() }

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
	if p.result.Kind() != ResultModel {
		return Plan{}, invalidPlanError("relation projection cannot combine with a non-model result")
	}
	clone := p.clone()
	value := projection.clone()
	clone.relationProjection = &value
	return clone, nil
}

func (p Plan) WithConditions(conditions ...Condition) Plan {
	clone := p.clone()
	expressions := make([]Expression, 0, len(conditions)+1)
	if clone.where.node != nil {
		expressions = append(expressions, clone.where)
	}
	for _, condition := range conditions {
		expressions = append(expressions, newUncheckedExpression(condition))
	}
	clone.where = uncheckedAndExpressions(expressions...)
	return clone
}

// WithWhere derives a plan by implicitly AND-ing one validated expression
// with the existing authoritative where tree. It is the error-returning path
// used by ORM construction; low-level callers that need the historical
// non-error signature continue to use WithConditions.
func (p Plan) WithWhere(expression Expression) (Plan, error) {
	if err := expression.validate(); err != nil {
		return Plan{}, err
	}
	where := expression
	if p.where.node != nil {
		var err error
		where, err = AndExpressions(p.where, expression)
		if err != nil {
			return Plan{}, err
		}
	}
	if err := p.validateWhereSource(where); err != nil {
		return Plan{}, err
	}
	clone := p.clone()
	clone.where = where
	return clone, nil
}

func (p Plan) validateWhereSource(expression Expression) error {
	return p.validateWhereNode(expression.node, true)
}

func (p Plan) validateWhereNode(node *expressionNode, relationAtRootConjunction bool) error {
	if node.kind == ExpressionLeaf {
		condition := node.condition
		path := condition.relationPath
		if path == nil {
			if !slices.Contains(p.sourceFields, condition.field) {
				return invalidPlanError("query expression scalar field is not part of the plan source metadata")
			}
			return nil
		}
		if !relationAtRootConjunction {
			return &Error{
				Category: CategoryQuery,
				Code:     CodeUnsupported,
				Field:    condition.field.name,
				Lookup:   string(condition.lookup),
				Detail:   "relation predicates under OR or NOT are not supported",
			}
		}
		if len(path.hops) != 1 {
			return invalidPlanError("query expression relation path must contain exactly one hop")
		}
		hop := path.hops[0]
		switch path.scope {
		case RelationTerminalSourceKey:
			if !slices.Contains(p.sourceFields, condition.field) {
				return invalidPlanError("query expression relation source key is not part of the plan source metadata")
			}
		case RelationTerminalRelatedField:
			switch hop.direction {
			case RelationForward:
				sourceKey := NewFieldRef(hop.field, hop.sourceColumn, FieldInteger, hop.nullable)
				if hop.sourceTable != p.table || !slices.Contains(p.sourceFields, sourceKey) {
					return invalidPlanError("query expression forward relation source key is not part of the plan source metadata")
				}
			case RelationReverse:
				if hop.targetTable != p.table || !containsPlanIntegerColumn(p.sourceFields, hop.targetPrimaryKeyColumn) {
					return invalidPlanError("query expression reverse relation root key is not part of the plan source metadata")
				}
			default:
				return invalidPlanError("query expression relation direction is invalid")
			}
		default:
			return invalidPlanError("query expression relation terminal scope is invalid")
		}
		return nil
	}

	childRelationAtRoot := relationAtRootConjunction && node.kind == ExpressionAnd
	for _, child := range node.children {
		if err := p.validateWhereNode(child.node, childRelationAtRoot); err != nil {
			return err
		}
	}
	return nil
}

func containsPlanIntegerColumn(fields []FieldRef, column string) bool {
	for _, field := range fields {
		if field.column == column && field.kind == FieldInteger && !field.nullable {
			return true
		}
	}
	return false
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

func (p Plan) WithOffset(offset int) (Plan, error) {
	if offset < 0 || int64(offset) > math.MaxInt32 {
		return Plan{}, &Error{Category: CategoryQuery, Code: CodeInvalidOffset, Detail: "offset must be between zero and 2147483647"}
	}
	clone := p.clone()
	clone.offset = &offset
	return clone, nil
}

func (p Plan) WithDistinct() Plan {
	clone := p.clone()
	clone.distinct = true
	return clone
}

func (p Plan) WithResultShape(result ResultShape) (Plan, error) {
	if err := result.validate(); err != nil {
		return Plan{}, err
	}
	if p.relationProjection != nil && result.Kind() != ResultModel {
		return Plan{}, invalidPlanError("relation projection cannot combine with a non-model result")
	}
	for _, expression := range result.Expressions() {
		if field, ok := expression.Field(); ok && !slices.Contains(p.sourceFields, field) {
			return Plan{}, invalidPlanError("result field is not part of the plan source metadata")
		}
	}
	clone := p.clone()
	clone.result = result.clone()
	return clone, nil
}

func (p Plan) Equal(other Plan) bool {
	if p.table != other.table || !slices.Equal(p.sourceFields, other.sourceFields) ||
		p.distinct != other.distinct || !p.result.Equal(other.result) {
		return false
	}
	if !p.where.Equal(other.where) {
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
	leftOffset, leftOK := p.Offset()
	rightOffset, rightOK := other.Offset()
	if leftOK != rightOK || (leftOK && leftOffset != rightOffset) {
		return false
	}
	leftProjection, leftOK := p.RelationProjection()
	rightProjection, rightOK := other.RelationProjection()
	return leftOK == rightOK && (!leftOK || leftProjection.Equal(rightProjection))
}

func (p Plan) clone() Plan {
	clone := p
	clone.sourceFields = append([]FieldRef(nil), p.sourceFields...)
	clone.orderings = append([]Ordering(nil), p.orderings...)
	if p.limit != nil {
		limit := *p.limit
		clone.limit = &limit
	}
	if p.offset != nil {
		offset := *p.offset
		clone.offset = &offset
	}
	clone.result = p.result.clone()
	if p.relationProjection != nil {
		projection := p.relationProjection.clone()
		clone.relationProjection = &projection
	}
	return clone
}
