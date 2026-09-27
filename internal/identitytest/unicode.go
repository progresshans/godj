package identitytest

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/systemstate"
)

//go:embed testdata/unicode-inputs.json
var identityUnicodeInputs []byte

//go:embed testdata/unicode-django61.json
var identityUnicodeReference []byte

func RunIdentityUnicodeProfile(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	t.Helper()
	var inputs struct {
		Cases []struct{ Name, Username, Email string }
	}
	var reference struct {
		Django, Python, Unicode string
		InputSHA256             string `json:"input_sha256"`
		Cases                   []struct {
			Name, Username, Email string
			FormValid             bool    `json:"form_valid"`
			FormUsername          *string `json:"form_username"`
		}
	}
	if err := json.Unmarshal(identityUnicodeInputs, &inputs); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(identityUnicodeReference, &reference); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(identityUnicodeInputs)
	if reference.Django != "6.1" || reference.Python != "3.14.3" || reference.Unicode != "16.0.0" || reference.InputSHA256 != hex.EncodeToString(digest[:]) || len(reference.Cases) != len(inputs.Cases) {
		t.Fatal("Unicode reference binding mismatch")
	}
	for i, input := range inputs.Cases {
		want := reference.Cases[i]
		if want.Name != input.Name {
			t.Fatal("reference ordering mismatch")
		}
		t.Run("manager/"+input.Name, func(t *testing.T) {
			backend, second := open(t)
			f := newManagementFixture(t, backend, 0)
			manager := f.manager(t, f.runtime)
			created, err := manager.CreateUser(t.Context(), f.actor, identity.NewUserCreate("unicode-candidate", input.Username).WithEmail(input.Email), managementNewPassword)
			if err != nil || created.Username != want.Username || created.Email != want.Email {
				t.Fatal("manager normalization differs from Django", created.Username, created.Email, err)
			}
			if f.hasher.calls.Load() != 1 {
				t.Fatal("creation did not hash once")
			}
			reopened, err := systemstate.OpenIdentity(t.Context(), second, systemstate.IdentityRuntimeConfig{PasswordHasher: f.hasher.PasswordHasher, MaxSessions: 512})
			if err != nil {
				t.Fatal(err)
			}
			h := newIdentityHTTPWithStore(t, reopened.Authenticator(), reopened.SessionStore())
			h.login(t, h.client, want.Username, managementNewPassword, 200)
			if want.Username != input.Username {
				h.login(t, h.newClient(t), input.Username, managementNewPassword, 401)
			}
			current, err := manager.User(t.Context(), f.actor, created.ID)
			if err != nil || current.Username != want.Username || current.Revision != 1 {
				t.Fatal("reopen/login changed profile", err)
			}
			// Valid storage-width names above the creation form's limit remain
			// editable; an unchanged value must not fail form initialization.
			if input.Name == "astral256" || input.Name == "ascii256" || input.Name == "compatibility_expansion" {
				adminHTTP := newIdentityAdminHTTP(t, f, f.runtime)
				adminHTTP.loginAdmin(t, adminHTTP.client, "manager", managementOldPassword)
				data := adminUserData(current)
				data.Set("first_name", "Round trip")
				// The manager corpus also observes non-email strings; submit a
				// valid replacement when exercising the Admin email validator.
				data.Set("email", "Edited@EXAMPLE.TEST")
				adminHTTP.postForm(t, adminObjectPath("users", "change", created.ID), data, 302)
				after, err := manager.User(t.Context(), f.actor, created.ID)
				if err != nil || after.Username != want.Username || after.FirstName != "Round trip" || after.Email != "Edited@example.test" || after.Revision != 2 {
					t.Fatal("edit lost storage-width username", err)
				}
			}
		})
		t.Run("admin/"+input.Name, func(t *testing.T) {
			backend, _ := open(t)
			f := newManagementFixture(t, backend, 0)
			h := newIdentityAdminHTTP(t, f, f.runtime)
			h.loginAdmin(t, h.client, "manager", managementOldPassword)
			status := 200
			if want.FormValid {
				status = 302
			}
			h.postForm(t, "/admin/users/add/", url.Values{"username": {input.Username}, "password1": {managementNewPassword}, "password2": {managementNewPassword}}, status)
			count, err := models.UserObjects.Using(backend).Count(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if !want.FormValid {
				if count != 2 || f.hasher.calls.Load() != 0 {
					t.Fatal("rejected form hashed or wrote")
				}
				return
			}
			if count != 3 || f.hasher.calls.Load() != 1 || want.FormUsername == nil {
				t.Fatal("accepted form did not create exactly one user")
			}
			if _, err := f.runtime.Authenticator().Authenticate(t.Context(), *want.FormUsername, managementNewPassword); err != nil {
				t.Fatal("created Unicode form account cannot authenticate", err)
			}
		})
	}
	t.Run("storage_and_expansion_limits_reject_before_hash", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		manager := f.manager(t, f.runtime)
		for _, username := range []string{strings.Repeat("x", 257), strings.Repeat("한", 257), strings.Repeat("\U000105c0", 257), strings.Repeat("ﬃ", 86), strings.Repeat("\ufdfa", 15), strings.Repeat("x", auth.MaximumUsernameBytes+1)} {
			if result, err := manager.CreateUser(t.Context(), f.actor, identity.NewUserCreate("invalid", username), managementNewPassword); err == nil || result.ID != 0 {
				t.Fatal("invalid storage input accepted")
			}
		}
		if f.hasher.calls.Load() != 0 {
			t.Fatal("invalid text reached hash work")
		}
		if count, err := models.UserObjects.Using(backend).Count(t.Context()); err != nil || count != 2 {
			t.Fatal("invalid text wrote", err)
		}
	})
	t.Run("bootstrap_normalizes_full_unicode_username", func(t *testing.T) {
		backend, second := open(t)
		applyIdentitySources(t, backend, systemstate.IdentityMigrationSources()...)
		hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10000})
		if err != nil {
			t.Fatal(err)
		}
		principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "bootstrap-unicode", Active: true, Staff: true, Superuser: true})
		if err != nil {
			t.Fatal(err)
		}
		username := "\U0001ccd6" + strings.Repeat("\U000105c0", 149)
		if _, err := systemstate.ProvisionIdentity(t.Context(), backend, systemstate.ProvisionIdentityConfig{Username: username, Password: managementNewPassword, PasswordHasher: hasher, Principal: principal}); err != nil {
			t.Fatal(err)
		}
		runtime, err := systemstate.OpenIdentity(t.Context(), second, systemstate.IdentityRuntimeConfig{PasswordHasher: hasher})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.Authenticator().Authenticate(t.Context(), "A"+strings.Repeat("\U000105c0", 149), managementNewPassword); err != nil {
			t.Fatal("fresh Unicode bootstrap cannot authenticate", err)
		}
	})
	t.Run("adoption_preserves_existing_unicode_username", func(t *testing.T) {
		backend, second := open(t)
		applyIdentitySources(t, backend, systemstate.InitialDefinitionSource())
		hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10000})
		if err != nil {
			t.Fatal(err)
		}
		principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "legacy-unicode", Active: true, Permissions: []auth.Permission{identity.ViewUser}})
		if err != nil {
			t.Fatal(err)
		}
		policy := systemstate.CredentialPolicy{Principal: principal, PasswordHasher: hasher}
		username := "Ｆ" + strings.Repeat("\U000105c0", 149)
		if err := systemstate.ProvisionOperator(t.Context(), backend, systemstate.ProvisionOperatorConfig{Username: username, Password: managementNewPassword, CredentialPolicy: policy}); err != nil {
			t.Fatal(err)
		}
		applyIdentitySources(t, backend, systemstate.IdentityMigrationSources()...)
		if _, err := systemstate.AdoptOperator(t.Context(), backend, systemstate.AdoptOperatorConfig{Expected: systemstate.RuntimeConfig{CredentialPolicy: policy}, Staff: true}); err != nil {
			t.Fatal(err)
		}
		runtime, err := systemstate.OpenIdentity(t.Context(), second, systemstate.IdentityRuntimeConfig{PasswordHasher: hasher})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.Authenticator().Authenticate(t.Context(), username, managementNewPassword); err != nil {
			t.Fatal("adoption changed source username", err)
		}
		if _, err := runtime.Authenticator().Authenticate(t.Context(), "F"+strings.Repeat("\U000105c0", 149), managementNewPassword); err == nil {
			t.Fatal("adoption silently normalized stored identity")
		}
	})
}
