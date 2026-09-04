// Package operatorconfig owns the Article example's immutable operator
// identity, authorization, password-hash profile, and bounded runtime policy.
// It deliberately contains no username, raw password, or database selection.
package operatorconfig

import (
	"github.com/progresshans/godj/admin"
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

// CredentialPolicy returns the one secret-free operator policy shared by the
// project-linked provisioning runner and every Article site process. The
// returned hasher uses GoDj's current default encoded-password profile.
func CredentialPolicy() (systemstate.CredentialPolicy, error) {
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{
		ID:     PrincipalID,
		Active: true,
		Permissions: []auth.Permission{
			admin.DefaultAccessPermission,
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

// RuntimeConfig returns the raw-password-free runtime policy used after the
// sole durable operator has been explicitly provisioned.
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
