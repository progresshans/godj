// Package query owns GoDj's database-independent query AST. Constructors copy
// caller-owned containers; immutable handles share private storage. Accessors
// copy mutable containers so a derived QuerySet cannot mutate its source.
package query

import (
	"math"
	"slices"
	"strings"
)

type FieldKind string

const (
	FieldInteger  FieldKind = "integer"
	FieldString   FieldKind = "string"
	FieldBoolean  FieldKind = "boolean"
	FieldDateTime FieldKind = "datetime"
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
	LookupExact              Lookup = "exact"
	LookupGreaterThan        Lookup = "gt"
	LookupGreaterThanOrEqual Lookup = "gte"
	LookupLessThan           Lookup = "lt"
	LookupLessThanOrEqual    Lookup = "lte"
	LookupIContains          Lookup = "icontains"
	LookupIsNull             Lookup = "isnull"
	LookupIn                 Lookup = "in"
)

type conditionRHSKind uint8

const (
	conditionRHSLiteral conditionRHSKind = iota + 1
	conditionRHSList
	conditionRHSField
)

type conditionRHS struct {
	kind   conditionRHSKind
	value  Value
	values []Value
	field  FieldRef
}

type Condition struct {
	field        FieldRef
	lookup       Lookup
	rhs          *conditionRHS
	relationPath *RelationPath
}

func NewCondition(field FieldRef, lookup Lookup, value Value) Condition {
	return Condition{
		field:  field,
		lookup: lookup,
		rhs:    &conditionRHS{kind: conditionRHSLiteral, value: value},
	}
}

// NewInCondition constructs one immutable scalar-list membership condition.
// The values are copied so later caller mutation cannot alter the condition or
// a query plan that contains it.
// Empty lists are valid. Explicit NULL members remain part of the AST because
// negation of a nullable field distinguishes them from an absent member.
func NewInCondition(field FieldRef, values []Value) (Condition, error) {
	if !validInValues(field, values) {
		return Condition{}, &Error{
			Category: CategoryQuery,
			Code:     CodeInvalidPlan,
			Detail:   "IN requires a supported field and a same-kind scalar or NULL value list",
		}
	}
	return Condition{
		field:  field,
		lookup: LookupIn,
		rhs:    &conditionRHS{kind: conditionRHSList, values: append([]Value(nil), values...)},
	}, nil
}

// NewRelatedInCondition owns the same scalar list as NewInCondition while
// retaining a direct forward path. Reverse and source-key membership are not
// supported; nullable source-key isnull remains a separate path scope.
func NewRelatedInCondition(path RelationPath, values []Value) (Condition, error) {
	if !forwardMembershipPath(path) {
		return Condition{}, invalidPlanError("related IN requires a valid forward target-field path")
	}
	condition, err := NewInCondition(path.Terminal(), values)
	if err != nil {
		return Condition{}, err
	}
	condition.relationPath = &path
	return condition, nil
}

func forwardMembershipPath(path RelationPath) bool {
	return path.scope == RelationTerminalRelatedField && path.validateForward() == nil
}

// NewFieldCondition constructs one scalar comparison whose right-hand side is
// another field in the same eventual plan source. Source membership is
// intentionally deferred to Plan.WithWhere and repeated by each backend;
// this constructor validates the lookup, kinds, and scalar-only shape.
func NewFieldCondition(field FieldRef, lookup Lookup, right FieldRef) (Condition, error) {
	condition := Condition{
		field:  field,
		lookup: lookup,
		rhs:    &conditionRHS{kind: conditionRHSField, field: right},
	}
	if err := validateExpressionCondition(condition); err != nil {
		return Condition{}, err
	}
	return condition, nil
}

// NewRelatedCondition constructs a condition over an immutable relation path.
func NewRelatedCondition(path RelationPath, lookup Lookup, value Value) Condition {
	return Condition{
		field:        path.Terminal(),
		lookup:       lookup,
		rhs:          &conditionRHS{kind: conditionRHSLiteral, value: value},
		relationPath: &path,
	}
}

func (c Condition) Field() FieldRef { return c.field }
func (c Condition) Lookup() Lookup  { return c.lookup }

// OperandNullable reports whether the left scalar can be NULL in the supported
// read paths. An optional forward target can be absent even when its field is
// declared non-null. This describes the operand, not the Boolean lookup result.
func (c Condition) OperandNullable() bool {
	if c.field.Nullable() {
		return true
	}
	path := c.relationPath
	if path != nil {
		for _, hop := range path.hops {
			if hop.direction == RelationForward && hop.nullable {
				return true
			}
		}
	}
	return false
}
func (c Condition) Value() Value {
	if c.lookup == LookupIn || c.rhs == nil || c.rhs.kind != conditionRHSLiteral {
		return Value{}
	}
	return c.rhs.value
}
func (c Condition) Values() ([]Value, bool) {
	if c.lookup != LookupIn || c.rhs == nil || c.rhs.kind != conditionRHSList ||
		(c.relationPath != nil && (!forwardMembershipPath(*c.relationPath) || !c.field.Equal(c.relationPath.terminal))) ||
		!validInValues(c.field, c.rhs.values) {
		return nil, false
	}
	return append([]Value(nil), c.rhs.values...), true
}

// RHSField returns the right-hand-side source field for a field-to-field
// comparison. The returned value is detached and immutable.
func (c Condition) RHSField() (FieldRef, bool) {
	if c.rhs == nil || c.rhs.kind != conditionRHSField || c.relationPath != nil {
		return FieldRef{}, false
	}
	return c.rhs.field, true
}
func (c Condition) RelationPath() (RelationPath, bool) {
	if c.relationPath == nil {
		return RelationPath{}, false
	}
	return *c.relationPath, true
}
func (c Condition) Equal(other Condition) bool {
	if c.field != other.field || c.lookup != other.lookup || (c.rhs == nil) != (other.rhs == nil) {
		return false
	}
	if c.rhs != nil {
		if c.rhs.kind != other.rhs.kind || c.rhs.value != other.rhs.value || c.rhs.field != other.rhs.field ||
			!slices.EqualFunc(c.rhs.values, other.rhs.values, func(left, right Value) bool {
				return left.Equal(right)
			}) {
			return false
		}
	}
	leftPath, leftOK := c.RelationPath()
	rightPath, rightOK := other.RelationPath()
	return leftOK == rightOK && (!leftOK || leftPath.Equal(rightPath))
}

func validInValues(field FieldRef, values []Value) bool {
	if field.name == "" || field.column == "" ||
		strings.ContainsRune(field.name, '\x00') || strings.ContainsRune(field.column, '\x00') {
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
	case FieldDateTime:
		expected = ValueDateTime
	default:
		return false
	}
	for _, value := range values {
		if !value.IsNull() && value.Kind() != expected {
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
	table               string
	sourceFields        []FieldRef
	where               Expression
	orderings           []Ordering
	limit               *int
	offset              *int
	distinct            bool
	result              ResultShape
	relationProjections []RelationProjection
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

func (p Plan) ResultShape() ResultShape { return p.result }

// RelationProjections returns a detached, canonical list of immutable selected
// targets. Declaration names own ordering, independent of caller order.
func (p Plan) RelationProjections() []RelationProjection {
	return slices.Clone(p.relationProjections)
}

// WithoutRelationProjections preserves the logical source and its filters,
// ordering and slice while removing all eager materialization.
func (p Plan) WithoutRelationProjections() Plan {
	p.relationProjections = nil
	return p
}

// WithRelationProjections adds selected targets. Repeated identical declarations
// coalesce only after validation; conflicting metadata never overwrites a target.
func (p Plan) WithRelationProjections(projections ...RelationProjection) (Plan, error) {
	if len(projections) == 0 {
		return Plan{}, invalidPlanError("relation selection requires at least one projection")
	}
	if p.result.Kind() != ResultModel {
		return Plan{}, invalidPlanError("relation projection cannot combine with a non-model result")
	}
	all := make([]RelationProjection, 0, len(p.relationProjections)+len(projections))
	all = append(all, p.relationProjections...)
	all = append(all, projections...)
	byField := make(map[string]RelationProjection, len(all))
	var root RelationHop
	for index, projection := range all {
		if err := projection.validate(); err != nil {
			return Plan{}, err
		}
		hop := projection.Hop()
		if index == 0 {
			root = hop
		} else if hop.Source() != root.Source() || hop.SourceTable() != root.SourceTable() {
			return Plan{}, invalidPlanError("selected projections do not share one source model")
		}
		if previous, exists := byField[hop.Field()]; exists && !previous.Equal(projection) {
			return Plan{}, invalidPlanError("selected projections contain conflicting FK or target metadata")
		}
		byField[hop.Field()] = projection
	}
	canonical := make([]RelationProjection, 0, len(byField))
	for _, projection := range byField {
		canonical = append(canonical, projection)
	}
	slices.SortFunc(canonical, func(left, right RelationProjection) int {
		return strings.Compare(left.Hop().Field(), right.Hop().Field())
	})
	p.relationProjections = canonical
	return p, nil
}

// WithConditions adds one validated conjunction. Invalid conditions and
// oversized input fail during construction, before any backend can perform I/O.
func (p Plan) WithConditions(conditions ...Condition) (Plan, error) {
	if len(conditions) == 0 {
		return p, nil
	}
	if len(conditions) >= maximumExpressionNodes {
		return Plan{}, invalidPlanError("query expression exceeds the maximum node count of 1024")
	}
	expressions := make([]Expression, len(conditions))
	for index, condition := range conditions {
		expression, err := NewExpression(condition)
		if err != nil {
			return Plan{}, err
		}
		expressions[index] = expression
	}
	where := expressions[0]
	if len(expressions) > 1 {
		var err error
		where, err = AndExpressions(expressions[0], expressions[1], expressions[2:]...)
		if err != nil {
			return Plan{}, err
		}
	}
	return p.WithWhere(where)
}

// WithWhere derives a plan by implicitly AND-ing one validated expression
// with the existing authoritative where tree.
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
	if err := p.validateWhereSource(expression); err != nil {
		return Plan{}, err
	}
	clone := p
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
			if right, ok := condition.RHSField(); ok && !slices.Contains(p.sourceFields, right) {
				return invalidPlanError("query expression right-hand-side field is not part of the plan source metadata")
			}
			return nil
		}
		if err := path.Validate(); err != nil {
			return err
		}
		hop := path.hops[0]
		if hop.direction == RelationReverse {
			if !relationAtRootConjunction {
				return &Error{Category: CategoryQuery, Code: CodeUnsupported, Field: condition.field.name, Lookup: string(condition.lookup), Detail: "reverse relation predicates under OR or NOT are not supported"}
			}
			if hop.targetTable != p.table || !containsPlanIntegerColumn(p.sourceFields, hop.targetPrimaryKeyColumn) {
				return invalidPlanError("query expression reverse relation root key is not part of the plan source metadata")
			}
		} else {
			sourceKey := NewFieldRef(hop.field, hop.sourceColumn, FieldInteger, hop.nullable)
			if hop.sourceTable != p.table || !slices.Contains(p.sourceFields, sourceKey) {
				return invalidPlanError("query expression forward relation source key is not part of the plan source metadata")
			}
		}
		if path.scope == RelationTerminalSourceKey && condition.lookup != LookupIsNull {
			return invalidPlanError("relation source-key terminals support isnull only")
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
	clone := p
	clone.orderings = append([]Ordering(nil), orderings...)
	return clone
}

func (p Plan) WithLimit(limit int) (Plan, error) {
	if limit < 0 {
		return Plan{}, &Error{Category: CategoryQuery, Code: CodeInvalidLimit, Detail: "limit cannot be negative"}
	}
	clone := p
	clone.limit = &limit
	return clone, nil
}

func (p Plan) WithOffset(offset int) (Plan, error) {
	if offset < 0 || int64(offset) > math.MaxInt32 {
		return Plan{}, &Error{Category: CategoryQuery, Code: CodeInvalidOffset, Detail: "offset must be between zero and 2147483647"}
	}
	clone := p
	clone.offset = &offset
	return clone, nil
}

func (p Plan) WithDistinct() Plan {
	clone := p
	clone.distinct = true
	return clone
}

func (p Plan) WithResultShape(result ResultShape) (Plan, error) {
	if err := result.validate(); err != nil {
		return Plan{}, err
	}
	if len(p.relationProjections) != 0 && result.Kind() != ResultModel {
		return Plan{}, invalidPlanError("relation projection cannot combine with a non-model result")
	}
	for _, expression := range result.Expressions() {
		if field, ok := expression.Field(); ok && !slices.Contains(p.sourceFields, field) {
			return Plan{}, invalidPlanError("result field is not part of the plan source metadata")
		}
	}
	clone := p
	clone.result = result
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
	return slices.EqualFunc(p.relationProjections, other.relationProjections, func(left, right RelationProjection) bool { return left.Equal(right) })
}
