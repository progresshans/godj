package accountstoretest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

func creationPreflightAndRecheck(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	actor := profileActor(t, f).Principal()
	hasher := &creationHasher{PasswordHasher: f.hasher}
	for _, mode := range []string{"duplicate_login", "duplicate_principal", "case_insensitive", "missing_group", "missing_permission", "model_policy", "password_policy", "password_policy_failure", "additional_grant"} {
		t.Run(mode, func(t *testing.T) {
			config := creationConfig()
			login := "preflight-" + mode
			input := creationInput(t, login)
			switch mode {
			case "duplicate_login":
				input = creationInput(t, f.user.Username)
			case "duplicate_principal":
				config.PrincipalID = func(context.Context) (string, error) { return f.user.PrincipalID, nil }
			case "case_insensitive":
				input = creationInput(t, "ALICE")
				config.CaseInsensitiveLogin = true
			case "missing_group":
				input = roleInput(t, map[string]query.Value{"username": query.String(login)}, map[string][]int64{"groups": {mathMaxKey}})
			case "missing_permission":
				input = roleInput(t, map[string]query.Value{"username": query.String(login)}, map[string][]int64{"permissions": {mathMaxKey}})
			case "model_policy":
				config.Validate = func(context.Context, db.Queryer, models.User, accountmodel.RoleMemberships) (validation.Errors, error) {
					return validation.NewErrors(validation.New("first_name", "policy")), nil
				}
			case "password_policy":
				config.ValidatePassword = func(context.Context, string, models.User) (validation.Errors, error) {
					return validation.NewErrors(validation.New("password", "policy")), nil
				}
			case "password_policy_failure":
				config.ValidatePassword = func(context.Context, string, models.User) (validation.Errors, error) {
					return validation.NewErrors(validation.New("password", "policy")), errors.New("private strength failure")
				}
			case "additional_grant":
				config.AdditionalPermissions = []auth.Permission{"account.absent"}
			}
			creator := accountCreator(t, backend, hasher, config)
			_, err := creator.Prepare(t.Context(), actor, input, password)
			if err == nil || hasher.calls.Load() != 0 {
				t.Fatal("invalid creation reached hash", mode, err)
			}
			_, rejected := validation.Rejected(err)
			if mode == "additional_grant" {
				requireProfileCode(t, err, accountmodel.CodePermission)
			} else if mode == "password_policy_failure" {
				if rejected {
					t.Fatal("private failure became validation")
				}
				requireProfileCode(t, err, accountmodel.CodePersistence)
			} else if !rejected {
				t.Fatal("input rejection lost", mode, err)
			}
		})
	}
	creator := accountCreator(t, backend, hasher, creationConfig())
	pending, err := creator.Prepare(t.Context(), actor, creationInput(t, "revoked-before-commit"), password)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := models.UserPermissionsLinkObjects.Delete(t.Context(), backend, &f.direct); err != nil {
		t.Fatal(err)
	}
	if result, err := creator.Commit(t.Context(), actor, pending); result.Revision != 0 {
		t.Fatal("revoked actor published creation")
	} else {
		requireProfileCode(t, err, accountmodel.CodePermission)
	}
	if creationExists(t, reopen(t), "revoked-before-commit") {
		t.Fatal("revoked actor created a row")
	}
	if _, err := creator.Commit(t.Context(), actor, pending); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeConflict}) {
		t.Fatal("revoked attempt was reusable", err)
	}
	f.direct, err = models.UserPermissionsLinkObjects.Create(t.Context(), backend, models.NewUserPermissionsLinkCreate(f.user.ID, f.grants[0].ID))
	if err != nil {
		t.Fatal(err)
	}
	config := creationConfig()
	reject := false
	config.ValidatePassword = func(context.Context, string, models.User) (validation.Errors, error) {
		if reject {
			return validation.NewErrors(validation.New("password", "changed_policy")), nil
		}
		return validation.Errors{}, nil
	}
	creator = accountCreator(t, backend, hasher, config)
	pending, err = creator.Prepare(t.Context(), actor, creationInput(t, "policy-before-commit"), password)
	if err != nil {
		t.Fatal(err)
	}
	reject = true
	if result, err := creator.Commit(t.Context(), actor, pending); result.Revision != 0 {
		t.Fatal("changed password policy published creation")
	} else if diagnostics, rejected := validation.Rejected(err); !rejected || diagnostics.ByField("password").Empty() {
		t.Fatal("current password policy not rechecked", err)
	}
	if creationExists(t, reopen(t), "policy-before-commit") {
		t.Fatal("failed password policy left row")
	}
	creator = accountCreator(t, backend, hasher, creationConfig())
	pending, err = creator.Prepare(t.Context(), actor, creationInput(t, "taken-after-preflight"), password)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := models.UserObjects.Create(t.Context(), backend, models.NewUserCreate("other-writer", "taken-after-preflight", f.user.EncodedPassword, joined)); err != nil {
		t.Fatal(err)
	}
	if result, err := creator.Commit(t.Context(), actor, pending); result.Revision != 0 {
		t.Fatal("duplicate final creation published")
	} else if diagnostics, rejected := validation.Rejected(err); !rejected || diagnostics.ByField("username").Empty() {
		t.Fatal("final unique check missing", err)
	}
	// A group can disappear without changing the actor's authority.
	group, err := models.GroupObjects.Create(t.Context(), backend, models.NewGroupCreate("temporary-creation-choice"))
	if err != nil {
		t.Fatal(err)
	}
	pending, err = creator.Prepare(t.Context(), actor, roleInput(t, map[string]query.Value{"username": query.String("missing-final-choice")}, map[string][]int64{"groups": {group.ID}}), password)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := models.GroupObjects.Delete(t.Context(), backend, &group); err != nil {
		t.Fatal(err)
	}
	if result, err := creator.Commit(t.Context(), actor, pending); result.Revision != 0 {
		t.Fatal("missing choice creation published")
	} else if diagnostics, rejected := validation.Rejected(err); !rejected || diagnostics.ByField("groups").Empty() {
		t.Fatal("final choices not rechecked", err)
	}
	if creationExists(t, backend, "missing-final-choice") {
		t.Fatal("failed final choice left row")
	}
	if hasher.calls.Load() != 4 {
		t.Fatal("final refusal performed extra hashing", hasher.calls.Load())
	}
}

type creationEncodedHasher struct {
	auth.PasswordHasher
	encoded               string
	failure, errorEncoded error
	cancel                context.CancelFunc
	calls                 int
}

func (hasher *creationEncodedHasher) Hash(context.Context, string) (string, error) {
	hasher.calls++
	if hasher.cancel != nil {
		hasher.cancel()
	}
	return hasher.encoded, hasher.failure
}
func (hasher *creationEncodedHasher) ValidateEncoded(string) error { return hasher.errorEncoded }

func creationHasherFailures(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	actor := profileActor(t, f).Principal()
	fault := errors.New("private-hash-detail")
	for _, mode := range []string{"direct_input", "wrapped_input", "entropy", "invalid_encoded", "empty_encoded", "unusable_encoded", "oversized_encoded", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			hasher := &creationEncodedHasher{PasswordHasher: f.hasher, encoded: f.user.EncodedPassword}
			invalid := &auth.Error{Code: auth.CodeInvalidInput, Field: "password"}
			switch mode {
			case "direct_input":
				hasher.failure = invalid
			case "wrapped_input":
				hasher.failure = fmt.Errorf("%w: %w", invalid, fault)
			case "entropy":
				hasher.failure = &auth.Error{Code: auth.CodeEntropy, Cause: fault}
			case "invalid_encoded":
				hasher.errorEncoded = fault
			case "empty_encoded":
				hasher.encoded = ""
			case "unusable_encoded":
				hasher.encoded = "!reserved"
			case "oversized_encoded":
				hasher.encoded = strings.Repeat("h", 2049)
			case "cancel":
				hasher.cancel = cancel
				hasher.failure = fault
			}
			creator := accountCreator(t, backend, hasher, creationConfig())
			_, err := creator.Prepare(ctx, actor, creationInput(t, "hash-"+mode), password)
			if err == nil || hasher.calls != 1 || creationExists(t, backend, "hash-"+mode) {
				t.Fatal("hash failure wrote or retried", err)
			}
			_, rejected := validation.Rejected(err)
			if (mode == "direct_input") != rejected {
				t.Fatal("hasher failure classification differs", mode, err)
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("hash cancellation lost", err)
			}
			for _, text := range []string{fmt.Sprintf("%v", err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
				if strings.Contains(text, fault.Error()) || strings.Contains(text, password) || strings.Contains(text, hasher.encoded) && hasher.encoded != "" {
					t.Fatal("hasher diagnostic leaked private material")
				}
			}
		})
	}
}
