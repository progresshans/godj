package identitytest

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity"
	identityadmin "github.com/progresshans/godj/identity/admin"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/validation"
)

func runAdminPasswordCreation(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	t.Run("creation_password_choice", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		h := newIdentityAdminHTTP(t, f, f.runtime)
		h.loginAdmin(t, h.client, "manager", managementOldPassword)
		actual := map[string]any{}
		for _, probe := range []struct {
			name, first, second string
			disabled, valid     bool
		}{
			{"default_missing", "", "", false, false}, {"enabled_missing", "", "", false, false},
			{"enabled_mismatch", "left", "right", false, false}, {"enabled_equal", "  new password  ", "  new password  ", false, true},
			{"disabled_empty", "", "", true, true}, {"disabled_mismatch", "left", "right", true, true},
			{"disabled_equal", "  new password  ", "  new password  ", true, true}, {"disabled_duplicate", "", "", true, false},
		} {
			username := "candidate_" + probe.name
			if probe.name == "disabled_duplicate" {
				username = "MEMBER"
			}
			values := url.Values{"username": {username}, "password1": {probe.first}, "password2": {probe.second}}
			if probe.disabled {
				values.Set("unusable_password", "on")
			} else if probe.name != "default_missing" {
				values.Set("unusable_password", "false")
			}
			want := 200
			if probe.valid {
				want = 302
			}
			response := h.postForm(t, "/admin/users/add/", values, want)
			failures := map[string][]string{}
			for _, match := range regexp.MustCompile(`data-error-field="([^"]+)" data-error-code="([^"]+)"`).FindAllStringSubmatch(response.body, -1) {
				failures[match[1]] = append(failures[match[1]], match[2])
			}
			observation := map[string]any{"valid": response.status == 302, "errors": failures}
			if probe.valid {
				row, found, err := models.UserObjects.Using(backend).Filter(models.UserFields.Username.Exact(username)).OrderBy(models.UserFields.ID.Asc()).First(t.Context())
				if err != nil || !found || row.Revision != 1 {
					t.Fatal("Admin creation did not persist", err)
				}
				current, err := f.runtime.Authenticator().Resolve(t.Context(), row.PrincipalID)
				if err != nil {
					t.Fatal(err)
				}
				_, authenticationError := f.runtime.Authenticator().Authenticate(t.Context(), username, probe.first)
				if authenticationError != nil && !errors.Is(authenticationError, auth.ErrInvalidCredentials) {
					t.Fatal("password state became a storage error", authenticationError)
				}
				observation["usable"], observation["active"], observation["matches_input"] = current.HasUsablePassword(), row.Active, authenticationError == nil
			}
			for _, secret := range []string{probe.first, probe.second} {
				if secret != "" && strings.Contains(response.body, `value="`+secret+`"`) {
					t.Fatal("creation redisplayed password")
				}
			}
			actual[probe.name] = observation
		}
		assertUnusableReference(t, "admin_creation", actual)
		if f.hasher.calls.Load() != 1 {
			t.Fatal("unusable creation hashed or usable creation was skipped")
		}
	})
	t.Run("unusable_creation_policy_and_validation", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		calls := 0
		policy := identity.PasswordValidatorFunc(func(string, identity.Profile) validation.Errors {
			calls++
			return validation.NewErrors(validation.New("password", "blocked_by_host"))
		})
		h := newIdentityAdminHTTP(t, f, f.runtime, func(config identityadmin.Config) identityadmin.Config { return config.WithPasswordValidators(policy) })
		h.loginAdmin(t, h.client, "manager", managementOldPassword)
		form := h.call(t, h.client, "GET", "/admin/users/add/", nil, 200)
		form.contains(t, `name="unusable_password"`)
		h.call(t, h.client, "POST", "/admin/users/add/", url.Values{"username": {"Candidate"}, "unusable_password": {"on"}}, 403)
		h.postForm(t, "/admin/users/add/", url.Values{"username": {"bad/name"}, "unusable_password": {"on"}}, 200)
		h.postForm(t, "/admin/users/add/", url.Values{"username": {"NoPassword"}, "unusable_password": {"on"}, "password1": {"ignored"}, "password2": {"different"}}, 302)
		if calls != 0 || f.hasher.calls.Load() != 0 {
			t.Fatal("disabled creation invoked password policy")
		}
		response := h.postForm(t, "/admin/users/add/", url.Values{"username": {"HasPassword"}, "password1": {"password"}, "password2": {"password"}}, 200)
		response.contains(t, `data-error-code="blocked_by_host"`)
		if calls != 1 || f.hasher.calls.Load() != 0 {
			t.Fatal("enabled creation bypassed host policy")
		}
		if count, err := models.UserObjects.Using(backend).Count(t.Context()); err != nil || count != 3 {
			t.Fatal("rejected creation wrote a user", err)
		}
	})
	t.Run("unusable_creation_current_authority", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		boundary := &adminMutationBoundary{Runtime: f.runtime, before: func(ctx context.Context) error {
			return f.backend.CoordinatedAtomic(ctx, func(session db.Session) error {
				_, err := models.UserObjects.Update(ctx, session, f.root, models.UserPatch{}.WithSuperuser(false).WithRevision(2))
				return err
			})
		}}
		h := newIdentityAdminHTTP(t, f, boundary)
		h.loginAdmin(t, h.client, "manager", managementOldPassword)
		h.postForm(t, "/admin/users/add/", url.Values{"username": {"Candidate"}, "unusable_password": {"on"}}, 403)
		if count, err := models.UserObjects.Using(backend).Count(t.Context()); err != nil || count != 2 || boundary.calls != 1 || f.hasher.calls.Load() != 0 {
			t.Fatal("stale creation authority admitted", err)
		}
	})
}
