// Package operatorconfig owns the Article example's immutable operator
// identity, authorization, password-hash profile, and bounded runtime policy.
// It deliberately contains no username, raw password, or database selection.
package operatorconfig

import (
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/examples/article/internal/articlepermissions"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/systemstate"
)

const (
	PrincipalID       = "article-development-admin"
	maximumSessions   = 256
	maximumAuditItems = 1024
)

// CredentialPolicy returns the secret-free legacy operator policy for the
// historical operator source used for explicit adoption. The
// returned hasher uses GoDj's current default encoded-password profile.
func CredentialPolicy() (systemstate.CredentialPolicy, error) {
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{
		ID:     PrincipalID,
		Active: true,
		Permissions: []auth.Permission{
			"godj.admin.access",
			articlepermissions.ArticleViewPermission,
			articlepermissions.ArticleAddPermission,
			articlepermissions.ArticleChangePermission,
			articlepermissions.ArticleDeletePermission,
		},
	})
	if err != nil {
		return systemstate.CredentialPolicy{}, err
	}
	hasher, err := auth.NewDefaultPBKDF2()
	if err != nil {
		return systemstate.CredentialPolicy{}, err
	}
	return systemstate.CredentialPolicy{
		Principal:      principal,
		PasswordHasher: hasher,
	}, nil
}

// RuntimeConfig describes the historical operator source and inspection bounds
// for explicit adoption. New site processes use IdentityRuntimeConfig.
func RuntimeConfig() (systemstate.RuntimeConfig, error) {
	policy, err := CredentialPolicy()
	if err != nil {
		return systemstate.RuntimeConfig{}, err
	}
	return systemstate.RuntimeConfig{
		CredentialPolicy: policy,
		SessionLimits:    sessions.DefaultLimits(),
		MaxSessions:      maximumSessions,
		AuditCapacity:    maximumAuditItems,
	}, nil
}

// InitialSuperuser is written once by explicit fresh-domain provisioning.
// Runtime authorization always resolves stored users, never this declaration.
func InitialSuperuser() (auth.Principal, error) {
	legacy, err := CredentialPolicy()
	if err != nil {
		return auth.Principal{}, err
	}
	return auth.NewPrincipal(auth.PrincipalConfig{ID: PrincipalID, Active: true, Staff: true, Superuser: true, Permissions: legacy.Principal.Permissions()})
}

// IdentityRuntimeConfig contains only the hash profile and resource bounds.
func IdentityRuntimeConfig() (systemstate.IdentityRuntimeConfig, error) {
	hasher, err := auth.NewDefaultPBKDF2()
	if err != nil {
		return systemstate.IdentityRuntimeConfig{}, err
	}
	return systemstate.IdentityRuntimeConfig{PasswordHasher: hasher, SessionLimits: sessions.DefaultLimits(), MaxSessions: maximumSessions, AuditCapacity: maximumAuditItems}, nil
}
