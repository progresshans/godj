package accountstoretest

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/validation"
)

func passwordResetReadFailures(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	if _, err := models.UserObjects.Patch(t.Context(), backend, f.user, models.UserPatch{}.WithEmail("member@example.test")); err != nil {
		t.Fatal(err)
	}
	now := joined
	base := passwordComposition(t, backend, &now, f.hasher, accountmodel.PasswordChangeConfig[models.User]{})
	baseOwner := selectedResetOwner(t, base, selectedResetConfig(t, &now))
	token := selectedResetToken(t, baseOwner, f.user.PrincipalID)
	proof := selectedResetProof(t, baseOwner, sessions.Record{}, token)
	for _, test := range []struct {
		name  string
		stage int
	}{
		{"issue", 1}, {"mail", 1}, {"entry_account", 1}, {"entry_session", 2}, {"check_session", 1}, {"check_account", 2}, {"finish_session", 1}, {"finish_account", 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, mode := range []string{"skipped", "nil", "typed_nil", "root", "repeated", "query", "close", "swallowed", "cleanup", "cancel"} {
				t.Run(mode, func(t *testing.T) {
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					fault := errors.New("private reset read failure")
					reads := 0
					readOwner := &creationSnapshotBackend{Backend: backend}
					readOwner.read = snapshotFunc(func(ctx context.Context, observe func(db.Queryer) error) error {
						reads++
						if reads != test.stage {
							return backend.ReadSnapshot(ctx, observe)
						}
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
							probe := &profileReadProbe{Queryer: scope, validator: scope.(db.SessionValidator)}
							if mode == "query" || mode == "close" || mode == "swallowed" {
								probe.fault = fault
								probe.closeFail = mode == "close"
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
					tracked := &passwordScopeOwner{Backend: readOwner}
					hasher := &passwordChangeHasher{PasswordHasher: f.hasher, scopes: &tracked.scopes}
					configured := passwordComposition(t, tracked, &now, hasher, accountmodel.PasswordChangeConfig[models.User]{})
					owner := selectedResetOwner(t, configured, selectedResetConfig(t, &now))
					hasher.hashes.Store(0)
					before := passwordState(t, backend)
					var err error
					switch test.name {
					case "issue":
						var issued auth.PasswordResetToken
						issued, err = owner.IssueToken(ctx, f.user.PrincipalID)
						if issued.Encoded() != "" || issued.PrincipalID() != "" || issued.Username() != "" || issued.Email() != "" {
							t.Fatal("failed read published a token")
						}
					case "mail":
						var issued []auth.PasswordResetToken
						issued, err = owner.PasswordResetCandidates(ctx, "member@example.test")
						if len(issued) != 0 {
							t.Fatal("failed candidate read published a prefix")
						}
					case "entry_account", "entry_session":
						var result sessions.Record
						result, err = owner.StartPasswordReset(ctx, proof.ID(), f.user.PrincipalID, token.Encoded())
						if !reflect.DeepEqual(result, sessions.Record{}) {
							t.Fatal("failed entry read published a proof")
						}
					case "check_session", "check_account":
						err = owner.CheckPasswordReset(ctx, proof.ID(), f.user.PrincipalID, nil)
					case "finish_session", "finish_account":
						var result auth.PasswordResetResult
						result, err = owner.ResetPassword(ctx, proof.ID(), f.user.PrincipalID, password+"-reset")
						selectedResetNoResult(t, result, err)
					}
					requireProfileCode(t, err, accountmodel.CodePersistence)
					if _, rejected := validation.Rejected(err); rejected || err == auth.ErrInvalidResetProof || err == auth.ErrInvalidResetToken {
						t.Fatal("reset read failure became input or identity denial")
					}
					if reads != test.stage || hasher.hashes.Load() != 0 || tracked.writes.Load() != 0 {
						t.Fatal("read failure progressed or retried", reads, test.stage)
					}
					if (mode == "query" || mode == "close" || mode == "swallowed" || mode == "cleanup") && !errors.Is(err, fault) {
						t.Fatal("read failure cause was lost", err)
					}
					if mode == "cancel" && !errors.Is(err, context.Canceled) {
						t.Fatal("read cancellation was lost", err)
					}
					passwordUnchanged(t, reopen(t), before)
				})
			}
		})
	}
	for _, mode := range []string{"absent_cleanup", "inactive_cleanup"} {
		t.Run(mode, func(t *testing.T) {
			target := roleTarget(t, backend, f, "reset-read-"+mode)
			token := selectedResetToken(t, baseOwner, target.PrincipalID)
			proof := selectedResetProof(t, baseOwner, sessions.Record{}, token)
			principalID := target.PrincipalID
			if mode == "absent_cleanup" {
				if _, err := models.UserObjects.Delete(t.Context(), backend, &target); err != nil {
					t.Fatal(err)
				}
			} else if _, err := models.UserObjects.Patch(t.Context(), backend, target, models.UserPatch{}.WithActive(false)); err != nil {
				t.Fatal(err)
			}
			reads := 0
			fault := errors.New("private refused reset cleanup failure")
			readOwner := &creationSnapshotBackend{Backend: backend}
			readOwner.read = snapshotFunc(func(ctx context.Context, observe func(db.Queryer) error) error {
				reads++
				err := backend.ReadSnapshot(ctx, observe)
				if reads == 2 {
					return errors.Join(err, fault)
				}
				return err
			})
			tracked := &passwordScopeOwner{Backend: readOwner}
			hasher := &passwordChangeHasher{PasswordHasher: f.hasher, scopes: &tracked.scopes}
			configured := passwordComposition(t, tracked, &now, hasher, accountmodel.PasswordChangeConfig[models.User]{})
			owner := selectedResetOwner(t, configured, selectedResetConfig(t, &now))
			hasher.hashes.Store(0)
			before := passwordState(t, backend)
			result, err := owner.ResetPassword(t.Context(), proof.ID(), principalID, password+"-reset")
			selectedResetNoResult(t, result, err)
			requireProfileCode(t, err, accountmodel.CodePersistence)
			if !errors.Is(err, fault) || reads != 2 || hasher.hashes.Load() != 0 || tracked.writes.Load() != 0 {
				t.Fatal("cleanup failure was hidden by target refusal", err)
			}
			passwordUnchanged(t, reopen(t), before)
		})
	}
}
