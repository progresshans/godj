package openapi

import (
	"time"

	"github.com/progresshans/godj/serializers"
)

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
