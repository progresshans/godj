package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/mail"
)

type noResetSend struct{}

func (*noResetSend) Send(context.Context, mail.Message) error { return nil }

type resetMailSource struct {
	t          *testing.T
	authority  auth.PasswordResetAuthority
	email      bool
	candidates func(context.Context, string) ([]auth.PasswordResetToken, error)
}

func (source *resetMailSource) PasswordResetAuthority() auth.PasswordResetAuthority {
	return source.authority
}
func (source *resetMailSource) PasswordResetEmailAvailable() bool { return source.email }
func (source *resetMailSource) PasswordResetCandidates(ctx context.Context, email string) ([]auth.PasswordResetToken, error) {
	if source.candidates != nil {
		return source.candidates(ctx, email)
	}
	source.t.Fatal("mail startup read accounts or issued tokens")
	return nil, nil
}

type resetMailSender func(context.Context, mail.Message) error

func (sender resetMailSender) Send(ctx context.Context, message mail.Message) error {
	return sender(ctx, message)
}

type nilResetContext struct{ context.Context }

func TestPasswordResetMailConsumesCompleteSnapshotTokens(t *testing.T) {
	keys, err := auth.NewPasswordResetKeyRing(bytes.Repeat([]byte{63}, 32))
	if err != nil {
		t.Fatal(err)
	}
	domain, err := auth.NewCredentialDomain(strings.Repeat("c", 64))
	if err != nil {
		t.Fatal(err)
	}
	issue := func(id int64, email string) auth.PasswordResetToken {
		t.Helper()
		principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: fmt.Sprintf("principal-%d", id), Active: true})
		if err != nil {
			t.Fatal(err)
		}
		credential, err := auth.NewCredentialForDomain(domain, fmt.Sprintf("login-%d", id), "private-hash", principal)
		if err != nil {
			t.Fatal(err)
		}
		state, err := auth.NewPasswordResetState(id, credential, email, nil)
		if err != nil {
			t.Fatal(err)
		}
		token, err := keys.Issue(state, 1790000000)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	first, second := issue(0, "member@example.test"), issue(-17, "member@example.test")
	for _, mode := range []string{"complete", "empty", "error_with_prefix", "overflow", "zero_after_valid", "bad_recipient_after_valid", "cancel_after_source"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls, sends, renders := 0, 0, 0
			fault := errors.New("private reset source failure")
			source := &resetMailSource{t: t, authority: auth.NewPasswordResetAuthority(), email: true}
			source.candidates = func(_ context.Context, email string) ([]auth.PasswordResetToken, error) {
				calls++
				if email != "member@example.test" {
					t.Fatal("source did not receive cleaned input")
				}
				switch mode {
				case "empty":
					return nil, nil
				case "error_with_prefix":
					return []auth.PasswordResetToken{first}, fault
				case "overflow":
					return make([]auth.PasswordResetToken, auth.MaximumPasswordResetCandidates+1), nil
				case "zero_after_valid":
					return []auth.PasswordResetToken{first, {}}, nil
				case "bad_recipient_after_valid":
					return []auth.PasswordResetToken{first, issue(-17, "invalid")}, nil
				case "cancel_after_source":
					cancel()
				}
				return []auth.PasswordResetToken{first, second}, nil
			}
			from, err := mail.ParseAddress("sender@example.test")
			if err != nil {
				t.Fatal(err)
			}
			config := PasswordResetMailConfig{From: from, Origin: "https://reset.example.test", Renderer: PasswordResetMailRendererFunc(func(_ context.Context, data PasswordResetMail) (PasswordResetMailContent, error) {
				renders++
				expected := first
				if renders == 2 {
					expected = second
				}
				if data.Username() != expected.Username() || !strings.Contains(data.Link(), "/"+expected.Encoded()+"/") {
					t.Fatal("rendered recipient material differs from signed snapshot")
				}
				return defaultPasswordResetMail(ctx, data)
			})}
			sender := resetMailSender(func(_ context.Context, message mail.Message) error {
				sends++
				if renders != 2 || len(message.Recipients()) != 1 || message.Recipients()[0].Mailbox() != "member@example.test" {
					t.Fatal("message escaped complete recipient batch")
				}
				return nil
			})
			mailer, err := NewPasswordResetMailer(source, sender, config)
			if err != nil || calls != 0 || sends != 0 || renders != 0 || !mailer.PasswordResetAuthority().Matches(source.authority) {
				t.Fatal("mail startup executed work or lost owner", err)
			}
			err = mailer.Request(ctx, "  member@example.test  ")
			if calls != 1 {
				t.Fatal("mail source was retried")
			}
			switch mode {
			case "complete":
				if err != nil || sends != 2 || renders != 2 {
					t.Fatal("complete snapshot tokens were not delivered", err)
				}
			case "empty":
				if err != nil || sends != 0 || renders != 0 {
					t.Fatal("empty candidate result was not private", err)
				}
			default:
				if err == nil || sends != 0 {
					t.Fatal("incomplete source published a message prefix", err)
				}
				if mode == "error_with_prefix" && !errors.Is(err, fault) {
					t.Fatal("source failure cause was lost")
				}
				if mode == "cancel_after_source" && !errors.Is(err, context.Canceled) {
					t.Fatal("cancellation was lost")
				}
			}
		})
	}
}

func TestPasswordResetMailConfigurationAndDiagnostics(t *testing.T) {
	from, err := mail.ParseAddress("sender@example.test")
	if err != nil {
		t.Fatal(err)
	}
	// This test exercises configuration only. No backend or hash work is used.
	resetter := &resetMailSource{t: t, authority: auth.NewPasswordResetAuthority(), email: true}
	config := PasswordResetMailConfig{From: from, Origin: "https://reset.example.test", ConfirmPath: "/account/reset", SiteName: "Example"}
	for _, origin := range []string{"https://reset.example.test", "https://localhost", "http://127.0.0.1:8000", "http://[::1]:8000"} {
		c := config
		c.Origin = origin
		if _, err := NewPasswordResetMailer(resetter, &noResetSend{}, c); err != nil {
			t.Fatal("valid configured origin rejected", err)
		}
	}
	for _, origin := range []string{"", "http://reset.example.test", "http://localhost", "https://user@reset.example.test", "https://reset.example.test/", "https://reset.example.test/path", "https://reset.example.test?x=1", "https://reset.example.test?", "https://reset.example.test#", "https://reset.example.test#token", "https://reset.example.test:", "https://:443", "https://reset.example.test:0", "https://reset.example.test:70000", "//reset.example.test", "https://reset.example.test\x00", "https://reset.example.test\\other"} {
		c := config
		c.Origin = origin
		if _, err := NewPasswordResetMailer(resetter, &noResetSend{}, c); !errors.Is(err, &Error{Code: CodeInvalidConfig}) {
			t.Fatal("invalid origin admitted", err)
		}
	}
	for _, path := range []string{"/", "relative", "//other.test/reset", "/account/reset/", "/a/../reset", "/reset/%2f", "/reset?token=", "/reset/<str:id>", "/reset\x01"} {
		c := config
		c.ConfirmPath = path
		if _, err := NewPasswordResetMailer(resetter, &noResetSend{}, c); !errors.Is(err, &Error{Code: CodeInvalidConfig}) {
			t.Fatal("invalid link path admitted", err)
		}
	}
	for _, mode := range []string{"nil_source", "typed_nil_source", "zero_source", "no_email", "nil_sender", "typed_nil_sender", "from", "site", "renderer"} {
		t.Run(mode, func(t *testing.T) {
			c := config
			var r auth.PasswordResetMailSource = resetter
			var sender mail.Sender = &noResetSend{}
			switch mode {
			case "nil_source":
				r = nil
			case "typed_nil_source":
				r = (*resetMailSource)(nil)
			case "zero_source":
				r = &resetMailSource{t: t, email: true}
			case "no_email":
				r = &resetMailSource{t: t, authority: resetter.authority}
			case "nil_sender":
				sender = nil
			case "typed_nil_sender":
				sender = (*noResetSend)(nil)
			case "from":
				c.From = mail.Address{}
			case "site":
				c.SiteName = "bad\nsubject"
			case "renderer":
				c.Renderer = PasswordResetMailRendererFunc(nil)
			}
			if _, err := NewPasswordResetMailer(r, sender, c); !errors.Is(err, &Error{Code: CodeInvalidConfig}) {
				t.Fatal("invalid configuration admitted", err)
			}
		})
	}
	value := PasswordResetMail{&passwordResetMail{"private-reset-user", "private-reset-site", "https://reset.example.test/private-reset-token"}}
	content, err := defaultPasswordResetMail(t.Context(), value)
	if err != nil || !strings.Contains(content.Text, value.Link()) {
		t.Fatal("default message omitted link", err)
	}
	mailer, err := NewPasswordResetMailer(resetter, &noResetSend{}, config)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := auth.NewPasswordResetKeyRing(bytes.Repeat([]byte{51}, 32))
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []any{value, content, config, mailer, PasswordResetConfig{Keys: keys}} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
			text := fmt.Sprintf(format, item)
			if strings.Contains(text, "private-reset-") || strings.Contains(text, "example.test") {
				t.Fatal("format leaked mail material")
			}
		}
		encoded, err := json.Marshal(item)
		if err != nil || strings.Contains(string(encoded), "private-reset-") || strings.Contains(string(encoded), "example.test") {
			t.Fatal("JSON leaked mail material", err)
		}
	}
	for _, err := range []error{mailer.Request(nil, "member@example.test"), mailer.Request((*nilResetContext)(nil), "member@example.test"), (*PasswordResetMailer)(nil).Request(t.Context(), "member@example.test")} {
		if err == nil {
			t.Fatal("invalid call admitted")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := mailer.Request(ctx, "member@example.test"); !errors.Is(err, context.Canceled) {
		t.Fatal("pre-cancelled request was not explicit", err)
	}
}
