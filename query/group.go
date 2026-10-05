package query

import "slices"

const (
	MaxGroupKeys            = 32
	MaxAggregateExpressions = 64
	MaxGroupExpressionNodes = 4096
)

// CountResult counts non-NULL values. The operand retains the exact scalar
// metadata and optional forward route; it is never a SQL string or alias.
func CountResult(operand ResultExpression) (ResultExpression, error) {
	if err := validateGroupKey(operand); err != nil {
		return ResultExpression{}, err
	}
	operand.kind = ResultCount
	return operand, nil
}

func (e ResultExpression) IsAggregate() bool {
	return e.kind == ResultCountAll || e.kind == ResultCount || e.kind == ResultMin || e.kind == ResultMax
}
func (e ResultExpression) Distinct() bool             { return e.distinct }
func (e ResultExpression) Filter() (Expression, bool) { return e.filter, e.filter.node != nil }

func (e ResultExpression) WithDistinct() (ResultExpression, error) {
	if err := e.validateAggregate(); err != nil {
		return ResultExpression{}, err
	}
	if e.kind != ResultCount {
		return ResultExpression{}, groupUnsupported("DISTINCT requires a counted field")
	}
	e.distinct = true
	return e, nil
}

func (e ResultExpression) WithFilter(filter Expression) (ResultExpression, error) {
	if err := e.validateAggregate(); err != nil {
		return ResultExpression{}, err
	}
	if e.kind == ResultCountAll {
		return ResultExpression{}, groupUnsupported("filtered count requires an explicit field")
	}
	if err := filter.validate(); err != nil {
		return ResultExpression{}, err
	}
	if err := validateAggregateFilter(filter); err != nil {
		return ResultExpression{}, err
	}
	if e.filter.node != nil {
		var err error
		filter, err = AndExpressions(e.filter, filter)
		if err != nil {
			return ResultExpression{}, err
		}
	}
	e.filter = filter
	return e, nil
}

func (e ResultExpression) validateAggregate() error {
	if e.path.Valid() {
		return groupUnsupported("aggregate JSON paths are not implemented")
	}
	if e.kind == ResultCountAll {
		if e.field != (FieldRef{}) || e.relation != nil || e.distinct || e.filter.node != nil {
			return invalidPlanError("COUNT(*) cannot contain a field, DISTINCT, or filter")
		}
		return nil
	}
	if e.kind != ResultCount && e.kind != ResultMin && e.kind != ResultMax {
		return invalidPlanError("aggregate expression is invalid")
	}
	if !validResultField(e.field) {
		return invalidPlanError("aggregate requires valid scalar field metadata")
	}
	if e.field.Kind() == FieldJSON {
		return groupUnsupported("JSON aggregate operands are not implemented")
	}
	if e.kind != ResultCount && e.field.Kind() == FieldBoolean {
		return invalidPlanError("MIN/MAX require an ordered scalar field")
	}
	if e.distinct && e.kind != ResultCount {
		return invalidPlanError("DISTINCT requires a counted field")
	}
	if e.relation != nil {
		if !e.relation.Terminal().Equal(e.field) {
			return invalidPlanError("aggregate route terminal differs from field metadata")
		}
		if err := e.relation.validateForwardSelection(); err != nil {
			return err
		}
	}
	if e.filter.node != nil {
		return validateAggregateFilter(e.filter)
	}
	return nil
}

func validateAggregateFilter(e Expression) error {
	if err := e.validate(); err != nil {
		return err
	}
	if condition, leaf := e.Condition(); leaf {
		if path, related := condition.RelationPath(); related {
			if err := path.validateForward(); err != nil {
				return groupUnsupported("conditional aggregates require finite forward relation predicates")
			}
		}
		return nil
	}
	for _, child := range e.Children() {
		if err := validateAggregateFilter(child); err != nil {
			return err
		}
	}
	return nil
}

func validateGroupKey(e ResultExpression) error {
	if e.kind != ResultField || e.distinct || e.filter.node != nil || e.path.Valid() {
		return groupUnsupported("group keys require scalar fields")
	}
	if !validResultField(e.field) {
		return invalidPlanError("group key has invalid field metadata")
	}
	if e.field.Kind() == FieldJSON {
		return groupUnsupported("JSON group keys require a separate equality contract")
	}
	if e.relation != nil {
		if !e.relation.Terminal().Equal(e.field) {
			return invalidPlanError("group key route differs from its terminal field")
		}
		return e.relation.validateForwardSelection()
	}
	return nil
}

// GroupExpression is a bounded immutable predicate over selected group keys
// and aggregate expressions. It cannot refer to an arbitrary source column.
// NOT follows SQL three-valued logic, including NULL aggregate results.
type GroupExpression struct{ node *groupExpressionNode }
type groupExpressionNode struct {
	kind         ExpressionKind
	value        ResultExpression
	lookup       Lookup
	literal      Value
	children     []GroupExpression
	nodes, depth int
}

func NewGroupExpression(value ResultExpression, lookup Lookup, literal Value) (GroupExpression, error) {
	var err error
	if value.IsAggregate() {
		err = value.validateAggregate()
	} else {
		err = validateGroupKey(value)
	}
	if err != nil {
		return GroupExpression{}, err
	}
	kind := value.field.Kind()
	if value.kind == ResultCount || value.kind == ResultCountAll {
		kind = FieldInteger
	}
	if _, err := literal.DatabaseValue(); err != nil {
		return GroupExpression{}, err
	}
	switch lookup {
	case LookupIsNull:
		if literal.Kind() != ValueBoolean {
			return GroupExpression{}, invalidPlanError("group isnull requires Boolean")
		}
	case LookupExact:
		if !expressionValueMatchesField(literal.Kind(), kind) {
			return GroupExpression{}, invalidPlanError("group comparison value does not match result kind")
		}
	case LookupGreaterThan, LookupGreaterThanOrEqual, LookupLessThan, LookupLessThanOrEqual:
		if !expressionOrderedValueMatchesField(literal.Kind(), kind) {
			return GroupExpression{}, invalidPlanError("group comparison requires an ordered same-kind value")
		}
	default:
		return GroupExpression{}, groupUnsupported("group predicate lookup is not implemented")
	}
	return GroupExpression{node: &groupExpressionNode{kind: ExpressionLeaf, value: value, lookup: lookup, literal: literal, nodes: 1, depth: 1}}, nil
}

func (e GroupExpression) Kind() ExpressionKind {
	if e.node == nil {
		return 0
	}
	return e.node.kind
}
func (e GroupExpression) Leaf() (ResultExpression, Lookup, Value, bool) {
	if e.node == nil || e.node.kind != ExpressionLeaf {
		return ResultExpression{}, "", Value{}, false
	}
	return e.node.value, e.node.lookup, e.node.literal, true
}
func (e GroupExpression) Children() []GroupExpression {
	if e.node == nil {
		return nil
	}
	return slices.Clone(e.node.children)
}
func (e GroupExpression) Equal(other GroupExpression) bool {
	if e.node == nil || other.node == nil {
		return e.node == other.node
	}
	return e.node.kind == other.node.kind && e.node.value.Equal(other.node.value) && e.node.lookup == other.node.lookup &&
		e.node.literal == other.node.literal && slices.EqualFunc(e.node.children, other.node.children, GroupExpression.Equal)
}

func AndGroupExpressions(left, right GroupExpression, rest ...GroupExpression) (GroupExpression, error) {
	if len(rest) > maximumExpressionNodes-3 {
		return GroupExpression{}, invalidPlanError("group predicate exceeds 1024 nodes")
	}
	return connectGroupExpressions(ExpressionAnd, append([]GroupExpression{left, right}, rest...))
}
func OrGroupExpressions(left, right GroupExpression, rest ...GroupExpression) (GroupExpression, error) {
	if len(rest) > maximumExpressionNodes-3 {
		return GroupExpression{}, invalidPlanError("group predicate exceeds 1024 nodes")
	}
	return connectGroupExpressions(ExpressionOr, append([]GroupExpression{left, right}, rest...))
}
func NotGroupExpression(value GroupExpression) (GroupExpression, error) {
	return connectGroupExpressions(ExpressionNot, []GroupExpression{value})
}
func connectGroupExpressions(kind ExpressionKind, values []GroupExpression) (GroupExpression, error) {
	if len(values) > maximumExpressionNodes {
		return GroupExpression{}, invalidPlanError("group predicate exceeds 1024 nodes")
	}
	nodes, depth := 1, 0
	for _, value := range values {
		if value.node == nil {
			return GroupExpression{}, invalidPlanError("group predicate is zero")
		}
		nodes += value.node.nodes
		depth = max(depth, value.node.depth)
	}
	if nodes > maximumExpressionNodes || depth >= maximumExpressionDepth {
		return GroupExpression{}, invalidPlanError("group predicate exceeds node or depth limit")
	}
	return GroupExpression{node: &groupExpressionNode{kind: kind, children: slices.Clone(values), nodes: nodes, depth: depth + 1}}, nil
}

type NullOrder string

const (
	NullsNative NullOrder = "native"
	NullsFirst  NullOrder = "first"
	NullsLast   NullOrder = "last"
)

type GroupOrdering struct {
	value     ResultExpression
	direction Direction
	nulls     NullOrder
}

func NewGroupOrdering(value ResultExpression, direction Direction, nulls NullOrder) (GroupOrdering, error) {
	var err error
	if value.IsAggregate() {
		err = value.validateAggregate()
	} else {
		err = validateGroupKey(value)
	}
	if err != nil {
		return GroupOrdering{}, err
	}
	if direction != Ascending && direction != Descending {
		return GroupOrdering{}, invalidPlanError("group ordering direction is invalid")
	}
	if nulls != NullsNative && nulls != NullsFirst && nulls != NullsLast {
		return GroupOrdering{}, invalidPlanError("group NULL ordering is invalid")
	}
	return GroupOrdering{value, direction, nulls}, nil
}
func (o GroupOrdering) Expression() ResultExpression { return o.value }
func (o GroupOrdering) Direction() Direction         { return o.direction }
func (o GroupOrdering) Nulls() NullOrder             { return o.nulls }
func (o GroupOrdering) Equal(other GroupOrdering) bool {
	return o.value.Equal(other.value) && o.direction == other.direction && o.nulls == other.nulls
}

type GroupResultMode uint8

const (
	GroupRows GroupResultMode = iota + 1
	GroupCount
	GroupPage
)

type groupResult struct {
	keys   int
	having GroupExpression
	order  []GroupOrdering
	mode   GroupResultMode
}

func NewGroupedResult(keys, aggregates []ResultExpression) (ResultShape, error) {
	if len(keys) == 0 || len(keys) > MaxGroupKeys || len(aggregates) == 0 || len(aggregates) > MaxAggregateExpressions {
		return ResultShape{}, invalidPlanError("grouping requires 1..32 keys and 1..64 aggregates")
	}
	shape := ResultShape{kind: ResultGrouped, expressions: append(slices.Clone(keys), aggregates...), group: &groupResult{keys: len(keys), mode: GroupRows}}
	if err := shape.validateGrouped(); err != nil {
		return ResultShape{}, err
	}
	return shape, nil
}
func (s ResultShape) GroupKeys() []ResultExpression {
	if s.group == nil {
		return nil
	}
	return slices.Clone(s.expressions[:s.group.keys])
}
func (s ResultShape) GroupAggregates() []ResultExpression {
	if s.group == nil {
		return nil
	}
	return slices.Clone(s.expressions[s.group.keys:])
}
func (s ResultShape) GroupHaving() (GroupExpression, bool) {
	if s.group == nil {
		return GroupExpression{}, false
	}
	return s.group.having, s.group.having.node != nil
}
func (s ResultShape) GroupOrderings() []GroupOrdering {
	if s.group == nil {
		return nil
	}
	return slices.Clone(s.group.order)
}
func (s ResultShape) GroupMode() GroupResultMode {
	if s.group == nil {
		return 0
	}
	return s.group.mode
}
func equalGroupResult(left, right *groupResult) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.keys == right.keys && left.mode == right.mode && left.having.Equal(right.having) && slices.EqualFunc(left.order, right.order, GroupOrdering.Equal)
}
func (s ResultShape) validateGrouped() error {
	if s.group == nil || s.group.keys < 1 || s.group.keys > MaxGroupKeys || len(s.expressions) <= s.group.keys || len(s.expressions)-s.group.keys > MaxAggregateExpressions {
		return invalidPlanError("group result has invalid keys or aggregates")
	}
	if s.group.mode != GroupRows && s.group.mode != GroupCount && s.group.mode != GroupPage {
		return invalidPlanError("group result mode is invalid")
	}
	nodes := 0
	for index, value := range s.expressions {
		var err error
		if index < s.group.keys {
			err = validateGroupKey(value)
		} else {
			err = value.validateAggregate()
		}
		if err != nil {
			return err
		}
		if slices.ContainsFunc(s.expressions[:index], value.Equal) {
			return invalidPlanError("group result repeats a selected expression")
		}
		if value.filter.node != nil {
			nodes += value.filter.node.nodes
		}
	}
	if s.group.having.node != nil {
		nodes += s.group.having.node.nodes
		var validate func(GroupExpression) error
		validate = func(value GroupExpression) error {
			if selected, _, _, leaf := value.Leaf(); leaf {
				if !slices.ContainsFunc(s.expressions, selected.Equal) {
					return invalidPlanError("HAVING expression is not a selected group result")
				}
			}
			for _, child := range value.Children() {
				if err := validate(child); err != nil {
					return err
				}
			}
			return nil
		}
		if err := validate(s.group.having); err != nil {
			return err
		}
	}
	if nodes > MaxGroupExpressionNodes {
		return invalidPlanError("group predicates exceed the total node budget")
	}
	if len(s.group.order) > len(s.expressions) {
		return invalidPlanError("group ordering exceeds selected values")
	}
	for index, order := range s.group.order {
		if !slices.ContainsFunc(s.expressions, order.value.Equal) {
			return invalidPlanError("group ordering is not a selected result")
		}
		for _, previous := range s.group.order[:index] {
			if previous.value.Equal(order.value) {
				return invalidPlanError("group ordering repeats a result")
			}
		}
	}
	return nil
}

func (p Plan) WithGroupHaving(having GroupExpression) (Plan, error) {
	if p.result.group == nil || having.node == nil {
		return Plan{}, invalidPlanError("HAVING requires a grouped query and a predicate")
	}
	group := *p.result.group
	if group.having.node != nil {
		var err error
		having, err = AndGroupExpressions(group.having, having)
		if err != nil {
			return Plan{}, err
		}
	}
	group.having = having
	p.result.group = &group
	if err := p.ValidateGrouping(); err != nil {
		return Plan{}, err
	}
	return p, nil
}
func (p Plan) WithGroupOrderings(orderings ...GroupOrdering) (Plan, error) {
	if p.result.group == nil {
		return Plan{}, invalidPlanError("group ordering requires a grouped query")
	}
	group := *p.result.group
	group.order = slices.Clone(orderings)
	p.result.group = &group
	if err := p.ValidateGrouping(); err != nil {
		return Plan{}, err
	}
	return p, nil
}
func (p Plan) WithGroupMode(mode GroupResultMode) (Plan, error) {
	if p.result.group == nil {
		return Plan{}, invalidPlanError("group result mode requires a grouped query")
	}
	group := *p.result.group
	group.mode = mode
	p.result.group = &group
	if err := p.ValidateGrouping(); err != nil {
		return Plan{}, err
	}
	return p, nil
}

func (p Plan) ValidateGrouping() error {
	if p.result.kind != ResultGrouped {
		if p.result.group != nil {
			return invalidPlanError("non-grouped result contains grouping state")
		}
		return nil
	}
	if err := p.result.validateGrouped(); err != nil {
		return err
	}
	if len(p.orderings) != 0 || p.rowLock != nil || len(p.relationProjections) != 0 || p.prefetchWindow != nil {
		return groupUnsupported("grouped results cannot combine with row ordering, eager selection, windows or row locks")
	}
	for _, expression := range p.result.expressions {
		if err := p.validateResultSource(expression); err != nil {
			return err
		}
	}
	return nil
}

func groupUnsupported(detail string) error {
	return &Error{Category: CategoryQuery, Code: CodeUnsupported, Detail: detail}
}
