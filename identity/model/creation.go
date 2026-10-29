package model

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"sync/atomic"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

// CreationValidator checks the actual detached model and proposed membership
// under current authority, both before hashing and under the final write fence.
// The reader is borrowed and must not be retained, mutated or re-entered. The
// password field is deliberately redacted in this callback's model copy.
type CreationValidator[M any] func(context.Context, db.Queryer, M, AccountMemberships) (validation.Errors, error)

// CreationPolicy explicitly selects login, scalar/FK/OneToOne/file profile,
// editable role flags and group/direct-permission or explicitly bound profile
// collections. Each selected profile M2M requires Collections policy. Files require
// their explicit policy and the Form entry point. Model defaults apply to omitted
// fields. Principal ID, encoded password, revision and selected timestamps are
// server-owned. Inputs must already have passed their Form/JSON cleaning policy.
// All callbacks must be safe for concurrent use.
type CreationPolicy[M any] struct {
	Fields      []string
	Relations   []ProfileRelation[M]
	Collections []ProfileCollection[M]
	// Each selected identity-through collection has its own creation owner.
	// These are independent of an editor's through policies.
	ThroughCreations     map[string]ThroughCreation
	ThroughCreationLimit int
	// RelatedPermissions governs selected-model effects of the collected creation graph.
	RelatedPermissions RelatedRowPermissions
	// Files configures selected file fields for CreateForm. Raw ModelInput
	// never acquires permission to assign file references or image dimensions.
	Files            []ProfileFile[M]
	Validate         CreationValidator[M]
	ValidatePassword PasswordValidator[M]
	// MaxPasswordBytes defaults to 1024; the supported bound is 1..4096. The
	// hasher can impose a stricter limit. Empty input never means unusable.
	MaxPasswordBytes int
	// CaseInsensitiveLogin is an optional additional creation uniqueness policy;
	// it does not change the selected write or authentication normalization.
	CaseInsensitiveLogin bool
	Now                  func() time.Time
	// PrincipalID is a server-side source, never client input. Nil uses 256 bits
	// of system randomness as "user-" plus hexadecimal (69 characters).
	PrincipalID func(context.Context) (string, error)
}

// CreationConfig combines model/password policy with current actor authority.
// Add AND change permission are required, as are all Additional permissions.
type CreationConfig[M any] struct {
	CreationPolicy[M]
	AddPermission, ChangePermission auth.Permission
	AdditionalPermissions           []auth.Permission
}

// Creator owns authorized creation using the selected model and native DB.
// It does not initialize or adopt an account domain; first-account provisioning
// requires its separate operator policy.
type Creator[M any] struct{ state *creationState[M] }
type creationState[M any] struct {
	*creationDefinition[M]
	store     *CredentialStore[M]
	backend   db.CoordinatedRelationAtomic
	overlay   auth.Authorizer
	required  []auth.Permission
	lifecycle *deletionLifecycle[M]
	through   map[string]*throughCreationState
}

type creationDefinition[M any] struct {
	binding                         Binding[M]
	hasher                          auth.PasswordHasher
	metadata                        ir.Model
	fields                          []string
	allowed                         map[string]bool
	relations                       []ProfileRelation[M]
	collections                     []ProfileCollection[M]
	files                           []ProfileFile[M]
	dimensions                      map[string]string
	config                          CreationPolicy[M]
	forced                          map[string]query.Value
	auditFields                     []string
	groupsChange, permissionsChange func(M, []int64) (orm.CollectionChange, error)
	throughDefinitions              map[string]*throughCreationState
}

func (Creator[M]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.Creator{redacted}"))
}

// NewCreator is normally called by the generated project factory. The selected
// role and both actual collection capabilities must match the session
// owner's complete source. Construction performs no I/O or password work.
func NewCreator[M, G, P, GL, PL any](sessions *SessionStore[M], expected Binding[M], groups orm.ManyToMany[M, G, GL], permissions orm.ManyToMany[M, P, PL], overlay auth.Authorizer, hasher auth.PasswordHasher, config CreationConfig[M]) (*Creator[M], error) {
	if sessions == nil || sessions.state == nil || nilModelValue(overlay) {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	store := sessions.state.credentials
	required, err := auth.RequiredPermissions(config.AddPermission, append([]auth.Permission{config.ChangePermission}, config.AdditionalPermissions...)...)
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}

	state, err := newCreationState(store, expected, groups, permissions, hasher, config.CreationPolicy, nil)
	if err != nil {
		return nil, err
	}
	if store.state.relatedLifecycle == nil {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	state.lifecycle, err = store.state.relatedLifecycle(sessions, expected, overlay, creationLifecycleConfig(state.config))
	if err != nil {
		return nil, err
	}
	state.overlay, state.required = overlay, required
	return &Creator[M]{state: state}, nil
}

func newCreationState[M, G, P, GL, PL any](store *CredentialStore[M], expected Binding[M], groups orm.ManyToMany[M, G, GL], permissions orm.ManyToMany[M, P, PL], hasher auth.PasswordHasher, config CreationPolicy[M], forced map[string]query.Value) (*creationState[M], error) {
	if err := store.valid(); err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	if expected.state == nil || nilModelValue(hasher) || len(config.Fields) == 0 || len(config.Fields) >= admin.MaximumChangedFields {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	backend, ok := store.state.backend.(db.CoordinatedRelationAtomic)
	if !ok || nilModelValue(backend) {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	binding := store.state.binding
	if binding.state.roleFingerprint != expected.state.roleFingerprint {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	definition, err := prepareCreationDefinition(binding, groups, permissions, hasher, config, forced)
	if err != nil {
		return nil, err
	}
	through, err := attachThroughDefinitions(store, definition.throughDefinitions)
	if err != nil {
		return nil, err
	}
	return &creationState[M]{creationDefinition: definition, store: store, backend: backend, through: through}, nil
}

func prepareCreationDefinition[M, G, P, GL, PL any](binding Binding[M], groups orm.ManyToMany[M, G, GL], permissions orm.ManyToMany[M, P, PL], hasher auth.PasswordHasher, config CreationPolicy[M], forced map[string]query.Value) (*creationDefinition[M], error) {
	if binding.state == nil || nilModelValue(hasher) || len(config.Fields) == 0 || len(config.Fields) >= admin.MaximumChangedFields {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	role, project := binding.state.role, binding.state.project
	for _, err := range []error{groups.ValidateSource(project, role.Model, role.Groups), permissions.ValidateSource(project, role.Model, role.Permissions)} {
		if err != nil {
			return nil, managementError(CodeInvalidConfig, err)
		}
	}
	through, ordinary, err := prepareThroughDefinitions(binding, config.Fields, config.ThroughCreations, config.ThroughCreationLimit)
	if err != nil {
		return nil, err
	}
	// Omitted roles start empty; selected identity-through fields require their
	// own explicit owner and never inherit an edit's creation permissions.
	if err := validateAccountCollectionStorage(project, role, ordinary); err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	for _, policy := range []*DeletionPermissions{&config.RelatedPermissions.Account, &config.RelatedPermissions.Group, &config.RelatedPermissions.Permission} {
		if _, err := lifecycleRequiredPermissions(policy.Change, policy.Additional, false); err != nil {
			return nil, managementError(CodeInvalidConfig, err)
		}
		if _, err := lifecycleRequiredPermissions(policy.Delete, policy.Additional, false); err != nil {
			return nil, managementError(CodeInvalidConfig, err)
		}
		policy.Additional = slices.Clone(policy.Additional)
	}
	metadata, err := binding.state.objects.Metadata()
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	files, dimensions, err := prepareProfileFiles(binding, config.Fields, config.Files)
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	protected := map[string]bool{role.PrincipalID: true, role.Password: true, role.Revision: true, role.LastLogin: true, role.CreatedAt: true}
	for name := range forced {
		protected[name] = true
	}
	allowed := make(map[string]bool, len(config.Fields))
	for _, name := range config.Fields {
		if name == "" || allowed[name] || protected[name] || dimensions[name] != "" {
			return nil, managementError(CodeInvalidConfig, nil)
		}
		if _, collection := namedCollection(metadata, name); !collection {
			field, found := namedField(metadata, name)
			if !found || field.PrimaryKey || field.NonEditable {
				return nil, managementError(CodeInvalidConfig, nil)
			}
		}
		allowed[name] = true
	}
	relations, err := prepareProfileRelations(binding, config.Fields, config.Relations)
	if err != nil {
		return nil, err
	}
	collections, err := prepareProfileCollections(binding, config.Fields, config.Collections)
	if err != nil {
		return nil, err
	}
	for _, field := range metadata.ManyToMany {
		if field.Name != role.Groups && field.Name != role.Permissions && !field.Blank && !allowed[field.Name] {
			return nil, managementError(CodeInvalidConfig, nil)
		}
	}
	if !allowed[role.Login] {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	initial, err := orm.InitialModelValues(metadata)
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	for _, field := range metadata.Fields {
		if field.PrimaryKey || protected[field.Name] || allowed[field.Name] || allowed[dimensions[field.Name]] {
			continue
		}
		value := initial[field.Name]
		text, textual := value.String()
		if value.IsNull() && !field.Nullable || textual && text == "" && !field.Blank && field.Default == nil {
			// A newly required profile field must be selected explicitly. It
			// cannot become an empty string/Go zero through an old creator.
			return nil, managementError(CodeInvalidConfig, nil)
		}
	}
	fields := slices.Clone(config.Fields)
	auditFields := profileStoredFields(metadata, fields, dimensions)
	for _, field := range metadata.Fields {
		if _, present := forced[field.Name]; present {
			auditFields = append(auditFields, field.Name)
		}
	}
	auditFields = append(auditFields, role.Password)
	if _, err := admin.PrepareEventTemplate("creation-startup", role.Model.AppLabel+"."+role.Model.ModelName, admin.ActionAdd, auditFields, ""); err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	if config.MaxPasswordBytes == 0 {
		config.MaxPasswordBytes = 1024
	}
	if config.MaxPasswordBytes < 1 || config.MaxPasswordBytes > 4096 {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.PrincipalID == nil {
		if field := binding.state.credentialFields[role.PrincipalID]; field.MaxLength > 0 && field.MaxLength < 69 {
			return nil, managementError(CodeInvalidConfig, nil)
		}
		config.PrincipalID = newAccountPrincipalID
	}
	config.Fields = nil
	config.Relations = nil
	config.Collections = nil
	config.Files = nil
	config.ThroughCreations = nil
	state := &creationDefinition[M]{binding: binding, hasher: hasher, metadata: metadata, fields: fields, allowed: allowed, relations: relations, collections: collections, files: files, dimensions: dimensions, config: config, forced: maps.Clone(forced), auditFields: auditFields, throughDefinitions: through}
	state.groupsChange = func(row M, keys []int64) (orm.CollectionChange, error) { return groups.SetKeysChange(row, keys) }
	state.permissionsChange = func(row M, keys []int64) (orm.CollectionChange, error) { return permissions.SetKeysChange(row, keys) }
	return state, nil
}

func newAccountPrincipalID(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var entropy [32]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", err
	}
	return "user-" + hex.EncodeToString(entropy[:]), ctx.Err()
}
func (creator *Creator[M]) Fields() []string {
	if creator == nil || creator.state == nil {
		return nil
	}
	return slices.Clone(creator.state.fields)
}

// Manager exposes pure metadata for typed Form/API preparation, no authority.
func (creator *Creator[M]) Manager() orm.Manager[M] {
	if creator == nil || creator.state == nil {
		return orm.Manager[M]{}
	}
	return creator.state.store.state.binding.state.objects
}

type PreparedCreation[M any] struct{ state *preparedCreation[M] }
type preparedCreation[M any] struct {
	owner     *Creator[M]
	actorID   string
	domain    auth.CredentialDomain
	input     *creationCandidate[M]
	attempted atomic.Bool
}
type creationCandidate[M any] struct {
	prepared            orm.PreparedModel[M]
	row                 M
	groups, permissions []int64
	profile             map[string][]int64
	raw                 string
	unusable            bool
	form                *formmodel.BoundForm
	pendingFiles        []string
	stored              bool
	through             *creationThroughPreparation
}

func (PreparedCreation[M]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.PreparedCreation{redacted}"))
}
func (PreparedCreation[M]) MarshalJSON() ([]byte, error) {
	return []byte(`"identity.model.PreparedCreation{redacted}"`), nil
}

// Prepare validates in one current snapshot, closes it, then hashes once.
// It writes nothing. Drop an abandoned candidate; it retains private material
// needed for final policy validation until Commit consumes its sole attempt.
func (creator *Creator[M]) Prepare(ctx context.Context, actor auth.Principal, input orm.ModelInput[M], password string) (PreparedCreation[M], error) {
	prepared, _, err := creator.prepare(ctx, actor, input, password, false, nil)
	return prepared, err
}

// PrepareUnusable explicitly creates a credential that cannot authenticate.
// It never invokes the password hasher or strength policy.
func (creator *Creator[M]) PrepareUnusable(ctx context.Context, actor auth.Principal, input orm.ModelInput[M]) (PreparedCreation[M], error) {
	prepared, _, err := creator.prepare(ctx, actor, input, "", true, nil)
	return prepared, err
}
func (creator *Creator[M]) validCall(ctx context.Context, actor auth.Principal) error {
	if creator == nil || creator.state == nil {
		return managementError(CodeInvalidConfig, nil)
	}
	if nilModelValue(ctx) {
		return managementError(CodeInvalidInput, nil)
	}
	if err := ctx.Err(); err != nil {
		return managementError(CodeInvalidInput, err)
	}
	if _, bound := actor.CredentialDomain(); !actor.Authenticated() || !bound {
		return managementError(CodePermission, nil)
	}
	return nil
}
func (creator *Creator[M]) prepare(ctx context.Context, actor auth.Principal, input orm.ModelInput[M], raw string, unusable bool, form *formmodel.BoundForm) (PreparedCreation[M], []formmodel.FilePublication, error) {
	if err := creator.validCall(ctx, actor); err != nil {
		return PreparedCreation[M]{}, nil, err
	}
	state := creator.state
	if !unusable {
		if raw == "" {
			return PreparedCreation[M]{}, nil, passwordInputError("required")
		}
		if len(raw) > state.config.MaxPasswordBytes {
			return PreparedCreation[M]{}, nil, passwordInputError("max_length")
		}
	}
	candidate, err := state.prepareInput(ctx, input, form)
	if err != nil {
		return PreparedCreation[M]{}, nil, err
	}
	candidate.raw, candidate.unusable = raw, unusable
	namespace, _ := actor.CredentialDomain()
	if _, err := state.store.state.binding.prepareCandidateCredential(namespace, candidate.row, nil); err != nil {
		return PreparedCreation[M]{}, nil, managementError(CodeInvalidConfig, err)
	}
	check := func(work context.Context, scope db.Queryer) error {
		current, err := state.preflight(work, scope, actor, candidate, false)
		if err != nil {
			return err
		}
		if len(candidate.pendingFiles) != 0 {
			return nil
		}
		return state.prepareCreationThrough(work, scope, current, namespace, candidate, false)
	}
	if err := creationSnapshot(ctx, state.store.state.backend, check); err != nil {
		return PreparedCreation[M]{}, nil, err
	}
	var publications []formmodel.FilePublication
	if len(candidate.pendingFiles) != 0 {
		publications, err = state.publishFiles(ctx, candidate)
		if err != nil {
			return PreparedCreation[M]{}, publications, err
		}
		// Publication can run arbitrary storage I/O while account authority
		// changes. Check the actual stored names and current policy before hash.
		if err := creationSnapshot(ctx, state.store.state.backend, check); err != nil {
			return PreparedCreation[M]{}, publications, err
		}
	}
	candidate.form = nil
	if err := state.encodeCandidate(ctx, namespace, candidate); err != nil {
		return PreparedCreation[M]{}, publications, err
	}
	if err := candidate.through.encode(ctx); err != nil {
		return PreparedCreation[M]{}, publications, err
	}
	return PreparedCreation[M]{state: &preparedCreation[M]{owner: creator, actorID: actor.ID(), domain: namespace, input: candidate}}, publications, nil
}

// Creation publishes only confirmed metadata, without granting profile access.
type Creation struct {
	RevokedSessions int
	ID, Revision    int64
	ChangedFields   []string
}

// Commit binds copies to one creator, actor AND domain, and one write attempt.
// It rechecks current authority, choices, all uniqueness and policy under the
// native write fence. Row, memberships and value-free audit commit together.
// Failure/unknown consumes the candidate, publishes zero, and is never retried.
// Cancellation before claiming the attempt does not consume it.
func (creator *Creator[M]) Commit(ctx context.Context, actor auth.Principal, prepared PreparedCreation[M]) (Creation, error) {
	if err := creator.validCall(ctx, actor); err != nil {
		return Creation{}, err
	}
	candidate := prepared.state
	if candidate == nil || candidate.owner != creator {
		return Creation{}, managementError(CodeInvalidInput, nil)
	}
	if candidate.actorID != actor.ID() || !candidate.domain.MatchesPrincipal(actor) {
		return Creation{}, managementError(CodePermission, nil)
	}
	if !candidate.attempted.CompareAndSwap(false, true) {
		return Creation{}, managementError(CodeConflict, nil)
	}
	// Transfer private material to this attempt before entering a host scope.
	// Even an invalid backend that returns before its callback finishes cannot
	// race cleanup of a row/raw string still in use by that callback.
	input := candidate.input
	candidate.input = nil
	state := creator.state
	return managementRelationWrite(ctx, state.backend, func(work context.Context, scope db.RelationSession) (Creation, error) {
		currentActor, err := state.preflight(work, scope, actor, input, true)
		if err != nil {
			return Creation{}, err
		}
		namespace, _ := currentActor.CredentialDomain()
		_, result, err := state.insertIn(work, scope, namespace, currentActor, input, time.Time{}, false)
		return result, err
	})
}

func creationLifecycleConfig[M any](policy CreationPolicy[M]) AccountDeleteConfig {
	return AccountDeleteConfig{Account: policy.RelatedPermissions.Account, Group: policy.RelatedPermissions.Group, Permission: policy.RelatedPermissions.Permission, Now: policy.Now}
}
