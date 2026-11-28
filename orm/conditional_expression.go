package orm

import "github.com/progresshans/godj/query"

type WhenExpression[M, V any] struct {
	branch query.ScalarWhen
	err    error
	marker [0]func(M, V)
}

func When[M, V any](predicate Predicate[M], operand ScalarOperand[M, V]) WhenExpression[M, V] {
	if predicate.err != nil {
		return WhenExpression[M, V]{err: predicate.err}
	}
	value, err := operandExpression(operand)
	if err != nil {
		return WhenExpression[M, V]{err: err}
	}
	branch, err := query.WhenScalar(predicate.expression, value)
	return WhenExpression[M, V]{branch: branch, err: err}
}

// Case evaluates branches in order and uses the explicit fallback when no
// predicate is true. SQL UNKNOWN is not true. The selected value may be NULL;
// the typed result is Optional[V], including for nullable field references.
func Case[M, V any](fallback ScalarOperand[M, V], branches ...WhenExpression[M, V]) ScalarExpression[M, V] {
	value, err := operandExpression(fallback)
	if err != nil {
		return ScalarExpression[M, V]{err: err}
	}
	if len(branches) == 0 {
		return ScalarExpression[M, V]{expression: value}
	}
	if len(branches) >= query.MaximumScalarNodes {
		return ScalarExpression[M, V]{err: invalidResultBuilder("conditional expression requires a bounded branch list")}
	}
	selected := make([]query.ScalarWhen, len(branches))
	for index, branch := range branches {
		if branch.err != nil {
			return ScalarExpression[M, V]{err: branch.err}
		}
		selected[index] = branch.branch
	}
	value, err = query.CaseScalar(value, selected...)
	return ScalarExpression[M, V]{expression: value, err: err}
}
