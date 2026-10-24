package accountstoretest

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

type creationSnapshotBackend struct {
	Backend
	read snapshotFunc
}

func (owner *creationSnapshotBackend) ReadSnapshot(ctx context.Context, read func(db.Queryer) error) error {
	return owner.read.ReadSnapshot(ctx, read)
}

func creationReadFailures(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	actor := profileActor(t, f).Principal()
	for _, mode := range []string{"skipped", "nil", "typed_nil", "root", "repeated", "query", "close", "swallowed", "cleanup", "cancel", "denial_cleanup", "validation_cleanup"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fault := errors.New("private creation read failure")
			owner := &creationSnapshotBackend{Backend: backend, read: snapshotFunc(func(ctx context.Context, observe func(db.Queryer) error) error {
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
				if strings.HasSuffix(mode, "cleanup") {
					return errors.Join(err, fault)
				}
				if mode == "cancel" {
					cancel()
				}
				return err
			})}
			config := creationConfig()
			input := creationInput(t, "read-"+mode)
			if mode == "denial_cleanup" {
				config.AdditionalPermissions = []auth.Permission{"account.absent"}
			}
			if mode == "validation_cleanup" {
				input = creationInput(t, f.user.Username)
			}
			hasher := &creationHasher{PasswordHasher: f.hasher}
			creator := accountCreator(t, owner, hasher, config)
			result, err := creator.Prepare(ctx, actor, input, password)
			requireProfileCode(t, err, accountmodel.CodePersistence)
			if !reflect.DeepEqual(result, accountmodel.PreparedCreation[models.User]{}) || hasher.calls.Load() != 0 {
				t.Fatal("failed snapshot published candidate or hashed")
			}
			if _, rejected := validation.Rejected(err); rejected {
				t.Fatal("cleanup failure became input rejection")
			}
			if mode == "query" || mode == "close" || mode == "swallowed" || strings.HasSuffix(mode, "cleanup") {
				if !errors.Is(err, fault) {
					t.Fatal("read cause lost", err)
				}
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("read cancellation lost")
			}
		})
	}
}
