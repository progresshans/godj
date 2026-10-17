package postgres

import (
	"strconv"
	"strings"

	"github.com/progresshans/godj/db/internal/queryplan"
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
	temporalOperand := expression.OperandKind() == query.FieldDateTime || expression.OperandKind() == query.FieldDuration
	if expression.Kind() == query.ResultSum || expression.Kind() == query.ResultAvg || ordered && expression.OperandKind() == query.FieldDecimal || temporalOperand {
		var err error
		if _, computed := expression.Scalar(); !computed {
			if temporalOperand {
				operand, err = readScalarField(field, operand)
			} else {
				operand, err = numericAggregateOperand(field, operand)
			}
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
	if expression.OperandKind() == query.FieldDecimal && (expression.Kind() == query.ResultSum || expression.Kind() == query.ResultAvg) {
		return decimalResultSQL(result)
	}
	if expression.ResultValueKind() == query.FieldDateTime || expression.ResultValueKind() == query.FieldDuration {
		// Keep the completed aggregate at its owning query level. A nested
		// SELECT here would incorrectly aggregate constant computed operands.
		return temporalResultSQL(expression.ResultValueKind(), result)
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
		var err error
		valid, err = queryplan.ScalarSQL(column, " = trunc(", column, ", ", strconv.Itoa(places), ") AND abs(", column, ") < '1e", strconv.Itoa(digits-places), "'::numeric")
		if err != nil {
			return "", err
		}
		kind, diagnostic = "numeric", "godj_invalid_decimal_operand"
	case query.FieldDuration:
		return temporalResultSQL(query.FieldDuration, column)
	default:
		return "", invalidPlan("numeric aggregate has a nonnumeric operand")
	}
	return guardedScalarSQL(column, valid, kind, diagnostic)
}
