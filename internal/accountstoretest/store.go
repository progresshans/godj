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

	accountaudit "github.com/progresshans/godj/account/audit"
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
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	sessiondb "github.com/progresshans/godj/sessions/database"
)

type Backend interface {
	db.Session
	db.CoordinatedAtomic
	db.CoordinatedRelationAtomic
	db.SnapshotReader
	mb.RevisionFencedBackend
}
type Open func(*testing.T) (Backend, func(*testing.T) Backend)

// StoredKeyFixture changes a newly created, unreferenced fixture row's key.
// It establishes existing database state; it does not claim that a backend
// implements explicit generated-key insertion or sequence reconciliation.
type StoredKeyFixture func(*testing.T, Backend, string, int64, int64)

func Run(t *testing.T, referenceBackend string, open Open, storedKey StoredKeyFixture) {
	if storedKey == nil {
		t.Fatal("stored account key fixture is required")
	}
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
		{"membership_bound", membershipBound},
		{"zero_and_negative_keys", func(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
			nonpositiveKeys(t, backend, storedKey)
		}},
		{"selected_login_policy", loginPolicy},
		{"authoritative_actor", authoritativeActor},
		{"authorization_scope_and_errors", authorizationScopeAndErrors},
		{"profile_scalar_edit", profileScalarEdit},
		{"profile_write_normalization", profileWriteNormalization},
		{"profile_target_integrity", profileTargetIntegrity},
		{"profile_domain_and_revocation", profileDomainAndRevocation},
		{"profile_commit_failures", profileCommitFailures},
		{"profile_concurrent_revision", profileConcurrentRevision},
		{"profile_read_selection_and_history", profileReadSelectionAndHistory},
		{"profile_read_authority_and_domain", profileReadAuthorityAndDomain},
		{"profile_read_failures", profileReadFailures},
		{"profile_read_coherent_page", profileReadCoherentPage},
		{"profile_read_target_integrity", profileReadTargetIntegrity},
		{"catalog_crud", catalogCRUD},
		{"catalog_nonpositive_keys", func(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
			catalogNonpositiveKeys(t, backend, storedKey)
		}},
		{"catalog_affected_batches", func(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
			catalogAffectedBatches(t, backend, reopen, storedKey)
		}},
		{"catalog_authority_and_domain", catalogAuthorityAndDomain},
		{"catalog_operation_policies", catalogOperationPolicies},
		{"catalog_target_integrity", catalogTargetIntegrity},
		{"catalog_read_failures", catalogReadFailures},
		{"catalog_concurrent_writes", catalogConcurrentWrites},
		{"catalog_host_graph", catalogHostGraph},
		{"catalog_commit_failures", catalogCommitFailures},
		{"catalog_relation_rollback", catalogRelationRollback},
		{"session_login_and_domain", sessionLoginAndDomain},
		{"session_login_failures", sessionLoginFailures},
		{"session_credential_recheck", sessionCredentialRecheck},
		{"session_revoked_previous", sessionRevokedPrevious},
		{"session_optional_last_login", sessionOptionalLastLogin},
		{"session_domain_snapshot", sessionDomainSnapshot},
		{"password_change_lifecycle", passwordChangeLifecycle},
		{"password_reset_lifecycle", passwordResetLifecycle},
		{"password_reset_rechecks", passwordResetRechecks},
		{"password_reset_commit_failures", passwordResetCommitFailures},
		{"password_reset_entry_failures", passwordResetEntryFailures},
		{"password_reset_read_failures", passwordResetReadFailures},
		{"password_reset_validation", passwordResetValidation},
		{"password_reset_admission", passwordResetAdmission},
		{"password_reset_session_fence", passwordResetSessionFence},
		{"password_reset_entropy", passwordResetEntropy},
		{"password_reset_many_sessions", passwordResetManySessions},
		{"password_reset_concurrent", passwordResetConcurrent},
		{"password_reset_mail", func(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
			passwordResetMail(t, referenceBackend, backend, reopen)
		}},
		{"password_reset_mail_bound", passwordResetMailBound},
		{"password_reset_mail_snapshot", passwordResetMailSnapshot},
		{"password_reset_nonpositive_keys", func(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
			passwordResetNonpositiveKeys(t, backend, storedKey)
		}},
		{"password_admin_lifecycle", passwordAdminLifecycle},
		{"password_admin_admission", passwordAdminAdmission},
		{"password_admin_rechecks", passwordAdminRechecks},
		{"password_admin_commit_failures", passwordAdminCommitFailures},
		{"password_admin_read_failures", passwordAdminReadFailures},
		{"password_admin_concurrent", passwordAdminConcurrent},
		{"password_admin_nonpositive_keys", func(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
			passwordAdminNonpositiveKeys(t, backend, storedKey)
		}},
		{"password_validation", passwordValidation},
		{"password_session_admission", passwordSessionAdmission},
		{"password_hasher_failures", passwordHasherFailures},
		{"password_rechecks", passwordRechecks},
		{"password_verify_recheck", passwordVerifyRecheck},
		{"password_commit_failures", passwordCommitFailures},
		{"password_read_failures", passwordReadFailures},
		{"password_concurrent", passwordConcurrent},
		{"password_versus_login", passwordVersusLogin},
		{"password_entropy", passwordEntropy},
		{"password_nonpositive_keys", func(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
			passwordNonpositiveKeys(t, backend, storedKey)
		}},
		{"first_account_lifecycle", firstAccountLifecycle},
		{"first_account_nonfresh", func(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
			firstAccountNonfresh(t, backend, reopen, storedKey)
		}},
		{"first_account_commit_failures", firstAccountCommitFailures},
		{"first_account_unknown_commit", firstAccountUnknownCommit},
		{"first_account_read_failures", firstAccountReadFailures},
		{"first_account_concurrent", firstAccountConcurrent},
		{"creation_lifecycle", creationLifecycle},
		{"creation_preflight_and_recheck", creationPreflightAndRecheck},
		{"creation_hasher_failures", creationHasherFailures},
		{"creation_commit_failures", creationCommitFailures},
		{"creation_read_failures", creationReadFailures},
		{"creation_concurrent_writes", creationConcurrentWrites},
		{"creation_domain_binding", creationDomainBinding},
		{"creation_bounds", creationBounds},
		{"creation_nonpositive_choices", func(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
			creationNonpositiveChoices(t, backend, storedKey)
		}},
		{"role_edit_and_deactivation", roleEditAndDeactivation},
		{"account_profile_and_roles", combinedProfileAndRoles},
		{"role_authority_and_domain", roleAuthorityAndDomain},
		{"role_bounds", roleBounds},
		{"role_target_integrity", roleTargetIntegrity},
		{"role_nonpositive_keys", func(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
			roleNonpositiveKeys(t, backend, storedKey)
		}},
		{"role_commit_failures", roleCommitFailures},
		{"role_concurrent_revision", roleConcurrentRevision},
		{"role_concurrent_catalog_expansion", roleConcurrentCatalogExpansion},
		{"role_deactivation_versus_login", roleDeactivationVersusLogin},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend, reopen := open(t)
			sources := append(identity.MigrationSources(), domain.MigrationSources()...)
			sources = append(sources, accountaudit.MigrationSources()...)
			if strings.HasPrefix(test.name, "session_") || strings.HasPrefix(test.name, "role_") || strings.HasPrefix(test.name, "password_") || test.name == "first_account_lifecycle" || test.name == "account_profile_and_roles" {
				sources = append(sources, sessiondb.MigrationSources()...)
			}
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
	runAccountDeletion(t, open, storedKey)
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

func provision(t *testing.T, backend Backend, normalization ir.AccountNormalization, additional ...ir.Schema) *fixture {
	t.Helper()
	role := modeldef.AccountRole()
	role.AuthenticateNormalization = normalization
	schemas := append([]ir.Schema{models.GoDjRelationSchema()}, additional...)
	binding, err := accountmodel.Bind(role, models.UserDescriptor{}, schemas...)
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

func (r *observedReader) ValidateSession(ctx context.Context) error {
	validator, ok := r.Queryer.(db.SessionValidator)
	if !ok {
		return errors.New("observation did not borrow a native snapshot")
	}
	return validator.ValidateSession(ctx)
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
				return backend.ReadSnapshot(ctx, func(reader db.Queryer) error {
					_ = callback(&profileReadProbe{Queryer: reader, validator: reader.(db.SessionValidator), fault: fault})
					return nil
				})
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

func membershipBound(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	before := profileActor(t, f)
	reader := profileReader(t, backend, f, auth.PrincipalAuthorizer{}, profileReadConfig())
	editor, err := project.NewAccountProfileEditor(backend, auth.PrincipalAuthorizer{}, profileConfig())
	if err != nil {
		t.Fatal(err)
	}
	var overflow models.UserGroupsLink
	if err := backend.CoordinatedAtomic(t.Context(), func(tx db.Session) error {
		for index := range accountmodel.MaximumAccountGroups {
			group, err := models.GroupObjects.Create(t.Context(), tx, models.NewGroupCreate(fmt.Sprintf("membership-%03d", index)))
			if err != nil {
				return err
			}
			// The effective permission union stays at three. Its bound cannot
			// stand in for the independent bound on group memberships.
			if _, err := models.GroupPermissionsLinkObjects.Create(t.Context(), tx, models.NewGroupPermissionsLinkCreate(group.ID, f.grants[1].ID)); err != nil {
				return err
			}
			overflow, err = models.UserGroupsLinkObjects.Create(t.Context(), tx, models.NewUserGroupsLinkCreate(f.user.ID, group.ID))
			if err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, lookup := range []struct {
		name string
		read func(context.Context, string) (auth.Credential, bool, error)
		key  string
	}{{"id", f.store.CredentialByID, f.user.PrincipalID}, {"login", f.store.CredentialByUsername, f.user.Username}} {
		t.Run(lookup.name, func(t *testing.T) {
			credential, found, err := lookup.read(t.Context(), lookup.key)
			if !errors.Is(err, &auth.Error{Code: auth.CodeCredential}) || found || credential.SessionStamp() != "" {
				t.Fatal("membership overflow published authority", err)
			}
		})
	}
	t.Run("current_authority", func(t *testing.T) {
		if err := backend.ReadSnapshot(t.Context(), func(scope db.Queryer) error {
			current, allowed, err := f.store.AuthorizeIn(t.Context(), scope, before.Principal(), auth.PrincipalAuthorizer{}, "account.direct")
			if err == nil || allowed || current.ID() != "" {
				t.Fatal("membership overflow reused previous actor authority", err)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("profile_read", func(t *testing.T) {
		if profile, err := reader.Get(t.Context(), before.Principal(), f.user.ID); err == nil || profile.Revision() != 0 {
			t.Fatal("membership overflow published profile", err)
		}
	})
	t.Run("profile_write", func(t *testing.T) {
		if result, err := editor.Update(t.Context(), before.Principal(), f.user.ID, 1, profileInput(t, map[string]query.Value{"first_name": query.String("must-not-save")})); err == nil || result.Revision != 0 {
			t.Fatal("membership overflow edited profile", err)
		}
		if row := profileRow(t, reopen(t), f.user.ID); row.FirstName != "" || row.Revision != 1 || row.EncodedPassword != f.user.EncodedPassword || len(profileHistory(t, backend, before.Principal(), f.user.ID)) != 0 {
			t.Fatal("rejected membership state changed durable profile/audit")
		}
	})
	other, err := models.UserObjects.Create(t.Context(), backend, models.NewUserCreate("bounded-admin", "bounded-admin", f.user.EncodedPassword, joined))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := models.UserPermissionsLinkObjects.Create(t.Context(), backend, models.NewUserPermissionsLinkCreate(other.ID, f.grants[0].ID)); err != nil {
		t.Fatal(err)
	}
	otherCredential, found, err := f.store.CredentialByID(t.Context(), other.PrincipalID)
	requireGrants(t, otherCredential, found, err, "account.direct")
	t.Run("target_read", func(t *testing.T) {
		if value, err := reader.Get(t.Context(), otherCredential.Principal(), f.user.ID); err == nil || value.Revision() != 0 {
			t.Fatal("valid actor read overflowing target memberships", err)
		}
	})
	t.Run("target_write", func(t *testing.T) {
		if result, err := editor.Update(t.Context(), otherCredential.Principal(), f.user.ID, 1, profileInput(t, map[string]query.Value{"first_name": query.String("must-not-save")})); err == nil || result.Revision != 0 {
			t.Fatal("valid actor edited overflowing target memberships", err)
		}
		if row := profileRow(t, reopen(t), f.user.ID); row.FirstName != "" || row.Revision != 1 || len(profileHistory(t, backend, before.Principal(), f.user.ID)) != 0 {
			t.Fatal("rejected target memberships changed durable state")
		}
	})
	if count, err := models.UserGroupsLinkObjects.Delete(t.Context(), backend, &overflow); err != nil || count != 1 {
		t.Fatal("remove overflowing membership", err)
	}
	t.Run("exact_bound", func(t *testing.T) {
		credential, found, err := f.store.CredentialByID(t.Context(), f.user.PrincipalID)
		requireGrants(t, credential, found, err, "account.direct", "account.shared", "account.team")
		if credential.SessionStamp() != before.SessionStamp() {
			t.Fatal("membership changes altered credential session stamp")
		}
		if value, err := reader.Get(t.Context(), before.Principal(), f.user.ID); err != nil || value.Revision() != 1 {
			t.Fatal("exact membership bound rejected profile", err)
		}
	})
}
func nonpositiveKeys(t *testing.T, backend Backend, storedKey StoredKeyFixture) {
	f := provision(t, backend, ir.AccountExact)
	reader := profileReader(t, backend, f, auth.PrincipalAuthorizer{}, profileReadConfig())
	editor, err := project.NewAccountProfileEditor(backend, auth.PrincipalAuthorizer{}, profileConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []int64{0, -17} {
		user, err := models.UserObjects.Create(t.Context(), backend, models.NewUserCreate(fmt.Sprintf("key-%d", key), fmt.Sprintf("key-%d", key), f.user.EncodedPassword, joined))
		if err != nil {
			t.Fatal(err)
		}
		storedKey(t, backend, "user", user.ID, key)
		(models.UserDescriptor{}).SetPrimaryKey(&user, key)
		if _, err := models.UserPermissionsLinkObjects.Create(t.Context(), backend, models.NewUserPermissionsLinkCreate(key, f.grants[0].ID)); err != nil {
			t.Fatal(err)
		}
		got, found, err := f.store.CredentialByID(t.Context(), user.PrincipalID)
		requireGrants(t, got, found, err, "account.direct")
		if err := backend.CoordinatedAtomic(t.Context(), func(tx db.Session) error {
			current, allowed, err := f.store.AuthorizeIn(t.Context(), tx, got.Principal(), auth.PrincipalAuthorizer{}, "account.direct")
			if err != nil {
				return err
			}
			if !allowed || current.ID() != user.PrincipalID {
				return errors.New("present nonpositive key lost authority")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		change, err := editor.Update(t.Context(), got.Principal(), key, 1, profileInput(t, map[string]query.Value{"first_name": query.String("signed-key-profile")}))
		if err != nil || change.ID != key || change.Revision != 2 {
			t.Fatal("present signed key profile edit failed", key, change, err)
		}
		if actual := profileRow(t, backend, key); actual.FirstName != "signed-key-profile" || actual.Revision != 2 || len(profileHistory(t, backend, got.Principal(), key)) != 1 {
			t.Fatal("signed profile/audit state missing", key)
		}
		profile, err := reader.Get(t.Context(), got.Principal(), key)
		if err != nil {
			t.Fatal("stored signed profile key was treated as absent", key, err)
		}
		requireReadProfile(t, profile, key, 2, "signed-key-profile")
		if stored, present := profile.View().PrimaryKey(); !present || !stored.Equal(query.Integer(key)) {
			t.Fatal("view lost stored signed key", key)
		}
		if entries, err := reader.History(t.Context(), got.Principal(), key, 100); err != nil || len(entries) != 1 || entries[0].ObjectID() != key {
			t.Fatal("signed key read history", key, err)
		}
	}
	page, err := reader.List(t.Context(), profileActor(t, f).Principal(), 0, 100)
	if err != nil || len(page.Items) != 3 || page.Items[0].ID() != -17 || page.Items[1].ID() != 0 {
		t.Fatal("signed profile keys lost their stored ordering", err)
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
