package identitytest

import (
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	hostmodels "github.com/progresshans/godj/conformance/identityfixture/models"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity"
	identityadmin "github.com/progresshans/godj/identity/admin"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/validation"
)

func adminUserData(value identity.UserDetails) url.Values {
	data := url.Values{"username": {value.Username}, "first_name": {value.FirstName}, "last_name": {value.LastName}, "email": {value.Email}}
	for name, enabled := range map[string]bool{"active": value.Active, "staff": value.Staff, "superuser": value.Superuser} {
		if enabled {
			data.Set(name, "on")
		}
	}
	for _, id := range value.GroupIDs {
		data.Add("groups", strconv.FormatInt(id, 10))
	}
	for _, id := range value.PermissionIDs {
		data.Add("permissions", strconv.FormatInt(id, 10))
	}
	return data
}

func adminPermissions(t *testing.T, f *managementFixture, staff bool, permissions ...auth.Permission) {
	t.Helper()
	if err := f.backend.CoordinatedAtomic(t.Context(), func(session db.Session) error {
		for _, code := range permissions {
			permission, err := models.PermissionObjects.Create(t.Context(), session, models.NewPermissionCreate(string(code), string(code)))
			if err != nil {
				return err
			}
			if _, err := models.UserPermissionsLinkObjects.Create(t.Context(), session, models.NewUserPermissionsLinkCreate(f.root.ID, permission.ID)); err != nil {
				return err
			}
		}
		_, err := models.UserObjects.Update(t.Context(), session, f.root, models.UserPatch{}.WithSuperuser(false).WithStaff(staff).WithRevision(2))
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func RunManagementAdmin(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	t.Helper()
	runAdminPasswordCreation(t, open)
	runAdminFailureBoundaries(t, open)
	runAdminReadBoundaries(t, open)
	runAdminSelectionBoundaries(t, open)
	runPasswordPolicyBoundaries(t, open)
	runUserChangeModelValidation(t, open)
	t.Run("real_forms_relations_password_history_and_host_delete", func(t *testing.T) {
		backend, second := open(t)
		f := newManagementFixture(t, backend, 3)
		h := newIdentityAdminHTTP(t, f, f.runtime)
		h.loginAdmin(t, h.client, "manager", managementOldPassword)
		h.postForm(t, "/admin/permissions/add/", url.Values{"code": {"helpdesk.ticket.archive"}, "name": {"Archive <tickets>"}}, 302)
		permission, found, err := models.PermissionObjects.Using(backend).Filter(models.PermissionFields.Code.Exact("helpdesk.ticket.archive")).OrderBy(models.PermissionFields.ID.Asc()).First(t.Context())
		if err != nil || !found {
			t.Fatal("permission not persisted", err)
		}
		pid := strconv.FormatInt(permission.ID, 10)
		h.postForm(t, "/admin/groups/add/", url.Values{"name": {"Editors <web>"}, "permissions": {pid, pid}}, 302)
		group, found, err := models.GroupObjects.Using(backend).Filter(models.GroupFields.Name.Exact("Editors <web>")).OrderBy(models.GroupFields.ID.Asc()).First(t.Context())
		if err != nil || !found {
			t.Fatal("group not persisted", err)
		}
		add := h.call(t, h.client, "GET", "/admin/users/add/", nil, 200)
		add.contains(t, `name="username"`, `type="password" name="password1"`, `type="password" name="password2"`)
		add.excludes(t, `name="groups"`, `name="staff"`, `name="revision"`)
		const rawPassword = "  retained Admin secret  "
		h.postForm(t, "/admin/users/add/", url.Values{"username": {" Ｆｒｅｄ "}, "password1": {rawPassword}, "password2": {rawPassword}}, 302)
		user, found, err := models.UserObjects.Using(backend).Filter(models.UserFields.Username.Exact("Fred")).OrderBy(models.UserFields.ID.Asc()).First(t.Context())
		if err != nil || !found || user.Staff || user.Superuser || !user.Active || user.Revision != 1 {
			t.Fatal("creation defaults/normalization", err)
		}
		if _, err := f.runtime.Authenticator().Authenticate(t.Context(), "Fred", rawPassword); err != nil {
			t.Fatal("password whitespace lost", err)
		}
		if _, err := f.runtime.Authenticator().Authenticate(t.Context(), "Fred", strings.TrimSpace(rawPassword)); err == nil {
			t.Fatal("trimmed password accepted")
		}
		manager := f.manager(t, f.runtime)
		details, err := manager.User(t.Context(), f.actor, user.ID)
		if err != nil {
			t.Fatal(err)
		}
		data := adminUserData(details)
		data.Set("first_name", "Edited")
		data.Set("email", "Mixed@EXAMPLE.COM")
		data.Set("staff", "on")
		data.Set("groups", strconv.FormatInt(group.ID, 10))
		path := adminObjectPath("users", "change", user.ID)
		change := h.call(t, h.client, "GET", path, nil, 200)
		change.contains(t, "Editors &lt;web&gt;", "Archive &lt;tickets&gt;", `type="email" name="email"`)
		change.excludes(t, user.EncodedPassword, rawPassword)
		h.postForm(t, path, data, 302)
		details, err = manager.User(t.Context(), f.actor, user.ID)
		if err != nil || details.Revision != 2 || details.Email != "Mixed@example.com" || !reflect.DeepEqual(details.GroupIDs, []int64{group.ID}) {
			t.Fatal("profile or membership update", err)
		}
		h.postForm(t, path, adminUserData(details), 302)
		if current, err := manager.User(t.Context(), f.actor, user.ID); err != nil || current.Revision != 2 {
			t.Fatal("no-op advanced revision", err)
		}
		stale := adminUserData(details)
		stale.Set("expected_revision", "1")
		stale.Set("first_name", "Lost")
		h.postForm(t, path, stale, 409)
		invalid := adminUserData(details)
		invalid.Set("email", "not-an-email")
		beforeEmailAudit, err := f.runtime.AuditHistory(t.Context(), "godj_identity.user", user.ID, 100)
		if err != nil {
			t.Fatal(err)
		}
		h.postForm(t, path, invalid, 200).contains(t, `data-error-field="email" data-error-code="invalid"`)
		if current, err := manager.User(t.Context(), f.actor, user.ID); err != nil || !reflect.DeepEqual(current, details) {
			t.Fatal("invalid email changed the stored user or revision", err)
		}
		if current, err := f.runtime.AuditHistory(t.Context(), "godj_identity.user", user.ID, 100); err != nil || !reflect.DeepEqual(current, beforeEmailAudit) {
			t.Fatal("invalid email added an audit entry", err)
		}
		beforeHashes := f.hasher.calls.Load()
		h.postForm(t, "/admin/users/add/", url.Values{"username": {"fRED"}, "password1": {rawPassword}, "password2": {rawPassword}}, 200).contains(t, `data-error-field="username" data-error-code="unique"`)
		if f.hasher.calls.Load() != beforeHashes {
			t.Fatal("duplicate hashed password")
		}
		member := h.newClient(t)
		h.loginAdmin(t, member, "Fred", rawPassword)
		targetSession := managedSession(t, f.runtime, user.PrincipalID)
		command := adminObjectPath("users", "command/password", user.ID)
		h.postForm(t, command, url.Values{"password1": {managementNewPassword}, "password2": {"different"}}, 200).contains(t, `data-error-field="password2" data-error-code="password_mismatch"`)
		if f.hasher.calls.Load() != beforeHashes {
			t.Fatal("confirmation failure hashed password")
		}
		h.postForm(t, command, url.Values{"password1": {managementNewPassword}, "password2": {managementNewPassword}}, 302)
		h.call(t, member, "GET", "/admin/", nil, 302)
		if _, found, err := f.runtime.SessionStore().Load(t.Context(), targetSession); err != nil || found {
			t.Fatal("unused target session survived password change", err)
		}
		history := h.call(t, h.client, "GET", adminObjectPath("users", "history", user.ID), nil, 200)
		history.contains(t, "password", "first_name")
		history.excludes(t, user.EncodedPassword, rawPassword, managementNewPassword)
		reopened, err := systemstate.OpenIdentity(t.Context(), second, systemstate.IdentityRuntimeConfig{PasswordHasher: f.hasher.PasswordHasher, MaxSessions: 512})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := reopened.Authenticator().Authenticate(t.Context(), "Fred", managementNewPassword); err != nil {
			t.Fatal("reopened credential missing", err)
		}
		if _, err := reopened.Authenticator().Authenticate(t.Context(), "Fred", rawPassword); err == nil {
			t.Fatal("old password revived")
		}
		if _, err := hostmodels.NoteObjects.Create(t.Context(), backend, hostmodels.NewNoteCreate("Cascade", user.ID)); err != nil {
			t.Fatal(err)
		}
		guard, err := hostmodels.GuardObjects.Create(t.Context(), backend, hostmodels.NewGuardCreate(user.ID))
		if err != nil {
			t.Fatal(err)
		}
		deletePath := adminObjectPath("users", "delete", user.ID)
		h.postForm(t, deletePath, url.Values{}, 200).contains(t, "protected")
		if _, err := hostmodels.GuardObjects.Delete(t.Context(), backend, &guard); err != nil {
			t.Fatal(err)
		}
		h.postForm(t, deletePath, url.Values{}, 302)
		if count, err := hostmodels.NoteObjects.Using(backend).Count(t.Context()); err != nil || count != 0 {
			t.Fatal("host cascade omitted", err)
		}
		if exists, err := models.UserObjects.Using(backend).Filter(models.UserFields.ID.Exact(user.ID)).Exists(t.Context()); err != nil || exists {
			t.Fatal("user delete not persisted", err)
		}
		h.postForm(t, adminObjectPath("permissions", "change", permission.ID), url.Values{"code": {"helpdesk.ticket.read_archive"}, "name": {"Read archive"}}, 302)
		h.postForm(t, adminObjectPath("permissions", "delete", permission.ID), url.Values{}, 302)
		current, err := manager.Group(t.Context(), f.actor, group.ID)
		if err != nil || current.Revision != 2 || len(current.PermissionIDs) != 0 {
			t.Fatal("permission delete did not advance group owner", err)
		}
		h.postForm(t, adminObjectPath("groups", "change", group.ID), url.Values{"name": {"Empty editors"}}, 302)
		h.postForm(t, adminObjectPath("groups", "delete", group.ID), url.Values{}, 302)
		for index, id := range f.ids {
			if _, found, err := f.runtime.SessionStore().Load(t.Context(), id); err != nil || !found {
				t.Fatal("unrelated session changed", index, err)
			}
		}
	})
	t.Run("transport_and_private_field_rejections_do_not_write", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		h := newIdentityAdminHTTP(t, f, f.runtime)
		h.loginAdmin(t, h.client, "manager", managementOldPassword)
		h.call(t, h.client, "POST", "/admin/users/add/", url.Values{"username": {"unused"}, "password1": {managementOldPassword}, "password2": {managementOldPassword}}, 403)
		for _, name := range []string{"principal_id", "encoded_password", "revision", "staff", "groups"} {
			values := url.Values{"username": {"unused"}, "password1": {managementOldPassword}, "password2": {managementOldPassword}, name: {"forged"}}
			h.postForm(t, "/admin/users/add/", values, 400)
		}
		if f.hasher.calls.Load() != 0 {
			t.Fatal("rejected browser input reached hasher")
		}
		if count, err := models.UserObjects.Using(backend).Count(t.Context()); err != nil || count != 2 {
			t.Fatal("rejected browser input wrote user", err)
		}
		f.assertOutcome(t, false)
	})
	for _, tc := range []struct {
		name   string
		grants []auth.Permission
		path   string
		status int
		detail bool
	}{
		{"user_change_can_select_without_target_view", []auth.Permission{identity.ChangeUser}, "users/change", 200, false},
		{"user_view_is_read_only", []auth.Permission{identity.ViewUser}, "users/change", 200, true},
		{"user_add_requires_change", []auth.Permission{identity.AddUser}, "users/add", 403, false},
		{"user_add_and_change", []auth.Permission{identity.AddUser, identity.ChangeUser}, "users/add", 200, false},
		{"group_add_can_select_without_target_view", []auth.Permission{identity.AddGroup}, "groups/add", 200, false},
		{"group_change_can_select_without_target_view", []auth.Permission{identity.ChangeGroup}, "groups/change", 200, false},
		{"group_view_is_read_only", []auth.Permission{identity.ViewGroup}, "groups/change", 200, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend, _ := open(t)
			f := newManagementFixture(t, backend, 0)
			group, _, _ := managementUserSelections(t, f)
			adminPermissions(t, f, true, tc.grants...)
			h := newIdentityAdminHTTP(t, f, f.runtime)
			h.loginAdmin(t, h.client, "manager", managementOldPassword)
			path := "/admin/" + tc.path + "/"
			if strings.HasSuffix(tc.path, "change") {
				id := f.user.ID
				if strings.HasPrefix(tc.path, "groups") {
					id = group.ID
				}
				path += "?id=" + strconv.FormatInt(id, 10)
			}
			result := h.call(t, h.client, "GET", path, nil, tc.status)
			if tc.status == 200 && tc.detail {
				result.contains(t, `data-admin-view="detail"`)
				result.excludes(t, ` name="permissions"`, ` name="username"`, ` name="expected_revision"`)
				h.call(t, h.client, "POST", path, url.Values{"csrfmiddlewaretoken": {result.token(t)}, "expected_revision": {"1"}}, 403)
			}
			if tc.status == 200 && !tc.detail && strings.HasPrefix(tc.path, "groups") {
				result.contains(t, "View ticket", "Change ticket")
			}
			if tc.name == "user_change_can_select_without_target_view" {
				h.call(t, h.client, "POST", "/admin/users/add/", url.Values{"username": {"Candidate"}, "unusable_password": {"on"}, "csrfmiddlewaretoken": {result.token(t)}}, 403)
				result.contains(t, "Editors", "View ticket")
				h.call(t, h.client, "GET", "/admin/groups/", nil, 403)
				h.call(t, h.client, "GET", "/admin/permissions/", nil, 403)
			}
		})
	}
	t.Run("self_password_success_survives_session_revocation", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 3)
		h := newIdentityAdminHTTP(t, f, f.runtime)
		h.loginAdmin(t, h.client, "manager", managementOldPassword)
		h.postForm(t, adminObjectPath("users", "command/password", f.root.ID), url.Values{"password1": {managementNewPassword}, "password2": {managementNewPassword}}, 302)
		h.call(t, h.client, "GET", "/admin/", nil, 302)
		h.loginAdmin(t, h.client, "manager", managementNewPassword)
		h.call(t, h.client, "GET", "/admin/users/", nil, 200)
	})
	t.Run("configured_password_policy_rejects_before_hash", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		validator := identity.PasswordValidatorFunc(func(password string, profile identity.Profile) validation.Errors {
			if strings.Contains(password, profile.Username) {
				return validation.NewErrors(validation.New("password", "password_too_similar"))
			}
			return validation.Errors{}
		})
		h := newIdentityAdminHTTP(t, f, f.runtime, func(config identityadmin.Config) identityadmin.Config {
			return config.WithPasswordValidators(validator)
		})
		h.loginAdmin(t, h.client, "manager", managementOldPassword)
		h.postForm(t, "/admin/users/add/", url.Values{"username": {"Policy"}, "password1": {"Policy password"}, "password2": {"Policy password"}}, 200).contains(t, `data-error-field="password2" data-error-code="password_too_similar"`)
		h.postForm(t, adminObjectPath("users", "command/password", f.user.ID), url.Values{"password1": {"member password"}, "password2": {"member password"}}, 200).contains(t, `data-error-field="password2" data-error-code="password_too_similar"`)
		if f.hasher.calls.Load() != 0 {
			t.Fatal("policy rejection hashed")
		}
		f.assertOutcome(t, false)
	})
}
