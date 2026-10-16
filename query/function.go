package query

import "slices"

// FunctionKind is a closed semantic operation, not a SQL function name. The
// common AST owns arity and input/output domains; each backend owns execution.
// Calendar extraction from an instant uses the model's UTC representation.
type FunctionKind uint8

const (
	FunctionDate FunctionKind = iota + 1
	FunctionTime
	FunctionYear
	FunctionISOYear
	FunctionQuarter
	FunctionMonth
	FunctionWeek
	FunctionWeekDay
	FunctionISOWeekDay
	FunctionDay
	FunctionHour
	FunctionMinute
	FunctionSecond
)

// FunctionExpression snapshots the ordered argument container. Expressions
// keep their own kinds and source authority, including typed NULLs, finite
// relations and selected-result scopes. No field metadata is synthesized.
func FunctionExpression(function FunctionKind, arguments ...ScalarExpression) (ScalarExpression, error) {
	// Every currently supported function is unary. Check arity before copying
	// or traversing an arbitrarily large caller-provided argument list.
	if len(arguments) != 1 {
		return ScalarExpression{}, invalidPlanError("calendar extraction requires exactly one operand")
	}
	argument := arguments[0]
	if err := argument.Validate(); err != nil {
		return ScalarExpression{}, err
	}
	result, err := functionResultKind(function, argument.ResultKind())
	if err != nil {
		return ScalarExpression{}, err
	}
	if argument.Depth() >= MaximumScalarDepth || argument.NodeCount() >= MaximumScalarNodes {
		return ScalarExpression{}, invalidPlanError("scalar function exceeds its depth or node budget")
	}
	return ScalarExpression{&scalarNode{
		kind: ScalarFunction, function: function, arguments: slices.Clone(arguments), result: result,
		nullable: argument.Nullable(), depth: argument.Depth() + 1, nodes: argument.NodeCount() + 1,
		resultDepth: argument.resultReferenceDepth(), hasRelations: argument.HasRelations(),
	}}, nil
}

func functionResultKind(function FunctionKind, input FieldKind) (FieldKind, error) {
	switch function {
	case FunctionDate:
		if input == FieldDateTime {
			return FieldDate, nil
		}
	case FunctionTime:
		if input == FieldDateTime {
			return FieldTime, nil
		}
	case FunctionYear, FunctionISOYear, FunctionQuarter, FunctionMonth, FunctionWeek, FunctionWeekDay, FunctionISOWeekDay, FunctionDay:
		if input == FieldDate || input == FieldDateTime {
			return FieldInteger, nil
		}
	case FunctionHour, FunctionMinute, FunctionSecond:
		if input == FieldTime || input == FieldDateTime {
			return FieldInteger, nil
		}
	default:
		return "", invalidPlanError("unknown scalar function")
	}
	return "", &Error{Category: CategoryQuery, Code: CodeUnsupported, Detail: "function operand does not match its explicit calendar or clock signature"}
}

// Function returns an owned argument slice; replacing its entries cannot
// change this immutable expression or a query that has already captured it.
func (expression ScalarExpression) Function() (FunctionKind, []ScalarExpression, bool) {
	if expression.Kind() != ScalarFunction {
		return 0, nil, false
	}
	return expression.node.function, slices.Clone(expression.node.arguments), true
}
