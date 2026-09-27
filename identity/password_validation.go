package identity

import (
	"context"
	"errors"

	"github.com/progresshans/godj/validation"
)

// PasswordValidator performs pure, concurrency-safe validation. It must not
// retain a password or profile, perform I/O, or acquire this manager's database
// domain. Errors belong to "password" or NonField and never include the value.
type PasswordValidator interface {
	ValidatePassword(string, Profile) validation.Errors
}

type PasswordValidatorFunc func(string, Profile) validation.Errors

func (f PasswordValidatorFunc) ValidatePassword(password string, profile Profile) validation.Errors {
	return f(password, profile)
}

type ManagerOption interface{ applyManager(*managerState) error }
type managerOption func(*managerState) error

func (option managerOption) applyManager(state *managerState) error { return option(state) }

// WithPasswordValidators selects a form/host password policy. Like Django's
// default AUTH_PASSWORD_VALIDATORS, an omitted policy performs no strength
// validation. It does not change manager normalization or hasher constraints.
func WithPasswordValidators(validators ...PasswordValidator) ManagerOption {
	owned := append([]PasswordValidator(nil), validators...)
	return managerOption(func(state *managerState) error {
		for _, validator := range owned {
			if nilIdentityValue(validator) {
				return managementError(CodeInvalidConfig, "password", nil)
			}
		}
		state.passwordValidators = append(state.passwordValidators, owned...)
		return nil
	})
}

func (manager *Manager) validatePassword(ctx context.Context, password string, profile Profile) error {
	var groups []validation.Errors
	for _, validator := range manager.state.passwordValidators {
		input := profile
		if input.LastLogin != nil {
			instant := *input.LastLogin
			input.LastLogin = &instant
		}
		failures := validator.ValidatePassword(password, input)
		for _, item := range failures.All() {
			if item.Field() != "password" && item.Field() != validation.NonField {
				return managementError(CodeInvalidConfig, "password", nil)
			}
		}
		groups = append(groups, failures)
	}
	failures := validation.Join(groups...)
	if err := ctx.Err(); err != nil {
		var rejected error
		if !failures.Empty() {
			rejected = validation.Reject(failures, nil)
		}
		return managementError(CodeInvalidInput, "context", errors.Join(err, rejected))
	}
	if !failures.Empty() {
		return validation.Reject(failures, nil)
	}
	return nil
}
