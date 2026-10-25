package accountstoretest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/schema/ir"
	sessiondb "github.com/progresshans/godj/sessions/database"
	"github.com/progresshans/godj/validation"
)

func managementConfig() accountmodel.AccountManagementConfig[models.User] {
	fields := creationConfig().Fields
	return accountmodel.AccountManagementConfig[models.User]{ReadFields: fields, CreateFields: fields, EditFields: fields,
		Permissions:           accountmodel.AccountPermissions{View: "account.view", Add: "account.direct", Change: "account.team", Delete: "account.delete", Additional: []auth.Permission{"account.shared"}},
		GroupPermissions:      accountmodel.DeletionPermissions{Delete: "catalog.delete_group", Change: "catalog.change_group"},
		PermissionPermissions: accountmodel.DeletionPermissions{Delete: "catalog.delete_permission", Change: "catalog.change_permission"},
	}
}

func accountManagement(t *testing.T, backend sessiondb.Backend, hasher auth.PasswordHasher, config accountmodel.AccountManagementConfig[models.User]) *accountmodel.AccountManagement[models.User] {
	t.Helper()
	sessions := catalogSessions(t, backend)
	verifier, err := sessions.Authenticator(t.Context(), hasher)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := project.NewAccountManagement(sessions, verifier, auth.PrincipalAuthorizer{}, config)
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

func managementBoundForm(t *testing.T, owner *accountmodel.AccountManagement[models.User], data map[string][]string, current *models.User, groupKeys, permissionKeys []int64) formmodel.BoundForm {
	t.Helper()
	action := admin.ActionAdd
	if current != nil {
		action = admin.ActionChange
	}
	definition, err := owner.FormDefinition(action)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := definition.Spec(models.UserDescriptor{}.Metadata())
	if err != nil {
		t.Fatal(err)
	}
	for name, keys := range map[string][]int64{"groups": groupKeys, "permissions": permissionKeys} {
		choices := make([]forms.Choice, len(keys))
		for index, key := range keys {
			choices[index] = forms.Choice{Value: forms.Integer(key), Label: "Selected"}
		}
		spec, err = spec.WithModelChoices(name, choices...)
		if err != nil {
			t.Fatal(err)
		}
	}
	bound, err := formmodel.BindInstance(t.Context(), models.UserObjects, spec, forms.NewData(data), current, formmodel.PostClean{})
	if err != nil {
		t.Fatal(err)
	}
	return bound.BoundForm()
}

func managementFormChecks(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	actor := profileActor(t, f).Principal()
	calls := 0
	config := managementConfig()
	config.PrincipalID = func(context.Context) (string, error) { calls++; return "unexpected", nil }
	config.ValidateCreate = func(context.Context, db.Queryer, models.User, accountmodel.RoleMemberships) (validation.Errors, error) {
		calls++
		return validation.Errors{}, nil
	}
	config.ValidateEdit = func(context.Context, db.Queryer, models.User, models.User, accountmodel.RoleMemberships) (validation.Errors, error) {
		calls++
		return validation.Errors{}, nil
	}
	config.ValidatePassword = func(context.Context, string, models.User) (validation.Errors, error) {
		calls++
		return validation.Errors{}, nil
	}
	hasher := &creationHasher{PasswordHasher: f.hasher}
	owner := accountManagement(t, backend, hasher, config)
	beforeHash := hasher.calls.Load()
	target := roleTarget(t, backend, f, "form-target")
	before, err := models.UserObjects.Using(backend).OrderBy(models.UserFields.ID.Asc()).All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"partial_unique", "normalized_unique", "case_insensitive_unique", "current_excluded", "stale", "missing", "both_invalid_choices", "create_requires_change", "additional_permission", "revoked", "inactive", "old_domain"} {
		t.Run(mode, func(t *testing.T) {
			selected, acting := owner, actor
			data := map[string][]string{"username": {f.user.Username}, "email": {"not-an-email"}, "first_name": {"Form"}}
			if mode == "normalized_unique" {
				data["username"] = []string{"ａｌｉｃｅ"}
			}
			if mode == "case_insensitive_unique" {
				copy := config
				copy.CaseInsensitiveCreationLogin = true
				selected = accountManagement(t, backend, f.hasher, copy)
				data["username"] = []string{strings.ToUpper(f.user.Username)}
			}
			if mode == "create_requires_change" || mode == "additional_permission" {
				copy := config
				if mode == "create_requires_change" {
					copy.Permissions.Change = "account.unheld"
				} else {
					copy.Permissions.Additional = []auth.Permission{"account.unheld"}
				}
				selected = accountManagement(t, backend, f.hasher, copy)
			}
			if mode == "old_domain" {
				namespace, err := f.binding.CredentialDomain([32]byte{9})
				if err != nil {
					t.Fatal(err)
				}
				principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: actor.ID(), Active: true, Permissions: actor.Permissions()})
				if err != nil {
					t.Fatal(err)
				}
				foreign, err := auth.NewCredentialForDomain(namespace, actor.ID(), f.user.EncodedPassword, principal)
				if err != nil {
					t.Fatal(err)
				}
				acting = foreign.Principal()
			}
			if mode == "revoked" {
				if count, err := models.UserPermissionsLinkObjects.Delete(t.Context(), backend, &f.direct); err != nil || count != 1 {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					var err error
					f.direct, err = models.UserPermissionsLinkObjects.Create(context.Background(), backend, models.NewUserPermissionsLinkCreate(f.user.ID, f.grants[0].ID))
					if err != nil {
						t.Error(err)
					}
				})
			}
			if mode == "inactive" {
				inactive := f.user
				inactive.Active = false
				if err := models.UserObjects.Save(t.Context(), backend, &inactive); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := models.UserObjects.Save(context.Background(), backend, &f.user); err != nil {
						t.Error(err)
					}
				})
			}
			var err error
			switch mode {
			case "current_excluded", "stale", "missing":
				row := target
				if mode == "stale" {
					row.Revision++
				}
				if mode == "missing" {
					row.ID = mathMaxKey
				}
				data["username"], data["email"] = []string{target.Username}, []string{"person@example.test"}
				bound := managementBoundForm(t, selected, data, &row, nil, nil)
				err = selected.CheckChangeForm(t.Context(), acting, row.ID, row.Revision, bound)
				if mode == "current_excluded" {
					if err != nil {
						t.Fatal("included current unique value", err)
					}
				} else {
					code := accountmodel.CodeConflict
					if mode == "missing" {
						code = accountmodel.CodeNotFound
					}
					requireProfileCode(t, err, code)
				}
			case "both_invalid_choices":
				data["groups"], data["permissions"] = []string{strconv.FormatInt(mathMaxKey, 10)}, []string{strconv.FormatInt(mathMaxKey, 10)}
				bound := managementBoundForm(t, selected, data, nil, []int64{mathMaxKey}, []int64{mathMaxKey})
				err = selected.CheckCreateForm(t.Context(), acting, bound)
				failures, rejected := validation.Rejected(err)
				if !rejected || failures.ByField("username").Empty() || failures.ByField("groups").Empty() || failures.ByField("permissions").Empty() {
					t.Fatal("partial input suppressed independent checks", err)
				}
			default:
				bound := managementBoundForm(t, selected, data, nil, nil, nil)
				if bound.Form().Errors().ByField("email").Empty() {
					t.Fatal("partial-invalid fixture is valid")
				}
				err = selected.CheckCreateForm(t.Context(), acting, bound)
				if mode == "partial_unique" || mode == "normalized_unique" || mode == "case_insensitive_unique" {
					failures, rejected := validation.Rejected(err)
					if !rejected || failures.ByField("username").Empty() || !failures.ByField("email").Empty() {
						t.Fatal("valid login uniqueness skipped or existing errors repeated", err)
					}
				} else {
					requireProfileCode(t, err, accountmodel.CodePermission)
				}
			}
		})
	}
	after, err := models.UserObjects.Using(reopen(t)).OrderBy(models.UserFields.ID.Asc()).All(t.Context())
	if err != nil || !reflect.DeepEqual(before, after) || len(profileHistory(t, backend, actor, target.ID)) != 0 || calls != 0 || hasher.calls.Load() != beforeHash {
		t.Fatal("advisory check mutated rows, audit, password or callbacks", err)
	}
	// A successful advisory observation cannot reserve uniqueness or replace
	// the creator's final validation after another connection commits.
	bound := managementBoundForm(t, owner, map[string][]string{"username": {"form-race"}, "email": {"person@example.test"}}, nil, nil, nil)
	if err := owner.CheckCreateForm(t.Context(), actor, bound); err != nil {
		t.Fatal(err)
	}
	roleTarget(t, reopen(t), f, "form-race")
	input, err := formmodel.PrepareInput(models.UserObjects, bound)
	if err != nil {
		t.Fatal(err)
	}
	_, err = owner.Creator().Prepare(t.Context(), actor, input, password)
	if failures, rejected := validation.Rejected(err); !rejected || failures.ByField("username").Empty() || hasher.calls.Load() != beforeHash {
		t.Fatal("advisory check bypassed later uniqueness or hashed duplicate", err)
	}
}

func managementFormFailures(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	actor := profileActor(t, f).Principal()
	for _, mode := range []string{"skipped", "nil", "root", "repeated", "query", "close", "swallowed", "cleanup", "denial_cleanup", "conflict_cleanup", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			fault := errors.New("private account form snapshot details")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			backendOwner := &catalogReadBackend{Backend: backend}
			backendOwner.read = func(ctx context.Context, observe func(db.Queryer) error) error {
				switch mode {
				case "skipped":
					return nil
				case "nil":
					return observe(nil)
				case "root":
					return observe(backend)
				}
				err := backend.ReadSnapshot(ctx, func(scope db.Queryer) error {
					probe := &catalogReadProbe{Queryer: scope, validator: scope.(db.SessionValidator), table: "godj_identity_user", skip: 1}
					if mode == "query" || mode == "close" || mode == "swallowed" {
						probe.fault, probe.closeFail = fault, mode == "close"
					}
					err := observe(probe)
					if mode == "swallowed" {
						return nil
					}
					if mode == "repeated" {
						return errors.Join(err, observe(probe))
					}
					return err
				})
				if strings.HasSuffix(mode, "cleanup") {
					return errors.Join(err, fault)
				}
				if mode == "cancel" {
					cancel()
				}
				return err
			}
			config := managementConfig()
			if mode == "denial_cleanup" {
				config.Permissions.Add = "account.denied"
			}
			owner := accountManagement(t, backendOwner, f.hasher, config)
			var current *models.User
			if mode == "conflict_cleanup" {
				row := f.user
				row.Revision++
				current = &row
			}
			bound := managementBoundForm(t, owner, map[string][]string{"username": {f.user.Username}, "email": {"invalid"}}, current, nil, nil)
			var err error
			if current == nil {
				err = owner.CheckCreateForm(ctx, actor, bound)
			} else {
				err = owner.CheckChangeForm(ctx, actor, current.ID, current.Revision, bound)
			}
			requireProfileCode(t, err, accountmodel.CodePersistence)
			if _, rejected := validation.Rejected(err); rejected || strings.Contains(fmt.Sprintf("%+v", err), "private") {
				t.Fatal("failed snapshot published diagnostics or cause")
			}
			if mode == "query" || mode == "close" || mode == "swallowed" || strings.HasSuffix(mode, "cleanup") {
				if !errors.Is(err, fault) {
					t.Fatal("snapshot lost cause", err)
				}
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("snapshot lost cancellation", err)
			}
		})
	}
}

func managementFormNonpositiveKeys(t *testing.T, backend Backend, storedKey StoredKeyFixture) {
	f := provision(t, backend, ir.AccountExact)
	owner, actor := accountManagement(t, backend, f.hasher, managementConfig()), profileActor(t, f).Principal()
	for _, id := range []int64{0, -19} {
		t.Run(strconv.FormatInt(id, 10), func(t *testing.T) {
			row := roleTarget(t, backend, f, fmt.Sprintf("form-signed%d", -id))
			storedKey(t, backend, "user", row.ID, id)
			row.ID = id
			bound := managementBoundForm(t, owner, map[string][]string{"username": {row.Username}, "email": {"person@example.test"}}, &row, nil, nil)
			if err := owner.CheckChangeForm(t.Context(), actor, id, row.Revision, bound); err != nil {
				t.Fatal("existing signed key treated as creation", err)
			}
			requireProfileCode(t, owner.CheckCreateForm(t.Context(), actor, bound), accountmodel.CodeInvalidInput)
		})
	}
}
