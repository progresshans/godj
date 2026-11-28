package query

import (
	"slices"

	"github.com/progresshans/godj/timezone"
)

// FunctionKind is a closed semantic operation, not a SQL function name. The
// common AST owns arity and input/output domains; each backend owns execution.
// Calendar functions on an instant use UTC unless a Zone is explicit.
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
	FunctionTruncYear
	FunctionTruncQuarter
	FunctionTruncMonth
	FunctionTruncWeek
	FunctionTruncDay
	FunctionTruncHour
	FunctionTruncMinute
	FunctionTruncSecond
)

// FunctionExpression snapshots the ordered argument container. Expressions
// keep their own kinds and source authority, including typed NULLs, finite
// relations and selected-result scopes. No field metadata is synthesized.
func FunctionExpression(function FunctionKind, arguments ...ScalarExpression) (ScalarExpression, error) {
	return functionExpression(function, timezone.Zone{}, false, arguments)
}

// FunctionExpressionInZone applies a calendar operation to the local civil
// components of a DateTime operand. Zone is part of the immutable operation,
// not a process or connection setting. Date and Time have no instant to convert.
func FunctionExpressionInZone(function FunctionKind, zone timezone.Zone, arguments ...ScalarExpression) (ScalarExpression, error) {
	return functionExpression(function, zone, true, arguments)
}

func functionExpression(function FunctionKind, zone timezone.Zone, explicitZone bool, arguments []ScalarExpression) (ScalarExpression, error) {
	// Every currently supported function is unary. Check arity before copying
	// or traversing an arbitrarily large caller-provided argument list.
	if len(arguments) != 1 {
		return ScalarExpression{}, invalidPlanError("calendar function requires exactly one operand")
	}
	argument := arguments[0]
	if err := argument.Validate(); err != nil {
		return ScalarExpression{}, err
	}
	if explicitZone {
		if err := zone.Validate(); err != nil {
			return ScalarExpression{}, &Error{Category: CategoryQuery, Code: CodeInvalidValue, Detail: "calendar function requires a valid explicit time zone", Cause: err}
		}
		if argument.ResultKind() != FieldDateTime {
			return ScalarExpression{}, &Error{Category: CategoryQuery, Code: CodeUnsupported, Detail: "an explicit time zone requires a DateTime operand"}
		}
	}
	result, err := functionResultKind(function, argument.ResultKind())
	if err != nil {
		return ScalarExpression{}, err
	}
	if argument.Depth() >= MaximumScalarDepth || argument.NodeCount() >= MaximumScalarNodes {
		return ScalarExpression{}, invalidPlanError("scalar function exceeds its depth or node budget")
	}
	return ScalarExpression{&scalarNode{
		kind: ScalarFunction, function: function, zone: zone, arguments: slices.Clone(arguments), result: result,
		nullable: argument.Nullable(), depth: argument.Depth() + 1, nodes: argument.NodeCount() + 1,
		resultDepth: argument.resultReferenceDepth(), hasRelations: argument.HasRelations(),
		hasAnnotations: argument.HasAnnotations(), aggregate: argument.ContainsAggregate(),
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
	case FunctionTruncYear, FunctionTruncQuarter, FunctionTruncMonth, FunctionTruncWeek, FunctionTruncDay:
		if input == FieldDate || input == FieldDateTime {
			return input, nil
		}
	case FunctionTruncHour, FunctionTruncMinute, FunctionTruncSecond:
		if input == FieldTime || input == FieldDateTime {
			return input, nil
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

// FunctionZone returns the explicit zone, if any. An absent zone retains the
// operation's UTC default; it does not consult the host or session time zone.
func (expression ScalarExpression) FunctionZone() (timezone.Zone, bool) {
	if expression.Kind() != ScalarFunction || expression.node.zone.Name() == "" {
		return timezone.Zone{}, false
	}
	return expression.node.zone, true
}
