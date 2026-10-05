package orm

import (
	"fmt"
	"time"

	"github.com/progresshans/godj/binaryvalue"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/uuid"
)

// ScalarOperand is sealed to model- and value-specific field references and
// scalar expressions. Related fields are not operands for a row assignment.
type ScalarOperand[M, V any] interface {
	scalarOperand(M, V) (query.ScalarExpression, error)
}

type ScalarExpression[M, V any] struct {
	expression query.ScalarExpression
	err        error
	marker     [0]func(M, V)
}

func (expression ScalarExpression[M, V]) scalarOperand(M, V) (query.ScalarExpression, error) {
	return expression.expression, expression.err
}
func (reference FieldReference[M, V]) scalarOperand(M, V) (query.ScalarExpression, error) {
	if reference.err != nil {
		return query.ScalarExpression{}, reference.err
	}
	return query.FieldExpression(reference.reference)
}
func (field BooleanField[M]) referenceField(M, bool) (query.FieldRef, error) {
	return field.reference, field.err
}
func (field NullableBooleanField[M]) referenceField(M, bool) (query.FieldRef, error) {
	return field.reference, field.err
}

// Value creates an owned scalar literal. Field-based Assign infers V directly;
// stand-alone numeric values use int64 or float64 explicitly.
func Value[M, V any](value V) ScalarExpression[M, V] {
	literal, err := scalarValue(value)
	if err != nil {
		return ScalarExpression[M, V]{err: err}
	}
	expression, err := query.LiteralExpression(literal)
	return ScalarExpression[M, V]{expression: expression, err: err}
}

func NullValue[M, V any]() ScalarExpression[M, V] {
	expression, err := query.LiteralExpression(query.Null())
	return ScalarExpression[M, V]{expression: expression, err: err}
}

func scalarValue(raw any) (query.Value, error) {
	var value query.Value
	switch raw := raw.(type) {
	case nil:
		value = query.Null()
	case int:
		value = query.Integer(int64(raw))
	case int64:
		value = query.Integer(raw)
	case float64:
		value = query.Float(raw)
	case string:
		value = query.String(raw)
	case bool:
		value = query.Boolean(raw)
	case decimal.Decimal:
		value = query.Decimal(raw)
	case uuid.UUID:
		value = query.UUID(raw)
	case binaryvalue.Value:
		value = query.Binary(raw)
	case jsonvalue.Value:
		value = query.JSON(raw)
	case duration.Duration:
		value = query.Duration(raw)
	case clock.Time:
		value = query.Time(raw)
	case calendar.Date:
		value = query.Date(raw)
	case time.Time:
		value = query.DateTime(raw)
	default:
		return query.Value{}, &query.Error{Category: query.CategoryField, Code: query.CodeInvalidValue, Detail: fmt.Sprintf("unsupported scalar input %T", raw)}
	}
	if _, err := value.DatabaseValue(); err != nil {
		return query.Value{}, err
	}
	return value, nil
}

func operandExpression[M, V any](operand ScalarOperand[M, V]) (query.ScalarExpression, error) {
	if interfaceIsNil(operand) {
		return query.ScalarExpression{}, invalidWritePlan("scalar operand is nil")
	}
	var model M
	var value V
	expression, err := operand.scalarOperand(model, value)
	if err != nil {
		return query.ScalarExpression{}, err
	}
	return expression, expression.Validate()
}

// ArithmeticNumber excludes implicit numeric conversions and types whose
// precision/duration arithmetic needs a separate contract.
type ArithmeticNumber interface{ int64 | float64 }

func arithmeticOperands[M any, N ArithmeticNumber](operator query.ArithmeticOperator, left, right ScalarOperand[M, N]) ScalarExpression[M, N] {
	first, err := operandExpression(left)
	if err != nil {
		return ScalarExpression[M, N]{err: err}
	}
	second, err := operandExpression(right)
	if err != nil {
		return ScalarExpression[M, N]{err: err}
	}
	expression, err := query.Arithmetic(operator, first, second)
	return ScalarExpression[M, N]{expression: expression, err: err}
}

func Add[M any, N ArithmeticNumber](left ScalarOperand[M, N], right N) ScalarExpression[M, N] {
	return AddExpressions(left, Value[M](right))
}
func Subtract[M any, N ArithmeticNumber](left ScalarOperand[M, N], right N) ScalarExpression[M, N] {
	return SubtractExpressions(left, Value[M](right))
}
func Multiply[M any, N ArithmeticNumber](left ScalarOperand[M, N], right N) ScalarExpression[M, N] {
	return MultiplyExpressions(left, Value[M](right))
}
func Divide[M any, N ArithmeticNumber](left ScalarOperand[M, N], right N) ScalarExpression[M, N] {
	return DivideExpressions(left, Value[M](right))
}
func Remainder[M any](left ScalarOperand[M, int64], right int64) ScalarExpression[M, int64] {
	return RemainderExpressions(left, Value[M](right))
}
func AddExpressions[M any, N ArithmeticNumber](left, right ScalarOperand[M, N]) ScalarExpression[M, N] {
	return arithmeticOperands(query.ArithmeticAdd, left, right)
}
func SubtractExpressions[M any, N ArithmeticNumber](left, right ScalarOperand[M, N]) ScalarExpression[M, N] {
	return arithmeticOperands(query.ArithmeticSubtract, left, right)
}
func MultiplyExpressions[M any, N ArithmeticNumber](left, right ScalarOperand[M, N]) ScalarExpression[M, N] {
	return arithmeticOperands(query.ArithmeticMultiply, left, right)
}
func DivideExpressions[M any, N ArithmeticNumber](left, right ScalarOperand[M, N]) ScalarExpression[M, N] {
	return arithmeticOperands(query.ArithmeticDivide, left, right)
}
func RemainderExpressions[M any](left, right ScalarOperand[M, int64]) ScalarExpression[M, int64] {
	return arithmeticOperands(query.ArithmeticModulo, left, right)
}
func Negate[M any, N ArithmeticNumber](value ScalarOperand[M, N]) ScalarExpression[M, N] {
	expression, err := operandExpression(value)
	if err == nil {
		expression, err = query.NegateScalar(expression)
	}
	return ScalarExpression[M, N]{expression: expression, err: err}
}

// UpdateAssignment is an immutable, model-specific assignment. Unlike Save's
// update mask, QuerySet.Update can explicitly assign its primary key.
type UpdateAssignment[M any] struct {
	assignment query.ScalarAssignment
	err        error
	marker     [0]func(M)
}

func Assign[M, V any](field ReferenceField[M, V], value V) UpdateAssignment[M] {
	return AssignExpression(field, Value[M](value))
}
func AssignExpression[M, V any](field ReferenceField[M, V], operand ScalarOperand[M, V]) UpdateAssignment[M] {
	reference := F(field)
	if reference.err != nil {
		return UpdateAssignment[M]{err: reference.err}
	}
	expression, err := operandExpression(operand)
	if err != nil {
		return UpdateAssignment[M]{err: err}
	}
	assignment, err := query.NewScalarAssignment(reference.reference, expression)
	return UpdateAssignment[M]{assignment: assignment, err: err}
}

type NullableReferenceField[M, V any] interface {
	ReferenceField[M, V]
	nullableAssignment(M, V)
}

func AssignNull[M, V any](field NullableReferenceField[M, V]) UpdateAssignment[M] {
	return AssignExpression[M, V](field, NullValue[M, V]())
}
func (NullableIntegerField[M]) nullableAssignment(M, int64)              {}
func (NullableForeignKeyField[M]) nullableAssignment(M, int64)           {}
func (NullableStringField[M]) nullableAssignment(M, string)              {}
func (NullableBooleanField[M]) nullableAssignment(M, bool)               {}
func (NullableFloatField[M]) nullableAssignment(M, float64)              {}
func (NullableDecimalField[M]) nullableAssignment(M, decimal.Decimal)    {}
func (NullableUUIDField[M]) nullableAssignment(M, uuid.UUID)             {}
func (NullableBinaryField[M]) nullableAssignment(M, binaryvalue.Value)   {}
func (NullableJSONField[M]) nullableAssignment(M, jsonvalue.Value)       {}
func (NullableDateTimeField[M]) nullableAssignment(M, time.Time)         {}
func (NullableDateField[M]) nullableAssignment(M, calendar.Date)         {}
func (NullableTimeField[M]) nullableAssignment(M, clock.Time)            {}
func (NullableDurationField[M]) nullableAssignment(M, duration.Duration) {}
