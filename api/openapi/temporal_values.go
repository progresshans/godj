package openapi

import (
	"time"

	"github.com/progresshans/godj/serializers"
)

// Date is a Gregorian day without a clock or time zone, shared by model and
// computed outputs. Canonical parameters add their stricter spelling policy.
func Date() Schema {
	schema, _ := schemaAnnotate(String(), serializers.MemberOf("format", serializers.String("date")))
	return schema
}

// Time is the existing model clock wire, not RFC 3339's offset-bearing time.
// The model input policy also describes accepted aliases; canonical parameter
// codecs separately reject offsets and noncanonical fractional text.
func Time() Schema {
	policy, _ := serializers.NewObject(
		serializers.MemberOf("timezone", serializers.String("none")),
		serializers.MemberOf("precision", serializers.String("microsecond")),
		serializers.MemberOf("inputOffset", serializers.String("discard-preserving-clock")),
		serializers.MemberOf("subMicrosecond", serializers.String("truncate")),
	)
	schema, _ := schemaAnnotate(String(),
		serializers.MemberOf("pattern", serializers.String(`^([01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9](\.[0-9]{6})?$`)),
		serializers.MemberOf("minLength", serializers.Integer(8)),
		serializers.MemberOf("maxLength", serializers.Integer(15)),
		serializers.MemberOf("x-godj-time", policy.Value()),
	)
	return schema
}

// DateTime is the shared model/output instant schema. Parameter declarations
// add their accepted spelling, explicit offset and resource limits separately.
func DateTime() Schema {
	policy, _ := serializers.NewObject(
		serializers.MemberOf("timezone", serializers.String("UTC")),
		serializers.MemberOf("precision", serializers.String("microsecond")),
		serializers.MemberOf("subMicrosecond", serializers.String("truncate")),
		serializers.MemberOf("minimum", serializers.String("0001-01-01T00:00:00.000000Z")),
		serializers.MemberOf("maximum", serializers.String("9999-12-31T23:59:59.999999Z")),
	)
	// The pinned ogen client's default date-time encoder drops fractions. Its
	// supported format preserves input precision until the server truncates.
	schema, _ := schemaAnnotate(String(),
		serializers.MemberOf("format", serializers.String("date-time")),
		serializers.MemberOf("x-godj-datetime", policy.Value()),
		serializers.MemberOf("x-ogen-time-format", serializers.String(time.RFC3339Nano)),
	)
	return schema
}
