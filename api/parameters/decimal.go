package parameters

import (
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/decimal"
)

// CanonicalDecimal accepts Decimal.String's exact spelling within a declared
// precision: no exponent, plus sign, leading/trailing zero aliases or rounding.
// Negative zero remains an explicit value, following the Decimal value domain.
func CanonicalDecimal(maxDigits, decimalPlaces int) Codec[decimal.Decimal] {
	return canonicalDecimal(maxDigits, decimalPlaces, false)
}

// CanonicalPositiveDecimal has the same exact text and precision contract as
// CanonicalDecimal and additionally requires a value greater than zero.
func CanonicalPositiveDecimal(maxDigits, decimalPlaces int) Codec[decimal.Decimal] {
	return canonicalDecimal(maxDigits, decimalPlaces, true)
}

func canonicalDecimal(maxDigits, decimalPlaces int, positive bool) Codec[decimal.Decimal] {
	return Codec[decimal.Decimal]{
		schema: func(fallback *decimal.Decimal) (openapi.Schema, error) {
			if positive {
				return openapi.QueryPositiveDecimal(maxDigits, decimalPlaces, fallback)
			}
			return openapi.QueryDecimal(maxDigits, decimalPlaces, fallback)
		},
		headerSchema: func(maximumBytes int, fallback *decimal.Decimal) (openapi.Schema, error) {
			if positive {
				return openapi.HeaderPositiveDecimal(maxDigits, decimalPlaces, maximumBytes, fallback)
			}
			return openapi.HeaderDecimal(maxDigits, decimalPlaces, maximumBytes, fallback)
		},
		decode: func(raw string) (decimal.Decimal, bool) {
			value, err := decimal.Parse(raw)
			if err != nil || !value.Fits(maxDigits, decimalPlaces) || value.String() != raw {
				return decimal.Decimal{}, false
			}
			if positive {
				order, err := value.Compare(decimal.Decimal{})
				if err != nil || order <= 0 {
					return decimal.Decimal{}, false
				}
			}
			return value, true
		},
	}
}
