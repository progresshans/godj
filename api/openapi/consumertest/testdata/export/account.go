package main

import (
	"context"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/examples/article/apiapp"
	identityaccount "github.com/progresshans/godj/identity/account"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web/sessionauth"
)

func accountDocument(guard *authenticationGuard, manager *sessions.Manager) (schemaFile, error) {
	paths, err := identityaccount.AllowedNextPaths("")
	if err != nil {
		return schemaFile{}, err
	}
	runtime, err := sessionauth.New(sessionauth.Config{Sessions: manager, Authenticator: guard, Authorizer: guard, PasswordChangePersistence: accountExportPassword{manager, guard}, LoginPath: "/account/login/", FallbackPath: paths[0], AllowedNextPaths: paths})
	if err != nil {
		return schemaFile{}, err
	}
	configured, err := settings.New(settings.Definition{ProjectName: "account_export", InstalledApps: []apps.Config{{Name: "github.com/progresshans/godj/examples/article/articleapp", Label: apiapp.Namespace}}})
	if err != nil {
		return schemaFile{}, err
	}
	application, err := identityaccount.New(identityaccount.Config{Apps: configured.Apps(), Namespace: apiapp.Namespace, Auth: runtime})
	if err != nil {
		return schemaFile{}, err
	}
	document, err := application.OpenAPI()
	if err != nil {
		return schemaFile{}, err
	}
	return schemaFile{name: "accountsession.json", data: document.Bytes()}, nil
}

type accountExportPassword struct {
	manager *sessions.Manager
	guard   *authenticationGuard
}

func (p accountExportPassword) Sessions() *sessions.Manager { return p.manager }
func (p accountExportPassword) CheckPasswordChange(context.Context, sessions.ID, *string, *string) error {
	return p.guard.reject("password check")
}
func (p accountExportPassword) ChangePassword(context.Context, sessions.ID, string, string) (auth.PasswordChangeResult, error) {
	return auth.PasswordChangeResult{}, p.guard.reject("password change")
}
