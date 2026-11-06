package model

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/internal/sessionwire"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/sessions"
	sessiondb "github.com/progresshans/godj/sessions/database"
)

// RuntimeConfig composes explicit account and catalog policies over one session
// owner. Related-row grants must agree with the corresponding primary policy;
// a catalog side effect cannot use weaker grants than direct account editing.
// Accounts' password policy and byte envelope also govern self-service changes.
// Callbacks retain their existing pure/concurrent-safe ownership contracts.
type RuntimeConfig[M, G, P any] struct {
	Accounts       AccountManagementConfig[M]
	Catalog        CatalogConfig[G, P]
	SessionStorage sessiondb.Config
	Sessions       sessions.Config
}

func (RuntimeConfig[M, G, P]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.RuntimeConfig{redacted}"))
}
func (RuntimeConfig[M, G, P]) MarshalJSON() ([]byte, error) {
	return []byte(`"identity.model.RuntimeConfig{redacted}"`), nil
}

// Runtime is a complete transport-independent selected-account composition.
// It never opens/closes a backend, applies migrations, provisions an account or
// caches a startup observation as authority. Hosts inspect startup explicitly,
// then reuse these immutable owners for HTTP/Admin/API and local commands.
type Runtime[M, G, P any] struct {
	store          *SessionStore[M]
	verifier       *Authenticator[M]
	sessions       *sessions.Manager
	login          auth.LoginPersistence
	passwordChange auth.PasswordChangePersistence
	accounts       *AccountManagement[M]
	catalog        *Catalog[G, P]
	passwordPolicy PasswordChangeConfig[M]
}

func (*Runtime[M, G, P]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.Runtime{redacted}"))
}

// NewRuntime is normally called by the generated project factory, which
// supplies exact typed descriptors, collections and complete host deletions.
// Construction performs the authenticator's bounded dummy-hash setup; no DB
// operation, model validator, permission callback or session entropy is used.
// The backend remains caller-owned on both success and failure.
func NewRuntime[M, G, P, GL, PL, GP any](ctx context.Context, backend sessiondb.Backend, binding Binding[M],
	groups orm.RelationObjectDescriptor[G], permissions orm.RelationObjectDescriptor[P],
	memberships orm.ManyToMany[M, G, GL], grants orm.ManyToMany[M, P, PL], groupGrants orm.ManyToMany[G, P, GP],
	deleteAccounts orm.RelationDeleter[M], deleteGroups orm.RelationDeleter[G], deletePermissions orm.RelationDeleter[P],
	hasher auth.PasswordHasher, overlay auth.Authorizer, config RuntimeConfig[M, G, P],
) (*Runtime[M, G, P], error) {
	if nilModelValue(ctx) || binding.state == nil || nilModelValue(backend) || nilModelValue(hasher) || nilModelValue(overlay) {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	if err := ctx.Err(); err != nil {
		return nil, managementError(CodeInvalidInput, err)
	}
	storageLimits, storageErr := sessionwire.NormalizeLimits(config.SessionStorage.Limits)
	managerLimits, managerErr := sessionwire.NormalizeLimits(config.Sessions.Limits)
	if storageErr != nil || managerErr != nil || storageLimits != managerLimits {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	account := config.Accounts.Permissions
	group := config.Catalog.Group.Permissions
	permission := config.Catalog.Permission.Permissions
	if !sameRuntimeDeletionPermissions(config.Catalog.AccountPermissions, DeletionPermissions{Delete: account.Delete, Change: account.Change, Additional: account.Additional}) ||
		!sameRuntimeDeletionPermissions(config.Accounts.GroupPermissions, DeletionPermissions{Delete: group.Delete, Change: group.Change, Additional: group.Additional}) ||
		!sameRuntimeDeletionPermissions(config.Accounts.PermissionPermissions, DeletionPermissions{Delete: permission.Delete, Change: permission.Change, Additional: permission.Additional}) {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	role, source := binding.state.role, binding.state.project
	for _, err := range []error{
		memberships.ValidateSource(source, role.Model, role.Groups), grants.ValidateSource(source, role.Model, role.Permissions),
		groupGrants.ValidateSource(source, role.Catalog.Group, role.Catalog.GroupPermissions),
		deleteAccounts.ValidateSource(source, role.Model), deleteGroups.ValidateSource(source, role.Catalog.Group),
		deletePermissions.ValidateSource(source, role.Catalog.Permission),
	} {
		if err != nil {
			return nil, managementError(CodeInvalidConfig, err)
		}
	}
	credentials, err := NewCredentialStore(backend, binding, groups, permissions)
	if err != nil {
		return nil, err
	}
	store, err := NewSessionStore(credentials, config.SessionStorage)
	if err != nil {
		return nil, err
	}
	manager, err := sessions.NewManager(store, config.Sessions)
	if err != nil {
		return nil, err
	}
	catalog, err := NewCatalog(store, binding, groups, permissions, groupGrants, deleteGroups, deletePermissions, overlay, config.Catalog)
	if err != nil {
		return nil, err
	}
	verifier, err := store.Authenticator(ctx, hasher)
	if err != nil {
		return nil, err
	}
	accounts, err := NewAccountManagement(store, binding, verifier, groups, permissions, memberships, grants, deleteAccounts, overlay, config.Accounts)
	if err != nil {
		return nil, err
	}
	login, err := store.LoginPersistence(manager)
	if err != nil {
		return nil, err
	}
	passwordPolicy := PasswordChangeConfig[M]{
		MaxPasswordBytes: config.Accounts.MaxPasswordBytes, ValidatePassword: config.Accounts.ValidatePassword,
	}
	passwordChange, err := store.PasswordChangePersistence(manager, verifier, passwordPolicy)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, managementError(CodeInvalidInput, err)
	}
	return &Runtime[M, G, P]{store, verifier, manager, login, passwordChange, accounts, catalog, passwordPolicy}, nil
}

func sameRuntimeDeletionPermissions(left, right DeletionPermissions) bool {
	if left.Delete != right.Delete || left.Change != right.Change {
		return false
	}
	a, b := slices.Clone(left.Additional), slices.Clone(right.Additional)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

func (runtime *Runtime[M, G, P]) SessionStore() *SessionStore[M] {
	if runtime == nil {
		return nil
	}
	return runtime.store
}

// Credentials returns this runtime's immutable selected-account reader and
// scoped authorizer. Host business operations use AuthorizeIn with the full
// authenticated principal inside their own native snapshot or write fence.
// The returned owner does not open or close the host backend.
func (runtime *Runtime[M, G, P]) Credentials() *CredentialStore[M] {
	if runtime == nil || runtime.store == nil || runtime.store.state == nil {
		return nil
	}
	return runtime.store.state.credentials
}
func (runtime *Runtime[M, G, P]) Sessions() *sessions.Manager {
	if runtime == nil {
		return nil
	}
	return runtime.sessions
}
func (runtime *Runtime[M, G, P]) Authenticator() *Authenticator[M] {
	if runtime == nil {
		return nil
	}
	return runtime.verifier
}
func (runtime *Runtime[M, G, P]) LoginPersistence() auth.LoginPersistence {
	if runtime == nil {
		return nil
	}
	return runtime.login
}
func (runtime *Runtime[M, G, P]) PasswordChangePersistence() auth.PasswordChangePersistence {
	if runtime == nil {
		return nil
	}
	return runtime.passwordChange
}
func (runtime *Runtime[M, G, P]) Accounts() *AccountManagement[M] {
	if runtime == nil {
		return nil
	}
	return runtime.accounts
}
func (runtime *Runtime[M, G, P]) Catalog() *Catalog[G, P] {
	if runtime == nil {
		return nil
	}
	return runtime.catalog
}
func (runtime *Runtime[M, G, P]) InspectStartup(ctx context.Context) (StartupInspection, error) {
	if runtime == nil {
		return StartupInspection{}, managementError(CodeInvalidConfig, nil)
	}
	return runtime.store.InspectStartup(ctx)
}

// RuntimeResetConfig adds signing and lifetime policy to a composed runtime.
// Reset always shares its account creation/change strength policy and byte
// envelope. There is no independent default that could silently weaken reset.
type RuntimeResetConfig struct {
	Keys    auth.PasswordResetKeyRing `json:"-"`
	Timeout time.Duration
	Clock   func() time.Time `json:"-"`
}

func (RuntimeResetConfig) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.RuntimeResetConfig{redacted}"))
}

// PasswordResetter uses this exact session manager, verifier and strength
// policy. No key generation, token issue or delivery occurs during construction.
func (runtime *Runtime[M, G, P]) PasswordResetter(config RuntimeResetConfig) (*PasswordResetter[M], error) {
	if runtime == nil || runtime.store == nil {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	return NewPasswordResetter(runtime.store, runtime.store.state.credentials.state.binding, runtime.sessions, runtime.verifier, PasswordResetConfig[M]{
		Keys: config.Keys, Timeout: config.Timeout, Clock: config.Clock,
		MaxPasswordBytes: runtime.passwordPolicy.MaxPasswordBytes, ValidatePassword: runtime.passwordPolicy.ValidatePassword,
	})
}
