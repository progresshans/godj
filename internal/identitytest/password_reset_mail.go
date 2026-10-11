package identitytest

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/mail"
	"github.com/progresshans/godj/validation"
)

type resetSenderFunc func(context.Context, mail.Message) error

func (fn resetSenderFunc) Send(ctx context.Context, message mail.Message) error {
	return fn(ctx, message)
}

type resetMailSnapshot struct {
	identity.ManagementBackend
	inside bool
	after  func() error
}

func (boundary *resetMailSnapshot) ReadSnapshot(ctx context.Context, callback func(db.Queryer) error) error {
	err := boundary.ManagementBackend.ReadSnapshot(ctx, func(reader db.Queryer) error {
		boundary.inside = true
		defer func() { boundary.inside = false }()
		return callback(reader)
	})
	if err == nil && boundary.after != nil {
		after := boundary.after
		boundary.after = nil
		err = after()
	}
	return err
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
	value, err := mail.NewMemory(mail.MemoryConfig{MaximumMessages: identity.MaximumPasswordResetCandidates + 1})
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

func RunPasswordResetMail(t *testing.T, referenceBackend string, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	t.Run("native_recipient_selection", func(t *testing.T) {
		raw, err := passwordResetReferences.ReadFile("testdata/password-reset-django61-" + referenceBackend + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var reference struct {
			Django, Backend string
			Observations    struct {
				Cases []struct {
					Label, Input, Cleaned string
					Valid                 bool
					Accounts              []struct {
						Username, Email string
						Active, Usable  bool
					}
					Selected, Delivered []string
					UsersUnchanged      bool `json:"users_unchanged"`
				} `json:"request_selection"`
			}
		}
		if err := json.Unmarshal(raw, &reference); err != nil || reference.Django != "6.1" || len(reference.Observations.Cases) != 12 {
			t.Fatal("incomplete native recipient reference", err)
		}
		if reference.Backend != map[string]string{"sqlite": "sqlite", "postgres": "postgresql"}[referenceBackend] {
			t.Fatal("reference backend mismatch")
		}
		for _, row := range reference.Observations.Cases {
			t.Run(row.Label, func(t *testing.T) {
				backend, _ := open(t)
				f := newManagementFixture(t, backend, 0)
				accounts := map[string]models.User{}
				for index, input := range row.Accounts {
					encoded := f.user.EncodedPassword
					if !input.Usable {
						encoded, err = auth.MakeUnusablePassword(t.Context())
						if err != nil {
							t.Fatal(err)
						}
					}
					user, err := models.UserObjects.Create(t.Context(), backend, models.NewUserCreate(fmt.Sprintf("reset-recipient-%d", index), input.Username, encoded, loginInstant).WithEmail(input.Email).WithActive(input.Active))
					if err != nil {
						t.Fatal(err)
					}
					accounts[user.PrincipalID] = user
				}
				now := loginInstant
				resetter := resetService(t, f.runtime, f.hasher, resetConfig(t, &now))
				memory := resetMemory(t)
				config := resetMailConfig(t)
				mailer := resetMailer(t, resetter, memory, config)
				config.Origin, config.ConfirmPath = "https://wrong.example.test", "/wrong"
				before, audit := snapshotIdentitySystemRows(t, backend)
				if err := mailer.Request(t.Context(), row.Input); err != nil {
					t.Fatal("reset request failed", err)
				}
				deliveries, err := memory.Snapshot()
				if err != nil {
					t.Fatal(err)
				}
				actual := []string{}
				for _, delivery := range deliveries {
					message := delivery.Message()
					id, token := deliveredResetToken(t, message)
					account, found := accounts[id]
					if !found || resetter.CheckPassword(t.Context(), id, token, nil) != nil {
						t.Fatal("link was not valid for its snapshot identity")
					}
					address, err := mail.ParseAddress(account.Email)
					if err != nil || len(message.Recipients()) != 1 || message.Recipients()[0].Mailbox() != address.Mailbox() || message.From().Mailbox() != "sender@example.test" {
						t.Fatal("link was sent to a different recipient", err)
					}
					if message.Subject() != "Password reset on Reference" || !strings.Contains(message.Text(), "Reference") {
						t.Fatal("configured content was lost")
					}
					actual = append(actual, account.Username)
				}
				slices.Sort(actual)
				if !row.Valid || !row.UsersUnchanged || !reflect.DeepEqual(actual, row.Selected) || !reflect.DeepEqual(actual, row.Delivered) {
					t.Fatal("native recipient set differs", row.Label, actual, row.Selected)
				}
				after, afterAudit := snapshotIdentitySystemRows(t, backend)
				if f.hasher.calls.Load() != 0 || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(audit, afterAudit) {
					t.Fatal("request changed account/session/audit or hashed a password")
				}
			})
		}
	})
	t.Run("delivered_token_resets_and_revokes_atomically", func(t *testing.T) {
		backend, other := open(t)
		f := newManagementFixture(t, backend, 12)
		now := loginInstant
		resetter := resetService(t, f.runtime, f.hasher, resetConfig(t, &now))
		memory := resetMemory(t)
		if err := resetMailer(t, resetter, memory, resetMailConfig(t)).Request(t.Context(), f.user.Email); err != nil {
			t.Fatal(err)
		}
		deliveries, err := memory.Drain()
		if err != nil || len(deliveries) != 1 {
			t.Fatal("missing reset delivery", err)
		}
		id, token := deliveredResetToken(t, deliveries[0].Message())
		before := f.stored(t)
		if err := resetter.ResetPassword(t.Context(), id, token, selfPassword); err != nil {
			t.Fatal(err)
		}
		assertResetCommit(t, f, before, selfPassword, before.Revision+1)
		if err := resetter.CheckPassword(t.Context(), id, token, nil); err != identity.ErrInvalidResetToken {
			t.Fatal("delivered token replay was accepted", err)
		}
		stored, found, err := models.UserObjects.Using(other).Filter(models.UserFields.ID.Exact(f.user.ID)).OrderBy(models.UserFields.ID.Asc()).First(t.Context())
		if err != nil || !found || stored.EncodedPassword == before.EncodedPassword || stored.Revision != before.Revision+1 {
			t.Fatal("mail-driven reset did not persist on other connection", err)
		}
	})
}

func RunPasswordResetMailBoundaries(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	t.Run("read_failures_and_invalid_input_publish_no_mail", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		now := loginInstant
		for _, mode := range []string{"read_zero", "read_nil", "read_twice", "read_swallowed_failure", "read_end_failure"} {
			t.Run(mode, func(t *testing.T) {
				boundary := &managementBoundary{ManagementBackend: f.runtime, mode: mode}
				resetter := resetService(t, boundary, f.hasher, resetConfig(t, &now))
				memory := resetMemory(t)
				err := resetMailer(t, resetter, memory, resetMailConfig(t)).Request(t.Context(), f.user.Email)
				if err == nil {
					t.Fatal("incomplete snapshot permitted mail")
				}
				if _, input := validation.Rejected(err); input {
					t.Fatal("execution failure became ordinary email validation")
				}
				if messages, _ := memory.Snapshot(); len(messages) != 0 || f.hasher.calls.Load() != 0 || boundary.calls != 0 {
					t.Fatal("read failure sent mail or mutated state")
				}
			})
		}
		broken := resetService(t, &managementBoundary{ManagementBackend: f.runtime, mode: "read_zero"}, f.hasher, resetConfig(t, &now))
		for _, input := range []string{"", "invalid", "\xff", strings.Repeat("x", 5000)} {
			err := resetMailer(t, broken, resetMemory(t), resetMailConfig(t)).Request(t.Context(), input)
			if _, rejected := validation.Rejected(err); !rejected {
				t.Fatal("invalid input reached broken snapshot", err)
			}
		}
	})
	for _, mode := range []string{"renderer_failure", "renderer_header", "renderer_size", "renderer_cancel", "delivery_failure", "delivery_unknown", "cancel_before_second_send", "late_cancel_after_last_send"} {
		t.Run(mode, func(t *testing.T) {
			backend, _ := open(t)
			f := newManagementFixture(t, backend, 3)
			if _, err := models.UserObjects.Create(t.Context(), backend, models.NewUserCreate("second-recipient", "second-recipient", f.user.EncodedPassword, loginInstant).WithEmail(f.user.Email)); err != nil {
				t.Fatal(err)
			}
			boundary := &resetMailSnapshot{ManagementBackend: f.runtime}
			now := loginInstant
			resetter := resetService(t, boundary, f.hasher, resetConfig(t, &now))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			memory := resetMemory(t)
			marker := errors.New("private-render-or-delivery-error")
			rendered, sent := 0, 0
			config := resetMailConfig(t)
			config.Renderer = identity.PasswordResetMailRendererFunc(func(ctx context.Context, data identity.PasswordResetMail) (identity.PasswordResetMailContent, error) {
				rendered++
				if boundary.inside {
					t.Fatal("renderer retained database scope")
				}
				content := identity.PasswordResetMailContent{Subject: "Rendered", Text: data.Link() + "\n", HTML: "<p>Rendered</p>"}
				if rendered == 2 {
					switch mode {
					case "renderer_failure":
						return identity.PasswordResetMailContent{}, marker
					case "renderer_header":
						content.Subject = "bad\r\nInjected: yes"
					case "renderer_size":
						content.Text = strings.Repeat("x", identity.MaximumPasswordResetMailBytes+1)
					case "renderer_cancel":
						cancel()
					}
				}
				return content, nil
			})
			sender := resetSenderFunc(func(ctx context.Context, message mail.Message) error {
				sent++
				if boundary.inside || rendered != 2 {
					t.Fatal("delivery started before complete snapshot/render preparation")
				}
				if sent == 1 {
					switch mode {
					case "delivery_failure":
						return marker
					case "delivery_unknown":
						return &mail.Error{Code: mail.CodeOutcomeUnknown, Stage: "data_acknowledgement"}
					case "cancel_before_second_send":
						if err := memory.Send(ctx, message); err != nil {
							return err
						}
						cancel()
						return nil
					}
				}
				err := memory.Send(ctx, message)
				if sent == 2 && mode == "late_cancel_after_last_send" {
					cancel()
				}
				return err
			})
			before, audit := snapshotIdentitySystemRows(t, backend)
			err := resetMailer(t, resetter, sender, config).Request(ctx, f.user.Email)
			if mode == "late_cancel_after_last_send" {
				if err != nil || sent != 2 {
					t.Fatal("late cancellation revoked confirmed deliveries", err)
				}
			} else if err == nil {
				t.Fatal("private failure was silently accepted")
			}
			messages, _ := memory.Snapshot()
			if strings.HasPrefix(mode, "renderer_") && (sent != 0 || len(messages) != 0) {
				t.Fatal("render failure published partial prefix")
			}
			if strings.HasPrefix(mode, "delivery_") && (sent != 2 || len(messages) != 1) {
				t.Fatal("send error stopped eligible sibling or retried")
			}
			if mode == "cancel_before_second_send" && (sent != 1 || len(messages) != 1 || !errors.Is(err, context.Canceled)) {
				t.Fatal("cancelled request sent remaining messages", err)
			}
			if (mode == "renderer_failure" || mode == "delivery_failure") && !errors.Is(err, marker) {
				t.Fatal("explicit private cause was lost", err)
			}
			if mode == "delivery_unknown" && (!errors.Is(err, &mail.Error{Code: mail.CodeOutcomeUnknown}) || !errors.Is(err, &identity.Error{Code: identity.CodeOutcomeUnknown})) {
				t.Fatal("unknown delivery classification or cause was lost", err)
			}
			if err != nil {
				requirePasswordPrivate(t, err, marker.Error(), f.user.Email)
			}
			after, afterAudit := snapshotIdentitySystemRows(t, backend)
			if f.hasher.calls.Load() != 0 || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(audit, afterAudit) {
				t.Fatal("mail failure changed durable identity state")
			}
		})
	}
	t.Run("candidate_limit_is_complete_not_a_sent_prefix", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		for index := range identity.MaximumPasswordResetCandidates - 1 {
			if _, err := models.UserObjects.Create(t.Context(), backend, models.NewUserCreate(fmt.Sprintf("candidate-%d", index), fmt.Sprintf("candidate-%d", index), f.user.EncodedPassword, loginInstant).WithEmail(f.user.Email)); err != nil {
				t.Fatal(err)
			}
		}
		now := loginInstant
		resetter := resetService(t, f.runtime, f.hasher, resetConfig(t, &now))
		memory := resetMemory(t)
		mailer := resetMailer(t, resetter, memory, resetMailConfig(t))
		if err := mailer.Request(t.Context(), f.user.Email); err != nil {
			t.Fatal("inclusive candidate boundary rejected", err)
		}
		if messages, _ := memory.Drain(); len(messages) != identity.MaximumPasswordResetCandidates {
			t.Fatal("inclusive candidate set was truncated")
		}
		if _, err := models.UserObjects.Create(t.Context(), backend, models.NewUserCreate("candidate-overflow", "candidate-overflow", f.user.EncodedPassword, loginInstant).WithEmail(f.user.Email)); err != nil {
			t.Fatal(err)
		}
		if err := mailer.Request(t.Context(), f.user.Email); !errors.Is(err, &identity.Error{Code: identity.CodePersistence, Field: "password_reset_candidates"}) {
			t.Fatal("candidate overflow was not explicit", err)
		}
		if messages, _ := memory.Snapshot(); len(messages) != 0 {
			t.Fatal("candidate overflow sent truncated prefix")
		}
	})
}

func RunPasswordResetMailSnapshotBinding(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	for _, mode := range []string{"email", "credential", "inactive", "last_login", "profile"} {
		t.Run(mode, func(t *testing.T) {
			backend, other := open(t)
			f := newManagementFixture(t, backend, 3)
			before := f.stored(t)
			boundary := &resetMailSnapshot{ManagementBackend: f.runtime}
			boundary.after = func() error {
				patch := models.UserPatch{}.WithRevision(before.Revision + 1)
				switch mode {
				case "email":
					patch = patch.WithEmail("changed@example.test")
				case "credential":
					encoded, err := f.hasher.PasswordHasher.Hash(t.Context(), selfPassword)
					if err != nil {
						return err
					}
					patch = patch.WithEncodedPassword(encoded)
				case "inactive":
					patch = patch.WithActive(false)
				case "last_login":
					patch = patch.WithLastLogin(loginInstant.Add(time.Second))
				case "profile":
					patch = patch.WithFirstName("Latest")
				}
				_, err := models.UserObjects.Patch(t.Context(), other, before, patch)
				return err
			}
			now := loginInstant
			resetter := resetService(t, boundary, f.hasher, resetConfig(t, &now))
			memory := resetMemory(t)
			if err := resetMailer(t, resetter, memory, resetMailConfig(t)).Request(t.Context(), before.Email); err != nil {
				t.Fatal(err)
			}
			messages, _ := memory.Drain()
			if len(messages) != 1 || messages[0].Message().Recipients()[0].Mailbox() != before.Email {
				t.Fatal("snapshot recipient changed")
			}
			id, token := deliveredResetToken(t, messages[0].Message())
			err := resetter.CheckPassword(t.Context(), id, token, nil)
			if mode == "profile" {
				if err != nil {
					t.Fatal("unrelated profile edit invalidated token", err)
				}
			} else if err != identity.ErrInvalidResetToken {
				t.Fatal("new-state token was delivered to old snapshot recipient", err)
			}
			stored := f.stored(t)
			if stored.Revision != before.Revision+1 || f.hasher.calls.Load() != 0 {
				t.Fatal("request overwrote competing edit or hashed a password")
			}
		})
	}
}
