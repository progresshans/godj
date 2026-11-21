package orm

import "github.com/progresshans/godj/query"

// DynamicColumn is an ordinal in the input's explicit selection. Binding gives
// it the same owned ResultRef as a typed SelectedField or SelectedAggregate.
// It cannot bind against model metadata or accept a SQL identifier.
func DynamicColumn(index int) DynamicExpression {
	if index < 0 || index >= query.MaxProjectionExpressions {
		return DynamicExpression{err: invalidResultBuilder("result column is outside its selection budget")}
	}
	return DynamicExpression{node: &dynamicScalarNode{column: &index, depth: 1, nodes: 1}}
}

func (source ResultSource[M]) Expression(input DynamicExpression) (DynamicScalar[ResultOf[M]], error) {
	var zero DynamicScalar[ResultOf[M]]
	if err := source.validate(); err != nil {
		return zero, err
	}
	scalar, err := input.bind(dynamicBinding{scope: source.scope})
	if err != nil {
		return zero, err
	}
	if err := scalar.ValidateResultSource(source.scope); err != nil {
		return zero, err
	}
	expression, err := query.ScalarResult(scalar)
	if err != nil {
		return zero, err
	}
	return DynamicScalar[ResultOf[M]]{expression: expression}, nil
}
func (source ResultSource[M]) Predicate(input DynamicPredicate) (Predicate[ResultOf[M]], error) {
	var zero Predicate[ResultOf[M]]
	if err := source.validate(); err != nil {
		return zero, err
	}
	expression, err := input.bind(dynamicBinding{scope: source.scope})
	if err != nil {
		return zero, err
	}
	if _, err := source.plan.WithWhere(expression); err != nil {
		return zero, err
	}
	return Predicate[ResultOf[M]]{expression: expression}, nil
}
func (source ResultSource[M]) Project(inputs ...DynamicExpression) (Projection[ResultOf[M], []query.Value], error) {
	var zero Projection[ResultOf[M], []query.Value]
	if err := source.validate(); err != nil {
		return zero, err
	}
	if len(inputs) == 0 || len(inputs) > query.MaxProjectionExpressions {
		return zero, invalidResultBuilder("dynamic result projection exceeds its selection budget")
	}
	expressions := make([]query.ResultExpression, len(inputs))
	for index, input := range inputs {
		value, err := source.Expression(input)
		if err != nil {
			return zero, err
		}
		expressions[index] = value.expression
	}
	if _, err := query.NewProjectionResult(expressions...); err != nil {
		return zero, err
	}
	factory, err := dynamicGroupDecoder(expressions)
	if err != nil {
		return zero, err
	}
	return Projection[ResultOf[M], []query.Value]{expressions: expressions, newDecoder: factory}, nil
}
