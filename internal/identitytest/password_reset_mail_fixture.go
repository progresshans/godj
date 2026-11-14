package identitytest

import (
	"context"
	"encoding/base64"
	"net/url"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/mail"
)

type resetSenderFunc func(context.Context, mail.Message) error

func (fn resetSenderFunc) Send(ctx context.Context, message mail.Message) error {
	return fn(ctx, message)
}

func resetMailConfig(t *testing.T) identity.PasswordResetMailConfig {
	t.Helper()
	from, err := mail.ParseAddress("sender@example.test")
	if err != nil {
		t.Fatal(err)
	}
	return identity.PasswordResetMailConfig{From: from, Origin: "https://reset.example.test", ConfirmPath: "/account/reset", SiteName: "Reference"}
}
func resetMailer(t *testing.T, resetter *identity.PasswordResetter, sender mail.Sender, config identity.PasswordResetMailConfig) *identity.PasswordResetMailer {
	t.Helper()
	mailer, err := identity.NewPasswordResetMailer(resetter, sender, config)
	if err != nil {
		t.Fatal(err)
	}
	return mailer
}
func resetMemory(t *testing.T) *mail.Memory {
	t.Helper()
	value, err := mail.NewMemory(mail.MemoryConfig{MaximumMessages: auth.MaximumPasswordResetCandidates + 1})
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func deliveredResetToken(t *testing.T, message mail.Message) (id, token string) {
	t.Helper()
	for _, line := range strings.Split(message.Text(), "\n") {
		if !strings.HasPrefix(line, "https://reset.example.test/account/reset/") {
			continue
		}
		link, err := url.Parse(line)
		if err != nil || link.Scheme != "https" || link.Host != "reset.example.test" || link.User != nil || link.RawQuery != "" || link.Fragment != "" {
			t.Fatal("reset link escaped configured origin", err)
		}
		parts := strings.Split(link.Path, "/")
		if len(parts) != 6 || parts[1] != "account" || parts[2] != "reset" || parts[5] != "" {
			t.Fatal("invalid reset route")
		}
		decoded, err := base64.RawURLEncoding.DecodeString(parts[3])
		if err != nil || base64.RawURLEncoding.EncodeToString(decoded) != parts[3] {
			t.Fatal("invalid principal identity encoding", err)
		}
		return string(decoded), parts[4]
	}
	t.Fatal("reset message omitted its configured link")
	return "", ""
}
