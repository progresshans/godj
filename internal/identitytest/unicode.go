package identitytest

import (
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/systemstate"
)

// Historical provisioning and adoption must retain their own normalization and
// byte-preservation checks. Selected service/Form observations live in the
// generated account consumers and use the original independent corpus.
func RunIdentityUnicodeProfile(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	t.Helper()
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
