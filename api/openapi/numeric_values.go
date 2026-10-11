package openapi

import (
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/serializers"
)

// Decimal describes a canonical exact Decimal string, independently of a
// model field's fixed scale. The extension preserves the model value domain;
// the pattern and length describe its string representation.
func Decimal() Schema {
	policy, _ := serializers.NewObject(
		serializers.MemberOf("maxSignificantDigits", serializers.Integer(decimal.MaxDigits)),
		serializers.MemberOf("minimumAdjustedExponent", serializers.Integer(-decimal.MaxAdjustedExponent)),
		serializers.MemberOf("maximumAdjustedExponent", serializers.Integer(decimal.MaxAdjustedExponent)),
		serializers.MemberOf("representation", serializers.String("canonical-decimal-string")),
	)
	schema, _ := schemaAnnotate(String(),
		serializers.MemberOf("pattern", serializers.String(`^-?(0|[1-9][0-9]*)(\.[0-9]*[1-9])?$`)),
		serializers.MemberOf("minLength", serializers.Integer(1)),
		serializers.MemberOf("maxLength", serializers.Integer(decimal.MaxDigits+decimal.MaxAdjustedExponent+2)),
		serializers.MemberOf("x-godj-decimal", policy.Value()),
	)
	return schema
}

// Duration describes the normalized model duration, including its full day
// range independently of a particular backend's narrower storage domain.
func Duration() Schema {
	schema, _ := schemaAnnotate(String(),
		serializers.MemberOf("pattern", serializers.String(`^(-?[1-9][0-9]{0,8} )?([01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9](\.[0-9]{6})?$`)),
		serializers.MemberOf("minLength", serializers.Integer(8)),
		serializers.MemberOf("maxLength", serializers.Integer(26)),
		serializers.MemberOf("x-godj-duration", serializers.String("normalized-days-and-microseconds")),
	)
	return schema
}
