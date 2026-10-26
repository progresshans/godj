package model

import (
	"errors"
	"testing"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

func TestPasswordDiagnosticOriginSurvivesOnlyConfirmedCompletion(t *testing.T) {
	shared := passwordPolicyDiagnostics(validation.NewErrors(validation.New("password", "weak")))
	if errors.Unwrap(shared) != nil {
		t.Fatal("shared self-service password diagnostics acquired transport-specific provenance")
	}
	private := validation.Reject(validation.NewErrors(validation.New("password", "weak")), errors.New("private validation failure"))
	if credentialPasswordRejection(private) != private {
		t.Fatal("private validation cause became a credential rejection")
	}
	password := credentialPasswordRejection(passwordPolicyDiagnostics(validation.NewErrors(validation.New("password", "weak"), validation.New(validation.NonField, "policy"))))
	model := validation.Reject(validation.NewErrors(validation.New("password", "model_choice")), nil)
	if failures, ok := PasswordInputDiagnostics(password); !ok || failures.Len() != 2 || failures.ByField("password").Len() != 1 {
		t.Fatal("password origin lost")
	}
	if _, ok := PasswordInputDiagnostics(model); ok {
		t.Fatal("model field became a credential command")
	}
	for _, test := range []struct {
		name              string
		failure, callback error
		password          bool
		code              ErrorCode
	}{
		{"password", password, password, true, ""},
		{"model", model, model, false, ""},
		{"wrapped", errors.Join(password), password, false, CodePersistence},
		{"cleanup", errors.Join(password, errors.New("private cleanup")), password, false, CodePersistence},
		{"unknown", errors.Join(password, &query.Error{Code: query.CodeCommitOutcomeUnknown}), password, false, CodeOutcomeUnknown},
		{"not_callback", password, model, false, CodePersistence},
	} {
		t.Run(test.name, func(t *testing.T) {
			value, err := managementCompletion(7, test.failure, test.callback)
			if value != 0 {
				t.Fatal("failure published a result")
			}
			_, marked := PasswordInputDiagnostics(err)
			if marked != test.password {
				t.Fatal("password origin classified a failure", err)
			}
			if test.code == "" {
				if err != test.failure {
					t.Fatal("confirmed rejection identity changed")
				}
			} else if failure, ok := err.(*Error); !ok || failure.Code != test.code {
				t.Fatal("execution failure became validation", err)
			}
		})
	}
	for _, code := range []validation.Code{"required", "max_length", "invalid"} {
		if failures, ok := PasswordInputDiagnostics(passwordInputError(code)); !ok || failures.ByField("password").Len() != 1 {
			t.Fatal("password input boundary lost origin")
		}
	}
	if err := passwordPolicyDiagnostics(validation.NewErrors(validation.New("profile", "invalid"))); !errors.Is(err, &Error{Code: CodeInvalidConfig}) {
		t.Fatal("policy named an unrelated field", err)
	}
	if err := passwordPolicyDiagnostics(validation.Errors{}); err != nil {
		t.Fatal(err)
	}
}
