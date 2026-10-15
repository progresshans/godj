package parameters

import (
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/decimal"
)

// CanonicalDecimal accepts Decimal.String's exact spelling within a declared
// precision: no exponent, plus sign, leading/trailing zero aliases or rounding.
// Negative zero remains an explicit value, following the Decimal value domain.
func CanonicalDecimal(maxDigits, decimalPlaces int) Codec[decimal.Decimal] {
	return Codec[decimal.Decimal]{
		schema: func(fallback *decimal.Decimal) (openapi.Schema, error) {
			return openapi.QueryDecimal(maxDigits, decimalPlaces, fallback)
		},
		headerSchema: func(maximumBytes int, fallback *decimal.Decimal) (openapi.Schema, error) {
			return openapi.HeaderDecimal(maxDigits, decimalPlaces, maximumBytes, fallback)
		},
		decode: func(raw string) (decimal.Decimal, bool) {
			value, err := decimal.Parse(raw)
			if err != nil || !value.Fits(maxDigits, decimalPlaces) || value.String() != raw {
				return decimal.Decimal{}, false
			}
			return value, true
		},
	}
}
