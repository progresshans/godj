package accountstoretest

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/identity"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
)

func runUserManagementReferences(t *testing.T, vendor string, open Open) {
	t.Run("user_input_refusals", func(t *testing.T) { userInputRefusals(t, open) })
	t.Run("user_native_reference", func(t *testing.T) {
		backend, reopen := open(t)
		userReferenceLifecycle(t, vendor, backend, reopen)
	})
	t.Run("user_native_email", func(t *testing.T) {
		backend, reopen := open(t)
		userReferenceEmail(t, vendor, backend, reopen)
	})
	t.Run("user_native_admission", func(t *testing.T) {
		userReferenceAdmission(t, vendor, open)
	})
}

func userReferenceEmail(t *testing.T, vendor string, backend Backend, reopen func(*testing.T) Backend) {
	reference := loadUserReference(t, vendor)
	var expected []string
	if err := json.Unmarshal(reference["email_normalization"], &expected); err != nil || len(expected) != 4 {
		t.Fatal("incomplete native email observations", err)
	}
	f := provisionUserReference(t, backend)
	row := roleTarget(t, backend, f.base, "normalization-target")
	actual := []string{}
	for index, example := range []struct{ name, input string }{
		{"unicode_i", "Upper@İ.EXAMPLE"}, {"final_sigma", "x@ΟΣ"}, {"nonfinal_sigma", "x@ΟΣ.EXAMPLE"}, {"python_whitespace", "\x1cUpper@EXAMPLE.COM\x1f"},
	} {
		if !t.Run(example.name, func(t *testing.T) {
			before := userReferenceSnapshot(t, reopen(t))
			changed, err := f.runtime.Accounts().Editor().Update(t.Context(), f.actor.Principal(), row.ID, row.Revision, profileInput(t, map[string]query.Value{"email": query.String(example.input)}))
			if err != nil || changed.ID != row.ID || changed.Revision != row.Revision+1 || changed.RevokedSessions != 0 || !slices.Equal(changed.ChangedFields, []string{"email"}) {
				t.Fatal("selected email edit envelope", err)
			}
			row.Email, row.Revision = expected[index], row.Revision+1
			userReferenceReplace(&before.account, row)
			assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), f.actor.Principal(), catalogExpectedEvent{"user", row.ID, admin.ActionChange, []string{"email"}})
			actual = append(actual, profileRow(t, backend, row.ID).Email)
		}) {
			return
		}
	}
	reference.assert(t, "email_normalization", actual)
	t.Run("unicode16_domain", func(t *testing.T) {
		before := userReferenceSnapshot(t, reopen(t))
		input := profileInput(t, map[string]query.Value{"email": query.String("Mixed@\u1c89.\ua7cb")})
		changed, err := f.runtime.Accounts().Editor().Update(t.Context(), f.actor.Principal(), row.ID, row.Revision, input)
		if err != nil || changed.ID != row.ID || changed.Revision != row.Revision+1 || changed.RevokedSessions != 0 || !slices.Equal(changed.ChangedFields, []string{"email"}) {
			t.Fatal("selected Unicode 16 email edit", err)
		}
		row.Email, row.Revision = "Mixed@\u1c8a.\u0264", row.Revision+1
		userReferenceReplace(&before.account, row)
		assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), f.actor.Principal(), catalogExpectedEvent{"user", row.ID, admin.ActionChange, []string{"email"}})
		value, present := input.Get("email")
		if !present || !value.Equal(query.String("Mixed@\u1c89.\ua7cb")) {
			t.Fatal("selected email normalization mutated the submitted input")
		}
	})
}

func userReferenceAdmission(t *testing.T, vendor string, open Open) {
	reference := loadUserReference(t, vendor)
	observations := map[string]map[string]bool{}
	for _, action := range []string{"none", "view", "add", "change", "delete", "add_change"} {
		if !t.Run(action, func(t *testing.T) {
			backend, reopen := open(t)
			f := provisionUserReference(t, backend)
			actor := f.actor.Principal()
			if !actor.Superuser() {
				t.Fatal("admission fixture lacks stale superuser authority")
			}
			if _, err := models.UserObjects.Patch(t.Context(), backend, f.base.user, models.UserPatch{}.WithSuperuser(false).WithRevision(3)); err != nil {
				t.Fatal(err)
			}
			if action != "none" {
				for _, granted := range strings.Split(action, "_") {
					permission, err := models.PermissionObjects.Create(t.Context(), backend, models.NewPermissionCreate("godj_identity."+granted+"_user", granted))
					if err != nil {
						t.Fatal(err)
					}
					if _, err := models.UserPermissionsLinkObjects.Create(t.Context(), backend, models.NewUserPermissionsLinkCreate(f.base.user.ID, permission.ID)); err != nil {
						t.Fatal(err)
					}
				}
			}
			current, err := f.runtime.Authenticator().Resolve(t.Context(), actor.ID())
			if err != nil || current.Principal().Superuser() {
				t.Fatal("admission fixture did not remove current superuser", err)
			}
			target := roleTarget(t, backend, f.base, "admission-target")
			management := f.runtime.Accounts()
			unchanged := userReferenceSnapshot(t, reopen(t))
			profile, viewErr := management.Reader().Get(t.Context(), actor, target.ID)
			if viewErr != nil {
				requireProfileCode(t, viewErr, accountmodel.CodePermission)
				if !reflect.DeepEqual(profile, accountmodel.Profile[models.User]{}) {
					t.Fatal("denied read published selected profile")
				}
			} else if profile.ID() != target.ID || profile.Revision() != target.Revision || catalogReferenceText(t, profile.View(), "username") != target.Username {
				t.Fatal("admitted read returned another user")
			}
			assertUserReferenceState(t, unchanged, userReferenceSnapshot(t, reopen(t)), actor)
			page, pageErr := management.Reader().List(t.Context(), actor, 0, 10)
			if pageErr != nil {
				requireProfileCode(t, pageErr, accountmodel.CodePermission)
				if !reflect.DeepEqual(page, accountmodel.ProfilePage[models.User]{}) {
					t.Fatal("denied list published rows")
				}
			} else if page.Total != 2 || len(page.Items) != 2 || page.Items[0].ID() != f.base.user.ID || page.Items[1].ID() != target.ID {
				t.Fatal("admitted list lost exact selected rows")
			}
			if (pageErr == nil) != (viewErr == nil) {
				t.Fatal("selected page/detail authority diverged")
			}
			assertUserReferenceState(t, unchanged, userReferenceSnapshot(t, reopen(t)), actor)
			updated, updateErr := management.Editor().Update(t.Context(), actor, target.ID, 1, profileInput(t, nil))
			if updateErr != nil {
				requireProfileCode(t, updateErr, accountmodel.CodePermission)
				if !reflect.DeepEqual(updated, accountmodel.AccountChange{}) {
					t.Fatal("denied update published metadata")
				}
			} else if updated.ID != target.ID || updated.Revision != 1 || updated.RevokedSessions != 0 || len(updated.ChangedFields) != 0 {
				t.Fatal("admitted no-op changed selected target")
			}
			assertUserReferenceState(t, unchanged, userReferenceSnapshot(t, reopen(t)), actor)
			candidate, createErr := management.Creator().Prepare(t.Context(), actor, profileInput(t, map[string]query.Value{"username": query.String("admission-created")}), userReferencePassword)
			assertUserReferenceState(t, unchanged, userReferenceSnapshot(t, reopen(t)), actor)
			if createErr != nil {
				requireProfileCode(t, createErr, accountmodel.CodePermission)
				if !reflect.DeepEqual(candidate, accountmodel.PreparedCreation[models.User]{}) || f.hasher.hashes.Load() != 0 {
					t.Fatal("denied creation hashed or published a candidate")
				}
			} else {
				created, err := management.Creator().Commit(t.Context(), actor, candidate)
				if err != nil || created.ID <= 0 || created.Revision != 1 || created.RevokedSessions != 0 || !slices.Equal(created.ChangedFields, userReferenceCreationFields) || f.hasher.hashes.Load() != 1 {
					t.Fatal("admitted selected creation failed", err)
				}
				expected := unchanged
				userReferenceReplace(&expected.account, userReferenceExpectedRow(created.ID, "admission-created", "", f.encoded.Load().(string)))
				after := userReferenceSnapshot(t, reopen(t))
				assertUserReferenceState(t, expected, after, actor, catalogExpectedEvent{"user", created.ID, admin.ActionAdd, userReferenceCreationFields})
				unchanged = after
			}
			deleted, deleteErr := management.Deleter().Delete(t.Context(), actor, target.ID, 1)
			if deleteErr != nil {
				requireProfileCode(t, deleteErr, accountmodel.CodePermission)
				if !reflect.DeepEqual(deleted, accountmodel.AccountDeletion{}) {
					t.Fatal("denied deletion published metadata")
				}
				assertUserReferenceState(t, unchanged, userReferenceSnapshot(t, reopen(t)), actor)
			} else {
				if deleted.ID != target.ID || deleted.Revision != 1 || deleted.DeletedRows != 1 || deleted.RevokedSessions != 0 {
					t.Fatal("delete-only authority lost selected deletion")
				}
				unchanged.account.users = slices.DeleteFunc(slices.Clone(unchanged.account.users), func(user models.User) bool { return user.ID == target.ID })
				assertUserReferenceState(t, unchanged, userReferenceSnapshot(t, reopen(t)), actor, catalogExpectedEvent{"user", target.ID, admin.ActionDelete, nil})
			}
			observations[action] = map[string]bool{"view": viewErr == nil, "add": current.Principal().Has(identity.AddUser), "change": updateErr == nil, "delete": deleteErr == nil, "create": createErr == nil}
		}) {
			return
		}
	}
	reference.assert(t, "admission", observations)
}
