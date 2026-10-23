// Package accountstoretest runs the production account store and generated
// factory against independent native SQLite and PostgreSQL connections.
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
	"github.com/progresshans/godj/identity"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/modeldef"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/migrations"
	mb "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type Backend interface {
	db.Session
	db.CoordinatedAtomic
	db.SnapshotReader
	mb.RevisionFencedBackend
}
type Open func(*testing.T) (Backend, func(*testing.T) Backend)

func Run(t *testing.T, open Open) {
	for _, test := range []struct {
		name string
		run  func(*testing.T, Backend, func(*testing.T) Backend)
	}{
		{"permission_union_and_freshness", permissionUnion},
		{"coherent_snapshot", coherentSnapshot},
		{"password_recheck", passwordRecheck},
		{"read_contract_and_cleanup", readContract},
		{"invalid_stored_grants", invalidGrants},
		{"permission_bound", permissionBound},
		{"zero_and_negative_keys", nonpositiveKeys},
		{"selected_login_policy", loginPolicy},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend, reopen := open(t)
			sources := append(identity.MigrationSources(), domain.MigrationSources()...)
			loaded, _, err := definition.Load(sources...)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := (migrations.Executor{Backend: backend}).Migrate(t.Context(), loaded, migrations.LatestLifecycleRequest()); err != nil {
				t.Fatal(err)
			}
			test.run(t, backend, reopen)
		})
	}
}

const password = "native-selected-account-password"

var joined = time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)

type fixture struct {
	binding   accountmodel.Binding[models.User]
	store     *accountmodel.CredentialStore[models.User]
	hasher    *auth.PBKDF2
	user      models.User
	grants    []models.Permission
	group     models.Group
	direct    models.UserPermissionsLink
	ownership domain.Receipt
}

func provision(t *testing.T, backend Backend, normalization ir.AccountNormalization) *fixture {
	t.Helper()
	role := modeldef.AccountRole()
	role.AuthenticateNormalization = normalization
	binding, err := accountmodel.Bind(role, models.UserDescriptor{}, models.GoDjRelationSchema())
	if err != nil {
		t.Fatal(err)
	}
	store, err := accountmodel.NewCredentialStore(backend, binding, models.GroupDescriptor{}, models.PermissionDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := hasher.Hash(t.Context(), password)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := binding.RoleFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	evidence := sha256.Sum256([]byte("native account store provision"))
	change, err := domain.Prepare(t.Context(), fingerprint, hex.EncodeToString(evidence[:]))
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{binding: binding, store: store, hasher: hasher}
	if err := backend.CoordinatedAtomic(t.Context(), func(tx db.Session) error {
		var err error
		f.ownership, err = domain.InitializeIn(t.Context(), tx, change, joined)
		if err != nil {
			return err
		}
		f.user, err = models.UserObjects.Create(t.Context(), tx, models.NewUserCreate("principal-1", "alice", encoded, joined))
		if err != nil {
			return err
		}
		f.group, err = models.GroupObjects.Create(t.Context(), tx, models.NewGroupCreate("operators"))
		if err != nil {
			return err
		}
		for _, code := range []string{"account.direct", "account.shared", "account.team"} {
			row, err := models.PermissionObjects.Create(t.Context(), tx, models.NewPermissionCreate(code, code))
			if err != nil {
				return err
			}
			f.grants = append(f.grants, row)
		}
		f.direct, err = models.UserPermissionsLinkObjects.Create(t.Context(), tx, models.NewUserPermissionsLinkCreate(f.user.ID, f.grants[0].ID))
		if err != nil {
			return err
		}
		if _, err := models.UserPermissionsLinkObjects.Create(t.Context(), tx, models.NewUserPermissionsLinkCreate(f.user.ID, f.grants[1].ID)); err != nil {
			return err
		}
		if _, err := models.UserGroupsLinkObjects.Create(t.Context(), tx, models.NewUserGroupsLinkCreate(f.user.ID, f.group.ID)); err != nil {
			return err
		}
		for _, grant := range f.grants[1:] {
			if _, err := models.GroupPermissionsLinkObjects.Create(t.Context(), tx, models.NewGroupPermissionsLinkCreate(f.group.ID, grant.ID)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return f
}
func requireGrants(t *testing.T, credential auth.Credential, found bool, err error, want ...auth.Permission) {
	t.Helper()
	if err != nil || !found || !reflect.DeepEqual(credential.Principal().Permissions(), want) {
		t.Fatalf("permissions=%v found=%t error=%v, want=%v", credential.Principal().Permissions(), found, err, want)
	}
}
func permissionUnion(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	// A malformed, unassigned catalog entry cannot poison unrelated accounts.
	if _, err := models.PermissionObjects.Create(t.Context(), backend, models.NewPermissionCreate("unassigned", "Unassigned").WithRevision(0)); err != nil {
		t.Fatal(err)
	}
	store, err := project.NewAccountCredentialStore(backend)
	if err != nil {
		t.Fatal(err)
	}
	before, found, err := store.CredentialByUsername(t.Context(), "alice")
	requireGrants(t, before, found, err, "account.direct", "account.shared", "account.team")
	detached := before.Principal().Permissions()
	detached[0] = "foreign"
	writer := reopen(t)
	if count, err := models.UserPermissionsLinkObjects.Delete(t.Context(), writer, &f.direct); err != nil || count != 1 {
		t.Fatal("revoke direct grant", err)
	}
	after, found, err := store.CredentialByID(t.Context(), f.user.PrincipalID)
	requireGrants(t, after, found, err, "account.shared", "account.team")
	requireGrants(t, before, true, nil, "account.direct", "account.shared", "account.team")
	restarted, err := project.NewAccountCredentialStore(reopen(t))
	if err != nil {
		t.Fatal(err)
	}
	fresh, found, err := restarted.CredentialByID(t.Context(), f.user.PrincipalID)
	requireGrants(t, fresh, found, err, "account.shared", "account.team")
	missing, found, err := store.CredentialByUsername(t.Context(), "absent")
	if err != nil || found || missing.SessionStamp() != "" {
		t.Fatal("missing account", err)
	}
}

type snapshotFunc func(context.Context, func(db.Queryer) error) error

func (f snapshotFunc) ReadSnapshot(ctx context.Context, callback func(db.Queryer) error) error {
	return f(ctx, callback)
}

type observedReader struct {
	db.Queryer
	once   bool
	change func() error
}

func (r *observedReader) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	rows, err := r.Queryer.Query(ctx, plan)
	if err == nil && !r.once && plan.Table() == (models.UserDescriptor{}).Metadata().DBTable {
		r.once = true
		if changeErr := r.change(); changeErr != nil {
			if rows != nil {
				_ = rows.Close()
			}
			return nil, changeErr
		}
	}
	return rows, err
}
func coherentSnapshot(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	writer := reopen(t)
	changed := false
	scopes := snapshotFunc(func(ctx context.Context, callback func(db.Queryer) error) error {
		return backend.ReadSnapshot(ctx, func(reader db.Queryer) error {
			probe := &observedReader{Queryer: reader, change: func() error {
				changed = true
				return writer.CoordinatedAtomic(ctx, func(tx db.Session) error {
					if _, err := models.UserObjects.Patch(ctx, tx, f.user, models.UserPatch{}.WithActive(false).WithRevision(2)); err != nil {
						return err
					}
					_, err := models.PermissionObjects.Patch(ctx, tx, f.grants[0], models.PermissionPatch{}.WithCode("account.changed").WithRevision(2))
					return err
				})
			}}
			return callback(probe)
		})
	})
	store, err := accountmodel.NewCredentialStore(scopes, f.binding, models.GroupDescriptor{}, models.PermissionDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	before, found, err := store.CredentialByID(t.Context(), f.user.PrincipalID)
	requireGrants(t, before, found, err, "account.direct", "account.shared", "account.team")
	if !changed || !before.Principal().Active() {
		t.Fatal("account/permission snapshot mixed observations")
	}
	after, found, err := f.store.CredentialByID(t.Context(), f.user.PrincipalID)
	requireGrants(t, after, found, err, "account.changed", "account.shared", "account.team")
	if after.Principal().Active() {
		t.Fatal("fresh snapshot retained old account state")
	}
}

type probeHasher struct {
	auth.PasswordHasher
	reading     *bool
	afterVerify func() error
}

func (h probeHasher) Hash(ctx context.Context, raw string) (string, error) {
	if *h.reading {
		return "", errors.New("hash retained native snapshot")
	}
	return h.PasswordHasher.Hash(ctx, raw)
}
func (h probeHasher) ValidateEncoded(encoded string) error {
	if *h.reading {
		return errors.New("hash validation retained native snapshot")
	}
	return h.PasswordHasher.ValidateEncoded(encoded)
}
func (h probeHasher) Verify(ctx context.Context, raw, encoded string) (bool, error) {
	if *h.reading {
		return false, errors.New("verification retained native snapshot")
	}
	valid, err := h.PasswordHasher.Verify(ctx, raw, encoded)
	if err == nil && valid && h.afterVerify != nil {
		err = h.afterVerify()
	}
	return valid, err
}
func passwordRecheck(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	writer := reopen(t)
	reading := false
	scopes := snapshotFunc(func(ctx context.Context, callback func(db.Queryer) error) error {
		return backend.ReadSnapshot(ctx, func(reader db.Queryer) error {
			reading = true
			defer func() { reading = false }()
			return callback(reader)
		})
	})
	revoked := false
	hasher := probeHasher{PasswordHasher: f.hasher, reading: &reading, afterVerify: func() error {
		if revoked {
			return nil
		}
		revoked = true
		_, err := models.UserPermissionsLinkObjects.Delete(t.Context(), writer, &f.direct)
		return err
	}}
	authenticator, err := project.NewAccountAuthenticator(t.Context(), scopes, hasher)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := authenticator.Authenticate(t.Context(), "alice", password)
	requireGrants(t, credential, err == nil, err, "account.shared", "account.team")
	if !revoked {
		t.Fatal("password recheck mutation was not executed")
	}
	fingerprint, err := f.binding.RoleFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	evidence := sha256.Sum256([]byte("new selected account domain"))
	change, err := domain.Prepare(t.Context(), fingerprint, hex.EncodeToString(evidence[:]))
	if err != nil {
		t.Fatal(err)
	}
	hasher.afterVerify = func() error {
		return writer.CoordinatedAtomic(t.Context(), func(tx db.Session) error {
			_, err := domain.TransitionIn(t.Context(), tx, f.ownership, change, joined.Add(time.Hour))
			return err
		})
	}
	authenticator, err = project.NewAccountAuthenticator(t.Context(), scopes, hasher)
	if err != nil {
		t.Fatal(err)
	}
	rejected, err := authenticator.Authenticate(t.Context(), "alice", password)
	if !errors.Is(err, auth.ErrInvalidCredentials) || rejected.SessionStamp() != "" {
		t.Fatal("domain change during password verification accepted", err)
	}
}

func readContract(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	fault := errors.New("private snapshot cleanup secret")
	for _, scenario := range []string{"cleanup", "twice", "zero", "nil", "typed_nil", "ignored_callback_error", "cancel"} {
		ctx, cancel := context.WithCancel(t.Context())
		scopes := snapshotFunc(func(ctx context.Context, callback func(db.Queryer) error) error {
			switch scenario {
			case "zero":
				return nil
			case "nil":
				_ = callback(nil)
				return nil
			case "typed_nil":
				var reader *observedReader
				_ = callback(reader)
				return nil
			case "ignored_callback_error":
				_ = callback(failedReader{fault})
				return nil
			}
			if err := backend.ReadSnapshot(ctx, callback); err != nil {
				return err
			}
			switch scenario {
			case "cleanup":
				return fault
			case "twice":
				_ = backend.ReadSnapshot(ctx, callback)
			case "cancel":
				cancel()
			}
			return nil
		})
		store, err := accountmodel.NewCredentialStore(scopes, f.binding, models.GroupDescriptor{}, models.PermissionDescriptor{})
		if err != nil {
			t.Fatal(err)
		}
		got, found, err := store.CredentialByID(ctx, f.user.PrincipalID)
		cancel()
		if err == nil || found || got.SessionStamp() != "" {
			t.Fatal(scenario, "published failed snapshot")
		}
		if (scenario == "cleanup" || scenario == "ignored_callback_error") && (!errors.Is(err, fault) || strings.Contains(fmt.Sprintf("%+v", err), "private")) {
			t.Fatal("failure cause or redaction", scenario, err)
		}
		if scenario == "cancel" && !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation lost", err)
		}
	}
}

type failedReader struct{ err error }

func (r failedReader) Query(context.Context, query.Plan) (db.Rows, error) { return nil, r.err }

func invalidGrants(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	for _, scenario := range []string{"permission_revision", "group_revision", "permission_code", "account_revision"} {
		var restore func() error
		switch scenario {
		case "permission_revision":
			if _, err := models.PermissionObjects.Patch(t.Context(), backend, f.grants[0], models.PermissionPatch{}.WithRevision(0)); err != nil {
				t.Fatal(err)
			}
			restore = func() error {
				_, err := models.PermissionObjects.Patch(t.Context(), backend, f.grants[0], models.PermissionPatch{}.WithRevision(1))
				return err
			}
		case "group_revision":
			if _, err := models.GroupObjects.Patch(t.Context(), backend, f.group, models.GroupPatch{}.WithRevision(0)); err != nil {
				t.Fatal(err)
			}
			restore = func() error {
				_, err := models.GroupObjects.Patch(t.Context(), backend, f.group, models.GroupPatch{}.WithRevision(1))
				return err
			}
		case "permission_code":
			if _, err := models.PermissionObjects.Patch(t.Context(), backend, f.grants[0], models.PermissionPatch{}.WithCode("bad code")); err != nil {
				t.Fatal(err)
			}
			restore = func() error {
				_, err := models.PermissionObjects.Patch(t.Context(), backend, f.grants[0], models.PermissionPatch{}.WithCode(f.grants[0].Code))
				return err
			}
		case "account_revision":
			if _, err := models.UserObjects.Patch(t.Context(), backend, f.user, models.UserPatch{}.WithRevision(0)); err != nil {
				t.Fatal(err)
			}
			restore = func() error {
				_, err := models.UserObjects.Patch(t.Context(), backend, f.user, models.UserPatch{}.WithRevision(1))
				return err
			}
		}
		got, found, err := f.store.CredentialByID(t.Context(), f.user.PrincipalID)
		if !errors.Is(err, &auth.Error{Code: auth.CodeCredential}) || found || got.SessionStamp() != "" {
			t.Fatal(scenario, "accepted invalid stored authority", err)
		}
		if err := restore(); err != nil {
			t.Fatal(err)
		}
		got, found, err = f.store.CredentialByID(t.Context(), f.user.PrincipalID)
		requireGrants(t, got, found, err, "account.direct", "account.shared", "account.team")
	}
}
func permissionBound(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	var overflow models.UserPermissionsLink
	if err := backend.CoordinatedAtomic(t.Context(), func(tx db.Session) error {
		for i := 3; i <= auth.MaximumPermissions; i++ {
			grant, err := models.PermissionObjects.Create(t.Context(), tx, models.NewPermissionCreate(fmt.Sprintf("limit.p%03d", i), "Limit"))
			if err != nil {
				return err
			}
			overflow, err = models.UserPermissionsLinkObjects.Create(t.Context(), tx, models.NewUserPermissionsLinkCreate(f.user.ID, grant.ID))
			if err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, found, err := f.store.CredentialByID(t.Context(), f.user.PrincipalID)
	if err == nil || found || got.SessionStamp() != "" {
		t.Fatal("permission overflow published truncated authority")
	}
	if _, err := models.UserPermissionsLinkObjects.Delete(t.Context(), backend, &overflow); err != nil {
		t.Fatal(err)
	}
	got, found, err = f.store.CredentialByID(t.Context(), f.user.PrincipalID)
	if err != nil || !found || len(got.Principal().Permissions()) != auth.MaximumPermissions {
		t.Fatal("exact permission bound rejected", err)
	}
}
func nonpositiveKeys(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	for _, key := range []int64{0, -17} {
		user := models.User{PrincipalID: fmt.Sprintf("key-%d", key), Username: fmt.Sprintf("key-%d", key), EncodedPassword: f.user.EncodedPassword, DateJoined: joined, Active: true, Revision: 1}
		(models.UserDescriptor{}).SetPrimaryKey(&user, key)
		if err := models.UserObjects.Save(t.Context(), backend, &user, orm.ForceInsert[models.User]()); err != nil {
			t.Fatal(err)
		}
		if _, err := models.UserPermissionsLinkObjects.Create(t.Context(), backend, models.NewUserPermissionsLinkCreate(key, f.grants[0].ID)); err != nil {
			t.Fatal(err)
		}
		got, found, err := f.store.CredentialByID(t.Context(), user.PrincipalID)
		requireGrants(t, got, found, err, "account.direct")
	}
}
func loginPolicy(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountUnicode16NFKC)
	authenticator, err := accountmodel.NewAuthenticator(t.Context(), f.store, f.hasher)
	if err != nil {
		t.Fatal(err)
	}
	got, err := authenticator.Authenticate(t.Context(), "ａｌｉｃｅ", password)
	if err != nil || got.Principal().ID() != "principal-1" {
		t.Fatal("selected Unicode normalization rejected canonical account", err)
	}
	for _, raw := range []string{"ALICE", " alice", "\xff", strings.Repeat("x", auth.MaximumUsernameBytes+1), strings.Repeat("㍿", 340)} {
		got, err := authenticator.Authenticate(t.Context(), raw, password)
		if !errors.Is(err, auth.ErrInvalidCredentials) || got.SessionStamp() != "" {
			t.Fatal("normalization widened login policy", err)
		}
	}
	// The production factory's exact policy cannot reinterpret an NFKC domain.
	exact, err := project.NewAccountAuthenticator(t.Context(), backend, f.hasher)
	if err != nil {
		t.Fatal(err)
	}
	got, err = exact.Authenticate(t.Context(), "alice", password)
	if !errors.Is(err, domain.ErrMismatch) || got.SessionStamp() != "" {
		t.Fatal("different selected login policy inherited stored ownership", err)
	}
}
