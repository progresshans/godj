package orm

import (
	"slices"

	"github.com/progresshans/godj/query"
)

type DynamicPredicate struct {
	node *dynamicPredicateNode
	err  error
}
type dynamicPredicateNode struct {
	kind         query.ExpressionKind
	value        DynamicExpression
	lookup       query.Lookup
	literal      query.Value
	children     []DynamicPredicate
	nodes, depth int
}

func (value DynamicExpression) comparison(lookup query.Lookup, literal any) DynamicPredicate {
	if err := value.validate(); err != nil {
		return DynamicPredicate{err: err}
	}
	right, err := scalarValue(literal)
	if err != nil {
		return DynamicPredicate{err: err}
	}
	if value.node.nodes >= query.MaximumScalarNodes || value.node.depth >= query.MaximumScalarDepth {
		return DynamicPredicate{err: invalidResultBuilder("dynamic predicate exceeds its expression budget")}
	}
	return DynamicPredicate{node: &dynamicPredicateNode{kind: query.ExpressionLeaf, value: value, lookup: lookup, literal: right,
		nodes: 1 + value.node.nodes, depth: 1 + value.node.depth}}
}
func (value DynamicExpression) Exact(literal any) DynamicPredicate {
	return value.comparison(query.LookupExact, literal)
}
func (value DynamicExpression) GreaterThan(literal any) DynamicPredicate {
	return value.comparison(query.LookupGreaterThan, literal)
}
func (value DynamicExpression) GreaterThanOrEqual(literal any) DynamicPredicate {
	return value.comparison(query.LookupGreaterThanOrEqual, literal)
}
func (value DynamicExpression) LessThan(literal any) DynamicPredicate {
	return value.comparison(query.LookupLessThan, literal)
}
func (value DynamicExpression) LessThanOrEqual(literal any) DynamicPredicate {
	return value.comparison(query.LookupLessThanOrEqual, literal)
}
func (value DynamicExpression) IsNull(isNull bool) DynamicPredicate {
	return value.comparison(query.LookupIsNull, isNull)
}

func DynamicAnd(left, right DynamicPredicate, rest ...DynamicPredicate) DynamicPredicate {
	if len(rest) > query.MaximumScalarNodes-3 {
		return DynamicPredicate{err: invalidResultBuilder("dynamic predicate exceeds its connector budget")}
	}
	return connectDynamicPredicates(query.ExpressionAnd, append([]DynamicPredicate{left, right}, rest...))
}
func DynamicOr(left, right DynamicPredicate, rest ...DynamicPredicate) DynamicPredicate {
	if len(rest) > query.MaximumScalarNodes-3 {
		return DynamicPredicate{err: invalidResultBuilder("dynamic predicate exceeds its connector budget")}
	}
	return connectDynamicPredicates(query.ExpressionOr, append([]DynamicPredicate{left, right}, rest...))
}
func DynamicNot(value DynamicPredicate) DynamicPredicate {
	return connectDynamicPredicates(query.ExpressionNot, []DynamicPredicate{value})
}

func (value DynamicPredicate) validate() error {
	if value.err != nil {
		return value.err
	}
	if value.node == nil {
		return invalidResultBuilder("dynamic predicate is empty")
	}
	return nil
}
func connectDynamicPredicates(kind query.ExpressionKind, inputs []DynamicPredicate) DynamicPredicate {
	if len(inputs) == 0 || len(inputs) >= query.MaximumScalarNodes {
		return DynamicPredicate{err: invalidResultBuilder("dynamic predicate exceeds its connector budget")}
	}
	nodes, depth := 1, 1
	for _, value := range inputs {
		if err := value.validate(); err != nil {
			return DynamicPredicate{err: err}
		}
		nodes += value.node.nodes
		depth = max(depth, value.node.depth+1)
		if nodes > query.MaximumScalarNodes || depth > query.MaximumScalarDepth {
			return DynamicPredicate{err: invalidResultBuilder("dynamic predicate exceeds its expression budget")}
		}
	}
	return DynamicPredicate{node: &dynamicPredicateNode{kind: kind, children: slices.Clone(inputs), nodes: nodes, depth: depth}}
}

func (value DynamicPredicate) bind(model *preparedModel) (query.Expression, error) {
	if err := value.validate(); err != nil {
		return query.Expression{}, err
	}
	node := value.node
	if node.kind == query.ExpressionLeaf {
		scalar, err := node.value.bind(model)
		if err != nil {
			return query.Expression{}, err
		}
		condition, err := query.NewScalarCondition(scalar, node.lookup, node.literal)
		if err != nil {
			return query.Expression{}, err
		}
		return query.NewExpression(condition)
	}
	children := make([]query.Expression, len(node.children))
	for index, child := range node.children {
		var err error
		children[index], err = child.bind(model)
		if err != nil {
			return query.Expression{}, err
		}
	}
	if node.kind == query.ExpressionNot {
		return query.NotExpression(children[0])
	}
	if node.kind == query.ExpressionAnd {
		return query.AndExpressions(children[0], children[1], children[2:]...)
	}
	return query.OrExpressions(children[0], children[1], children[2:]...)
}

func BindPredicate[M any](source QuerySet[M], input DynamicPredicate) (Predicate[M], error) {
	if source.configurationErr != nil {
		return Predicate[M]{}, source.configurationErr
	}
	if source.prepared == nil {
		return Predicate[M]{}, invalidResultBuilder("predicate binding requires model metadata")
	}
	expression, err := input.bind(source.prepared)
	if err != nil {
		return Predicate[M]{}, err
	}
	if _, err := source.plan.WithWhere(expression); err != nil {
		return Predicate[M]{}, err
	}
	return Predicate[M]{expression: expression}, nil
}

type DynamicWhenExpression struct {
	predicate DynamicPredicate
	value     DynamicExpression
	err       error
}

func DynamicWhen(predicate DynamicPredicate, value any) DynamicWhenExpression {
	branch := DynamicWhenExpression{predicate: predicate, value: DynamicValue(value)}
	branch.err = firstError(predicate.validate(), branch.value.validate())
	return branch
}

func DynamicCase(fallback any, branches ...DynamicWhenExpression) DynamicExpression {
	value := DynamicValue(fallback)
	if err := value.validate(); err != nil {
		return DynamicExpression{err: err}
	}
	if len(branches) == 0 {
		return value
	}
	if len(branches) >= query.MaximumScalarNodes {
		return DynamicExpression{err: invalidResultBuilder("dynamic conditional requires bounded branches")}
	}
	nodes, depth := 1+value.node.nodes, 1+value.node.depth
	for _, branch := range branches {
		if err := firstError(branch.err, branch.predicate.validate(), branch.value.validate()); err != nil {
			return DynamicExpression{err: err}
		}
		nodes += branch.predicate.node.nodes + branch.value.node.nodes
		depth = max(depth, 1+branch.predicate.node.depth, 1+branch.value.node.depth)
		if nodes > query.MaximumScalarNodes || depth > query.MaximumScalarDepth {
			return DynamicExpression{err: invalidResultBuilder("dynamic conditional exceeds its expression budget")}
		}
	}
	return DynamicExpression{node: &dynamicScalarNode{left: value, branches: slices.Clone(branches), nodes: nodes, depth: depth}}
}

func DynamicNull(kind query.FieldKind) DynamicExpression {
	_, err := query.NullExpression(kind)
	return DynamicExpression{node: &dynamicScalarNode{literal: query.Null(), result: kind, depth: 1, nodes: 1}, err: err}
}
