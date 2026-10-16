package postgres

import (
	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/query"
)

func compilePostgresFunction(expression query.ScalarExpression, operands []string) (string, error) {
	function, inputs, ok := expression.Function()
	if !ok || len(inputs) != 1 || len(operands) != 1 {
		return "", invalidPlan("calendar compiler requires one validated function operand")
	}
	// An extract can hide a source outside the model domain (infinity, BC,
	// year 10000 or 24:00). Bind and check its entire value before extracting.
	operand, err := temporalScalarResultSQL(inputs[0].ResultKind(), operands[0])
	if err != nil {
		return "", err
	}
	if inputs[0].ResultKind() == query.FieldDateTime {
		operand, err = queryplan.ScalarSQL("(", operand, " AT TIME ZONE 'UTC')")
		if err != nil {
			return "", err
		}
	}
	if function == query.FunctionDate {
		return queryplan.ScalarSQL("(", operand, ")::date")
	}
	if function == query.FunctionTime {
		return queryplan.ScalarSQL("(", operand, ")::time without time zone")
	}
	var part string
	switch function {
	case query.FunctionYear:
		part = "YEAR"
	case query.FunctionISOYear:
		part = "ISOYEAR"
	case query.FunctionQuarter:
		part = "QUARTER"
	case query.FunctionMonth:
		part = "MONTH"
	case query.FunctionWeek:
		part = "WEEK"
	case query.FunctionWeekDay:
		part = "DOW"
	case query.FunctionISOWeekDay:
		part = "ISODOW"
	case query.FunctionDay:
		part = "DAY"
	case query.FunctionHour:
		part = "HOUR"
	case query.FunctionMinute:
		part = "MINUTE"
	case query.FunctionSecond:
		part = "SECOND"
	default:
		return "", invalidPlan("unknown calendar function")
	}
	value, err := queryplan.ScalarSQL("EXTRACT(", part, " FROM ", operand, ")")
	if err != nil {
		return "", err
	}
	switch function {
	case query.FunctionWeekDay:
		value = "(" + value + " + 1)"
	case query.FunctionSecond:
		// PostgreSQL's numeric-to-integer cast rounds. ExtractSecond discards
		// the fractional part; it must never carry 59.999999 into the next minute.
		value = "TRUNC(" + value + ")"
	}
	return queryplan.ScalarSQL("(", value, ")::bigint")
}
