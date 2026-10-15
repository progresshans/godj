package orm

import (
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/query"
)

// DecimalExpression keeps exact Decimal arithmetic and aggregate results in
// their own value domain. Unlike integer/float expressions, AVG is Decimal.
// The embedded operand uses the same model, relation and result-scope checks.
type DecimalExpression[M any] struct {
	ScalarExpression[M, decimal.Decimal]
}

func Decimal[M any](operand ScalarOperand[M, decimal.Decimal]) DecimalExpression[M] {
	return DecimalExpression[M]{Scalar(operand)}
}

func (value DecimalExpression[M]) arithmetic(operator query.ArithmeticOperator, right ScalarOperand[M, decimal.Decimal]) DecimalExpression[M] {
	if value.err != nil {
		return value
	}
	operand, err := operandExpression(right)
	if err != nil {
		return DecimalExpression[M]{ScalarExpression[M, decimal.Decimal]{err: err}}
	}
	expression, err := query.Arithmetic(operator, value.expression, operand)
	return DecimalExpression[M]{ScalarExpression[M, decimal.Decimal]{expression: expression, err: err}}
}

func (value DecimalExpression[M]) Add(right decimal.Decimal) DecimalExpression[M] {
	return value.AddExpression(Value[M](right))
}
func (value DecimalExpression[M]) Subtract(right decimal.Decimal) DecimalExpression[M] {
	return value.SubtractExpression(Value[M](right))
}
func (value DecimalExpression[M]) Multiply(right decimal.Decimal) DecimalExpression[M] {
	return value.MultiplyExpression(Value[M](right))
}
func (value DecimalExpression[M]) Divide(right decimal.Decimal) DecimalExpression[M] {
	return value.DivideExpression(Value[M](right))
}
func (value DecimalExpression[M]) Remainder(right decimal.Decimal) DecimalExpression[M] {
	return value.RemainderExpression(Value[M](right))
}
func (value DecimalExpression[M]) AddExpression(right ScalarOperand[M, decimal.Decimal]) DecimalExpression[M] {
	return value.arithmetic(query.ArithmeticAdd, right)
}
func (value DecimalExpression[M]) SubtractExpression(right ScalarOperand[M, decimal.Decimal]) DecimalExpression[M] {
	return value.arithmetic(query.ArithmeticSubtract, right)
}
func (value DecimalExpression[M]) MultiplyExpression(right ScalarOperand[M, decimal.Decimal]) DecimalExpression[M] {
	return value.arithmetic(query.ArithmeticMultiply, right)
}
func (value DecimalExpression[M]) DivideExpression(right ScalarOperand[M, decimal.Decimal]) DecimalExpression[M] {
	return value.arithmetic(query.ArithmeticDivide, right)
}
func (value DecimalExpression[M]) RemainderExpression(right ScalarOperand[M, decimal.Decimal]) DecimalExpression[M] {
	return value.arithmetic(query.ArithmeticModulo, right)
}
func (value DecimalExpression[M]) Negate() DecimalExpression[M] {
	if value.err != nil {
		return value
	}
	expression, err := query.NegateScalar(value.expression)
	return DecimalExpression[M]{ScalarExpression[M, decimal.Decimal]{expression: expression, err: err}}
}

func (value DecimalExpression[M]) aggregate(kind query.ResultExpressionKind) (query.ResultExpression, func() scalarCell[Optional[decimal.Decimal]], error) {
	expression, _, err := value.scalarResultField(*new(M), Optional[decimal.Decimal]{})
	if err == nil {
		expression, err = query.AggregateResult(kind, expression)
	}
	return expression, optionalResultCell(nullableAggregateDecimalCell), err
}
func (value DecimalExpression[M]) scalarSumField(M, decimal.Decimal) (query.ResultExpression, func() scalarCell[Optional[decimal.Decimal]], error) {
	return value.aggregate(query.ResultSum)
}
func (value DecimalExpression[M]) scalarAvgField(M, decimal.Decimal) (query.ResultExpression, func() scalarCell[Optional[decimal.Decimal]], error) {
	return value.aggregate(query.ResultAvg)
}
func (value DecimalExpression[M]) scalarOrderedField(_ M, _ decimal.Decimal, kind query.ResultExpressionKind) (query.ResultExpression, func() scalarCell[Optional[decimal.Decimal]], error) {
	return value.aggregate(kind)
}
