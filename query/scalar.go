package query

import (
	"strings"
	"unicode/utf8"
)

const (
	MaximumScalarDepth = 64
	MaximumScalarNodes = 1024
)

// ScalarKind distinguishes value expressions from Boolean predicates and
// aggregate/result expressions. A zero ScalarExpression is invalid.
type ScalarKind uint8

const (
	ScalarLiteral ScalarKind = iota + 1
	ScalarField
	ScalarBinary
	ScalarNegate
)

type ArithmeticOperator string

const (
	ArithmeticAdd      ArithmeticOperator = "+"
	ArithmeticSubtract ArithmeticOperator = "-"
	ArithmeticMultiply ArithmeticOperator = "*"
	ArithmeticDivide   ArithmeticOperator = "/"
	ArithmeticModulo   ArithmeticOperator = "%"
)

// ScalarExpression is an immutable bounded value tree. Fields refer to the
// original row of one eventual source; they cannot introduce a relation join.
// Literal containers have already been snapshotted by Value constructors.
type ScalarExpression struct{ node *scalarNode }

type scalarNode struct {
	kind         ScalarKind
	value        Value
	field        FieldRef
	operator     ArithmeticOperator
	left, right  ScalarExpression
	result       FieldKind
	nullable     bool
	depth, nodes int
}

func LiteralExpression(value Value) (ScalarExpression, error) {
	if _, err := value.DatabaseValue(); err != nil {
		return ScalarExpression{}, err
	}
	result := FieldKind(value.Kind())
	if value.IsNull() {
		result = ""
	}
	return ScalarExpression{&scalarNode{kind: ScalarLiteral, value: value, result: result, nullable: value.IsNull(), depth: 1, nodes: 1}}, nil
}

func FieldExpression(field FieldRef) (ScalarExpression, error) {
	if !validScalarField(field) {
		return ScalarExpression{}, invalidPlanError("scalar reference requires valid field metadata")
	}
	return ScalarExpression{&scalarNode{kind: ScalarField, field: field, result: field.Kind(), nullable: field.Nullable(), depth: 1, nodes: 1}}, nil
}

func validScalarField(field FieldRef) bool {
	return field.ValidType() && field.Name() != "" && field.Column() != "" &&
		utf8.ValidString(field.Name()) && utf8.ValidString(field.Column()) &&
		!strings.ContainsRune(field.Name(), 0) && !strings.ContainsRune(field.Column(), 0)
}

// Arithmetic combines same-kind signed integers or binary64 values. It does
// not insert casts or coerce strings, booleans, decimals or temporal values.
// NULL adopts the other operand's numeric kind; two untyped NULLs fail closed.
func Arithmetic(operator ArithmeticOperator, left, right ScalarExpression) (ScalarExpression, error) {
	if err := left.Validate(); err != nil {
		return ScalarExpression{}, err
	}
	if err := right.Validate(); err != nil {
		return ScalarExpression{}, err
	}
	result := left.node.result
	if result == "" {
		result = right.node.result
	}
	if result == "" || left.node.result != "" && left.node.result != result || right.node.result != "" && right.node.result != result {
		return ScalarExpression{}, invalidPlanError("arithmetic operands require the same explicit numeric kind")
	}
	if result != FieldInteger && result != FieldFloat {
		return ScalarExpression{}, &Error{Category: CategoryQuery, Code: CodeUnsupported, Detail: "arithmetic currently requires int64 or float64; Decimal and temporal arithmetic need an explicit precision contract"}
	}
	switch operator {
	case ArithmeticAdd, ArithmeticSubtract, ArithmeticMultiply, ArithmeticDivide:
	case ArithmeticModulo:
		if result != FieldInteger {
			return ScalarExpression{}, invalidPlanError("remainder requires signed integer operands")
		}
	default:
		return ScalarExpression{}, invalidPlanError("unknown arithmetic operator")
	}
	depth, nodes := 1+max(left.node.depth, right.node.depth), 1+left.node.nodes+right.node.nodes
	if depth > MaximumScalarDepth || nodes > MaximumScalarNodes {
		return ScalarExpression{}, invalidPlanError("scalar expression exceeds its depth or node budget")
	}
	return ScalarExpression{&scalarNode{kind: ScalarBinary, operator: operator, left: left, right: right, result: result,
		nullable: left.node.nullable || right.node.nullable || operator == ArithmeticDivide || operator == ArithmeticModulo, depth: depth, nodes: nodes}}, nil
}

func NegateScalar(value ScalarExpression) (ScalarExpression, error) {
	if err := value.Validate(); err != nil {
		return ScalarExpression{}, err
	}
	if value.node.result != FieldInteger && value.node.result != FieldFloat {
		return ScalarExpression{}, &Error{Category: CategoryQuery, Code: CodeUnsupported, Detail: "numeric negation requires int64 or float64"}
	}
	if value.node.depth >= MaximumScalarDepth || value.node.nodes >= MaximumScalarNodes {
		return ScalarExpression{}, invalidPlanError("scalar expression exceeds its depth or node budget")
	}
	return ScalarExpression{&scalarNode{kind: ScalarNegate, left: value, result: value.node.result, nullable: value.node.nullable,
		depth: value.node.depth + 1, nodes: value.node.nodes + 1}}, nil
}

func (expression ScalarExpression) Validate() error {
	if expression.node == nil || expression.node.depth < 1 || expression.node.depth > MaximumScalarDepth || expression.node.nodes < 1 || expression.node.nodes > MaximumScalarNodes {
		return invalidPlanError("scalar expression is empty or exceeds its resource budget")
	}
	return nil // All node constructors validate their private immutable state.
}

func (expression ScalarExpression) Kind() ScalarKind {
	if expression.node == nil {
		return 0
	}
	return expression.node.kind
}
func (expression ScalarExpression) ResultKind() FieldKind {
	if expression.node == nil {
		return ""
	}
	return expression.node.result
}
func (expression ScalarExpression) Nullable() bool {
	return expression.node != nil && expression.node.nullable
}
func (expression ScalarExpression) NodeCount() int {
	if expression.node == nil {
		return 0
	}
	return expression.node.nodes
}
func (expression ScalarExpression) Depth() int {
	if expression.node == nil {
		return 0
	}
	return expression.node.depth
}
func (expression ScalarExpression) Literal() (Value, bool) {
	if expression.Kind() != ScalarLiteral {
		return Value{}, false
	}
	return expression.node.value, true
}
func (expression ScalarExpression) Field() (FieldRef, bool) {
	if expression.Kind() != ScalarField {
		return FieldRef{}, false
	}
	return expression.node.field, true
}
func (expression ScalarExpression) Binary() (ArithmeticOperator, ScalarExpression, ScalarExpression, bool) {
	if expression.Kind() != ScalarBinary {
		return "", ScalarExpression{}, ScalarExpression{}, false
	}
	return expression.node.operator, expression.node.left, expression.node.right, true
}
func (expression ScalarExpression) Negated() (ScalarExpression, bool) {
	if expression.Kind() != ScalarNegate {
		return ScalarExpression{}, false
	}
	return expression.node.left, true
}
func (expression ScalarExpression) Equal(other ScalarExpression) bool {
	if expression.node == other.node {
		return true
	}
	if expression.node == nil || other.node == nil {
		return false
	}
	left, right := expression.node, other.node
	return left.kind == right.kind && left.value == right.value && left.field == right.field && left.operator == right.operator &&
		left.left.Equal(right.left) && left.right.Equal(right.right)
}

func (expression ScalarExpression) validateSource(fields map[string]FieldRef) error {
	if err := expression.Validate(); err != nil {
		return err
	}
	if field, ok := expression.Field(); ok {
		if fields[field.Column()] != field {
			return invalidPlanError("scalar reference differs from its source metadata")
		}
	}
	if _, left, right, ok := expression.Binary(); ok {
		if err := left.validateSource(fields); err != nil {
			return err
		}
		return right.validateSource(fields)
	}
	if value, ok := expression.Negated(); ok {
		return value.validateSource(fields)
	}
	return nil
}

// ScalarAssignment binds an expression to one concrete source column. A
// nullable field expression may target a non-null column: native constraints
// reject rows that actually produce NULL, atomically with the whole statement.
type ScalarAssignment struct {
	field      FieldRef
	expression ScalarExpression
}

func NewScalarAssignment(field FieldRef, expression ScalarExpression) (ScalarAssignment, error) {
	assignment := ScalarAssignment{field, expression}
	if err := assignment.Validate(); err != nil {
		return ScalarAssignment{}, err
	}
	return assignment, nil
}
func (assignment ScalarAssignment) Field() FieldRef              { return assignment.field }
func (assignment ScalarAssignment) Expression() ScalarExpression { return assignment.expression }
func (assignment ScalarAssignment) Equal(other ScalarAssignment) bool {
	return assignment.field == other.field && assignment.expression.Equal(other.expression)
}
func (assignment ScalarAssignment) Validate() error {
	if !validScalarField(assignment.field) {
		return invalidPlanError("expression assignment has invalid field metadata")
	}
	if err := assignment.expression.Validate(); err != nil {
		return err
	}
	if kind := assignment.expression.ResultKind(); kind != "" && kind != assignment.field.Kind() {
		return invalidPlanError("expression result does not match its assigned field kind")
	}
	if value, ok := assignment.expression.Literal(); ok {
		if value.IsNull() && !assignment.field.Nullable() {
			return invalidPlanError("non-null field cannot be assigned a NULL literal")
		}
		if digits, places, ok := assignment.field.DecimalPrecision(); ok && !value.IsNull() {
			decimal, valid := value.Decimal()
			if !valid || !decimal.Fits(digits, places) {
				return invalidPlanError("decimal assignment exceeds declared precision or scale")
			}
		}
	}
	if source, ok := assignment.expression.Field(); ok && source.Kind() == FieldDecimal {
		fromDigits, fromPlaces, _ := source.DecimalPrecision()
		toDigits, toPlaces, _ := assignment.field.DecimalPrecision()
		if fromPlaces > toPlaces || fromDigits-fromPlaces > toDigits-toPlaces {
			return &Error{Category: CategoryQuery, Code: CodeUnsupported, Detail: "decimal field assignment cannot narrow declared precision or scale without an explicit conversion"}
		}
	}
	return nil
}
