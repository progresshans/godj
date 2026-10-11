package sqlite

import (
	"strconv"

	"github.com/progresshans/godj/query"
)

func renderAggregate(expression query.ResultExpression, operand, filter string) (string, error) {
	var function string
	switch expression.Kind() {
	case query.ResultCountAll:
		function, operand = "COUNT", "*"
	case query.ResultCount:
		function = "COUNT"
	case query.ResultMin:
		function = "MIN"
	case query.ResultMax:
		function = "MAX"
	case query.ResultSum:
		function = "SUM"
	case query.ResultAvg:
		function = "AVG"
	default:
		return "", invalidPlan("unsupported aggregate function")
	}
	field, _ := expression.Field()
	numeric := expression.Kind() == query.ResultSum || expression.Kind() == query.ResultAvg
	if numeric {
		switch expression.OperandKind() {
		case query.FieldInteger, query.FieldDuration:
			operand = "godj_int64(" + operand + ")"
		case query.FieldFloat:
			operand = "godj_float_operand(" + operand + ")"
		case query.FieldDecimal:
			digits, places, ok := field.DecimalPrecision()
			if !ok {
				return "", invalidPlan("decimal aggregate requires operand precision")
			}
			// A single packed argument permits native DISTINCT without losing
			// the declared source precision needed for every physical value.
			operand = "godj_decimal_aggregate_input(" + operand + ", " + strconv.Itoa(digits) + ", " + strconv.Itoa(places) + ")"
			if expression.Kind() == query.ResultSum {
				function = "godj_decimal_sum"
			} else {
				function = "godj_decimal_avg"
			}
		default:
			return "", invalidPlan("numeric aggregate has a nonnumeric operand")
		}
	}
	selected := operand
	if expression.Distinct() {
		selected = "DISTINCT " + selected
	}
	result := function + "(" + selected + ")" + filter
	if numeric && expression.OperandKind() == query.FieldFloat {
		// Native SQLite represents a NaN result as NULL. COUNT separates that
		// arithmetic failure from a legitimate empty/all-NULL aggregate. A
		// filtered call emits its bound arguments twice, in this same order.
		result = "godj_float_aggregate(" + result + ", COUNT(" + operand + ")" + filter + ")"
	}
	return result, nil
}
