package query

import (
	"slices"
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
	ScalarCase
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
	branches     []ScalarWhen
}

// ScalarWhen pairs a same-row Boolean predicate with a value. Both handles
// are immutable; CaseScalar snapshots the ordered branch container.
type ScalarWhen struct {
	when  Expression
	value ScalarExpression
}

func WhenScalar(when Expression, value ScalarExpression) (ScalarWhen, error) {
	if err := when.validate(); err != nil {
		return ScalarWhen{}, err
	}
	if err := value.Validate(); err != nil {
		return ScalarWhen{}, err
	}
	if when.HasRelations() {
		return ScalarWhen{}, &Error{Category: CategoryQuery, Code: CodeUnsupported, Detail: "conditional scalar predicates require same-row fields"}
	}
	return ScalarWhen{when: when, value: value}, nil
}
func (branch ScalarWhen) Predicate() Expression   { return branch.when }
func (branch ScalarWhen) Value() ScalarExpression { return branch.value }

func CaseScalar(fallback ScalarExpression, branches ...ScalarWhen) (ScalarExpression, error) {
	if err := fallback.Validate(); err != nil {
		return ScalarExpression{}, err
	}
	if len(branches) == 0 {
		return fallback, nil
	}
	if len(branches) >= MaximumScalarNodes {
		return ScalarExpression{}, invalidPlanError("conditional scalar requires a bounded branch list")
	}
	result, nullable := fallback.ResultKind(), fallback.Nullable()
	depth, nodes := fallback.Depth()+1, fallback.NodeCount()+1
	for _, branch := range branches {
		if _, err := WhenScalar(branch.when, branch.value); err != nil {
			return ScalarExpression{}, err
		}
		kind := branch.value.ResultKind()
		if result == "" {
			result = kind
		}
		if kind != "" && kind != result {
			return ScalarExpression{}, invalidPlanError("conditional scalar branches require one explicit result kind")
		}
		nullable = nullable || branch.value.Nullable()
		depth = max(depth, 1+branch.when.node.depth, 1+branch.value.Depth())
		nodes += branch.when.node.nodes + branch.value.NodeCount()
		if depth > MaximumScalarDepth || nodes > MaximumScalarNodes {
			return ScalarExpression{}, invalidPlanError("conditional scalar exceeds its resource budget")
		}
	}
	if result == "" {
		return ScalarExpression{}, invalidPlanError("conditional scalar has no explicit result kind")
	}
	return ScalarExpression{&scalarNode{kind: ScalarCase, result: result, nullable: nullable, left: fallback,
		branches: slices.Clone(branches), depth: depth, nodes: nodes}}, nil
}

func (expression ScalarExpression) Case() (ScalarExpression, []ScalarWhen, bool) {
	if expression.Kind() != ScalarCase {
		return ScalarExpression{}, nil, false
	}
	return expression.node.left, slices.Clone(expression.node.branches), true
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

// NullExpression gives a selected NULL an explicit result type. Unlike a
// FieldRef, this domain has no source column, nullability, or field precision.
func NullExpression(kind FieldKind) (ScalarExpression, error) {
	switch kind {
	case FieldInteger, FieldFloat, FieldDecimal, FieldUUID, FieldBinary, FieldJSON, FieldString, FieldBoolean, FieldDateTime, FieldDate, FieldTime, FieldDuration:
	default:
		return ScalarExpression{}, invalidPlanError("typed NULL requires a supported scalar result kind")
	}
	return ScalarExpression{&scalarNode{kind: ScalarLiteral, value: Null(), result: kind, nullable: true, depth: 1, nodes: 1}}, nil
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
		nullable: left.node.nullable || right.node.nullable || result == FieldFloat || operator == ArithmeticDivide || operator == ArithmeticModulo, depth: depth, nodes: nodes}}, nil
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
	return left.kind == right.kind && left.result == right.result && left.value == right.value && left.field == right.field && left.operator == right.operator &&
		left.left.Equal(right.left) && left.right.Equal(right.right) && slices.EqualFunc(left.branches, right.branches, func(a, b ScalarWhen) bool {
		return a.when.Equal(b.when) && a.value.Equal(b.value)
	})
}

// ValidateSource preserves exact IR metadata even when none of its columns
// are selected. Callers cannot replace a field's kind or precision to describe
// the result of an operation on that field.
func (expression ScalarExpression) ValidateSource(source []FieldRef) error {
	fields := make(map[string]FieldRef, len(source))
	for _, field := range source {
		if previous, present := fields[field.Column()]; present && previous != field {
			return invalidPlanError("scalar source has ambiguous column metadata")
		}
		fields[field.Column()] = field
	}
	return expression.validateSource(fields)
}

// Fields returns exact source leaves in traversal order. The result is owned
// by the caller; the expression and its metadata remain immutable.
func (expression ScalarExpression) Fields() []FieldRef {
	var fields []FieldRef
	var visit func(ScalarExpression)
	visit = func(value ScalarExpression) {
		if field, ok := value.Field(); ok {
			fields = append(fields, field)
		} else if _, left, right, ok := value.Binary(); ok {
			visit(left)
			visit(right)
		} else if child, ok := value.Negated(); ok {
			visit(child)
		} else if fallback, branches, ok := value.Case(); ok {
			visit(fallback)
			for _, branch := range branches {
				fields = append(fields, branch.when.SourceFields()...)
				visit(branch.value)
			}
		}
	}
	visit(expression)
	return fields
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
	if fallback, branches, ok := expression.Case(); ok {
		if err := fallback.validateSource(fields); err != nil {
			return err
		}
		for _, branch := range branches {
			for _, field := range branch.when.SourceFields() {
				if fields[field.Column()] != field {
					return invalidPlanError("conditional predicate differs from its source metadata")
				}
			}
			if err := branch.value.validateSource(fields); err != nil {
				return err
			}
		}
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
	}
	if assignment.field.Kind() == FieldDecimal {
		return validateAssignedDecimal(assignment.expression, assignment.field)
	}
	return nil
}

func validateAssignedDecimal(expression ScalarExpression, target FieldRef) error {
	if value, ok := expression.Literal(); ok && !value.IsNull() {
		digits, places, _ := target.DecimalPrecision()
		number, valid := value.Decimal()
		if !valid || !number.Fits(digits, places) {
			return invalidPlanError("decimal assignment exceeds declared precision or scale")
		}
	}
	if source, ok := expression.Field(); ok {
		fromDigits, fromPlaces, _ := source.DecimalPrecision()
		toDigits, toPlaces, _ := target.DecimalPrecision()
		if fromPlaces > toPlaces || fromDigits-fromPlaces > toDigits-toPlaces {
			return &Error{Category: CategoryQuery, Code: CodeUnsupported, Detail: "decimal field assignment cannot narrow declared precision or scale without an explicit conversion"}
		}
	}
	if fallback, branches, ok := expression.Case(); ok {
		if err := validateAssignedDecimal(fallback, target); err != nil {
			return err
		}
		for _, branch := range branches {
			if err := validateAssignedDecimal(branch.value, target); err != nil {
				return err
			}
		}
	}
	return nil
}
