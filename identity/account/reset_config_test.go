package identityaccount

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/mail"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web/sessionauth"
)

type resetConfigBackend struct{ identity.ManagementBackend }
type resetConfigHasher struct{ auth.PasswordHasher }
type resetConfigSender struct{ t *testing.T }

func (s resetConfigSender) Send(context.Context, mail.Message) error {
	s.t.Fatal("startup sent mail")
	return nil
}
func (g constructionGuard) StartPasswordReset(context.Context, sessions.ID, string, string) (sessions.Record, error) {
	g.t.Fatal("startup started reset proof")
	return sessions.Record{}, nil
}
func (g constructionGuard) CheckPasswordReset(context.Context, sessions.ID, string, *string) error {
	g.t.Fatal("startup checked reset proof")
	return nil
}
func (g constructionGuard) ResetPassword(context.Context, sessions.ID, string, string) (auth.PasswordResetResult, error) {
	g.t.Fatal("startup reset password")
	return auth.PasswordResetResult{}, nil
}

func TestPasswordResetConstructionAndConfigOwnership(t *testing.T) {
	for _, mode := range []string{"valid", "nil_mailer", "zero_mailer", "nil_reporter", "missing_persistence", "wrong_link_prefix", "api_overlap"} {
		t.Run(mode, func(t *testing.T) {
			store, err := sessions.NewMemoryStore(1)
			if err != nil {
				t.Fatal(err)
			}
			manager, err := sessions.NewManager(store, sessions.Config{})
			if err != nil {
				t.Fatal(err)
			}
			guard := constructionGuard{t, manager}
			paths, err := AllowedNextPaths("")
			if err != nil {
				t.Fatal(err)
			}
			config := sessionauth.Config{Sessions: manager, Authenticator: guard, Authorizer: guard, PasswordChangePersistence: guard, PasswordResetPersistence: guard, LoginPath: "/login/", FallbackPath: paths[0], AllowedNextPaths: paths}
			if mode == "missing_persistence" {
				config.PasswordResetPersistence = nil
			}
			runtime, err := sessionauth.New(config)
			if err != nil {
				t.Fatal(err)
			}
			keys, err := identity.NewPasswordResetKeyRing(bytes.Repeat([]byte{51}, 32))
			if err != nil {
				t.Fatal(err)
			}
			resetter, err := identity.NewPasswordResetter(resetConfigBackend{}, resetConfigHasher{}, identity.PasswordResetConfig{Keys: keys})
			if err != nil {
				t.Fatal(err)
			}
			from, err := mail.ParseAddress("sender@example.test")
			if err != nil {
				t.Fatal(err)
			}
			prefix := "/account/reset"
			if mode == "wrong_link_prefix" {
				prefix = "/wrong/reset"
			}
			mailer, err := identity.NewPasswordResetMailer(resetter, resetConfigSender{t}, identity.PasswordResetMailConfig{From: from, Origin: "https://reset.example.test", ConfirmPath: prefix})
			if err != nil {
				t.Fatal(err)
			}
			reports := 0
			reset := &PasswordResetConfig{Mailer: mailer, ReportError: func(context.Context, error) { reports++ }}
			switch mode {
			case "nil_mailer":
				reset.Mailer = nil
			case "zero_mailer":
				reset.Mailer = &identity.PasswordResetMailer{}
			case "nil_reporter":
				reset.ReportError = nil
			}
			configured, err := settings.New(settings.Definition{ProjectName: "reset_config", InstalledApps: []apps.Config{{Name: "account", Label: "account"}}})
			if err != nil {
				t.Fatal(err)
			}
			c := Config{Apps: configured.Apps(), Namespace: "account", Auth: runtime, PasswordReset: reset}
			if mode == "api_overlap" {
				c.APIBasePath = "/account/reset/x"
			}
			app, err := New(c)
			if mode != "valid" {
				if app != nil || err == nil {
					t.Fatal("invalid reset configuration published", mode)
				}
				return
			}
			if err != nil || !app.auth.CanResetPassword() || reports != 0 {
				t.Fatal("reset startup executed work or failed", err)
			}
			if len(app.Routes()) != 19 {
				t.Fatal("reset routes missing", len(app.Routes()))
			}
			before := app.resetMailer
			reset.Mailer = nil
			reset.ReportError = nil
			if app.resetMailer != before || app.reportResetError == nil {
				t.Fatal("application aliases reset configuration")
			}
			encoded, err := json.Marshal(PasswordResetConfig{Mailer: mailer, ReportError: app.reportResetError})
			if err != nil || !strings.Contains(string(encoded), "redacted") || strings.Contains(fmt.Sprintf("%#v", app), "sender@example.test") {
				t.Fatal("reset diagnostic disclosure")
			}
		})
	}
}
