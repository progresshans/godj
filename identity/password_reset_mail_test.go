package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/mail"
)

func TestPasswordResetEmailMatchesNativeForm(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres"} {
		raw, err := os.ReadFile("../internal/identitytest/testdata/password-reset-django61-" + backend + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var fixture struct {
			Django       string
			Observations struct {
				Inputs []struct {
					Label, Input, Cleaned string
					Valid                 bool
					Errors                map[string][]string
				} `json:"request_input"`
			}
		}
		if err := json.Unmarshal(raw, &fixture); err != nil || fixture.Django != "6.1" || len(fixture.Observations.Inputs) != 9 {
			t.Fatal("incomplete native reset input reference", err)
		}
		for _, row := range fixture.Observations.Inputs {
			t.Run(backend+"/"+row.Label, func(t *testing.T) {
				cleaned, failures := CleanPasswordResetEmail(row.Input)
				actual := map[string][]string{}
				for _, failure := range failures.All() {
					actual[string(failure.Field())] = append(actual[string(failure.Field())], string(failure.Code()))
				}
				if cleaned != row.Cleaned || failures.Empty() != row.Valid || !reflect.DeepEqual(actual, row.Errors) {
					t.Fatal("native reset input behavior differs", actual, row.Errors)
				}
			})
		}
	}
	for _, raw := range []string{"bad\xff@example.test", strings.Repeat(" ", 4097)} {
		if cleaned, failures := CleanPasswordResetEmail(raw); cleaned != "" || failures.Empty() {
			t.Fatal("Go input guard was bypassed")
		}
	}
}

type noResetSend struct{}

func (*noResetSend) Send(context.Context, mail.Message) error { return nil }

func TestPasswordResetMailConfigurationAndDiagnostics(t *testing.T) {
	from, err := mail.ParseAddress("sender@example.test")
	if err != nil {
		t.Fatal(err)
	}
	// This test exercises configuration only. No backend or hash work is used.
	resetter := &PasswordResetter{state: &passwordResetState{}}
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
	for _, mode := range []string{"nil_resetter", "empty_resetter", "nil_sender", "typed_nil_sender", "from", "site", "renderer"} {
		t.Run(mode, func(t *testing.T) {
			c := config
			r := resetter
			var sender mail.Sender = &noResetSend{}
			switch mode {
			case "nil_resetter":
				r = nil
			case "empty_resetter":
				r = &PasswordResetter{}
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
	for _, item := range []any{value, content, config, mailer} {
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
	for _, err := range []error{mailer.Request(nil, "member@example.test"), (*PasswordResetMailer)(nil).Request(t.Context(), "member@example.test")} {
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
