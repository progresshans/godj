package systemstate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync/atomic"
	"time"

	accountaudit "github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/internal/systemstatehandoff"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	sessiondb "github.com/progresshans/godj/sessions/database"
)

// IdentityHandoffConfig selects an explicit current permission and bounded
// historical inventories. This adopts the existing fixed identity.User into
// its matching selected-account role. A later typed Transition owns moving to
// a different model; a legacy operator must first use explicit AdoptOperator.
// No password hashing, role inference, schema application or retry occurs.
type IdentityHandoffConfig struct {
	Permission            auth.Permission
	AdditionalPermissions []auth.Permission
	Authorizer            auth.Authorizer
	MaxAccounts           int
	MaxCatalogRows        int
	SessionLimits         sessions.Limits
	MaxSessions           int
	AuditCapacity         int
	Now                   func() time.Time
}

func (IdentityHandoffConfig) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("systemstate.IdentityHandoffConfig{redacted}"))
}

type IdentityHandoff struct {
	backend              IdentityBackend
	binding              accountmodel.Binding[models.User]
	config               IdentityHandoffConfig
	required             []auth.Permission
	role, source, schema string
	sessionDatabase      *sessiondb.Database
	relations            project.Relations
}

func (*IdentityHandoff) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("systemstate.IdentityHandoff{redacted}"))
}

func NewIdentityHandoff(backend IdentityBackend, binding accountmodel.Binding[models.User], config IdentityHandoffConfig) (*IdentityHandoff, error) {
	if isNilInterface(backend) || isNilInterface(config.Authorizer) {
		return nil, handoffError(CodeInvalidConfig, nil)
	}
	role, err := binding.RoleFingerprint()
	if err != nil {
		return nil, handoffError(CodeInvalidConfig, err)
	}
	standard, err := project.BindAccount()
	if err != nil {
		return nil, handoffError(CodeInvalidConfig, err)
	}
	want, err := standard.RoleFingerprint()
	if err != nil || want != role {
		return nil, handoffError(CodeInvalidConfig, err)
	}
	bound, err := binding.Project()
	if err != nil {
		return nil, handoffError(CodeInvalidConfig, err)
	}
	source, err := bound.SourceSHA256()
	if err != nil {
		return nil, handoffError(CodeInvalidConfig, err)
	}
	required, err := auth.RequiredPermissions(config.Permission, config.AdditionalPermissions...)
	if err != nil {
		return nil, handoffError(CodeInvalidConfig, err)
	}
	for _, limit := range []*int{&config.MaxAccounts, &config.MaxCatalogRows} {
		if *limit == 0 {
			*limit = 4096
		}
		if *limit < 1 || *limit > domain.MaximumMappings {
			return nil, handoffError(CodeInvalidConfig, nil)
		}
	}
	config.SessionLimits, err = durableSessionLimits(config.SessionLimits)
	if err != nil {
		return nil, err
	}
	if config.MaxSessions == 0 {
		config.MaxSessions = defaultMaxDurableSessions
	}
	if config.MaxSessions < 1 || config.MaxSessions > hardMaxDurableSessions {
		return nil, handoffError(CodeInvalidConfig, nil)
	}
	config.AuditCapacity, err = normalizeAuditCapacity(config.AuditCapacity)
	if err != nil {
		return nil, err
	}
	config.AdditionalPermissions = slices.Clone(config.AdditionalPermissions)
	if config.Now == nil {
		config.Now = time.Now
	}
	newSessions, err := sessiondb.New(backend, sessiondb.Config{Limits: config.SessionLimits, MaxRecords: config.MaxSessions})
	if err != nil {
		return nil, err
	}
	schema, err := handoffSchemaDigest()
	if err != nil {
		return nil, err
	}
	relations, err := project.BindRelations()
	if err != nil {
		return nil, handoffError(CodeInvalidConfig, err)
	}
	return &IdentityHandoff{backend: backend, binding: binding, config: config, required: required, role: role, source: source, schema: schema, sessionDatabase: newSessions, relations: relations}, nil
}

// IdentityHandoffManifest contains detached review/reconciliation evidence.
// All three correspondence tables preserve actual keys and revisions. The
// digests cover exact private source bytes without publishing those bytes.
type IdentityHandoffManifest struct {
	Version     int                         `json:"version"`
	OperationID string                      `json:"operation_id"`
	RoleSHA256  string                      `json:"role_sha256"`
	At          time.Time                   `json:"at"`
	Origin      domain.LegacyIdentityOrigin `json:"origin"`
	Accounts    []domain.Mapping            `json:"accounts"`
	Catalog     domain.CatalogMappings      `json:"catalog"`
}

func (IdentityHandoffManifest) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("systemstate.IdentityHandoffManifest{redacted}"))
}

type PreparedIdentityHandoff struct{ state *preparedIdentityHandoff }
type preparedIdentityHandoff struct {
	owner    *IdentityHandoff
	actor    string
	manifest IdentityHandoffManifest
	change   domain.Change
	used     atomic.Bool
}

func (PreparedIdentityHandoff) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("systemstate.PreparedIdentityHandoff{redacted}"))
}
func (prepared PreparedIdentityHandoff) Manifest() IdentityHandoffManifest {
	if prepared.state == nil {
		return IdentityHandoffManifest{}
	}
	return cloneHandoffManifest(prepared.state.manifest)
}
func cloneHandoffManifest(value IdentityHandoffManifest) IdentityHandoffManifest {
	value.Accounts = slices.Clone(value.Accounts)
	value.Catalog.Groups = slices.Clone(value.Catalog.Groups)
	value.Catalog.Permissions = slices.Clone(value.Catalog.Permissions)
	return value
}

// Prepare reads the complete historical identity, memberships, owner, session
// and audit inventories in one native snapshot. No source or target is written.
// Retain its manifest before Commit, including across an unknown outcome.
func (owner *IdentityHandoff) Prepare(ctx context.Context, actor auth.Principal, operation string) (PreparedIdentityHandoff, error) {
	if err := owner.valid(ctx); err != nil {
		return PreparedIdentityHandoff{}, err
	}
	if _, err := systemstatehandoff.Password(operation); err != nil {
		return PreparedIdentityHandoff{}, handoffError(CodeInvalidInput, err)
	}
	if !actor.Authenticated() {
		return PreparedIdentityHandoff{}, handoffError(CodeHandoffDenied, nil)
	}
	if _, bound := actor.CredentialDomain(); bound {
		return PreparedIdentityHandoff{}, handoffError(CodeHandoffDenied, nil)
	}
	if err := owner.requireMigrations(ctx); err != nil {
		return PreparedIdentityHandoff{}, err
	}
	at, err := transitionInstant(owner.config.Now())
	if err != nil {
		return PreparedIdentityHandoff{}, err
	}
	observation, err := db.ReadSnapshotResult(ctx, owner.backend, func(work context.Context, scope db.Queryer) (handoffObservation, error) {
		if err := owner.requireFreshDestination(work, scope); err != nil {
			return handoffObservation{}, err
		}
		return owner.observe(work, scope, actor.ID(), "")
	})
	if err != nil {
		return PreparedIdentityHandoff{}, handoffError(CodePersistence, err)
	}
	manifest := IdentityHandoffManifest{Version: 1, OperationID: operation, RoleSHA256: owner.role, At: at, Origin: observation.origin, Accounts: observation.accounts, Catalog: observation.catalog}
	evidence, err := owner.manifestEvidence(manifest)
	if err != nil {
		return PreparedIdentityHandoff{}, err
	}
	change, err := domain.PrepareOperation(ctx, operation, owner.role, evidence)
	if err != nil {
		return PreparedIdentityHandoff{}, handoffError(CodePersistence, err)
	}
	return PreparedIdentityHandoff{state: &preparedIdentityHandoff{owner: owner, actor: actor.ID(), manifest: manifest, change: change}}, nil
}

// Commit rechecks the reviewed source and authority, appends the selected
// domain/correspondence and retires the historical runtime in ONE transaction.
// User/catalog/host rows and historical session/audit bytes remain unchanged.
// Old session cookies are never inserted into the new domain's namespace.
func (owner *IdentityHandoff) Commit(ctx context.Context, actor auth.Principal, prepared PreparedIdentityHandoff) (domain.Receipt, error) {
	if err := owner.valid(ctx); err != nil {
		return domain.Receipt{}, err
	}
	state := prepared.state
	if state == nil || state.owner != owner || !actor.Authenticated() || state.actor != actor.ID() {
		return domain.Receipt{}, handoffError(CodeInvalidInput, nil)
	}
	if _, bound := actor.CredentialDomain(); bound {
		return domain.Receipt{}, handoffError(CodeHandoffDenied, nil)
	}
	if !state.used.CompareAndSwap(false, true) {
		return domain.Receipt{}, handoffError(CodeHandoffConflict, nil)
	}
	if err := owner.requireMigrations(ctx); err != nil {
		return domain.Receipt{}, err
	}
	result, err := db.CoordinatedAtomicResult(ctx, owner.backend, func(work context.Context, scope db.Session) (domain.Receipt, error) {
		if err := owner.requireFreshDestination(work, scope); err != nil {
			return domain.Receipt{}, err
		}
		before, err := owner.observe(work, scope, actor.ID(), "")
		if err != nil {
			return domain.Receipt{}, err
		}
		if !sameHandoffObservation(before, state.manifest) {
			return domain.Receipt{}, handoffError(CodeHandoffConflict, nil)
		}
		origin := state.manifest.Origin
		catalog := state.manifest.Catalog
		receipt, err := domain.InitializeAdoptedIn(work, scope, state.change, state.manifest.At, domain.MappingInventory{Accounts: state.manifest.Accounts, Catalog: &catalog, Legacy: &origin})
		if err != nil {
			return domain.Receipt{}, err
		}
		password, err := systemstatehandoff.Password(state.manifest.OperationID)
		if err != nil {
			return domain.Receipt{}, err
		}
		affected, err := scope.Update(work, query.NewUpdatePlan(credentialTableName, []query.Assignment{
			query.NewAssignment(credentialEncodedPasswordRef, query.String(password)),
			query.NewAssignment(credentialDefinitionDigestRef, query.String("sha256:"+state.manifest.OperationID)),
		}, credentialIDRef, query.Integer(before.ownerID)))
		if err != nil {
			return domain.Receipt{}, err
		}
		if affected != 1 {
			return domain.Receipt{}, handoffError(CodeHandoffConflict, nil)
		}
		after, err := owner.observe(work, scope, actor.ID(), state.manifest.OperationID)
		if err != nil {
			return domain.Receipt{}, err
		}
		if !sameHandoffObservation(after, state.manifest) {
			return domain.Receipt{}, handoffError(CodeHandoffConflict, nil)
		}
		if _, err := owner.sessionDatabase.InspectIn(work, scope); err != nil {
			return domain.Receipt{}, err
		}
		if count, err := accountaudit.InspectIn(work, scope); err != nil || count != 0 {
			return domain.Receipt{}, handoffError(CodeHandoffConflict, err)
		}
		actual, found, err := owner.reconcileIn(work, scope, state.manifest)
		if err != nil || !found || !reflect.DeepEqual(actual, receipt) {
			return domain.Receipt{}, handoffError(CodeHandoffConflict, err)
		}
		return receipt, nil
	})
	if err != nil {
		return domain.Receipt{}, handoffError(CodePersistence, err)
	}
	return result, nil
}

// Reconcile verifies a retained operation against durable canonical evidence
// and the unchanged retired archive. Later account edits/model transitions do
// not invalidate its historical mapping. Absence never authorizes a retry.
func (owner *IdentityHandoff) Reconcile(ctx context.Context, manifest IdentityHandoffManifest) (domain.Receipt, bool, error) {
	if err := owner.valid(ctx); err != nil {
		return domain.Receipt{}, false, err
	}
	manifest = cloneHandoffManifest(manifest)
	if _, err := owner.manifestEvidence(manifest); err != nil {
		return domain.Receipt{}, false, err
	}
	if err := owner.requireMigrations(ctx); err != nil {
		return domain.Receipt{}, false, err
	}
	type result struct {
		receipt domain.Receipt
		found   bool
	}
	value, err := db.ReadSnapshotResult(ctx, owner.backend, func(work context.Context, scope db.Queryer) (result, error) {
		receipt, found, err := owner.reconcileIn(work, scope, manifest)
		return result{receipt, found}, err
	})
	if err != nil {
		return domain.Receipt{}, false, handoffError(CodePersistence, err)
	}
	return value.receipt, value.found, nil
}

// AuditHistoryIn reads the verified retired archive without assigning old
// actors or events to a new credential domain or inventing missing timestamps.
// The caller MUST authorize historical access with its current selected
// principal in this same borrowed scope. This low-level reader grants nothing.
func (owner *IdentityHandoff) AuditHistoryIn(ctx context.Context, scope db.Queryer, manifest IdentityHandoffManifest, model string, objectID int64, limit int) ([]admin.AuditEntry, error) {
	if err := owner.valid(ctx); err != nil {
		return nil, err
	}
	if limit < 1 || limit > admin.MaximumHistoryEntries {
		return nil, handoffError(CodeInvalidInput, nil)
	}
	if _, err := admin.PrepareEvent("handoff-history", model, objectID, admin.ActionChange, nil, ""); err != nil {
		return nil, handoffError(CodeInvalidInput, err)
	}
	if _, found, err := owner.reconcileIn(ctx, scope, cloneHandoffManifest(manifest)); err != nil || !found {
		return nil, handoffError(CodeHandoffConflict, err)
	}
	entries, err := readAuditHistory(ctx, scope, model, objectID, limit)
	validator, ok := scope.(db.SessionValidator)
	if !ok || isNilInterface(validator) {
		return nil, handoffError(CodeInvalidInput, nil)
	}
	if err := errors.Join(err, ctx.Err(), validator.ValidateSession(ctx)); err != nil {
		return nil, handoffError(CodePersistence, err)
	}
	return entries, nil
}

func (owner *IdentityHandoff) reconcileIn(ctx context.Context, scope db.Queryer, manifest IdentityHandoffManifest) (domain.Receipt, bool, error) {
	evidence, err := owner.manifestEvidence(manifest)
	if err != nil {
		return domain.Receipt{}, false, err
	}
	receipt, found, err := domain.LookupIn(ctx, scope, manifest.OperationID)
	if err != nil || !found {
		return domain.Receipt{}, false, err
	}
	if receipt.Sequence() != 1 || receipt.RoleFingerprint() != manifest.RoleSHA256 || receipt.EvidenceSHA256() != evidence || !receipt.At().Equal(manifest.At) {
		return domain.Receipt{}, false, handoffError(CodeHandoffConflict, nil)
	}
	inventory, err := domain.LookupMappingInventoryIn(ctx, scope, receipt)
	if err != nil {
		return domain.Receipt{}, false, err
	}
	if inventory.Legacy == nil || *inventory.Legacy != manifest.Origin || inventory.Catalog == nil || !slices.Equal(inventory.Accounts, manifest.Accounts) || !slices.Equal(inventory.Catalog.Groups, manifest.Catalog.Groups) || !slices.Equal(inventory.Catalog.Permissions, manifest.Catalog.Permissions) || len(inventory.Collections) != 0 {
		return domain.Receipt{}, false, handoffError(CodeHandoffConflict, nil)
	}
	if _, err := domain.InspectIn(ctx, scope); err != nil {
		return domain.Receipt{}, false, err
	}
	archive, _, err := owner.observeArchive(ctx, scope, manifest.OperationID)
	if err != nil {
		return domain.Receipt{}, false, err
	}
	if archive.OwnerSHA256 != manifest.Origin.OwnerSHA256 || archive.SessionsSHA256 != manifest.Origin.SessionsSHA256 || archive.SessionRecords != manifest.Origin.SessionRecords || archive.AuditSHA256 != manifest.Origin.AuditSHA256 || archive.AuditEvents != manifest.Origin.AuditEvents {
		return domain.Receipt{}, false, handoffError(CodeHandoffConflict, nil)
	}
	return receipt, true, nil
}

func (owner *IdentityHandoff) manifestEvidence(value IdentityHandoffManifest) (string, error) {
	// Reconciliation binds these fingerprints to the original durable receipt,
	// not to today's host graph. Prepare creates them from this owner and Commit
	// consumes only its private prepared value, reobserving the current source.
	if value.Version != 1 || value.At.IsZero() || value.At.Year() < 1 || value.At.Year() > 9999 || value.At.Nanosecond()%1000 != 0 || value.At.Location() != time.UTC || len(value.Accounts) < 1 || len(value.Accounts) > owner.config.MaxAccounts || len(value.Catalog.Groups) > owner.config.MaxCatalogRows || len(value.Catalog.Permissions) > owner.config.MaxCatalogRows {
		return "", handoffError(CodeInvalidInput, nil)
	}
	if _, err := systemstatehandoff.Password(value.OperationID); err != nil {
		return "", handoffError(CodeInvalidInput, err)
	}
	if value.Origin.SessionRecords < 0 || value.Origin.SessionRecords > owner.config.MaxSessions || value.Origin.AuditEvents < 0 || value.Origin.AuditEvents > owner.config.AuditCapacity {
		return "", handoffError(CodeInvalidInput, nil)
	}
	for _, digest := range []string{value.RoleSHA256, value.Origin.ProjectSHA256, value.Origin.SchemaSHA256, value.Origin.OwnerSHA256, value.Origin.AccountsSHA256, value.Origin.CatalogSHA256, value.Origin.SessionsSHA256, value.Origin.AuditSHA256} {
		if _, err := systemstatehandoff.Password(digest); err != nil {
			return "", handoffError(CodeInvalidInput, err)
		}
	}
	for _, rows := range [][]domain.Mapping{value.Accounts, value.Catalog.Groups, value.Catalog.Permissions} {
		for i, row := range rows {
			if row.SourceRevision <= 0 || row.SourceID != row.TargetID || row.SourceRevision != row.TargetRevision || i > 0 && row.SourceID <= rows[i-1].SourceID {
				return "", handoffError(CodeInvalidInput, nil)
			}
		}
	}
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) > domain.MaximumMappingBytes {
		return "", handoffError(CodeInvalidInput, err)
	}
	digest := sha256.Sum256(append([]byte("godj.systemstate.account-handoff.v1\x00"), encoded...))
	return hex.EncodeToString(digest[:]), nil
}

func (owner *IdentityHandoff) requireFreshDestination(ctx context.Context, scope db.Queryer) error {
	_, err := domain.InspectIn(ctx, scope)
	if err == nil {
		return handoffError(CodeHandoffConflict, nil)
	}
	if err != domain.ErrUninitialized {
		return err
	}
	if count, err := accountaudit.InspectIn(ctx, scope); err != nil || count != 0 {
		return handoffError(CodeHandoffConflict, err)
	}
	_, err = owner.sessionDatabase.InspectIn(ctx, scope)
	return err
}

func (owner *IdentityHandoff) valid(ctx context.Context) error {
	if owner == nil || isNilInterface(owner.backend) || owner.sessionDatabase == nil {
		return handoffError(CodeInvalidConfig, nil)
	}
	return validateSystemStateCall(ctx, owner.backend)
}

func (owner *IdentityHandoff) requireMigrations(ctx context.Context) error {
	sources := append(IdentityMigrationSources(), domain.MigrationSources()...)
	sources = append(sources, accountaudit.MigrationSources()...)
	sources = append(sources, sessiondb.MigrationSources()...)
	return requireSystemMigrationSources(ctx, owner.backend, sources)
}

func handoffSchemaDigest() (string, error) {
	writer := newHandoffDigest("schema", nil)
	for _, source := range IdentityMigrationSources() {
		writer.part([]byte(source.SourceID))
		writer.part(source.Document)
	}
	return writer.sum(), writer.err
}

func handoffError(code ErrorCode, cause error) error {
	return &Error{Code: code, Field: "account_handoff", Detail: "selected-account handoff was not completed", Cause: cause}
}
