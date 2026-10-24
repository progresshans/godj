package model

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"
	"time"

	accountaudit "github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

// CreationValidator checks the actual detached model and proposed membership
// under current authority, both before hashing and under the final write fence.
// The reader is borrowed and must not be retained, mutated or re-entered. The
// password field is deliberately redacted in this callback's model copy.
type CreationValidator[M any] func(context.Context, db.Queryer, M, RoleMemberships) (validation.Errors, error)

// CreationPasswordValidator is a pure, concurrent-safe strength policy over
// the raw password and actual typed profile, whose encoded password is redacted.
// Return value-free diagnostics on "password" or NonField; do not retain raw
// material or publish external effects. It runs before hashing and at commit.
type CreationPasswordValidator[M any] func(context.Context, string, M) (validation.Errors, error)

// CreationConfig explicitly selects login, scalar profile, editable role flags
// and group/direct-permission collections. Model defaults apply to omitted
// fields. Principal ID, encoded password, revision and selected timestamps are
// server-owned. Inputs must already have passed their Form/JSON cleaning policy.
// Add AND change permission are required, as are all Additional permissions.
// All callbacks must be safe for concurrent use.
type CreationConfig[M any] struct {
	Fields                          []string
	AddPermission, ChangePermission auth.Permission
	AdditionalPermissions           []auth.Permission
	Validate                        CreationValidator[M]
	ValidatePassword                CreationPasswordValidator[M]
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

// Creator owns authorized creation using the selected model and native DB.
// It does not initialize or adopt an account domain; first-account provisioning
// requires its separate operator policy.
type Creator[M any] struct{ state *creationState[M] }
type creationState[M any] struct {
	store                       *CredentialStore[M]
	backend                     db.CoordinatedRelationAtomic
	overlay                     auth.Authorizer
	hasher                      auth.PasswordHasher
	metadata                    ir.Model
	fields                      []string
	allowed                     map[string]bool
	required                    []auth.Permission
	config                      CreationConfig[M]
	saveGroups, savePermissions func(context.Context, db.Session, M, []int64) error
}

func (Creator[M]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.Creator{redacted}"))
}

// NewCreator is normally called by the generated project factory. The selected
// role and both actual collection capabilities must match the credential
// store's complete source. Construction performs no I/O or password work.
func NewCreator[M, G, P, GL, PL any](store *CredentialStore[M], expected Binding[M], groups orm.ManyToMany[M, G, GL], permissions orm.ManyToMany[M, P, PL], overlay auth.Authorizer, hasher auth.PasswordHasher, config CreationConfig[M]) (*Creator[M], error) {
	if err := store.valid(); err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	if expected.state == nil || nilModelValue(overlay) || nilModelValue(hasher) || len(config.Fields) == 0 || len(config.Fields) >= admin.MaximumChangedFields {
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
	role, project := binding.state.role, binding.state.project
	for _, err := range []error{groups.ValidateSource(project, role.Model, role.Groups), permissions.ValidateSource(project, role.Model, role.Permissions)} {
		if err != nil {
			return nil, managementError(CodeInvalidConfig, err)
		}
	}
	required, err := auth.RequiredPermissions(config.AddPermission, append([]auth.Permission{config.ChangePermission}, config.AdditionalPermissions...)...)
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	metadata, err := binding.state.objects.Metadata()
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	protected := map[string]bool{role.PrincipalID: true, role.Password: true, role.Revision: true, role.LastLogin: true, role.CreatedAt: true}
	allowed := make(map[string]bool, len(config.Fields))
	for _, name := range config.Fields {
		if name == "" || allowed[name] || protected[name] {
			return nil, managementError(CodeInvalidConfig, nil)
		}
		if name != role.Groups && name != role.Permissions {
			field, found := namedField(metadata, name)
			if !found || field.PrimaryKey || field.NonEditable || field.Kind.IsFile() || field.Relation != nil {
				return nil, managementError(CodeInvalidConfig, nil)
			}
		}
		allowed[name] = true
	}
	if !allowed[role.Login] {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	initial, err := orm.InitialModelValues(metadata)
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	for _, field := range metadata.Fields {
		if field.PrimaryKey || protected[field.Name] || allowed[field.Name] {
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
	if _, err := admin.PrepareEventTemplate("creation-startup", role.Model.AppLabel+"."+role.Model.ModelName, admin.ActionAdd, append(slices.Clone(fields), role.Password), ""); err != nil {
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
	config.Fields, config.AdditionalPermissions = nil, nil
	state := &creationState[M]{store: store, backend: backend, overlay: overlay, hasher: hasher, metadata: metadata, fields: fields, allowed: allowed, required: required, config: config}
	state.saveGroups = func(ctx context.Context, scope db.Session, row M, keys []int64) error {
		collection, err := groups.InSession(scope, row)
		if err != nil {
			return err
		}
		return collection.SetKeys(ctx, keys)
	}
	state.savePermissions = func(ctx context.Context, scope db.Session, row M, keys []int64) error {
		collection, err := permissions.InSession(scope, row)
		if err != nil {
			return err
		}
		return collection.SetKeys(ctx, keys)
	}
	return &Creator[M]{state: state}, nil
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
	owner               *Creator[M]
	actorID             string
	domain              auth.CredentialDomain
	prepared            orm.PreparedModel[M]
	row                 M
	groups, permissions []int64
	raw                 string
	unusable            bool
	attempted           atomic.Bool
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
	return creator.prepare(ctx, actor, input, password, false)
}

// PrepareUnusable explicitly creates a credential that cannot authenticate.
// It never invokes the password hasher or strength policy.
func (creator *Creator[M]) PrepareUnusable(ctx context.Context, actor auth.Principal, input orm.ModelInput[M]) (PreparedCreation[M], error) {
	return creator.prepare(ctx, actor, input, "", true)
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
func (creator *Creator[M]) prepare(ctx context.Context, actor auth.Principal, input orm.ModelInput[M], raw string, unusable bool) (PreparedCreation[M], error) {
	if err := creator.validCall(ctx, actor); err != nil {
		return PreparedCreation[M]{}, err
	}
	state := creator.state
	if !unusable {
		if raw == "" {
			return PreparedCreation[M]{}, profileInputError("password", "required")
		}
		if len(raw) > state.config.MaxPasswordBytes {
			return PreparedCreation[M]{}, profileInputError("password", "max_length")
		}
	}
	candidate, err := creator.prepareInput(ctx, input)
	if err != nil {
		return PreparedCreation[M]{}, err
	}
	candidate.owner, candidate.actorID, candidate.raw, candidate.unusable = creator, actor.ID(), raw, unusable
	candidate.domain, _ = actor.CredentialDomain()
	if _, err := state.store.state.binding.prepareCandidateCredential(candidate.domain, candidate.row, nil); err != nil {
		return PreparedCreation[M]{}, managementError(CodeInvalidConfig, err)
	}
	type observation struct{ rejection error }
	observed, err := db.ReadSnapshotResult(ctx, state.store.state.backend, func(work context.Context, scope db.Queryer) (observation, error) {
		_, failure := state.preflight(work, scope, actor, candidate, false)
		if _, rejected := validation.Rejected(failure); rejected && errors.Unwrap(failure) == nil {
			return observation{failure}, nil
		}
		if classified, ok := failure.(*Error); ok && classified.Code == CodePermission && classified.Unwrap() == nil {
			return observation{failure}, nil
		}
		return observation{}, failure
	})
	if err != nil {
		return PreparedCreation[M]{}, managementError(CodePersistence, err)
	}
	if observed.rejection != nil {
		return PreparedCreation[M]{}, observed.rejection
	}
	encoded, err := state.encode(ctx, raw, unusable)
	if err != nil {
		return PreparedCreation[M]{}, err
	}
	binding := state.store.state.binding
	candidate.row, err = binding.state.objects.ApplyValues(candidate.row, map[string]query.Value{binding.state.role.Password: query.String(encoded)})
	if err != nil {
		return PreparedCreation[M]{}, managementError(CodeInvalidConfig, err)
	}
	credential, err := binding.prepareCandidateCredential(candidate.domain, candidate.row, nil)
	if err != nil || credential.HasUsablePassword() == unusable {
		return PreparedCreation[M]{}, managementError(CodeInvalidConfig, err)
	}
	if err := ctx.Err(); err != nil {
		return PreparedCreation[M]{}, managementError(CodeInvalidInput, err)
	}
	return PreparedCreation[M]{state: candidate}, nil
}

// Creation publishes only confirmed metadata, without granting profile access.
type Creation struct {
	ID, Revision  int64
	ChangedFields []string
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
	attempt := &preparedCreation[M]{prepared: candidate.prepared, row: candidate.row, groups: candidate.groups, permissions: candidate.permissions, raw: candidate.raw, unusable: candidate.unusable}
	candidate.raw = ""
	var zero M
	candidate.row, candidate.prepared = zero, orm.PreparedModel[M]{}
	candidate.groups, candidate.permissions = nil, nil
	candidate = attempt
	state := creator.state
	return managementRelationWrite(ctx, state.backend, func(work context.Context, scope db.RelationSession) (Creation, error) {
		currentActor, err := state.preflight(work, scope, actor, candidate, true)
		if err != nil {
			return Creation{}, err
		}
		row, err := creator.Manager().ApplyValues(candidate.row, nil)
		if err != nil {
			return Creation{}, err
		}
		if err := candidate.prepared.Save(work, scope, &row); err != nil {
			return Creation{}, err
		}
		namespace, _ := currentActor.CredentialDomain()
		if _, err := state.store.state.binding.prepareCredentialForDomain(namespace, row, nil); err != nil {
			return Creation{}, err
		}
		if len(candidate.groups) != 0 {
			if err := state.saveGroups(work, scope, row, candidate.groups); err != nil {
				return Creation{}, err
			}
		}
		if len(candidate.permissions) != 0 {
			if err := state.savePermissions(work, scope, row, candidate.permissions); err != nil {
				return Creation{}, err
			}
		}
		key, present, err := creator.Manager().PrimaryKey(row)
		id, integer := key.Integer()
		if err != nil || !present || !integer {
			return Creation{}, invalidAccountCredential()
		}
		role := state.store.state.binding.state.role
		fields := append(slices.Clone(state.fields), role.Password)
		event, err := accountaudit.Prepare(currentActor, role.Model.AppLabel+"."+role.Model.ModelName, id, admin.ActionAdd, fields, state.config.Now())
		if err != nil {
			return Creation{}, err
		}
		if _, err := accountaudit.AppendIn(work, scope, event); err != nil {
			return Creation{}, err
		}
		return Creation{ID: id, Revision: 1, ChangedFields: fields}, nil
	})
}
