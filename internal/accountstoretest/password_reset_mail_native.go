package accountstoretest

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/mail"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/validation"
)

func resetNativeOwner(t *testing.T, f *loginFixture) *accountmodel.PasswordResetter[models.User] {
	t.Helper()
	keys, err := auth.NewPasswordResetKeyRing(bytes.Repeat([]byte{66}, 32))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := f.runtime.PasswordResetter(accountmodel.RuntimeResetConfig{Keys: keys, Clock: f.clock})
	if err != nil || owner.Sessions() != f.runtime.Sessions() {
		t.Fatal("native reset lost its composed session owner", err)
	}
	return owner
}

func resetExpectedRotation(t *testing.T, previous sessions.Record, id sessions.ID, values map[string]string, at time.Time) sessions.Record {
	t.Helper()
	snapshot := previous.Snapshot()
	snapshot.ID, snapshot.Values, snapshot.AccessedAt = id, values, at
	snapshot.IdleExpiresAt = at.Add(loginIdle)
	if snapshot.IdleExpiresAt.After(snapshot.AbsoluteExpiresAt) {
		snapshot.IdleExpiresAt = snapshot.AbsoluteExpiresAt
	}
	if id == previous.ID() {
		t.Fatal("reset reused the previous bearer")
	}
	record, err := sessions.RestoreRecord(snapshot, sessions.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func passwordResetMailDelivery(t *testing.T, open Open) {
	for _, mode := range []string{"anonymous", "self", "other"} {
		t.Run(mode, func(t *testing.T) {
			backend, reopen := open(t)
			f := provisionSelfPassword(t, backend)
			owner := resetNativeOwner(t, f)
			var previous sessions.Record
			switch mode {
			case "self":
				previous = f.targetSessions[0]
			case "other":
				previous = passwordSession(t, f.runtime.Sessions(), f.actor, "other-reset-browser")
			default:
				var err error
				previous, err = f.runtime.Sessions().Create(t.Context(), map[string]string{"cart": "retained", "csrf": "independent"})
				if err != nil {
					t.Fatal(err)
				}
			}
			before := userReferenceSnapshot(t, reopen(t))
			memory := selectedResetMemory(t)
			f.owner.boundary.calls = 0
			entropy := f.entropy.count()
			if err := selectedResetMailer(t, owner, memory, nil).Request(t.Context(), f.target.Email); err != nil {
				t.Fatal(err)
			}
			deliveries, err := memory.Drain()
			if err != nil || len(deliveries) != 1 {
				t.Fatal("native reset did not deliver exactly one recipient token", err)
			}
			principalID, token := selectedResetDelivery(t, deliveries[0].Message())
			if principalID != f.target.PrincipalID || f.owner.hasher.hashes.Load() != 0 || f.owner.hasher.verifies.Load() != 0 || f.owner.boundary.calls != 0 || f.entropy.count() != entropy {
				t.Fatal("mail request prepared password or session work")
			}
			assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
			f.instant.Add(int64(time.Minute))
			proof, err := owner.StartPasswordReset(t.Context(), previous.ID(), principalID, token)
			if err != nil {
				t.Fatal(err)
			}
			values := previous.Values()
			values[auth.SessionResetPrincipalIDKey], values[auth.SessionResetTokenKey] = principalID, token
			expectedProof := resetExpectedRotation(t, previous, f.entropy.one(t, entropy), values, f.clock())
			if !reflect.DeepEqual(proof.Snapshot(), expectedProof.Snapshot()) || f.owner.boundary.calls != 1 {
				t.Fatal("delivered token entry lost its exact native proof")
			}
			afterEntry := userReferenceSnapshot(t, reopen(t))
			expected := before
			namespace := loginNamespace(t, f.credential)
			loginRemoveRecord(t, &expected.account, namespace, previous.ID())
			loginPutRecord(t, &expected.account, afterEntry.account, namespace, expectedProof, false)
			assertUserReferenceState(t, expected, afterEntry, f.actor.Principal())
			if mode == "self" {
				f.targetSessions[0] = expectedProof
			}
			entropy = f.entropy.count()
			f.instant.Add(int64(time.Minute))
			result, err := owner.ResetPassword(t.Context(), proof.ID(), principalID, selfPassword)
			if err != nil || f.owner.hasher.hashes.Load() != 1 || f.owner.hasher.verifies.Load() != 0 || f.owner.policies.Load() != 2 || f.owner.boundary.calls != 2 {
				t.Fatal("delivered proof did not bind one hash and current policy to its write", err)
			}
			encoded := f.owner.encoded.Load().(string)
			if encoded == "" || encoded == f.target.EncodedPassword {
				t.Fatal("reset did not prepare a new hash before persistence")
			}
			after := userReferenceSnapshot(t, reopen(t))
			expected = afterEntry
			row := f.target
			row.EncodedPassword, row.Revision = encoded, row.Revision+1
			userReferenceReplace(&expected.account, row)
			for _, record := range f.targetSessions {
				loginRemoveRecord(t, &expected.account, namespace, record.ID())
			}
			if mode == "self" {
				if !reflect.DeepEqual(result, auth.PasswordResetResult{ClearSession: true}) || f.entropy.count() != entropy {
					t.Fatal("self reset retained authority or consumed unnecessary entropy")
				}
			} else {
				values := previous.Values()
				expectedRecord := resetExpectedRotation(t, expectedProof, f.entropy.one(t, entropy), values, f.clock())
				if result.ClearSession || !reflect.DeepEqual(result.Record.Snapshot(), expectedRecord.Snapshot()) {
					t.Fatal("reset changed unrelated browser authority or payload")
				}
				loginRemoveRecord(t, &expected.account, namespace, expectedProof.ID())
				loginPutRecord(t, &expected.account, after.account, namespace, expectedRecord, false)
			}
			assertUserReferenceStateAt(t, expected, after, f.credential.Principal(), f.clock(), catalogExpectedEvent{"user", f.target.ID, admin.ActionChange, []string{"encoded_password"}})
			reopened := newUserReferenceOwner(t, reopen(t), f.userReferenceFixture, f.config(t))
			credential, err := reopened.runtime.Authenticator().Authenticate(t.Context(), f.target.Username, selfPassword)
			if err != nil || credential.Principal().ID() != principalID {
				t.Fatal("mail-driven reset did not survive reconnect", err)
			}
			if _, err := reopened.runtime.Authenticator().Authenticate(t.Context(), f.target.Username, password); err != auth.ErrInvalidCredentials {
				t.Fatal("mail-driven reset retained the old password", err)
			}
			replay, err := owner.StartPasswordReset(t.Context(), sessions.ID{}, principalID, token)
			if err != auth.ErrInvalidResetProof || !reflect.DeepEqual(replay, sessions.Record{}) || f.owner.boundary.calls != 2 {
				t.Fatal("delivered token replay published another proof", err)
			}
			assertUserReferenceState(t, after, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
			for _, value := range []any{owner, result, err} {
				selfPasswordPrivate(t, value, selfPassword, encoded, token, proof.ID().Encoded(), result.Record.ID().Encoded())
			}
		})
	}
}

type resetNativeSender func(context.Context, mail.Message) error

func (send resetNativeSender) Send(ctx context.Context, message mail.Message) error {
	return send(ctx, message)
}

func passwordResetMailFailures(t *testing.T, open Open) {
	for _, mode := range []string{"renderer_failure", "renderer_header", "renderer_size", "renderer_cancel", "delivery_failure", "delivery_unknown", "delivery_unknown_accepted", "cancel_before_second_send", "late_cancel_after_last_send"} {
		t.Run(mode, func(t *testing.T) {
			backend, reopen := open(t)
			f := provisionSelfPassword(t, backend)
			second, err := models.UserObjects.Create(t.Context(), backend, models.NewUserCreate("second-reset-recipient", "second-reset-recipient", f.target.EncodedPassword, nativeLoginInstant).WithEmail(f.target.Email))
			if err != nil {
				t.Fatal(err)
			}
			boundary := &resetSnapshotBoundary{Backend: backend}
			f.owner = newUserReferenceOwner(t, boundary, f.userReferenceFixture, f.config(t))
			f.runtime = f.owner.runtime
			owner := resetNativeOwner(t, f)
			marker := errors.New("private selected reset delivery fault")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			rendered, sent := 0, 0
			var renderedLinks []string
			renderer := identity.PasswordResetMailRendererFunc(func(_ context.Context, data identity.PasswordResetMail) (identity.PasswordResetMailContent, error) {
				rendered++
				renderedLinks = append(renderedLinks, data.Link())
				if boundary.inside {
					t.Fatal("reset renderer retained the native read scope")
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
			memory := selectedResetMemory(t)
			sender := resetNativeSender(func(ctx context.Context, message mail.Message) error {
				sent++
				if boundary.inside || rendered != 2 {
					t.Fatal("mail delivery started before the complete native snapshot and rendering")
				}
				id, _ := selectedResetDelivery(t, message)
				expectedID := f.target.PrincipalID
				if sent == 2 {
					expectedID = second.PrincipalID
				}
				if id != expectedID {
					t.Fatal("mail send replayed or reordered a prepared recipient")
				}
				if sent == 1 {
					switch mode {
					case "delivery_failure":
						return marker
					case "delivery_unknown":
						return &mail.Error{Code: mail.CodeOutcomeUnknown, Stage: "data_acknowledgement"}
					case "delivery_unknown_accepted":
						if err := memory.Send(ctx, message); err != nil {
							return err
						}
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
			before := userReferenceSnapshot(t, reopen(t))
			entropy := f.entropy.count()
			err = selectedResetMailer(t, owner, sender, renderer).Request(ctx, f.target.Email)
			delivered, snapshotErr := memory.Snapshot()
			if snapshotErr != nil || rendered != 2 {
				t.Fatal("native reset mail preparation did not complete its expected prefix", snapshotErr)
			}
			if mode == "late_cancel_after_last_send" {
				if err != nil || sent != 2 || len(delivered) != 2 || ctx.Err() != context.Canceled {
					t.Fatal("late cancellation revoked confirmed reset deliveries", err)
				}
			} else {
				if err == nil {
					t.Fatal("mail failure was silently accepted")
				}
				if _, rejected := validation.Rejected(err); rejected {
					t.Fatal("mail execution failure became ordinary input rejection")
				}
				if strings.HasPrefix(mode, "renderer_") && (sent != 0 || len(delivered) != 0) {
					t.Fatal("render failure published a partial delivery prefix")
				}
				wantDelivered := 1
				if mode == "delivery_unknown_accepted" {
					wantDelivered = 2
				}
				if strings.HasPrefix(mode, "delivery_") && (sent != 2 || len(delivered) != wantDelivered) {
					t.Fatal("delivery failure retried or suppressed an eligible sibling")
				}
				if mode == "cancel_before_second_send" && (sent != 1 || len(delivered) != 1 || !errors.Is(err, context.Canceled)) {
					t.Fatal("canceled reset request delivered remaining mail", err)
				}
				if mode == "renderer_cancel" && !errors.Is(err, context.Canceled) {
					t.Fatal("renderer cancellation cause was lost", err)
				}
				if (mode == "renderer_failure" || mode == "delivery_failure") && !errors.Is(err, marker) {
					t.Fatal("mail failure lost its private cause", err)
				}
				code := identity.CodeDelivery
				if mode == "delivery_unknown" || mode == "delivery_unknown_accepted" {
					code = identity.CodeOutcomeUnknown
					if !errors.Is(err, &mail.Error{Code: mail.CodeOutcomeUnknown}) {
						t.Fatal("unknown transport outcome was hidden", err)
					}
				}
				if !errors.Is(err, &identity.Error{Code: code}) {
					t.Fatal("mail failure changed its outcome classification", err)
				}
			}
			if f.owner.hasher.hashes.Load() != 0 || f.owner.hasher.verifies.Load() != 0 || f.owner.boundary.calls != 0 || f.entropy.count() != entropy {
				t.Fatal("reset mail failure hashed, verified or started session/native writes")
			}
			assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
			selfPasswordPrivate(t, err, append(renderedLinks, marker.Error(), f.target.Email, f.target.EncodedPassword)...)
		})
	}
}

func passwordResetMailReadFailures(t *testing.T, open Open) {
	for _, mode := range []string{"skipped", "nil", "typed_nil", "root", "without_validator", "repeated", "query", "close", "swallowed", "cleanup", "cancel", "empty", "invalid", "invalid_utf8", "too_long", "canceled_before", "nil_context"} {
		t.Run(mode, func(t *testing.T) {
			backend, reopen := open(t)
			f := provisionSelfPassword(t, backend)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fault := errors.New("private selected reset mail read failure")
			reads := 0
			armed := false
			boundary := &creationSnapshotBackend{Backend: backend}
			boundary.read = snapshotFunc(func(ctx context.Context, observe func(db.Queryer) error) error {
				if !armed {
					return backend.ReadSnapshot(ctx, observe)
				}
				reads++
				switch mode {
				case "skipped":
					return nil
				case "nil":
					return observe(nil)
				case "typed_nil":
					return observe((*profileReadProbe)(nil))
				case "root":
					return observe(backend)
				}
				err := backend.ReadSnapshot(ctx, func(scope db.Queryer) error {
					if mode == "without_validator" {
						return observe(&struct{ db.Queryer }{scope})
					}
					probe := &profileReadProbe{Queryer: scope, validator: scope.(db.SessionValidator)}
					if mode == "query" || mode == "close" || mode == "swallowed" {
						probe.fault, probe.closeFail = fault, mode == "close"
					}
					err := observe(probe)
					if mode == "swallowed" {
						return nil
					}
					if mode == "repeated" {
						return errors.Join(err, observe(probe))
					}
					return err
				})
				if mode == "cleanup" {
					return errors.Join(err, fault)
				}
				if mode == "cancel" {
					cancel()
				}
				return err
			})
			f.owner = newUserReferenceOwner(t, boundary, f.userReferenceFixture, f.config(t))
			f.runtime = f.owner.runtime
			owner := resetNativeOwner(t, f)
			input, invalid := f.target.Email, false
			wantReads := 0
			switch mode {
			case "empty":
				input, invalid = "", true
			case "invalid":
				input, invalid = "invalid", true
			case "invalid_utf8":
				input, invalid = "\xff", true
			case "too_long":
				input, invalid = strings.Repeat("x", 5000), true
			case "canceled_before":
				cancel()
			case "nil_context":
				ctx = nil
			default:
				wantReads = 1
			}
			memory := selectedResetMemory(t)
			rendered := 0
			renderer := identity.PasswordResetMailRendererFunc(func(context.Context, identity.PasswordResetMail) (identity.PasswordResetMailContent, error) {
				rendered++
				return identity.PasswordResetMailContent{}, nil
			})
			before := userReferenceSnapshot(t, reopen(t))
			entropy := f.entropy.count()
			armed = true
			err := selectedResetMailer(t, owner, memory, renderer).Request(ctx, input)
			if err == nil {
				t.Fatal("invalid reset mail request succeeded", mode)
			}
			diagnostics, rejected := validation.Rejected(err)
			if invalid {
				if !rejected || diagnostics.ByField("email").Empty() || errors.Unwrap(err) != nil {
					t.Fatal("invalid email reached persistence or lost its direct diagnostics", err)
				}
			} else if rejected {
				t.Fatal("native mail read failure became ordinary input rejection", err)
			}
			if wantReads == 1 {
				requireProfileCode(t, err, accountmodel.CodePersistence)
			}
			if mode == "nil_context" && !errors.Is(err, &identity.Error{Code: identity.CodeInvalidInput}) {
				t.Fatal("nil mail context lost its configuration boundary", err)
			}
			if mode == "canceled_before" && !errors.Is(err, &identity.Error{Code: identity.CodePersistence}) {
				t.Fatal("canceled mail entry became ordinary input rejection", err)
			}
			if (mode == "query" || mode == "close" || mode == "swallowed" || mode == "cleanup") && !errors.Is(err, fault) {
				t.Fatal("native reset mail read lost its explicit cause", err)
			}
			if (mode == "cancel" || mode == "canceled_before") && !errors.Is(err, context.Canceled) {
				t.Fatal("reset mail cancellation cause was lost", err)
			}
			deliveries, snapshotErr := memory.Snapshot()
			if snapshotErr != nil || len(deliveries) != 0 || reads != wantReads || rendered != 0 || f.owner.hasher.hashes.Load() != 0 || f.owner.hasher.verifies.Load() != 0 || f.owner.boundary.calls != 0 || f.entropy.count() != entropy {
				t.Fatal("refused reset mail request performed work or published a prefix", snapshotErr, reads, wantReads)
			}
			assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
			selfPasswordPrivate(t, err, fault.Error(), f.target.Email, f.target.EncodedPassword)
		})
	}
}

func runResetMailNative(t *testing.T, open Open) {
	t.Run("password_reset_mail_delivery", func(t *testing.T) { passwordResetMailDelivery(t, open) })
	t.Run("password_reset_mail_failures", func(t *testing.T) { passwordResetMailFailures(t, open) })
	t.Run("password_reset_mail_read_failures", func(t *testing.T) { passwordResetMailReadFailures(t, open) })
}
