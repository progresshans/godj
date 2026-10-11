package openapi

import (
	"github.com/progresshans/godj/binaryvalue"
	"github.com/progresshans/godj/serializers"
)

func binaryFieldSchema(field serializers.Field, input bool) (Schema, error) {
	maximum := binaryvalue.MaxBytes
	if input && field.MaxLength() > 0 {
		maximum = field.MaxLength()
	}
	policy, err := serializers.NewObject(
		serializers.MemberOf("maximumDecodedBytes", serializers.Integer(int64(maximum))),
		serializers.MemberOf("padding", serializers.String("standard-required")),
		serializers.MemberOf("whitespace", serializers.String("reject")),
		serializers.MemberOf("nonzeroPadBits", serializers.String("accepted-input-canonical-output")),
	)
	if err != nil {
		return Schema{}, err
	}
	// String assertions stay inside the nullable string branch. The encoded
	// bound is a ceiling; the server also checks decoded length, recorded above.
	// Use JSON Schema's contentEncoding rather than a tool-specific byte-slice
	// format: pattern and length constrain the encoded JSON string, not decoded
	// bytes. Independent clients must retain that distinction.
	value, err := schemaAnnotate(String(),
		serializers.MemberOf("contentEncoding", serializers.String("base64")),
		serializers.MemberOf("pattern", serializers.String(`^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$`)),
		serializers.MemberOf("maxLength", serializers.Integer(int64((maximum+2)/3*4))),
		serializers.MemberOf("x-godj-binary", policy.Value()),
	)
	if err != nil {
		return Schema{}, err
	}
	if field.Nullable() {
		return Nullable(value)
	}
	return value, nil
}
