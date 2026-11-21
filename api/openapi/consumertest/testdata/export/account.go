package main

import (
	"bytes"
	"context"

	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	fixtureaccounts "github.com/progresshans/godj/conformance/identityfixture/accounts"
	hostproject "github.com/progresshans/godj/conformance/identityfixture/project"
	"github.com/progresshans/godj/examples/article/apiapp"
	"github.com/progresshans/godj/identity"
	identityaccount "github.com/progresshans/godj/identity/account"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/mail"
	sessiondb "github.com/progresshans/godj/sessions/database"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web/sessionauth"
)

func accountDocument(ctx context.Context, backend sessiondb.Backend, guard *authenticationGuard) (schemaFile, error) {
	paths, err := identityaccount.AllowedNextPaths("")
	if err != nil {
		return schemaFile{}, err
	}
	keys, err := auth.NewPasswordResetKeyRing(bytes.Repeat([]byte{37}, 32))
	if err != nil {
		return schemaFile{}, err
	}
	config, err := fixtureaccounts.Policy()
	if err != nil {
		return schemaFile{}, err
	}
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10000})
	if err != nil {
		return schemaFile{}, err
	}
	selected, err := hostproject.NewAccountRuntime(ctx, backend, hasher, guard, config)
	if err != nil {
		return schemaFile{}, err
	}
	resetter, err := selected.PasswordResetter(accountmodel.RuntimeResetConfig{Keys: keys})
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
	runtime, err := sessionauth.New(sessionauth.Config{Sessions: selected.Sessions(), Authenticator: selected.Authenticator(), Authorizer: guard, LoginPersistence: selected.LoginPersistence(), PasswordChangePersistence: selected.PasswordChangePersistence(), PasswordResetPersistence: resetter, LoginPath: "/account/login/", FallbackPath: paths[0], AllowedNextPaths: paths})
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

func (guard *authenticationGuard) LoginInput() (auth.LoginInput, error) {
	return auth.NewLoginInput(auth.LoginInputConfig{Label: "Username", MaxLength: 256, Normalization: auth.LoginUnicode16NFKC})
}

type accountExportSender struct{ guard *authenticationGuard }

func (s accountExportSender) Send(context.Context, mail.Message) error {
	return s.guard.reject("reset mail")
}
