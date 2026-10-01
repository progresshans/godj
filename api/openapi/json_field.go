package openapi

import "github.com/progresshans/godj/serializers"

func jsonFieldSchema(field serializers.Field, input bool) (Schema, error) {
	policy, err := serializers.NewObject(
		serializers.MemberOf("numbers", serializers.String("exact-token")),
		serializers.MemberOf("objectOrder", serializers.String("canonical")),
		serializers.MemberOf("duplicateKeys", serializers.String("reject")),
		serializers.MemberOf("unicode", serializers.String("valid-scalar-no-nul")),
		serializers.MemberOf("nullInput", serializers.String("sql-null")),
		serializers.MemberOf("limits", serializers.String("request-and-model-budgets")),
	)
	if err != nil {
		return Schema{}, err
	}
	members := []serializers.Member{serializers.MemberOf("x-godj-json", policy.Value())}
	if input && !field.Nullable() {
		// Any JSON value except null. The response remains any JSON, because a
		// non-null database column can contain the literal JSON null document.
		members = append(members, serializers.MemberOf("not", schemaPrimitive("null").value))
	}
	return schemaObject(members...)
}
