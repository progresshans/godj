package accountstoretest

import (
	"context"
	"encoding/base64"
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
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	identityreference "github.com/progresshans/godj/internal/identitytest/testdata"
	"github.com/progresshans/godj/mail"
	"github.com/progresshans/godj/sessions"
)

func selectedResetMailer(t *testing.T, owner *accountmodel.PasswordResetter[models.User], sender mail.Sender, renderer identity.PasswordResetMailRenderer) *identity.PasswordResetMailer {
	t.Helper()
	from, err := mail.ParseAddress("sender@example.test")
	if err != nil {
		t.Fatal(err)
	}
	config := identity.PasswordResetMailConfig{From: from, Origin: "https://reset.example.test", ConfirmPath: "/account/reset", SiteName: "Selected", Renderer: renderer}
	mailer, err := identity.NewPasswordResetMailer(owner, sender, config)
	if err != nil {
		t.Fatal(err)
	}
	config.Origin, config.ConfirmPath, config.SiteName = "https://wrong.example.test", "/wrong", "Changed"
	config.From, config.Renderer = mail.Address{}, nil
	return mailer
}
func selectedResetMemory(t *testing.T) *mail.Memory {
	t.Helper()
	memory, err := mail.NewMemory(mail.MemoryConfig{MaximumMessages: auth.MaximumPasswordResetCandidates + 1})
	if err != nil {
		t.Fatal(err)
	}
	return memory
}
func selectedResetDelivery(t *testing.T, message mail.Message) (string, string) {
	t.Helper()
	for _, line := range strings.Split(message.Text(), "\n") {
		if !strings.HasPrefix(line, "https://reset.example.test/account/reset/") {
			continue
		}
		link, err := url.Parse(line)
		if err != nil || link.Scheme != "https" || link.Host != "reset.example.test" || link.User != nil || link.RawQuery != "" || link.Fragment != "" {
			t.Fatal("selected reset link escaped trusted origin", err)
		}
		parts := strings.Split(link.Path, "/")
		if len(parts) != 6 || parts[1] != "account" || parts[2] != "reset" || parts[5] != "" {
			t.Fatal("selected reset link is malformed")
		}
		id, err := base64.RawURLEncoding.Strict().DecodeString(parts[3])
		if err != nil || base64.RawURLEncoding.EncodeToString(id) != parts[3] {
			t.Fatal("selected reset link lost principal encoding", err)
		}
		return string(id), parts[4]
	}
	t.Fatal("selected reset message omitted recipient link")
	return "", ""
}

func passwordResetMail(t *testing.T, referenceBackend string, backend Backend, reopen func(*testing.T) Backend) {
	host := provisionUserReference(t, backend)
	f := host.base
	now := joined
	tracked := &passwordScopeOwner{Backend: backend}
	hasher := &passwordChangeHasher{PasswordHasher: f.hasher, scopes: &tracked.scopes}
	configured := passwordComposition(t, tracked, &now, hasher, accountmodel.PasswordChangeConfig[models.User]{})
	owner := selectedResetOwner(t, configured, selectedResetConfig(t, &now))
	observations, err := identityreference.ResetRequests(referenceBackend)
	if err != nil {
		t.Fatal(err)
	}
	for _, observation := range observations {
		t.Run(observation.Label, func(t *testing.T) {
			accounts := map[string]models.User{}
			for i, input := range observation.Accounts {
				encoded := f.user.EncodedPassword
				if !input.Usable {
					encoded = "!explicit-disabled-candidate"
				}
				row, err := models.UserObjects.Create(t.Context(), backend, models.NewUserCreate(fmt.Sprintf("selected-candidate-%d", i), input.Username, encoded, joined).WithEmail(input.Email).WithActive(input.Active))
				if err != nil {
					t.Fatal(err)
				}
				accounts[row.PrincipalID] = row
			}
			defer func() {
				for _, row := range accounts {
					if _, err := models.UserObjects.Delete(t.Context(), backend, &row); err != nil {
						t.Error(err)
					}
				}
			}()
			cleaned, errors := auth.CleanPasswordResetEmail(observation.Input)
			if errors.Empty() != observation.Valid || cleaned != observation.Cleaned || !observation.UsersUnchanged {
				t.Fatal("selected reset input differs from pinned Django reference")
			}
			before := userReferenceSnapshot(t, reopen(t))
			memory := selectedResetMemory(t)
			hasher.hashes.Store(0)
			hasher.verifies.Store(0)
			tracked.writes.Store(0)
			if err := selectedResetMailer(t, owner, memory, nil).Request(t.Context(), observation.Input); err != nil {
				t.Fatal("selected native mail request failed", err)
			}
			messages, err := memory.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			actual := []string{}
			for _, delivery := range messages {
				message := delivery.Message()
				principalID, encoded := selectedResetDelivery(t, message)
				row, found := accounts[principalID]
				if !found {
					t.Fatal("selected mail inherited an unrelated account")
				}
				expected := selectedResetToken(t, owner, principalID)
				address, err := mail.ParseAddress(row.Email)
				if err != nil || expected.Encoded() != encoded || len(message.Recipients()) != 1 || message.Recipients()[0].Mailbox() != address.Mailbox() || message.From().Mailbox() != "sender@example.test" {
					t.Fatal("selected recipient/signature binding differs", err)
				}
				if message.Subject() != "Password reset on Selected" || !strings.Contains(message.Text(), "Selected") {
					t.Fatal("selected reset mail lost its owned content configuration")
				}
				actual = append(actual, row.Username)
			}
			slices.Sort(actual)
			if !slices.Equal(actual, observation.Delivered) || !slices.Equal(actual, observation.Selected) || hasher.hashes.Load() != 0 || hasher.verifies.Load() != 0 || tracked.writes.Load() != 0 {
				t.Fatal("selected native candidate set differs from Django", actual, observation.Delivered)
			}
			assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), host.actor.Principal())
		})
	}
}

func passwordResetMailBound(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	host := provisionUserReference(t, backend)
	f := host.base
	now := joined
	tracked := &passwordScopeOwner{Backend: backend}
	hasher := &passwordChangeHasher{PasswordHasher: f.hasher, scopes: &tracked.scopes}
	configured := passwordComposition(t, tracked, &now, hasher, accountmodel.PasswordChangeConfig[models.User]{})
	owner := selectedResetOwner(t, configured, selectedResetConfig(t, &now))
	const email = "bounded@candidate.test"
	rows := make([]models.User, auth.MaximumPasswordResetCandidates)
	for i := range rows {
		var err error
		rows[i], err = models.UserObjects.Create(t.Context(), backend, models.NewUserCreate(fmt.Sprintf("reset-bound-%d", i), fmt.Sprintf("reset-bound-%d", i), f.user.EncodedPassword, joined).WithEmail(email))
		if err != nil {
			t.Fatal(err)
		}
	}
	memory := selectedResetMemory(t)
	mailer := selectedResetMailer(t, owner, memory, nil)
	hasher.hashes.Store(0)
	t.Run("inclusive_256", func(t *testing.T) {
		before := userReferenceSnapshot(t, reopen(t))
		if err := mailer.Request(t.Context(), email); err != nil {
			t.Fatal("inclusive selected candidate bound refused", err)
		}
		messages, err := memory.Drain()
		if err != nil || len(messages) != 256 {
			t.Fatal("selected bounded delivery set was truncated", err, len(messages))
		}
		for index, delivery := range messages {
			id, _ := selectedResetDelivery(t, delivery.Message())
			if id != rows[index].PrincipalID || len(delivery.Message().Recipients()) != 1 || delivery.Message().Recipients()[0].Mailbox() != email {
				t.Fatal("inclusive candidate delivery was duplicated, reordered or misdirected")
			}
		}
		if tracked.writes.Load() != 0 || hasher.hashes.Load() != 0 || hasher.verifies.Load() != 0 {
			t.Fatal("inclusive mail candidate read wrote state or performed password work")
		}
		assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), host.actor.Principal())
	})
	if _, err := models.UserObjects.Create(t.Context(), backend, models.NewUserCreate("reset-bound-overflow", "reset-bound-overflow", "!disabled", joined).WithEmail(email)); err != nil {
		t.Fatal(err)
	}
	t.Run("overflow_before_usable_filter", func(t *testing.T) {
		before := userReferenceSnapshot(t, reopen(t))
		err := mailer.Request(t.Context(), email)
		requireProfileCode(t, err, accountmodel.CodePersistence)
		if messages, err := memory.Snapshot(); err != nil || len(messages) != 0 {
			t.Fatal("overflow sent a usable prefix", err)
		}
		if tracked.writes.Load() != 0 || hasher.hashes.Load() != 0 || hasher.verifies.Load() != 0 {
			t.Fatal("overflow mail candidate read wrote state or performed password work")
		}
		assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), host.actor.Principal())
	})
}

type resetSnapshotBoundary struct {
	Backend
	inside bool
	after  func() error
}

func (owner *resetSnapshotBoundary) ReadSnapshot(ctx context.Context, callback func(db.Queryer) error) error {
	err := owner.Backend.ReadSnapshot(ctx, func(scope db.Queryer) error {
		owner.inside = true
		defer func() { owner.inside = false }()
		return callback(scope)
	})
	if err == nil && owner.after != nil {
		after := owner.after
		owner.after = nil
		return after()
	}
	return err
}

func passwordResetMailSnapshot(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	host := provisionUserReference(t, backend)
	f := host.base
	now := joined
	for _, mode := range []string{"email", "login", "last_login", "inactive", "credential", "profile"} {
		t.Run(mode, func(t *testing.T) {
			target := roleTarget(t, backend, f, "reset-snapshot-"+mode)
			target, err := models.UserObjects.Patch(t.Context(), backend, target, models.UserPatch{}.WithEmail(mode+"@candidate.test"))
			if err != nil {
				t.Fatal(err)
			}
			boundary := &resetSnapshotBoundary{Backend: backend}
			tracked := &passwordScopeOwner{Backend: boundary}
			hasher := &passwordChangeHasher{PasswordHasher: f.hasher, scopes: &tracked.scopes}
			configured := passwordComposition(t, tracked, &now, hasher, accountmodel.PasswordChangeConfig[models.User]{})
			owner := selectedResetOwner(t, configured, selectedResetConfig(t, &now))
			expected := selectedResetToken(t, owner, target.PrincipalID)
			other := reopen(t)
			var checkpoint userReferenceState
			boundary.after = func() error {
				before := userReferenceSnapshot(t, other)
				row := target
				patch := models.UserPatch{}.WithRevision(7)
				row.Revision = 7
				switch mode {
				case "email":
					patch = patch.WithEmail("replacement@candidate.test")
					row.Email = "replacement@candidate.test"
				case "login":
					patch = patch.WithUsername("replacement-login")
					row.Username = "replacement-login"
				case "last_login":
					at := now.Add(time.Microsecond)
					patch, row.LastLogin = patch.WithLastLogin(at), &at
				case "inactive":
					patch = patch.WithActive(false)
					row.Active = false
				case "credential":
					encoded, err := f.hasher.Hash(t.Context(), selfPassword)
					if err != nil {
						return err
					}
					patch, row.EncodedPassword = patch.WithEncodedPassword(encoded), encoded
				case "profile":
					patch, row.FirstName = patch.WithFirstName("Latest"), "Latest"
				}
				_, err := models.UserObjects.Patch(t.Context(), other, target, patch)
				if err == nil {
					checkpoint = userReferenceSnapshot(t, other)
					userReferenceReplace(&before.account, row)
					assertUserReferenceState(t, before, checkpoint, host.actor.Principal())
				}
				return err
			}
			rendered := 0
			renderer := identity.PasswordResetMailRendererFunc(func(_ context.Context, data identity.PasswordResetMail) (identity.PasswordResetMailContent, error) {
				rendered++
				if boundary.inside || data.Username() != target.Username {
					t.Fatal("mail renderer retained DB scope or consumed another account snapshot")
				}
				return identity.PasswordResetMailContent{Subject: "Selected", Text: data.Link()}, nil
			})
			memory := selectedResetMemory(t)
			hasher.hashes.Store(0)
			if err := selectedResetMailer(t, owner, memory, renderer).Request(t.Context(), target.Email); err != nil {
				t.Fatal(err)
			}
			messages, err := memory.Snapshot()
			if err != nil || rendered != 1 || len(messages) != 1 {
				t.Fatal("selected snapshot delivery missing", err)
			}
			message := messages[0].Message()
			id, encoded := selectedResetDelivery(t, message)
			if id != expected.PrincipalID() || encoded != expected.Encoded() || message.Recipients()[0].Mailbox() != target.Email {
				t.Fatal("mail mixed old token and current recipient")
			}
			if hasher.hashes.Load() != 0 || hasher.verifies.Load() != 0 || tracked.writes.Load() != 0 {
				t.Fatal("snapshot-bound mail hashed, verified or wrote native state")
			}
			assertUserReferenceState(t, checkpoint, userReferenceSnapshot(t, reopen(t)), host.actor.Principal())
			proof, err := owner.StartPasswordReset(t.Context(), sessions.ID{}, id, encoded)
			if mode == "login" || mode == "profile" {
				if err != nil || !proof.ID().Valid() {
					t.Fatal("unsigned profile/login change invalidated reset", err)
				}
			} else {
				if err != auth.ErrInvalidResetProof || !reflect.DeepEqual(proof, sessions.Record{}) {
					t.Fatal("old delivery accepted after signed observation changed", err)
				}
				assertUserReferenceState(t, checkpoint, userReferenceSnapshot(t, reopen(t)), host.actor.Principal())
			}
		})
	}
}
