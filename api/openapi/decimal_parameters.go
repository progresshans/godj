package openapi

import (
	"strconv"

	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/serializers"
)

// QueryDecimal describes exact canonical decimal text in a declared precision
// domain. It is a string, so clients cannot silently round it through binary64.
func QueryDecimal(maxDigits, decimalPlaces int, fallback *decimal.Decimal) (Schema, error) {
	return decimalTextSchema(maxDigits, decimalPlaces, 0, fallback, "query", false)
}

// QueryPositiveDecimal publishes the canonical grammar for values strictly
// greater than zero, including values smaller than one when scale permits.
func QueryPositiveDecimal(maxDigits, decimalPlaces int, fallback *decimal.Decimal) (Schema, error) {
	return decimalTextSchema(maxDigits, decimalPlaces, 0, fallback, "query", true)
}

func HeaderDecimal(maxDigits, decimalPlaces, maximumBytes int, fallback *decimal.Decimal) (Schema, error) {
	if maximumBytes < 1 || maximumBytes > maximumHeaderValueBytes {
		return Schema{}, schemaConfigError("header.bytes", "header value budget must be between 1 and 1 MiB")
	}
	return decimalTextSchema(maxDigits, decimalPlaces, maximumBytes, fallback, "header", false)
}

func HeaderPositiveDecimal(maxDigits, decimalPlaces, maximumBytes int, fallback *decimal.Decimal) (Schema, error) {
	if maximumBytes < 1 || maximumBytes > maximumHeaderValueBytes {
		return Schema{}, schemaConfigError("header.bytes", "header value budget must be between 1 and 1 MiB")
	}
	return decimalTextSchema(maxDigits, decimalPlaces, maximumBytes, fallback, "header", true)
}

func decimalTextSchema(maxDigits, decimalPlaces, byteLimit int, fallback *decimal.Decimal, source string, positive bool) (Schema, error) {
	if maxDigits < 1 || maxDigits > decimal.MaxDigits || decimalPlaces < 0 || decimalPlaces > maxDigits {
		return Schema{}, schemaConfigError(source+".decimal", "decimal precision is invalid")
	}
	whole := maxDigits - decimalPlaces
	integer := "0"
	if whole > 0 {
		integer = "(0|[1-9][0-9]{0," + strconv.Itoa(whole-1) + "})"
	}
	pattern := "^-?" + integer
	maximum := 1 + max(whole, 1)
	if decimalPlaces > 0 {
		pattern += `(\.[0-9]{0,` + strconv.Itoa(decimalPlaces-1) + `}[1-9])?`
		maximum += 1 + decimalPlaces
	}
	minimum := 1
	if positive {
		maximum-- // A positive spelling has no sign.
		pattern = "^"
		if whole > 0 {
			pattern += `[1-9][0-9]{0,` + strconv.Itoa(whole-1) + `}`
		}
		if decimalPlaces > 0 {
			fraction := `\.[0-9]{0,` + strconv.Itoa(decimalPlaces-1) + `}[1-9]`
			if whole > 0 {
				pattern = "^(" + pattern[1:] + "(" + fraction + ")?|0" + fraction + ")"
			} else {
				pattern += "0" + fraction
				minimum = 3
			}
		}
	}
	// ECMAScript $ also matches before a final newline; the final assertion
	// makes the published grammar consume the complete parameter value.
	pattern += `$(?![\s\S])`
	if byteLimit != 0 {
		maximum = min(maximum, byteLimit)
	}
	if maximum < minimum {
		return Schema{}, schemaConfigError(source+".bytes", "decimal text cannot fit the input byte budget")
	}
	policy, err := serializers.NewObject(
		serializers.MemberOf("maxDigits", serializers.Integer(int64(maxDigits))),
		serializers.MemberOf("decimalPlaces", serializers.Integer(int64(decimalPlaces))),
		serializers.MemberOf("representation", serializers.String("canonical-decimal-string")),
	)
	if err != nil {
		return Schema{}, err
	}
	annotations := []serializers.Member{
		serializers.MemberOf("pattern", serializers.String(pattern)),
		serializers.MemberOf("minLength", serializers.Integer(int64(minimum))),
		serializers.MemberOf("maxLength", serializers.Integer(int64(maximum))),
		serializers.MemberOf("x-godj-max-bytes", serializers.Integer(int64(maximum))),
		serializers.MemberOf("x-godj-decimal", policy.Value()),
	}
	if fallback != nil {
		if !fallback.Fits(maxDigits, decimalPlaces) || len(fallback.String()) > maximum {
			return Schema{}, schemaConfigError(source+".default", "decimal default is outside the input domain")
		}
		if positive {
			order, err := fallback.Compare(decimal.Decimal{})
			if err != nil || order <= 0 {
				return Schema{}, schemaConfigError(source+".default", "decimal default must be positive")
			}
		}
		annotations = append(annotations, serializers.MemberOf("default", serializers.String(fallback.String())))
	}
	return schemaAnnotate(String(), annotations...)
}
