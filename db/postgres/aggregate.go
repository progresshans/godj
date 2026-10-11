package postgres

import (
	"strconv"
	"strings"

	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/query"
)

func renderAggregate(expression query.ResultExpression, operand, filter string, grouped bool) (string, error) {
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
	ordered := expression.Kind() == query.ResultMin || expression.Kind() == query.ResultMax
	if expression.Kind() == query.ResultSum || expression.Kind() == query.ResultAvg {
		var err error
		if _, computed := expression.Scalar(); !computed {
			operand, err = numericAggregateOperand(field, operand)
		}
		if err != nil {
			return "", err
		}
	}
	if ordered {
		switch expression.OperandKind() {
		case query.FieldBinary:
			// Hex under C collation preserves unsigned byte ordering.
			operand = "encode(" + operand + ", 'hex') COLLATE \"C\""
		case query.FieldUUID:
			// PostgreSQL 17 orders UUID but has no native MIN/MAX(uuid).
			operand += "::text COLLATE \"C\""
		}
	}
	if expression.Distinct() {
		operand = "DISTINCT " + operand
	}
	result := function + "(" + operand + ")" + filter
	if ordered {
		switch expression.OperandKind() {
		case query.FieldBinary:
			result = "decode(" + result + ", 'hex')"
		case query.FieldUUID:
			result += "::uuid"
		}
		if grouped {
			var statement strings.Builder
			statement.WriteString(result)
			appendDecimalResultPrecision(&statement, field)
			result = statement.String()
		}
	}
	// PostgreSQL returns NUMERIC for SUM/AVG(bigint). The outward result
	// type must match the AST even when native Decimal row adaptation is on.
	// Casting the completed aggregate preserves native intermediate sums and
	// makes an unrepresentable int64 result a database error, never a wrap.
	if expression.OperandKind() == query.FieldInteger {
		switch expression.Kind() {
		case query.ResultSum:
			result = "(" + result + ")::bigint"
		case query.ResultAvg:
			result = "(" + result + ")::double precision"
		}
	}
	return result, nil
}

// Native NUMERIC permits NaN and INTERVAL permits months and wider days than
// the model. Validate each contributing operand, so invalid external values
// cannot cancel each other or disappear behind a HAVING predicate. The error
// branch depends on the row and cannot be a planning-time constant cast; it
// never includes the stored value in its diagnostic text.
func numericAggregateOperand(field query.FieldRef, column string) (string, error) {
	var valid, kind, diagnostic string
	switch field.Kind() {
	case query.FieldInteger, query.FieldFloat:
		return column, nil
	case query.FieldDecimal:
		digits, places, ok := field.DecimalPrecision()
		if !ok {
			return "", invalidPlan("decimal aggregate requires source precision")
		}
		valid = column + " = trunc(" + column + ", " + strconv.Itoa(places) + ") AND abs(" + column + ") < '1e" + strconv.Itoa(digits-places) + "'::numeric"
		kind, diagnostic = "numeric", "godj_invalid_decimal_operand"
	case query.FieldDuration:
		valid = "EXTRACT(YEAR FROM " + column + ") = 0 AND EXTRACT(MONTH FROM " + column + ") = 0 AND " + column + " >= INTERVAL '" + strconv.Itoa(int(duration.MinDays)) + " days' AND " + column + " < INTERVAL '" + strconv.Itoa(int(duration.MaxDays)+1) + " days'"
		kind, diagnostic = "interval", "godj_invalid_duration_operand"
	default:
		return "", invalidPlan("numeric aggregate has a nonnumeric operand")
	}
	return "CASE WHEN " + column + " IS NULL OR (" + valid + ") THEN " + column + " ELSE ('" + diagnostic + "' || left(" + column + "::text, 0))::" + kind + " END", nil
}
