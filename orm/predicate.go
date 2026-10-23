package orm

import "github.com/progresshans/godj/query"

// And constructs one reusable typed predicate whose operands retain caller
// order. Nested AND predicates are canonicalized by the query expression
// layer without exposing mutable child storage.
func And[M any](left, right Predicate[M], rest ...Predicate[M]) Predicate[M] {
	expressions, err := predicateExpressions(left, right, rest...)
	if err != nil {
		return Predicate[M]{err: err}
	}
	expression, err := query.AndExpressions(expressions[0], expressions[1], expressions[2:]...)
	return Predicate[M]{expression: expression, err: err}
}

// Or constructs one reusable typed predicate whose operands retain caller
// order. Nested OR predicates are canonicalized by the query expression
// layer without exposing mutable child storage.
func Or[M any](left, right Predicate[M], rest ...Predicate[M]) Predicate[M] {
	expressions, err := predicateExpressions(left, right, rest...)
	if err != nil {
		return Predicate[M]{err: err}
	}
	expression, err := query.OrExpressions(expressions[0], expressions[1], expressions[2:]...)
	return Predicate[M]{expression: expression, err: err}
}

// Not constructs one unary typed predicate. It preserves the explicit source
// structure rather than simplifying double negation.
func Not[M any](predicate Predicate[M]) Predicate[M] {
	if predicate.err != nil {
		return Predicate[M]{err: predicate.err}
	}
	expression, err := query.NotExpression(predicate.expression)
	return Predicate[M]{expression: expression, err: err}
}

func predicateFromCondition[M any](condition query.Condition, err error) Predicate[M] {
	if err != nil {
		return Predicate[M]{err: err}
	}
	expression, expressionErr := query.NewExpression(condition)
	return Predicate[M]{expression: expression, err: expressionErr}
}

func predicateExpressions[M any](left, right Predicate[M], rest ...Predicate[M]) ([]query.Expression, error) {
	predicates := make([]Predicate[M], 0, len(rest)+2)
	predicates = append(predicates, left, right)
	predicates = append(predicates, rest...)
	expressions := make([]query.Expression, len(predicates))
	for index, predicate := range predicates {
		if predicate.err != nil {
			return nil, predicate.err
		}
		expressions[index] = predicate.expression
	}
	return expressions, nil
}
