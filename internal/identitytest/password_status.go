package identitytest

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/identity"
	identityapi "github.com/progresshans/godj/identity/api"
)

func apiPasswordStatus(t *testing.T, body map[string]json.RawMessage, want bool) {
	t.Helper()
	if string(body["password_usable"]) != strconv.FormatBool(want) {
		t.Fatal("password status is missing, mistyped, stale, or confused with activation")
	}
}

func RunPasswordStatus(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	backend, _ := open(t)
	f := newManagementFixture(t, backend, 2)
	manager := f.manager(t, f.runtime)
	directory, err := identity.NewDirectory(f.runtime)
	if err != nil {
		t.Fatal(err)
	}
	apiHTTP, _ := newManagementHTTP(t, f, f.runtime)
	adminHTTP := newIdentityAdminHTTP(t, f, f.runtime)
	adminHTTP.loginAdmin(t, adminHTTP.client, "manager", managementOldPassword)
	path := apiPath("users", f.user.ID)
	edit := adminObjectPath("users", "change", f.user.ID)
	t.Run("forged_state_is_not_an_input", func(t *testing.T) {
		before := f.stored(t)
		for _, probe := range []struct{ method, path, body, revision string }{
			{"PATCH", path, `{"password_usable":false}`, "1"},
			{"PUT", path, `{"username":"member","password_usable":false}`, "1"},
			{"POST", identityapi.BasePath + "users/", `{"username":"forged","password":"never hashed","password_usable":true}`, ""},
			{"POST", path + "password/", `{"password":null,"password_usable":true}`, "1"},
		} {
			_, body := managementCall(t, apiHTTP, apiHTTP.client, probe.method, probe.path, probe.body, probe.revision, true, 400)
			if _, leaked := body["password_usable"]; leaked {
				t.Fatal("rejected input returned a success profile")
			}
		}
		details, err := manager.User(t.Context(), f.actor, f.user.ID)
		if err != nil {
			t.Fatal(err)
		}
		data := adminUserData(details)
		data.Set("password_usable", "false")
		adminHTTP.postForm(t, edit, data, 400)
		if f.hasher.calls.Load() != 0 || !reflect.DeepEqual(before, f.stored(t)) {
			t.Fatal("forged state mutated stored credential or hashed")
		}
		f.assertOutcome(t, false)
		adminHTTP.call(t, adminHTTP.client, "GET", "/admin/users/add/", nil, 200).excludes(t, `data-field-name="password_usable"`)
	})
	observations := []map[string]any{}
	for _, state := range []struct {
		name           string
		usable, active bool
	}{
		{"usable", true, true}, {"disabled", false, true}, {"inactive_disabled", false, false}, {"restored_inactive", true, false}, {"reactivated", true, true},
	} {
		if !t.Run(state.name, func(t *testing.T) {
			stored := f.stored(t)
			switch state.name {
			case "disabled":
				profile, err := manager.SetUnusablePassword(t.Context(), f.actor, stored.ID, stored.Revision)
				if err != nil || profile.PasswordUsable {
					t.Fatal("disable returned incorrect status", err)
				}
			case "inactive_disabled", "reactivated":
				profile, err := manager.UpdateUser(t.Context(), f.actor, stored.ID, stored.Revision, identity.UserPatch{}.WithActive(state.active))
				if err != nil || profile.PasswordUsable != state.usable {
					t.Fatal("activation changed password status", err)
				}
			case "restored_inactive":
				profile, err := manager.SetPassword(t.Context(), f.actor, stored.ID, stored.Revision, managementNewPassword)
				if err != nil || !profile.PasswordUsable {
					t.Fatal("restore returned incorrect status", err)
				}
			}
			stored = f.stored(t)
			beforeHashes := f.hasher.calls.Load()
			details, err := manager.User(t.Context(), f.actor, stored.ID)
			if err != nil || details.PasswordUsable != state.usable || details.Active != state.active {
				t.Fatal("manager detail password status", err)
			}
			account, found, err := directory.ByPrincipalID(t.Context(), stored.PrincipalID)
			if err != nil || !found || account.Profile().PasswordUsable != state.usable || account.HasUsablePassword() != state.usable {
				t.Fatal("directory password state disagrees", err)
			}
			encoded, err := json.Marshal(details)
			if err != nil || strings.Contains(string(encoded), stored.EncodedPassword) {
				t.Fatal("password status disclosed stored encoding", err)
			}
			_, body := managementCall(t, apiHTTP, apiHTTP.client, "GET", path, "", "", false, 200)
			apiPasswordStatus(t, body, state.usable)
			_, page := managementCall(t, apiHTTP, apiHTTP.client, "GET", identityapi.BasePath+"users/", "", "", false, 200)
			var items []map[string]json.RawMessage
			if err := json.Unmarshal(page["items"], &items); err != nil {
				t.Fatal(err)
			}
			seen := false
			for _, item := range items {
				if apiInteger(t, item, "id") == stored.ID {
					apiPasswordStatus(t, item, state.usable)
					seen = true
				}
			}
			if !seen {
				t.Fatal("user list omitted target")
			}
			label := "Disabled"
			if state.usable {
				label = "Enabled"
			}
			for _, response := range []adminHTTPResult{
				adminHTTP.call(t, adminHTTP.client, "GET", edit, nil, 200),
				func() adminHTTPResult {
					data := adminUserData(details)
					data.Set("email", "invalid")
					return adminHTTP.postForm(t, edit, data, 200)
				}(),
			} {
				response.contains(t, `data-field-name="password_usable">`+label, "Password login")
				response.excludes(t, ` name="password_usable"`, stored.EncodedPassword)
			}
			if !reflect.DeepEqual(stored, f.stored(t)) || f.hasher.calls.Load() != beforeHashes {
				t.Fatal("display or rejected edit changed credential")
			}
			observations = append(observations, map[string]any{"state": state.name, "usable": details.PasswordUsable, "active": details.Active})
		}) {
			return
		}
	}
	assertUnusableReference(t, "password_status", observations)
	t.Run("view_only_and_current_authority", func(t *testing.T) {
		adminPermissions(t, f, true, identity.ViewUser)
		view := adminHTTP.call(t, adminHTTP.client, "GET", edit, nil, 200)
		view.contains(t, `data-field-name="password_usable">Enabled`)
		view.excludes(t, ` name="username"`, ` name="password_usable"`, "Change password", "Disable password login")
		_, body := managementCall(t, apiHTTP, apiHTTP.client, "GET", path, "", "", false, 200)
		apiPasswordStatus(t, body, true)
		managementCall(t, apiHTTP, apiHTTP.client, "POST", path+"password/", `{"password":null}`, "5", true, 403)
		adminHTTP.call(t, adminHTTP.client, "POST", adminObjectPath("users", "command/disable-password", f.user.ID), map[string][]string{"confirm": {"on"}, "expected_revision": {"5"}, "csrfmiddlewaretoken": {view.token(t)}}, 403)
		if row := f.stored(t); row.Revision != 5 || strings.HasPrefix(row.EncodedPassword, "!") {
			t.Fatal("readonly observer changed password")
		}
	})
}
