package main

import (
	"bytes"
	"context"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/examples/article/apiapp"
	"github.com/progresshans/godj/identity"
	identityaccount "github.com/progresshans/godj/identity/account"
	"github.com/progresshans/godj/mail"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web/sessionauth"
)

func accountDocument(guard *authenticationGuard, manager *sessions.Manager) (schemaFile, error) {
	paths, err := identityaccount.AllowedNextPaths("")
	if err != nil {
		return schemaFile{}, err
	}
	keys, err := identity.NewPasswordResetKeyRing(bytes.Repeat([]byte{37}, 32))
	if err != nil {
		return schemaFile{}, err
	}
	resetter, err := identity.NewPasswordResetter(identityExportBackend{guard}, identityExportHasher{guard}, identity.PasswordResetConfig{Keys: keys})
	if err != nil {
		return schemaFile{}, err
	}
	from, err := mail.ParseAddress("sender@example.test")
	if err != nil {
		return schemaFile{}, err
	}
	mailer, err := identity.NewPasswordResetMailer(resetter, accountExportSender{guard}, identity.PasswordResetMailConfig{From: from, Origin: "https://reset.example.test"})
	if err != nil {
		return schemaFile{}, err
	}
	runtime, err := sessionauth.New(sessionauth.Config{Sessions: manager, Authenticator: guard, Authorizer: guard, PasswordChangePersistence: accountExportPassword{manager, guard}, PasswordResetPersistence: accountExportPassword{manager, guard}, LoginPath: "/account/login/", FallbackPath: paths[0], AllowedNextPaths: paths})
	if err != nil {
		return schemaFile{}, err
	}
	configured, err := settings.New(settings.Definition{ProjectName: "account_export", InstalledApps: []apps.Config{{Name: "github.com/progresshans/godj/examples/article/articleapp", Label: apiapp.Namespace}}})
	if err != nil {
		return schemaFile{}, err
	}
	application, err := identityaccount.New(identityaccount.Config{Apps: configured.Apps(), Namespace: apiapp.Namespace, Auth: runtime, PasswordReset: &identityaccount.PasswordResetConfig{Mailer: mailer, ReportError: func(context.Context, error) { _ = guard.reject("reset error report") }}})
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

func (p accountExportPassword) StartPasswordReset(context.Context, sessions.ID, string, string) (sessions.Record, error) {
	return sessions.Record{}, p.guard.reject("reset proof start")
}
func (p accountExportPassword) CheckPasswordReset(context.Context, sessions.ID, string, *string) error {
	return p.guard.reject("reset proof check")
}
func (p accountExportPassword) ResetPassword(context.Context, sessions.ID, string, string) (auth.PasswordResetResult, error) {
	return auth.PasswordResetResult{}, p.guard.reject("reset completion")
}

type accountExportSender struct{ guard *authenticationGuard }

func (s accountExportSender) Send(context.Context, mail.Message) error {
	return s.guard.reject("reset mail")
}
