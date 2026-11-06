package openapi

import "github.com/progresshans/godj/serializers"

// These are executable input-policy descriptions, not JSON Schema assertions
// on the raw string. A normalized alias may be longer than the stored value or
// outside the stored enum. The raw UTF-8 byte envelope precedes every transform.
func stringInputPolicy(field serializers.Field) (serializers.Member, error) {
	transform := string(field.StringNormalization())
	if transform == "" {
		transform = "exact"
	}
	policy := []serializers.Member{
		serializers.MemberOf("transform", serializers.String(transform)),
		serializers.MemberOf("trimWhitespace", serializers.Boolean(field.TrimWhitespace())),
		serializers.MemberOf("allowEmptyAfterNormalization", serializers.Boolean(field.AllowEmpty())),
	}
	if field.TrimWhitespace() {
		profile := "go-unicode"
		if field.Kind() != serializers.FieldString {
			profile = "python-unicode-16"
		}
		policy = append(policy, serializers.MemberOf("whitespaceProfile", serializers.String(profile)))
	}
	if field.MaxInputBytes() > 0 {
		policy = append(policy, serializers.MemberOf("maxInputBytes", serializers.Integer(int64(field.MaxInputBytes()))))
	}
	if field.MaxLength() > 0 {
		policy = append(policy, serializers.MemberOf("maxLengthAfterNormalization", serializers.Integer(int64(field.MaxLength()))))
	}
	if field.Choices() != nil {
		enum, err := choiceInputValues(field)
		if err != nil {
			return serializers.Member{}, err
		}
		policy = append(policy, serializers.MemberOf("enumAfterNormalization", enum))
	}
	object, err := serializers.NewObject(policy...)
	if err != nil {
		return serializers.Member{}, schemaConfigError("normalization", "string input policy is invalid")
	}
	return serializers.MemberOf("x-godj-normalization", object.Value()), nil
}
