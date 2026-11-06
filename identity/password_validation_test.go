package identity

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/validation"
)

type passwordConfigurationBackend struct{ ManagementBackend }

func TestPasswordPolicyOwnsOptionsAndProfileAndRejectsInvalidDiagnostics(t *testing.T) {
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10000})
	if err != nil {
		t.Fatal(err)
	}
	for _, option := range []ManagerOption{nil, WithPasswordValidators(nil), WithPasswordValidators(PasswordValidatorFunc(nil))} {
		if manager, err := NewManager(passwordConfigurationBackend{}, hasher, auth.PrincipalAuthorizer{}, option); err == nil || manager != nil {
			t.Fatal("invalid option published a manager")
		}
	}
	instant := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	profile := Profile{Username: "User", LastLogin: &instant}
	validators := []PasswordValidator{
		PasswordValidatorFunc(func(_ string, input Profile) validation.Errors {
			*input.LastLogin = input.LastLogin.Add(time.Hour)
			return validation.Errors{}
		}),
		PasswordValidatorFunc(func(_ string, input Profile) validation.Errors {
			if !input.LastLogin.Equal(instant) {
				t.Error("earlier validator mutated another validator's profile")
			}
			return validation.NewErrors(validation.New("password", "policy_rejected"))
		}),
	}
	option := WithPasswordValidators(validators...)
	validators[1] = PasswordValidatorFunc(func(string, Profile) validation.Errors { return validation.Errors{} })
	manager, err := NewManager(passwordConfigurationBackend{}, hasher, auth.PrincipalAuthorizer{}, option)
	if err != nil {
		t.Fatal(err)
	}
	if failures, rejected := validation.Rejected(manager.validatePassword(t.Context(), "private", profile)); !rejected || failures.Len() != 1 {
		t.Fatal("policy options aliased caller's slice")
	}
	if !profile.LastLogin.Equal(instant) {
		t.Fatal("validator mutated caller profile")
	}
	manager, err = NewManager(passwordConfigurationBackend{}, hasher, auth.PrincipalAuthorizer{}, WithPasswordValidators(PasswordValidatorFunc(func(string, Profile) validation.Errors {
		return validation.NewErrors(validation.New("username", "invalid"))
	})))
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.validatePassword(t.Context(), "private", profile); !errors.Is(err, &Error{Code: CodeInvalidConfig}) {
		t.Fatal("password policy injected unrelated field diagnostics", err)
	}
	manager, err = NewManager(passwordConfigurationBackend{}, hasher, auth.PrincipalAuthorizer{})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.validatePassword(t.Context(), "1", profile); err != nil {
		t.Fatal("default added a strength policy", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := manager.validatePassword(ctx, "1", profile); !errors.Is(err, context.Canceled) {
		t.Fatal("policy lost cancellation", err)
	}
}
