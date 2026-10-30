package orm

import "github.com/progresshans/godj/query"

// BindAggregateExpression completes a computed aggregate against the source
// model. Related names require BindAggregateExpressionIn's project authority.
func BindAggregateExpression[M any](source QuerySet[M], kind query.ResultExpressionKind, input DynamicExpression) (AggregateExpression[M, query.Value], error) {
	return bindAggregateExpression(source, nil, kind, input)
}

// BindAggregateExpressionIn binds collection operands without exposing them as
// row scalars. Its result uses the same decoder and modifiers as typed aggregates.
func BindAggregateExpressionIn[M any](source QuerySet[M], binding BoundModel[M], kind query.ResultExpressionKind, input DynamicExpression) (AggregateExpression[M, query.Value], error) {
	return bindAggregateExpression(source, &binding, kind, input)
}

func bindAggregateExpression[M any](source QuerySet[M], binding *BoundModel[M], kind query.ResultExpressionKind, input DynamicExpression) (AggregateExpression[M, query.Value], error) {
	bound, err := dynamicExpressionBinding(source, binding)
	if err != nil {
		return AggregateExpression[M, query.Value]{}, err
	}
	operand, err := input.bind(bound)
	if err != nil {
		return AggregateExpression[M, query.Value]{}, err
	}
	expression, err := query.ScalarAggregateResult(kind, operand)
	if err != nil {
		return AggregateExpression[M, query.Value]{}, err
	}
	if err := source.plan.ValidateAggregateScalar(operand); err != nil {
		return AggregateExpression[M, query.Value]{}, err
	}
	factory, err := dynamicComputedCell(expression)
	if err != nil {
		return AggregateExpression[M, query.Value]{}, err
	}
	return AggregateExpression[M, query.Value]{expression: expression, newCell: factory}, nil
}
