package queryplan

import (
	"strings"

	"github.com/progresshans/godj/query"
)

// AppendScalarCondition retains SQL NULL under NOT. Its caller owns Boolean
// connectors; this leaf never borrows field lookup compensation semantics.
func AppendScalarCondition(statement *strings.Builder, condition query.Condition,
	render func(query.ScalarExpression) (string, error), parameter func(query.Value) (string, error)) error {
	scalar, present := condition.Scalar()
	if !present {
		return invalidPlan("computed comparison requires a scalar operand")
	}
	if _, err := query.NewExpression(condition); err != nil {
		return err
	}
	value, err := render(scalar)
	if err != nil {
		return err
	}
	statement.WriteString(value)
	if condition.Lookup() == query.LookupIsNull {
		isNull, _ := condition.Value().Boolean()
		if isNull {
			statement.WriteString(" IS NULL")
		} else {
			statement.WriteString(" IS NOT NULL")
		}
		return nil
	}
	switch condition.Lookup() {
	case query.LookupExact:
		statement.WriteString(" = ")
	case query.LookupGreaterThan:
		statement.WriteString(" > ")
	case query.LookupGreaterThanOrEqual:
		statement.WriteString(" >= ")
	case query.LookupLessThan:
		statement.WriteString(" < ")
	case query.LookupLessThanOrEqual:
		statement.WriteString(" <= ")
	default:
		return invalidPlan("computed comparison lookup is unsupported")
	}
	var right string
	if scalar, present := condition.RHSScalar(); present {
		right, err = render(scalar)
	} else {
		right, err = parameter(condition.Value())
	}
	if err != nil {
		return err
	}
	statement.WriteString(right)
	return nil
}

// ValidateScalarCondition checks both operands using the actual plan authority.
func ValidateScalarCondition(plan query.Plan, condition query.Condition) error {
	return (ScalarSource{Plan: plan}).ValidateCondition(condition)
}

func (source ScalarSource) ValidateCondition(condition query.Condition) error {
	if _, err := query.NewExpression(condition); err != nil {
		return err
	}
	left, present := condition.Scalar()
	if !present {
		return invalidPlan("computed comparison requires a scalar operand")
	}
	if err := source.Validate(left); err != nil {
		return err
	}
	if right, present := condition.RHSScalar(); present {
		return source.Validate(right)
	}
	return nil
}
