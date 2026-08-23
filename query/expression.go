package query

import "strings"

// ExpressionKind identifies one node in an immutable Boolean query
// expression. The zero value is invalid so a zero Expression fails closed when
// it is passed to a constructor.
type ExpressionKind uint8

const (
	ExpressionLeaf ExpressionKind = iota + 1
	ExpressionAnd
	ExpressionOr
	ExpressionNot
)

const (
	maximumExpressionDepth = 64
	maximumExpressionNodes = 1024
)

// Expression is an immutable handle to one Boolean query-expression node.
// Node state and child storage stay private; accessors return detached values
// so callers cannot mutate an expression after construction.
type Expression struct {
	node *expressionNode
}

type expressionNode struct {
	kind         ExpressionKind
	condition    Condition
	children     []Expression
	depth        int
	nodes        int
	hasRelations bool
}

// NewExpression constructs one validated leaf expression. The condition is
// cloned so list values and relation paths cannot alias caller-owned storage.
func NewExpression(condition Condition) (Expression, error) {
	if err := validateExpressionCondition(condition); err != nil {
		return Expression{}, err
	}
	return newUncheckedExpression(condition), nil
}

// newUncheckedExpression retains a low-level condition even when it is
// malformed. Plan.WithConditions uses this path because its historical
// signature cannot return an error; backend validation must still observe and
// reject the original leaf instead of receiving an empty or partial plan.
func newUncheckedExpression(condition Condition) Expression {
	cloned := condition.clone()
	return Expression{node: &expressionNode{
		kind:         ExpressionLeaf,
		condition:    cloned,
		depth:        1,
		nodes:        1,
		hasRelations: cloned.relationPath != nil,
	}}
}

// AndExpressions constructs one ordered n-ary AND. Nested AND nodes are
// flattened without reordering their children.
func AndExpressions(left, right Expression, rest ...Expression) (Expression, error) {
	return connectExpressions(ExpressionAnd, left, right, rest...)
}

// OrExpressions constructs one ordered n-ary OR. Nested OR nodes are flattened
// without reordering their children.
func OrExpressions(left, right Expression, rest ...Expression) (Expression, error) {
	return connectExpressions(ExpressionOr, left, right, rest...)
}

// NotExpression constructs one unary NOT. NOT nodes are deliberately not
// simplified so source structure and negation depth remain explicit.
func NotExpression(expression Expression) (Expression, error) {
	if err := expression.validate(); err != nil {
		return Expression{}, err
	}
	if expression.node.depth >= maximumExpressionDepth {
		return Expression{}, invalidPlanError("query expression exceeds the maximum depth of 64")
	}
	if expression.node.nodes >= maximumExpressionNodes {
		return Expression{}, invalidPlanError("query expression exceeds the maximum node count of 1024")
	}
	return Expression{node: &expressionNode{
		kind:         ExpressionNot,
		children:     []Expression{expression},
		depth:        expression.node.depth + 1,
		nodes:        expression.node.nodes + 1,
		hasRelations: expression.node.hasRelations,
	}}, nil
}

// Kind reports the node kind. A zero or malformed expression reports the zero
// kind, which is not a valid ExpressionKind.
func (e Expression) Kind() ExpressionKind {
	if e.node == nil {
		return 0
	}
	return e.node.kind
}

// Condition returns a detached condition for a leaf expression.
func (e Expression) Condition() (Condition, bool) {
	if e.node == nil || e.node.kind != ExpressionLeaf {
		return Condition{}, false
	}
	return e.node.condition.clone(), true
}

// Children returns a detached child slice for a connector expression. Child
// handles may share private immutable nodes, but replacing entries in the
// returned slice cannot mutate the source expression.
func (e Expression) Children() []Expression {
	if e.node == nil || e.node.kind == ExpressionLeaf || len(e.node.children) == 0 {
		return nil
	}
	return append([]Expression(nil), e.node.children...)
}

// Equal reports ordered structural equality. Connector child order is part of
// the expression because it determines deterministic argument traversal.
func (e Expression) Equal(other Expression) bool {
	return equalExpressionNodes(e.node, other.node)
}

// HasRelations reports whether any leaf retains relation-path provenance.
func (e Expression) HasRelations() bool {
	return e.node != nil && e.node.hasRelations
}

func connectExpressions(kind ExpressionKind, left, right Expression, rest ...Expression) (Expression, error) {
	if kind != ExpressionAnd && kind != ExpressionOr {
		return Expression{}, invalidPlanError("query expression connector kind is invalid")
	}
	// A connector contributes one node of its own, so at most 1,023
	// operands can survive flattening. Reject an obviously oversized call
	// before copying the variadic input or allocating child storage.
	if len(rest) > maximumExpressionNodes-3 {
		return Expression{}, invalidPlanError("query expression exceeds the maximum node count of 1024")
	}
	operands := make([]Expression, 0, len(rest)+2)
	operands = append(operands, left, right)
	operands = append(operands, rest...)
	for _, operand := range operands {
		if err := operand.validate(); err != nil {
			return Expression{}, err
		}
	}

	childCount := 0
	for _, operand := range operands {
		additionalChildren := 1
		if operand.node.kind == kind {
			additionalChildren = len(operand.node.children)
		}
		if additionalChildren > maximumExpressionNodes-1-childCount {
			return Expression{}, invalidPlanError("query expression exceeds the maximum node count of 1024")
		}
		childCount += additionalChildren
	}
	children := make([]Expression, 0, childCount)
	for _, operand := range operands {
		if operand.node.kind == kind {
			children = append(children, operand.node.children...)
		} else {
			children = append(children, operand)
		}
	}
	if len(children) < 2 {
		return Expression{}, invalidPlanError("AND and OR query expressions require at least two children")
	}

	depth := 1
	nodes := 1
	hasRelations := false
	for _, child := range children {
		if child.node.depth+1 > maximumExpressionDepth {
			return Expression{}, invalidPlanError("query expression exceeds the maximum depth of 64")
		}
		if child.node.nodes > maximumExpressionNodes-nodes {
			return Expression{}, invalidPlanError("query expression exceeds the maximum node count of 1024")
		}
		nodes += child.node.nodes
		if child.node.depth+1 > depth {
			depth = child.node.depth + 1
		}
		hasRelations = hasRelations || child.node.hasRelations
	}
	return Expression{node: &expressionNode{
		kind:         kind,
		children:     children,
		depth:        depth,
		nodes:        nodes,
		hasRelations: hasRelations,
	}}, nil
}

func (e Expression) validate() error {
	visited := 0
	_, _, _, err := validateExpressionNode(e.node, 1, &visited)
	return err
}

func validateExpressionNode(node *expressionNode, currentDepth int, visited *int) (int, int, bool, error) {
	if node == nil {
		return 0, 0, false, invalidPlanError("query expression is zero or malformed")
	}
	if currentDepth > maximumExpressionDepth {
		return 0, 0, false, invalidPlanError("query expression exceeds the maximum depth of 64")
	}
	(*visited)++
	if *visited > maximumExpressionNodes {
		return 0, 0, false, invalidPlanError("query expression exceeds the maximum node count of 1024")
	}

	switch node.kind {
	case ExpressionLeaf:
		if len(node.children) != 0 || node.depth != 1 || node.nodes != 1 {
			return 0, 0, false, invalidPlanError("query expression leaf is malformed")
		}
		if err := validateExpressionCondition(node.condition); err != nil {
			return 0, 0, false, err
		}
		hasRelations := node.condition.relationPath != nil
		if node.hasRelations != hasRelations {
			return 0, 0, false, invalidPlanError("query expression relation metadata is malformed")
		}
		return 1, 1, hasRelations, nil
	case ExpressionAnd, ExpressionOr:
		if len(node.children) < 2 {
			return 0, 0, false, invalidPlanError("AND and OR query expressions require at least two children")
		}
	case ExpressionNot:
		if len(node.children) != 1 {
			return 0, 0, false, invalidPlanError("NOT query expressions require exactly one child")
		}
	default:
		return 0, 0, false, invalidPlanError("query expression kind is invalid")
	}

	calculatedDepth := 1
	calculatedNodes := 1
	hasRelations := false
	for _, child := range node.children {
		childDepth, childNodes, childRelations, err := validateExpressionNode(child.node, currentDepth+1, visited)
		if err != nil {
			return 0, 0, false, err
		}
		if childDepth+1 > calculatedDepth {
			calculatedDepth = childDepth + 1
		}
		if childNodes > maximumExpressionNodes-calculatedNodes {
			return 0, 0, false, invalidPlanError("query expression exceeds the maximum node count of 1024")
		}
		calculatedNodes += childNodes
		hasRelations = hasRelations || childRelations
	}
	if calculatedDepth > maximumExpressionDepth {
		return 0, 0, false, invalidPlanError("query expression exceeds the maximum depth of 64")
	}
	if node.depth != calculatedDepth || node.nodes != calculatedNodes || node.hasRelations != hasRelations {
		return 0, 0, false, invalidPlanError("query expression cached metadata is malformed")
	}
	return calculatedDepth, calculatedNodes, hasRelations, nil
}

func validateExpressionCondition(condition Condition) error {
	field := condition.field
	if !validExpressionField(field) {
		return invalidPlanError("query expression condition has an empty or NUL-containing field")
	}
	switch field.kind {
	case FieldInteger, FieldString, FieldBoolean:
	default:
		return invalidPlanError("query expression condition has an unsupported field kind")
	}

	if condition.relationPath != nil {
		path := condition.relationPath
		if len(path.hops) == 0 || !path.terminal.Equal(field) {
			return invalidPlanError("query expression relation condition has a malformed path")
		}
		switch path.scope {
		case RelationTerminalRelatedField, RelationTerminalSourceKey:
		default:
			return invalidPlanError("query expression relation condition has an invalid terminal scope")
		}
	}

	if condition.rhs == nil {
		return invalidPlanError("query expression condition has no right-hand side")
	}
	if condition.relationPath != nil && condition.rhs.kind == conditionRHSField {
		return invalidPlanError("query expression relation condition cannot use a field right-hand side")
	}

	switch condition.rhs.kind {
	case conditionRHSLiteral:
		if len(condition.rhs.values) != 0 || condition.rhs.field != (FieldRef{}) {
			return invalidPlanError("query expression literal right-hand side is malformed")
		}
		switch condition.lookup {
		case LookupExact:
			if !expressionValueMatchesField(condition.rhs.value.Kind(), field.kind) {
				return invalidPlanError("query expression exact value does not match its field kind")
			}
		case LookupGreaterThan, LookupGreaterThanOrEqual, LookupLessThan, LookupLessThanOrEqual:
			if !expressionOrderedValueMatchesField(condition.rhs.value.Kind(), field.kind) {
				return invalidPlanError("query expression ordered comparison requires a same-kind Integer or String value")
			}
		case LookupIContains:
			if field.kind != FieldString || condition.rhs.value.Kind() != ValueString {
				return invalidPlanError("query expression icontains requires a string field and value")
			}
		case LookupIsNull:
			if condition.rhs.value.Kind() != ValueBoolean {
				return invalidPlanError("query expression isnull requires a Boolean value")
			}
		default:
			return invalidPlanError("query expression literal condition has an unknown lookup")
		}
	case conditionRHSList:
		if condition.lookup != LookupIn || condition.rhs.value != (Value{}) || condition.rhs.field != (FieldRef{}) {
			return invalidPlanError("query expression list right-hand side is malformed")
		}
		if _, ok := condition.Values(); !ok {
			return invalidPlanError("query expression IN condition is malformed")
		}
	case conditionRHSField:
		if condition.rhs.value != (Value{}) || len(condition.rhs.values) != 0 ||
			!validExpressionField(condition.rhs.field) {
			return invalidPlanError("query expression field right-hand side is malformed")
		}
		if condition.lookup != LookupExact && !orderedComparisonLookup(condition.lookup) {
			return invalidPlanError("query expression field right-hand side requires exact or ordered comparison")
		}
		if field.kind != condition.rhs.field.kind ||
			(field.kind != FieldInteger && field.kind != FieldString) {
			return invalidPlanError("query expression field comparison requires same-kind Integer or String fields")
		}
	default:
		return invalidPlanError("query expression condition has an unknown right-hand-side kind")
	}
	return nil
}

func validExpressionField(field FieldRef) bool {
	if field.name == "" || field.column == "" ||
		strings.ContainsRune(field.name, '\x00') || strings.ContainsRune(field.column, '\x00') {
		return false
	}
	switch field.kind {
	case FieldInteger, FieldString, FieldBoolean:
		return true
	default:
		return false
	}
}

func orderedComparisonLookup(lookup Lookup) bool {
	switch lookup {
	case LookupGreaterThan, LookupGreaterThanOrEqual, LookupLessThan, LookupLessThanOrEqual:
		return true
	default:
		return false
	}
}

func expressionOrderedValueMatchesField(value ValueKind, field FieldKind) bool {
	return value == ValueInteger && field == FieldInteger || value == ValueString && field == FieldString
}

func expressionValueMatchesField(value ValueKind, field FieldKind) bool {
	return value == ValueInteger && field == FieldInteger ||
		value == ValueString && field == FieldString ||
		value == ValueBoolean && field == FieldBoolean
}

func equalExpressionNodes(left, right *expressionNode) bool {
	if left == right {
		return true
	}
	if left == nil || right == nil || left.kind != right.kind || len(left.children) != len(right.children) {
		return false
	}
	if left.kind == ExpressionLeaf && !left.condition.Equal(right.condition) {
		return false
	}
	for index := range left.children {
		if !equalExpressionNodes(left.children[index].node, right.children[index].node) {
			return false
		}
	}
	return true
}

// uncheckedAndExpressions constructs the same ordered canonical AND shape as
// AndExpressions without validating leaves or enforcing resource caps. It is
// deliberately package-private and exists only for Plan.WithConditions,
// whose non-error-returning compatibility signature must preserve malformed
// input for later fail-closed backend validation.
func uncheckedAndExpressions(expressions ...Expression) Expression {
	if len(expressions) == 0 {
		return Expression{}
	}
	if len(expressions) == 1 {
		return expressions[0]
	}

	childCount := 0
	for _, expression := range expressions {
		if expression.node.kind == ExpressionAnd {
			childCount += len(expression.node.children)
		} else {
			childCount++
		}
	}
	children := make([]Expression, 0, childCount)
	for _, expression := range expressions {
		if expression.node.kind == ExpressionAnd {
			children = append(children, expression.node.children...)
		} else {
			children = append(children, expression)
		}
	}

	depth := 1
	nodes := 1
	hasRelations := false
	for _, child := range children {
		if child.node.depth+1 > depth {
			depth = child.node.depth + 1
		}
		nodes += child.node.nodes
		hasRelations = hasRelations || child.node.hasRelations
	}
	return Expression{node: &expressionNode{
		kind:         ExpressionAnd,
		children:     children,
		depth:        depth,
		nodes:        nodes,
		hasRelations: hasRelations,
	}}
}

func expressionConditions(expression Expression) []Condition {
	if expression.node == nil {
		return nil
	}
	conditions := make([]Condition, 0)
	appendExpressionConditions(&conditions, expression.node)
	return conditions
}

func appendExpressionConditions(conditions *[]Condition, node *expressionNode) {
	if node.kind == ExpressionLeaf {
		*conditions = append(*conditions, node.condition.clone())
		return
	}
	for _, child := range node.children {
		appendExpressionConditions(conditions, child.node)
	}
}
