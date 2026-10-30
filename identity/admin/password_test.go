package identityadmin

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	sessiondb "github.com/progresshans/godj/sessions/database"
	"github.com/progresshans/godj/validation"
)

func TestSelectedAccountPasswordCommandsPreservePrivateInputsAndPolicy(t *testing.T) {
	for _, owner := range []*accountmodel.PasswordEditor[models.User]{nil, {}} {
		if commands, err := PasswordCommands(owner); err == nil || commands != nil {
			t.Fatal("invalid owner admitted")
		}
	}
	store, err := project.NewAccountSessionStore(configOnlyBackend{}, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := store.Authenticator(t.Context(), hasher)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := project.NewAccountPasswordEditor(store, verifier, auth.PrincipalAuthorizer{}, accountmodel.PasswordEditConfig[models.User]{ChangePermission: "account.change", AdditionalPermissions: []auth.Permission{"account.password"}, MaxPasswordBytes: 20})
	if err != nil {
		t.Fatal(err)
	}
	commands, err := PasswordCommands(owner)
	if err != nil || len(commands) != 2 {
		t.Fatal("commands missing", err)
	}
	for _, command := range commands {
		if command.Permission != "account.change" || !reflect.DeepEqual(command.AdditionalPermissions, []auth.Permission{"account.password"}) {
			t.Fatal("command omitted owner authority")
		}
	}
	commands[0].AdditionalPermissions[0] = "account.forged"
	if commands[1].AdditionalPermissions[0] != "account.password" || owner.Permissions()[1] != "account.password" {
		t.Fatal("command metadata aliases another command/owner")
	}
	for _, field := range commands[0].Form.Fields() {
		if field.Widget() != forms.PasswordInput {
			t.Fatal("password input became public text")
		}
	}
	for _, test := range []struct {
		name, one, two string
		valid          bool
	}{
		{"bytes_preserved", "  λ secret  ", "  λ secret  ", true},
		{"required", "", "", false}, {"confirmation", "secret", "different", false},
		{"byte_limit", strings.Repeat("界", 7), strings.Repeat("界", 7), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			form, err := commands[0].Form.Bind(t.Context(), forms.NewData(map[string][]string{"password1": {test.one}, "password2": {test.two}}), nil)
			if err != nil || form.Valid() != test.valid {
				t.Fatal("selected password form policy differs", err)
			}
			if test.valid {
				if value, _ := form.Cleaned().String("password1"); value != test.one {
					t.Fatal("password whitespace changed")
				}
			}
		})
	}
	form, err := commands[1].Form.Bind(t.Context(), forms.NewData(nil), nil)
	if err != nil || form.Valid() {
		t.Fatal("disable did not require explicit confirmation", err)
	}
}

func TestSelectedCreationPreservesARealPasswordProfileDiagnostic(t *testing.T) {
	model := validation.Reject(validation.NewErrors(validation.New("password", "profile_policy")), nil)
	if result := selectedPasswordInputError(model, "password2_command"); result != model {
		t.Fatal("model password field was reassigned to a credential command")
	}
	wrapped := errors.Join(model, errors.New("private cleanup"))
	if result := selectedPasswordInputError(wrapped, "password2_command"); result != wrapped {
		t.Fatal("wrapped failure became input validation")
	}
}

func TestSelectedAccountPasswordErrorMappingKeepsOuterFailure(t *testing.T) {
	for _, test := range []struct {
		source accountmodel.ErrorCode
		target admin.OperationErrorCode
	}{
		{accountmodel.CodePermission, admin.OperationDenied}, {accountmodel.CodeConflict, admin.OperationConflict}, {accountmodel.CodeOutcomeUnknown, admin.OperationOutcomeUnknown},
	} {
		original := &accountmodel.Error{Code: test.source}
		mapped, ok := operationError(original).(*admin.OperationError)
		if !ok || mapped.Code != test.target || !errors.Is(mapped, original) {
			t.Fatal("selected operation classification lost")
		}
		joined := errors.Join(original, errors.New("cleanup failed"))
		if operationError(joined) != joined {
			t.Fatal("outer cleanup failure hidden")
		}
	}
	if operationError(&accountmodel.Error{Code: accountmodel.CodeNotFound}) != admin.ErrObjectNotFound {
		t.Fatal("selected absence not mapped")
	}
	pure := validation.Reject(validation.NewErrors(validation.New("password", "weak")), nil)
	if diagnostics, rejected := validation.Rejected(passwordError(pure)); !rejected || diagnostics.ByField("password2").Len() != 1 || !diagnostics.ByField("password").Empty() {
		t.Fatal("typed password policy not mapped to confirmation")
	}
	failed := errors.Join(pure, errors.New("rollback failed"))
	if passwordError(failed) != failed {
		t.Fatal("failed rollback became a password rejection")
	}
}
