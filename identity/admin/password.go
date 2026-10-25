package identityadmin

import (
	"context"
	"slices"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/identity/internal/forminput"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/validation"
)

// PasswordCommands composes the selected account owner with common Admin's
// conditional object commands. Register them on that account's model. The
// owner rechecks current authority and the target in its own transaction;
// command metadata controls presentation and admission, not persistence.
func PasswordCommands[M any](editor *accountmodel.PasswordEditor[M]) ([]admin.CommandConfig, error) {
	required, maximum := editor.Permissions(), editor.MaxPasswordBytes()
	if len(required) == 0 || maximum < 1 {
		return nil, &admin.ConfigError{Path: "identity.password", Code: "invalid_owner"}
	}
	fields, err := forminput.PasswordFields(true)
	if err != nil {
		return nil, err
	}
	limit := forms.CrossValidatorFunc(func(values forms.Values) validation.Errors {
		var failures []validation.Violation
		for _, name := range []string{"password1", "password2"} {
			if raw, present := values.String(name); present && len(raw) > maximum {
				failures = append(failures, validation.New(validation.Field(name), "max_length"))
			}
		}
		return validation.NewErrors(failures...)
	})
	password, err := forms.NewSpec(fields, forminput.PasswordConfirmation(), limit)
	if err != nil {
		return nil, err
	}
	confirm, err := forms.BooleanField("confirm", forms.WithRequired(true), forms.WithLabel("Disable password login and revoke this account's sessions"))
	if err != nil {
		return nil, err
	}
	disable, err := forms.NewSpec([]forms.Field{confirm})
	if err != nil {
		return nil, err
	}
	return []admin.CommandConfig{{
		Name: "password", Label: "Change password", Permission: required[0], AdditionalPermissions: slices.Clone(required[1:]), Form: password,
		Run: func(ctx context.Context, actor auth.Principal, mutation admin.Mutation, values forms.Values) (admin.CommandResult, error) {
			raw, present := values.String("password1")
			confirmation, confirmed := values.String("password2")
			if !present || !confirmed || raw != confirmation {
				return admin.CommandResult{}, invalidInput()
			}
			result, err := editor.SetPassword(ctx, actor, mutation.ID, mutation.Revision, raw)
			if err != nil {
				return admin.CommandResult{}, passwordError(err)
			}
			return admin.CommandResult{ID: result.ID, Revision: result.Revision}, nil
		},
	}, {
		Name: "disable-password", Label: "Disable password login", Permission: required[0], AdditionalPermissions: slices.Clone(required[1:]), Form: disable,
		Run: func(ctx context.Context, actor auth.Principal, mutation admin.Mutation, values forms.Values) (admin.CommandResult, error) {
			confirmed, present := values.Boolean("confirm")
			if !present || !confirmed {
				return admin.CommandResult{}, invalidInput()
			}
			result, err := editor.SetUnusablePassword(ctx, actor, mutation.ID, mutation.Revision)
			if err != nil {
				return admin.CommandResult{}, operationError(err)
			}
			return admin.CommandResult{ID: result.ID, Revision: result.Revision}, nil
		},
	}}, nil
}
