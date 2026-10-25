package model_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	hostmodels "github.com/progresshans/godj/conformance/identityfixture/models"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/modeldef"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/schema/ir"
	sessiondb "github.com/progresshans/godj/sessions/database"
	"github.com/progresshans/godj/validation"
)

func TestAccountPasswordEditorRequiresExactSourceAndOwnersBeforeIO(t *testing.T) {
	backend := &untouchedProfileBackend{}
	store, err := project.NewAccountSessionStore(backend, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := project.BindAccount()
	if err != nil {
		t.Fatal(err)
	}
	inner, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	hasher := &passwordSetupHasher{PasswordHasher: inner}
	verifier, err := store.Authenticator(t.Context(), hasher)
	if err != nil {
		t.Fatal(err)
	}
	other, err := project.NewAccountSessionStore(backend, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	foreignVerifier, err := other.Authenticator(t.Context(), hasher)
	if err != nil {
		t.Fatal(err)
	}
	foreignHost, err := accountmodel.Bind(modeldef.AccountRole(), models.UserDescriptor{}, models.GoDjRelationSchema(), hostmodels.GoDjRelationSchema())
	if err != nil {
		t.Fatal(err)
	}
	role := modeldef.AccountRole()
	role.LoginFormNormalization = ir.AccountExact
	foreignRole, err := accountmodel.Bind(role, models.UserDescriptor{}, models.GoDjRelationSchema())
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	config := accountmodel.PasswordEditConfig[models.User]{ChangePermission: "account.change", AdditionalPermissions: []auth.Permission{"account.password"},
		Now: func() time.Time { calls++; return time.Now() },
		ValidatePassword: func(context.Context, string, models.User) (validation.Errors, error) {
			calls++
			return validation.Errors{}, nil
		},
	}
	before := hasher.calls
	for _, name := range []string{"nil_store", "zero_store", "nil_verifier", "foreign_verifier", "foreign_host", "foreign_role", "nil_binding", "nil_overlay", "typed_nil_overlay", "no_permission", "bad_permission", "duplicate_permission", "negative_limit", "oversized_limit"} {
		t.Run(name, func(t *testing.T) {
			s, b, v, c := store, binding, verifier, config
			var overlay auth.Authorizer = auth.PrincipalAuthorizer{}
			switch name {
			case "nil_store":
				s = nil
			case "zero_store":
				s = &accountmodel.SessionStore[models.User]{}
			case "nil_verifier":
				v = nil
			case "foreign_verifier":
				v = foreignVerifier
			case "foreign_host":
				b = foreignHost
			case "foreign_role":
				b = foreignRole
			case "nil_binding":
				b = accountmodel.Binding[models.User]{}
			case "nil_overlay":
				overlay = nil
			case "typed_nil_overlay":
				overlay = (*auth.PrincipalAuthorizer)(nil)
			case "no_permission":
				c.ChangePermission = ""
			case "bad_permission":
				c.AdditionalPermissions = []auth.Permission{"bad permission"}
			case "duplicate_permission":
				c.AdditionalPermissions = []auth.Permission{c.ChangePermission}
			case "negative_limit":
				c.MaxPasswordBytes = -1
			case "oversized_limit":
				c.MaxPasswordBytes = 4097
			}
			if owner, err := accountmodel.NewPasswordEditor(s, b, v, overlay, c); owner != nil || !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeInvalidConfig}) {
				t.Fatal("invalid password owner accepted", err)
			}
		})
	}
	owner, err := project.NewAccountPasswordEditor(store, verifier, auth.PrincipalAuthorizer{}, config)
	if err != nil {
		t.Fatal(err)
	}
	config.AdditionalPermissions[0] = "account.forged"
	owner.Permissions()[0] = "account.forged"
	if !reflect.DeepEqual(owner.Permissions(), []auth.Permission{"account.change", "account.password"}) || owner.MaxPasswordBytes() != 1024 || fmt.Sprintf("%#v", owner) != "identity.model.PasswordEditor{redacted}" {
		t.Fatal("owner metadata leaked or aliased")
	}
	actor, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "actor", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, revision := range []int64{-1, 0, math.MaxInt64} {
		if result, err := owner.SetPassword(t.Context(), actor, 0, revision, "secret"); err == nil || result != (accountmodel.PasswordEditResult{}) {
			t.Fatal("invalid revision reached I/O")
		}
	}
	if _, err := owner.SetUnusablePassword(t.Context(), auth.Anonymous(), 0, 1); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodePermission}) {
		t.Fatal("anonymous actor admitted", err)
	}
	if _, err := owner.SetPassword(nil, actor, 0, 1, "secret"); err == nil {
		t.Fatal("nil context admitted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := owner.SetPassword(ctx, actor, 0, 1, "secret"); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
	if backend.calls != 0 || backend.writes != 0 || hasher.calls != before || calls != 0 {
		t.Fatal("composition or invalid entry performed work")
	}
}
