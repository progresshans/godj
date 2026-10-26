package model

import (
	"slices"

	"github.com/progresshans/godj/auth"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/serializers"
)

// FormDefinition projects an explicit selection of the bound account model.
// Login/email write policies run before Form length, grammar and static-choice
// checks; their raw byte envelopes run before normalization. It performs no
// I/O or callback evaluation and does not grant authority to save these fields.
func (binding Binding[M]) FormDefinition(fields []string) (formmodel.Definition, error) {
	if binding.state == nil {
		return formmodel.Definition{}, managementError(CodeInvalidConfig, nil)
	}
	metadata, err := binding.state.objects.Metadata()
	if err != nil {
		return formmodel.Definition{}, err
	}
	role := binding.state.role
	definition := formmodel.Definition{Fields: slices.Clone(fields)}
	for _, name := range fields {
		_, maximum := accountStringInputPolicy(role, name)
		if maximum == 0 {
			continue
		}
		definition.Overrides = append(definition.Overrides, formmodel.OverrideField(name,
			formmodel.WithMaxInputBytes(maximum),
			formmodel.WithStringNormalizer(func(value string) string {
				if name == role.Login {
					value = normalizeAccountLogin(role, value)
				}
				if name == role.Email {
					value = normalizeAccountEmail(role, value)
				}
				return value
			})))
	}
	if _, err := definition.Spec(metadata); err != nil {
		return formmodel.Definition{}, err
	}
	return definition, nil
}

// InputSpec retains FromModel's complete source and explicit exposure while
// applying this binding's login/email write policy before JSON validation.
// Raw byte limits may be narrowed, but not widened; a conflicting nonzero
// normalization is a declaration error. Read-only fields are never cleaned.
// Authorization, protected fields, current state and final model constraints
// remain with the actual creation/edit owner.
func (binding Binding[M]) InputSpec(selected ...serializers.ModelField) (serializers.Spec, error) {
	if binding.state == nil {
		return serializers.Spec{}, managementError(CodeInvalidConfig, nil)
	}
	metadata, err := binding.state.objects.Metadata()
	if err != nil {
		return serializers.Spec{}, err
	}
	selected = slices.Clone(selected)
	for index := range selected {
		field := &selected[index]
		if field.ReadOnly {
			continue
		}
		policy, maximum := accountStringInputPolicy(binding.state.role, field.Name)
		if maximum == 0 {
			continue
		}
		if field.Normalization != serializers.StringExact && field.Normalization != policy || field.MaxInputBytes < 0 || field.MaxInputBytes > maximum {
			return serializers.Spec{}, managementError(CodeInvalidConfig, nil)
		}
		field.Normalization = policy
		if field.MaxInputBytes == 0 {
			field.MaxInputBytes = maximum
		}
	}
	return serializers.FromModel(metadata, selected...)
}

func accountStringInputPolicy(role ir.AccountRole, name string) (serializers.StringNormalization, int) {
	policy, maximum := serializers.StringExact, 0
	if name == role.Login {
		maximum = auth.MaximumUsernameBytes
		if role.WriteLoginNormalization == ir.AccountUnicode16NFKC {
			policy = serializers.StringUnicode16NFKC
		}
	}
	if name == role.Email {
		if maximum == 0 {
			maximum = 4096
		}
		if role.WriteEmailNormalization == ir.AccountUnicode16EmailDomain {
			if policy == serializers.StringUnicode16NFKC {
				policy = serializers.StringUnicode16NFKCEmailDomain
			} else {
				policy = serializers.StringUnicode16EmailDomain
			}
		}
	}
	return policy, maximum
}
