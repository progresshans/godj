package sqlite

import (
	"database/sql/driver"
	"errors"
	"math/big"
	"strings"

	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/internal/decimalstorage"
	"github.com/progresshans/godj/query"
	modernsqlite "modernc.org/sqlite"
)

const invalidDecimalExpression = "godj: decimal expression requires canonical values and a result in the supported domain"
const maximumCanonicalDecimalPlaces = decimal.MaxAdjustedExponent + decimal.MaxDigits - 1

func decimalExpressionError(cause error) error {
	return errors.Join(errors.New(invalidDecimalExpression), cause)
}

func decodeDecimalExpression(raw driver.Value) (decimal.Decimal, error) {
	key, ok := raw.([]byte)
	if !ok {
		return decimal.Decimal{}, decimalExpressionError(nil)
	}
	value, err := decimalstorage.Decode(key)
	if err != nil {
		return decimal.Decimal{}, decimalExpressionError(err)
	}
	return value, nil
}

func decimalCoefficient(value decimal.Decimal) *big.Int {
	number := new(big.Int)
	if value.Coefficient != "" && value.Coefficient != "-0" {
		number.SetString(value.Coefficient, 10) // The caller has decoded a canonical value.
	}
	return number
}

func sqliteDecimalResult(_ *modernsqlite.FunctionContext, arguments []driver.Value) (driver.Value, error) {
	if len(arguments) != 1 {
		return nil, decimalExpressionError(nil)
	}
	if arguments[0] == nil {
		return nil, nil
	}
	value, err := decodeDecimalExpression(arguments[0])
	if err != nil {
		return nil, err
	}
	return decimalstorage.Encode(value)
}

func sqliteDecimalNegate(_ *modernsqlite.FunctionContext, arguments []driver.Value) (driver.Value, error) {
	if len(arguments) != 1 {
		return nil, decimalExpressionError(nil)
	}
	if arguments[0] == nil {
		return nil, nil
	}
	value, err := decodeDecimalExpression(arguments[0])
	if err != nil {
		return nil, err
	}
	number := decimalCoefficient(value)
	number.Neg(number)
	result, err := decimal.New(number.String(), value.Exponent)
	if err != nil {
		return nil, decimalExpressionError(err)
	}
	return decimalstorage.Encode(result)
}

func sqliteDecimalBinary(_ *modernsqlite.FunctionContext, arguments []driver.Value) (driver.Value, error) {
	if len(arguments) != 3 {
		return nil, decimalExpressionError(nil)
	}
	operator, ok := arguments[0].(string)
	if !ok {
		return nil, decimalExpressionError(nil)
	}
	switch query.ArithmeticOperator(operator) {
	case query.ArithmeticAdd, query.ArithmeticSubtract, query.ArithmeticMultiply, query.ArithmeticDivide, query.ArithmeticModulo:
	default:
		return nil, decimalExpressionError(nil)
	}
	var values [2]decimal.Decimal
	missing := false
	for index, raw := range arguments[1:] {
		if raw == nil {
			missing = true
			continue
		}
		value, err := decodeDecimalExpression(raw)
		if err != nil {
			return nil, err
		}
		values[index] = value
	}
	if missing {
		return nil, nil
	}
	left, right := values[0], values[1]
	numerator, denominator := decimalCoefficient(left), decimalCoefficient(right)
	exponent := min(left.Exponent, right.Exponent)
	var result decimal.Decimal
	var err error
	switch query.ArithmeticOperator(operator) {
	case query.ArithmeticDivide:
		if denominator.Sign() == 0 {
			return nil, nil // SQLite division by zero is SQL NULL.
		}
		scale := decimalDivisionScale(left, right)
		result, err = roundedDecimalRatio(numerator, denominator, int(left.Exponent-right.Exponent), scale)
	case query.ArithmeticMultiply:
		numerator.Mul(numerator, denominator)
		result, err = decimal.New(numerator.String(), left.Exponent+right.Exponent)
	default:
		// The bounded canonical exponent range limits each alignment to
		// 2999 zeros; no field or backend metadata is retained by this call.
		numerator.Mul(numerator, decimalPower(int(left.Exponent-exponent)))
		denominator.Mul(denominator, decimalPower(int(right.Exponent-exponent)))
		switch query.ArithmeticOperator(operator) {
		case query.ArithmeticAdd:
			numerator.Add(numerator, denominator)
		case query.ArithmeticSubtract:
			numerator.Sub(numerator, denominator)
		case query.ArithmeticModulo:
			if denominator.Sign() == 0 {
				return nil, nil
			}
			numerator.Rem(numerator, denominator)
		}
		result, err = decimal.New(numerator.String(), exponent)
	}
	if err != nil {
		return nil, decimalExpressionError(err)
	}
	return decimalstorage.Encode(result)
}

func roundedDecimalRatio(numerator, denominator *big.Int, exponent, scale int) (decimal.Decimal, error) {
	if denominator.Sign() == 0 || scale < 0 || scale > decimal.MaxDigits {
		return decimal.Decimal{}, decimalExpressionError(nil)
	}
	var top, bottom big.Int
	top.Abs(numerator)
	bottom.Abs(denominator)
	shift := exponent + scale
	if shift >= 0 {
		top.Mul(&top, decimalPower(shift))
	} else {
		bottom.Mul(&bottom, decimalPower(-shift))
	}
	var result, remainder big.Int
	result.QuoRem(&top, &bottom, &remainder)
	if remainder.Lsh(&remainder, 1).Cmp(&bottom) >= 0 {
		result.Add(&result, big.NewInt(1))
	}
	if numerator.Sign()*denominator.Sign() < 0 {
		result.Neg(&result)
	}
	return decimal.New(result.String(), -int32(scale))
}

// Use the same PostgreSQL 17.10 division-scale rule as decimalMeanScale, but
// with canonical operand scales. SQLite BLOBs do not retain NUMERIC display
// scale. That backend difference is explicit in ADR-0069 and the reference.
func decimalDivisionScale(left, right decimal.Decimal) int {
	leader := func(value decimal.Decimal) (int, int) {
		if value.Coefficient == "" || value.Coefficient == "-0" {
			return 0, 0
		}
		return decimalLeadingGroup(strings.TrimPrefix(value.Coefficient, "-"), -int(value.Exponent))
	}
	leftWeight, leftFirst := leader(left)
	rightWeight, rightFirst := leader(right)
	weight := leftWeight - rightWeight
	if leftFirst <= rightFirst {
		weight--
	}
	return min(decimal.MaxDigits, max(16-4*weight, -int(left.Exponent), -int(right.Exponent), 0))
}

func compileSQLiteBinary(expression query.ScalarExpression, left, right string) (string, error) {
	operator, _, _, valid := expression.Binary()
	if !valid {
		return "", invalidPlan("binary scalar compiler requires a binary node")
	}
	if expression.ResultKind() == query.FieldDecimal {
		return "godj_decimal_binary('" + string(operator) + "', " + left + ", " + right + ")", nil
	}
	return "(" + left + " " + string(operator) + " " + right + ")", nil
}

func compileSQLiteNegate(expression query.ScalarExpression, value string) (string, error) {
	if expression.ResultKind() == query.FieldDecimal {
		return "godj_decimal_negate(" + value + ")", nil
	}
	return "(-" + value + ")", nil
}
