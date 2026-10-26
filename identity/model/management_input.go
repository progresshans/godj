package model

import (
	"slices"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/serializers"
)

// InputSpec prepares JSON input inside the management owner's operation
// allowlist. It retains the selected model's complete source, login/email
// normalization and raw byte limits. It performs no I/O and grants no write
// authority; final state, revision and policy belong to the lifecycle owner.
func (management *AccountManagement[M]) InputSpec(action admin.Action, fields ...serializers.ModelField) (serializers.Spec, error) {
	if management == nil || management.state == nil {
		return serializers.Spec{}, managementError(CodeInvalidConfig, nil)
	}
	var allowed []string
	switch action {
	case admin.ActionAdd:
		allowed = management.state.creator.Fields()
	case admin.ActionChange:
		allowed = management.state.editor.Fields()
	default:
		return serializers.Spec{}, managementError(CodeInvalidConfig, nil)
	}
	for _, field := range fields {
		if field.ReadOnly || !slices.Contains(allowed, field.Name) {
			return serializers.Spec{}, managementError(CodeInvalidConfig, nil)
		}
	}
	return management.state.reader.state.store.state.binding.InputSpec(fields...)
}
