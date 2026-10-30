package model

import (
	"context"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/progresshans/godj/auth/passwordpolicy"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

// PasswordAttribute selects one actual string field for the similarity rule.
// Label is static, value-free presentation metadata. The credential/principal
// fields cannot be selected. A nullable attribute contributes no text when null.
type PasswordAttribute struct{ Field, Label string }

// DefaultPasswordValidator applies the pinned similarity, minimum-length,
// common-password and Unicode numeric rules to detached values of this model.
// Nil attributes selects the actual login and optional distinct email roles;
// an explicit empty slice selects no similarity attributes. Additional profile
// attributes are opt-in, retain their declared order and never use a User DTO.
// Construction reads no rows, invokes no validator and performs no hashing.
func (binding Binding[M]) DefaultPasswordValidator(attributes ...PasswordAttribute) (PasswordValidator[M], error) {
	if binding.state == nil {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	role := binding.state.role
	if attributes == nil {
		attributes = []PasswordAttribute{{Field: role.Login, Label: strings.ReplaceAll(role.Login, "_", " ")}}
		if role.Email != "" && role.Email != role.Login {
			attributes = append(attributes, PasswordAttribute{Field: role.Email, Label: strings.ReplaceAll(role.Email, "_", " ")})
		}
	}
	owned := slices.Clone(attributes)
	metadata, err := binding.state.objects.Metadata()
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	fields := make(map[string]ir.Field, len(metadata.Fields))
	for _, field := range metadata.Fields {
		fields[field.Name] = field
	}
	seen := make(map[string]bool, len(owned))
	for _, attribute := range owned {
		field, present := fields[attribute.Field]
		if !present || seen[attribute.Field] || attribute.Field == role.Password || attribute.Field == role.PrincipalID ||
			attribute.Field == role.Revision || attribute.Field == role.CreatedAt || attribute.Field == role.LastLogin ||
			attribute.Label == "" || len(attribute.Label) > 256 || !utf8.ValidString(attribute.Label) ||
			strings.IndexFunc(attribute.Label, unicode.IsControl) >= 0 {
			return nil, managementError(CodeInvalidConfig, nil)
		}
		switch field.Kind {
		case ir.FieldChar, ir.FieldText, ir.FieldEmail, ir.FieldURL, ir.FieldSlug:
		default:
			return nil, managementError(CodeInvalidConfig, nil)
		}
		seen[attribute.Field] = true
	}
	validators, err := passwordpolicy.Default()
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	return func(ctx context.Context, raw string, row M) (validation.Errors, error) {
		if nilModelValue(ctx) {
			return validation.Errors{}, managementError(CodeInvalidInput, nil)
		}
		if err := ctx.Err(); err != nil {
			return validation.Errors{}, err
		}
		values, err := binding.state.objects.ModelValues(row)
		if err != nil {
			return validation.Errors{}, managementError(CodeInvalidInput, err)
		}
		selected := make([]passwordpolicy.Attribute, len(owned))
		for i, attribute := range owned {
			value, present := values[attribute.Field]
			if !present {
				return validation.Errors{}, managementError(CodeInvalidInput, nil)
			}
			text, ok := value.String()
			if !ok && !(value.IsNull() && fields[attribute.Field].Nullable) {
				return validation.Errors{}, managementError(CodeInvalidInput, nil)
			}
			selected[i] = passwordpolicy.Attribute{Label: attribute.Label, Value: text}
		}
		results := make([]validation.Errors, 0, len(validators))
		for _, validator := range validators {
			results = append(results, validator.ValidatePassword(raw, selected))
		}
		if err := ctx.Err(); err != nil {
			return validation.Errors{}, err
		}
		return validation.Join(results...), nil
	}, nil
}
