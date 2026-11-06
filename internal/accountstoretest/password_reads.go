package accountstoretest

import (
	"context"
	"errors"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

func passwordReadFailures(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	now := joined
	_, manager, _ := accountSessions(t, backend, &now)
	current := passwordSession(t, manager, profileActor(t, f), "failed-reads")
	for stage, name := range []string{"session", "account", "confirmation", "confirmation_recheck"} {
		t.Run(name, func(t *testing.T) {
			for _, mode := range []string{"skipped", "nil", "typed_nil", "root", "repeated", "query", "close", "swallowed", "cleanup", "cancel"} {
				t.Run(mode, func(t *testing.T) {
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					fault := errors.New("private password snapshot failure")
					reads := 0
					readOwner := &creationSnapshotBackend{Backend: backend}
					readOwner.read = snapshotFunc(func(ctx context.Context, observe func(db.Queryer) error) error {
						reads++
						if reads != stage+1 {
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
					tracked := &passwordScopeOwner{Backend: readOwner}
					hasher := &passwordChangeHasher{PasswordHasher: f.hasher, scopes: &tracked.scopes}
					configured := passwordComposition(t, tracked, &now, hasher, accountmodel.PasswordChangeConfig[models.User]{})
					hasher.hashes.Store(0)
					before := passwordState(t, backend)
					result, err := configured.owner.ChangePassword(ctx, current.ID(), password, password+"-new")
					passwordNoResult(t, result, err)
					requireProfileCode(t, err, accountmodel.CodePersistence)
					if _, rejected := validation.Rejected(err); rejected || err == auth.ErrInvalidCredentials {
						t.Fatal("password read failure became input or admission denial")
					}
					if reads != stage+1 || hasher.hashes.Load() != 0 || tracked.writes.Load() != 0 {
						t.Fatal("read failure progressed past failed native scope", reads, stage+1)
					}
					if (mode == "query" || mode == "close" || mode == "swallowed" || mode == "cleanup") && !errors.Is(err, fault) {
						t.Fatal("snapshot cleanup cause lost", err)
					}
					if mode == "cancel" && !errors.Is(err, context.Canceled) {
						t.Fatal("snapshot cancellation lost", err)
					}
					passwordUnchanged(t, reopen(t), before)
				})
			}
		})
	}
}
