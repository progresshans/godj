package accountstoretest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/modeldef"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type overlayFunc func(context.Context, auth.Principal, auth.Permission) (bool, error)

func (f overlayFunc) Allowed(ctx context.Context, p auth.Principal, permission auth.Permission) (bool, error) {
	return f(ctx, p, permission)
}

type authorityProbe struct {
	db.Queryer
	validator    db.SessionValidator
	accountReads int
}

func (p *authorityProbe) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	if plan.Table() == (models.UserDescriptor{}).Metadata().DBTable {
		p.accountReads++
	}
	return p.Queryer.Query(ctx, plan)
}
func (p *authorityProbe) ValidateSession(ctx context.Context) error {
	return p.validator.ValidateSession(ctx)
}

func authoritativeActor(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	writer := reopen(t)
	authenticator, err := accountmodel.NewAuthenticator(t.Context(), f.store, f.hasher)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := authenticator.Authenticate(t.Context(), "alice", password)
	if err != nil {
		t.Fatal(err)
	}
	actor := credential.Principal()
	denied := errors.New("account action denied")
	calls := 0
	overlay := overlayFunc(func(ctx context.Context, current auth.Principal, permission auth.Permission) (bool, error) {
		calls++
		if current.ID() != actor.ID() || !current.Has(permission) {
			return false, errors.New("overlay did not receive current grants")
		}
		return true, nil
	})
	write := func(claim auth.Principal, firstName string) error {
		return backend.CoordinatedAtomic(t.Context(), func(tx db.Session) error {
			current, allowed, err := f.store.AuthorizeIn(t.Context(), tx, claim, overlay, "account.direct", "account.team")
			if err != nil {
				return err
			}
			if !allowed {
				if current.ID() != "" {
					return errors.New("denied call leaked principal")
				}
				return denied
			}
			row, found, err := models.UserObjects.Using(tx).Filter(models.UserFields.ID.Exact(f.user.ID)).OrderBy(models.UserFields.ID.Asc()).First(t.Context())
			if err != nil {
				return err
			}
			if !found {
				return errors.New("authorized account disappeared")
			}
			_, err = models.UserObjects.Patch(t.Context(), tx, row, models.UserPatch{}.WithFirstName(firstName).WithRevision(row.Revision+1))
			return err
		})
	}
	if err := write(actor, "Committed"); err != nil || calls != 2 {
		t.Fatal("authorized native write", err, calls)
	}
	rollbackFault := errors.New("failure after authorized mutation")
	if err := backend.CoordinatedAtomic(t.Context(), func(tx db.Session) error {
		_, allowed, err := f.store.AuthorizeIn(t.Context(), tx, actor, overlay, "account.direct")
		if err != nil {
			return err
		}
		if !allowed {
			return denied
		}
		if _, err := models.UserObjects.Patch(t.Context(), tx, f.user, models.UserPatch{}.WithFirstName("Rolled back").WithRevision(3)); err != nil {
			return err
		}
		return rollbackFault
	}); !errors.Is(err, rollbackFault) {
		t.Fatal("authorized callback failure did not propagate", err)
	}
	if count, err := models.UserPermissionsLinkObjects.Delete(t.Context(), writer, &f.direct); err != nil || count != 1 {
		t.Fatal(err)
	}
	calls = 0
	if !actor.Has("account.direct") {
		t.Fatal("old grant snapshot was not retained for the negative control")
	}
	if err := write(actor, "Unauthorized"); !errors.Is(err, denied) || calls != 0 {
		t.Fatal("old grants or overlay regranted missing stored permission", err, calls)
	}
	row, found, err := models.UserObjects.Using(backend).Filter(models.UserFields.ID.Exact(f.user.ID)).OrderBy(models.UserFields.ID.Asc()).First(t.Context())
	if err != nil || !found || row.FirstName != "Committed" || row.Revision != 2 {
		t.Fatal("denied mutation changed durable profile", err)
	}
	// Restore the grant so only domain ownership can deny the next old actor.
	if _, err := models.UserPermissionsLinkObjects.Create(t.Context(), writer, models.NewUserPermissionsLinkCreate(f.user.ID, f.grants[0].ID)); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := f.binding.RoleFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("authorization domain transition"))
	change, err := domain.Prepare(t.Context(), fingerprint, hex.EncodeToString(digest[:]))
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.CoordinatedAtomic(t.Context(), func(tx db.Session) error {
		_, err := domain.TransitionIn(t.Context(), tx, f.ownership, change, joined.Add(time.Hour))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	unbound, err := auth.NewPrincipal(auth.PrincipalConfig{ID: actor.ID(), Active: true, Superuser: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, claim := range []auth.Principal{actor, unbound} {
		if err := backend.CoordinatedAtomic(t.Context(), func(tx db.Session) error {
			probe := &authorityProbe{Queryer: tx, validator: tx.(db.SessionValidator)}
			current, allowed, err := f.store.AuthorizeIn(t.Context(), probe, claim, overlay, "account.direct")
			if err != nil || allowed || current.ID() != "" || probe.accountReads != 0 {
				return errors.New("old or unbound domain resolved a coincident account ID")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	fresh, err := authenticator.Authenticate(t.Context(), "alice", password)
	if err != nil {
		t.Fatal(err)
	}
	calls = 0
	if err := write(fresh.Principal(), "Fresh domain"); err != nil || calls != 2 {
		t.Fatal("fresh domain authority rejected", err, calls)
	}
	if _, err := models.UserObjects.Patch(t.Context(), writer, row, models.UserPatch{}.WithActive(false).WithRevision(4)); err != nil {
		t.Fatal(err)
	}
	calls = 0
	if err := write(fresh.Principal(), "Inactive write"); !errors.Is(err, denied) || calls != 0 {
		t.Fatal("inactive current account retained authority", err, calls)
	}
}

func authorizationScopeAndErrors(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	credential, found, err := f.store.CredentialByID(t.Context(), f.user.PrincipalID)
	if err != nil || !found {
		t.Fatal(err)
	}
	actor := credential.Principal()
	fault := errors.New("private authorization source secret")
	for _, scenario := range []string{"deny", "failure", "false_with_error", "cancel", "current_grants"} {
		ctx, cancel := context.WithCancel(t.Context())
		err := backend.ReadSnapshot(ctx, func(reader db.Queryer) error {
			calls := 0
			overlay := overlayFunc(func(ctx context.Context, current auth.Principal, _ auth.Permission) (bool, error) {
				calls++
				if !reflect.DeepEqual(current.Permissions(), []auth.Permission{"account.direct", "account.shared", "account.team"}) {
					return false, errors.New("overlay received a fabricated principal")
				}
				switch scenario {
				case "deny":
					return false, nil
				case "failure":
					return true, fault
				case "false_with_error":
					return false, fmt.Errorf("wrapped: %w", fault)
				case "cancel":
					cancel()
				}
				return true, nil
			})
			current, allowed, err := f.store.AuthorizeIn(ctx, reader, actor, overlay, "account.team")
			if calls != 1 {
				return errors.New("overlay invocation count differs")
			}
			switch scenario {
			case "current_grants":
				if err != nil || !allowed || current.ID() != actor.ID() {
					return errors.New("current authority rejected")
				}
			case "deny":
				if err != nil || allowed || current.ID() != "" {
					return errors.New("definite overlay denial changed meaning")
				}
			case "failure", "false_with_error":
				if !errors.Is(err, fault) || allowed || current.ID() != "" || strings.Contains(fmt.Sprintf("%+v", err), "private") {
					return errors.New("overlay error became denial or leaked private cause")
				}
			case "cancel":
				if !errors.Is(err, context.Canceled) || allowed || current.ID() != "" {
					return errors.New("overlay cancellation lost")
				}
			}
			return nil
		})
		cancel()
		if scenario == "cancel" {
			if !errors.Is(err, context.Canceled) {
				t.Fatal("native cancelled scope", err)
			}
		} else if err != nil {
			t.Fatal(scenario, err)
		}
	}
	// A selected-role mismatch is an execution/configuration failure, not a
	// permission denial that can fall through to another alternative.
	role := modeldef.AccountRole()
	role.AuthenticateNormalization = ir.AccountUnicode16NFKC
	binding, err := accountmodel.Bind(role, models.UserDescriptor{}, models.GoDjRelationSchema())
	if err != nil {
		t.Fatal(err)
	}
	other, err := accountmodel.NewCredentialStore(backend, binding, models.GroupDescriptor{}, models.PermissionDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.ReadSnapshot(t.Context(), func(reader db.Queryer) error {
		current, allowed, err := other.AuthorizeIn(t.Context(), reader, actor, auth.PrincipalAuthorizer{}, "account.team")
		if !errors.Is(err, domain.ErrMismatch) || allowed || current.ID() != "" {
			return errors.New("selected-role mismatch became a permission decision")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var expired db.Queryer
	if err := backend.ReadSnapshot(t.Context(), func(reader db.Queryer) error { expired = reader; return nil }); err != nil {
		t.Fatal(err)
	}
	for _, reader := range []db.Queryer{nil, backend, expired} {
		current, allowed, err := f.store.AuthorizeIn(t.Context(), reader, actor, auth.PrincipalAuthorizer{}, "account.team")
		if err == nil || allowed || current.ID() != "" {
			t.Fatal("missing, root or expired scope authorized")
		}
	}
}
