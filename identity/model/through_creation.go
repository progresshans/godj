package model

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	accountaudit "github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// MaximumThroughCreations is the supported upper bound for one account edit's
// newly allocated identities. A configured edit must choose its own positive
// ThroughCreationLimit; retained pairs do not consume that budget or hash work.
const MaximumThroughCreations = 4096

// ThroughEndpoints describes a missing physical pair. Either endpoint's key
// can be pending; actual keys can be zero or negative. Server-supplied
// endpoint fields cannot also appear in the factory's
// ModelInput, even with equal values. The enclosing operation owns the endpoint
// choice policy; the creation owner also checks its configured FK policies.
type ThroughEndpoints struct {
	source, target                     ir.Field
	sourceKey, targetKey               int64
	sourcePresent, targetPresent       bool
	sourceReference, targetReference   TransitionTargetReference
	sourceReferenced, targetReferenced bool
}

func (pair ThroughEndpoints) SourceField() string { return pair.source.Name }
func (pair ThroughEndpoints) TargetField() string { return pair.target.Name }

// SourceKey distinguishes a pending INSERT from an actual zero key. Ordinary
// root creation has an absent source; edits and staged transitions have a key.
func (pair ThroughEndpoints) SourceKey() (int64, bool) { return pair.sourceKey, pair.sourcePresent }
func (pair ThroughEndpoints) TargetKey() (int64, bool) { return pair.targetKey, pair.targetPresent }

// SourceReference and TargetReference identify reviewed transition
// correspondences. Their source keys are not the eventual endpoint keys.
// Ordinary creation and editing have no transition correspondence.
// Presence is independent of Kind: the account namespace has an empty Kind.
func (pair ThroughEndpoints) SourceReference() (TransitionTargetReference, bool) {
	return pair.sourceReference, pair.sourceReferenced
}
func (pair ThroughEndpoints) TargetReference() (TransitionTargetReference, bool) {
	return pair.targetReference, pair.targetReferenced
}
func (ThroughEndpoints) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.ThroughEndpoints{redacted}"))
}

// AccountThroughInput supplies cleaned scalar creation intent. Password and
// Unusable are mutually exclusive; an empty Password never implies Unusable.
// Endpoint FKs, principal identity, revision, hash and timestamps are supplied
// by the bound owners. This is through-row creation, not nested M2M assignment
// or file publication; those entries are rejected rather than ignored.
type AccountThroughInput[M any] struct {
	Input    orm.ModelInput[M]
	Password string
	Unusable bool
}

func (AccountThroughInput[M]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.AccountThroughInput{redacted}"))
}
func (AccountThroughInput[M]) MarshalJSON() ([]byte, error) {
	return []byte(`"identity.model.AccountThroughInput{redacted}"`), nil
}

// AccountThroughConfig prepares scalar defaults once per missing physical pair,
// after current create authority (or FirstAccount's fresh operator declaration)
// is checked. Prepare must be pure, bounded and
// safe for concurrent calls. It must not open a DB scope, hash a password or
// publish external effects. Hashing belongs to the enclosing account operation
// after the read snapshot closes. With an unsaved parent, complete model and
// password policies first run after its real key is bound, before child INSERT;
// no typed callback receives a fabricated parent key. They also run after INSERT.
type AccountThroughConfig[M any] struct {
	Prepare func(context.Context, ThroughEndpoints) (AccountThroughInput[M], error)
}

// ThroughCreation is a typed creation owner's sealed capability for one
// selected identity model and complete source. It retains no database,
// credential store or current authority. CreationPolicy and AccountEditConfig
// each bind it explicitly to their own collection fields.
type ThroughCreation struct{ state *throughCreationState }

type throughCreationState struct {
	project     orm.ProjectBinding
	role        [32]byte
	model       ir.ModelIdentity
	identity    string
	declaration string
	endpoints   func(string, string) error
	attach      func(any) (*throughCreationState, error)
	bind        func(context.Context, db.Queryer, auth.Principal, *throughPreparation, bool, orm.CollectionChange) (orm.CollectionChange, error)
	draft       func(context.Context, db.Queryer, auth.Principal, bool, ThroughEndpoints, auth.CredentialDomain) (*throughDraft, error)
}

func (ThroughCreation) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.ThroughCreation{redacted}"))
}

// NewAccountThroughCreation binds the generated account and collection types.
// It performs no I/O, entropy or password work and can be configured before a
// Runtime exists. The actual owner later binds it to its own CredentialStore.
// Selected fields are scalar: new through identities start with empty own M2M
// memberships, and cannot publish files through this operation.
func NewAccountThroughCreation[M, G, P, GL, PL any](binding Binding[M], groups orm.ManyToMany[M, G, GL], permissions orm.ManyToMany[M, P, PL], overlay auth.Authorizer, hasher auth.PasswordHasher, creation CreationConfig[M], config AccountThroughConfig[M]) (ThroughCreation, error) {
	var zero ThroughCreation
	if binding.state == nil || nilModelValue(overlay) || config.Prepare == nil {
		return zero, managementError(CodeInvalidConfig, nil)
	}
	definition, err := prepareCreationDefinition(binding, groups, permissions, hasher, creation.CreationPolicy, nil)
	if err != nil {
		return zero, err
	}
	required, err := auth.RequiredPermissions(creation.AddPermission, append([]auth.Permission{creation.ChangePermission}, creation.AdditionalPermissions...)...)
	if err != nil {
		return zero, managementError(CodeInvalidConfig, err)
	}
	if len(definition.files) != 0 {
		return zero, managementError(CodeInvalidConfig, nil)
	}
	for _, field := range definition.metadata.ManyToMany {
		if slices.Contains(definition.fields, field.Name) {
			return zero, managementError(CodeInvalidConfig, nil)
		}
	}
	memberships, err := bindDeletionMemberships(binding.state.project, binding.state.role)
	if err != nil {
		return zero, managementError(CodeInvalidConfig, err)
	}
	declaration, err := prepareThroughDeclaration(binding.state.role.Model, definition.fields, required, definition.config.RelatedPermissions, definition.config.MaxPasswordBytes, definition.config.CaseInsensitiveLogin)
	if err != nil {
		return zero, err
	}
	policy := &throughCreationState{project: binding.state.project,
		role: binding.state.roleFingerprint, model: binding.state.role.Model, identity: binding.state.role.PrincipalID, declaration: declaration}
	policy.endpoints = func(source, target string) error {
		if source == target || !definition.allowed[source] || !definition.allowed[target] {
			return managementError(CodeInvalidConfig, nil)
		}
		return nil
	}
	policy.attach = func(owner any) (*throughCreationState, error) {
		store, ok := owner.(*CredentialStore[M])
		if !ok || store == nil || store.state == nil {
			return nil, managementError(CodeInvalidConfig, nil)
		}
		if err := store.valid(); err != nil {
			return nil, managementError(CodeInvalidConfig, err)
		}
		backend, ok := store.state.backend.(db.CoordinatedRelationAtomic)
		if !ok || nilModelValue(backend) {
			return nil, managementError(CodeInvalidConfig, nil)
		}
		state := &creationState[M]{creationDefinition: definition, store: store, backend: backend, overlay: overlay, required: slices.Clone(required)}
		bound := *policy
		bound.attach = nil
		bindAccountThroughDraft(&bound, state, memberships, config)
		if err := bindAccountThroughInput(&bound, state, memberships, config); err != nil {
			return nil, err
		}
		return &bound, nil
	}
	return ThroughCreation{state: policy}, nil
}

func bindAccountThroughInput[M any](policy *throughCreationState, state *creationState[M], memberships []deletionMembership, config AccountThroughConfig[M]) error {
	return bindTypedThroughInput(policy, state.binding.state.model, state.binding.state.objects, state.overlay, state.required,
		func(ctx context.Context, scope db.Queryer, pair ThroughEndpoints, domain auth.CredentialDomain) (*throughCandidate, error) {
			intent, err := config.Prepare(ctx, pair)
			if err = errors.Join(err, validateAuthorityScope(ctx, scope.(db.SessionValidator))); err != nil {
				return nil, err
			}
			candidate, err := state.prepareThroughInput(ctx, intent, pair)
			if err != nil {
				return nil, err
			}
			return newThroughAccountCandidate(state, policy, candidate, domain, memberships)
		},
		func(prepared *throughCandidate) (M, error) {
			candidate, ok := prepared.value.(*creationCandidate[M])
			if !ok || candidate == nil {
				var zero M
				return zero, managementError(CodeInvalidConfig, nil)
			}
			return candidate.row, nil
		})
}

// Every identity kind shares candidate ownership and exact endpoint binding.
// The callbacks retain their real generated model type; only the enclosing
// heterogeneous operation erases it, behind this sealed construction boundary.
func bindTypedThroughInput[M any](policy *throughCreationState, bound orm.BoundModel[M], objects orm.Manager[M], overlay auth.Authorizer, required []auth.Permission,
	prepare func(context.Context, db.Queryer, ThroughEndpoints, auth.CredentialDomain) (*throughCandidate, error),
	value func(*throughCandidate) (M, error)) error {
	metadata, err := objects.Metadata()
	if err != nil {
		return err
	}
	policy.bind = func(ctx context.Context, scope db.Queryer, actor auth.Principal, pending *throughPreparation, final bool, change orm.CollectionChange) (orm.CollectionChange, error) {
		authorized := false
		input := typedThroughModelInput[M]{build: func(source, target ir.Field, sourceKey, targetKey int64) orm.Mutation[M] {
			if !authorized {
				if err := authorizeThroughActor(ctx, scope, actor, overlay, required); err != nil {
					return orm.InvalidMutation[M](err)
				}
				authorized = true
			}
			key := throughPairKey{policy: policy, source: source.Name, target: target.Name, sourceKey: sourceKey, targetKey: targetKey}
			prepared := pending.pairs[key]
			if prepared == nil {
				if final || len(pending.ordered) >= pending.limit {
					return orm.InvalidMutation[M](managementError(CodeInvalidInput, nil))
				}
				pair := ThroughEndpoints{source: source.Clone(), target: target.Clone(), sourceKey: sourceKey, targetKey: targetKey, sourcePresent: true, targetPresent: true}
				var err error
				prepared, err = prepare(ctx, scope, pair, pending.domain)
				if err = errors.Join(err, validateAuthorityScope(ctx, scope.(db.SessionValidator))); err != nil {
					return orm.InvalidMutation[M](err)
				}
				if prepared == nil || prepared.policy != policy || prepared.identity.model != policy.model || prepared.identity.value == "" || pending.identities[prepared.identity] != nil {
					return orm.InvalidMutation[M](managementError(CodeInvalidConfig, nil))
				}
				pending.pairs[key], pending.identities[prepared.identity] = prepared, prepared
				pending.ordered = append(pending.ordered, prepared)
			}
			row, err := value(prepared)
			if err != nil {
				return orm.InvalidMutation[M](err)
			}
			row, err = objects.ApplyValues(row, nil)
			if err != nil {
				return orm.InvalidMutation[M](err)
			}
			values, err := objects.ModelValues(row)
			if err != nil {
				return orm.InvalidMutation[M](err)
			}
			assignments := make([]query.Assignment, 0, len(metadata.Fields)-1)
			for _, field := range metadata.Fields {
				if !field.PrimaryKey {
					assignments = append(assignments, orm.NewAssignment(field, values[field.Name]))
				}
			}
			return orm.NewCreateMutation(row, metadata.DBTable, assignments)
		}}
		return orm.WithCollectionThroughInput(change, bound, input)
	}
	return nil
}

type typedThroughModelInput[M any] struct {
	build func(ir.Field, ir.Field, int64, int64) orm.Mutation[M]
}

func (input typedThroughModelInput[M]) BuildManyToManyCreate(source, target ir.Field, sourceKey, targetKey int64) orm.Mutation[M] {
	return input.build(source, target, sourceKey, targetKey)
}

type throughPairKey struct {
	policy               *throughCreationState
	source, target       string
	sourceKey, targetKey int64
}
type throughIdentity struct {
	model ir.ModelIdentity
	value string
}
type throughCandidate struct {
	policy   *throughCreationState
	identity throughIdentity
	value    any // The actual typed identity candidate; never a map-based model.
	encode   func(context.Context) error
	check    func(context.Context, db.Queryer, auth.Principal, bool, []int64) error
	finish   func(context.Context, db.RelationSession, auth.Principal, int64, time.Time) (accountaudit.Event, lifecycleVerifier, error)
}
type throughPreparation struct {
	domain      auth.CredentialDomain
	authority   auth.CredentialDomain
	actorID     string
	limit       int
	pairs       map[throughPairKey]*throughCandidate
	identities  map[throughIdentity]*throughCandidate
	ordered     []*throughCandidate
	observation orm.ObservedCollectionChanges
}

func (state *creationState[M]) prepareThroughInput(ctx context.Context, intent AccountThroughInput[M], pair ThroughEndpoints) (*creationCandidate[M], error) {
	if !pair.sourcePresent || !pair.targetPresent {
		return nil, managementError(CodeInvalidInput, nil)
	}
	if len(intent.Input.Collections()) != 0 || intent.Unusable && intent.Password != "" {
		return nil, managementError(CodeInvalidInput, nil)
	}
	if !intent.Unusable {
		if intent.Password == "" {
			return nil, passwordInputError("required")
		}
		if len(intent.Password) > state.config.MaxPasswordBytes {
			return nil, passwordInputError("max_length")
		}
	}
	input, err := state.binding.state.objects.WithServerValues(intent.Input, map[string]query.Value{
		pair.source.Name: query.Integer(pair.sourceKey), pair.target.Name: query.Integer(pair.targetKey),
	})
	if err != nil {
		return nil, managementError(CodeInvalidInput, err)
	}
	candidate, err := state.prepareInput(ctx, input, nil)
	if err != nil {
		return nil, err
	}
	candidate.raw, candidate.unusable = intent.Password, intent.Unusable
	return candidate, nil
}

// The actor is the fresh principal loaded by the enclosing owner in this exact
// scope before any writes. Rechecking only the deny overlay cannot expand its
// authority through provisional rows. No supplied/client principal reaches here.
func authorizeThroughActor(ctx context.Context, scope db.Queryer, actor auth.Principal, overlay auth.Authorizer, required []auth.Permission) error {
	validator, ok := scope.(db.SessionValidator)
	if !ok || nilModelValue(validator) || nilModelValue(overlay) || !actor.Authenticated() {
		return invalidAuthorityScope()
	}
	if err := validateAuthorityScope(ctx, validator); err != nil {
		return err
	}
	for _, permission := range required {
		if !actor.Has(permission) {
			return managementError(CodePermission, nil)
		}
	}
	for _, permission := range required {
		allowed, err := overlay.Allowed(ctx, actor, permission)
		if err = errors.Join(err, validateAuthorityScope(ctx, validator)); err != nil {
			return err
		}
		if !allowed {
			return managementError(CodePermission, nil)
		}
	}
	return validateAuthorityScope(ctx, validator)
}

func newThroughAccountCandidate[M any](state *creationState[M], policy *throughCreationState, candidate *creationCandidate[M], domain auth.CredentialDomain, lifecycleMemberships []deletionMembership) (*throughCandidate, error) {
	values, err := state.binding.state.objects.ModelValues(candidate.row)
	if err != nil {
		return nil, err
	}
	principal, valid := values[policy.identity].String()
	if !valid || principal == "" {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	required, err := throughEffectPermissions(state.binding.state.role, policy.model, state.required, state.config.RelatedPermissions, values, lifecycleMemberships)
	if err != nil {
		return nil, err
	}
	result := &throughCandidate{policy: policy, identity: throughIdentity{policy.model, principal}, value: candidate}
	result.encode = func(ctx context.Context) error { return state.encodeCandidate(ctx, domain, candidate) }
	result.check = func(ctx context.Context, scope db.Queryer, actor auth.Principal, encoded bool, deleted []int64) error {
		// The creation policy's related grants remain independent of the edit
		// policy. A weaker enclosing editor cannot erase this owner's grants.
		if err := authorizeThroughActor(ctx, scope, actor, state.overlay, required); err != nil {
			return err
		}
		return state.checkCandidate(ctx, scope, domain, actor, ProfileRelationCreate, candidate, encoded, deleted)
	}
	result.finish = func(ctx context.Context, scope db.RelationSession, actor auth.Principal, id int64, at time.Time) (accountaudit.Event, lifecycleVerifier, error) {
		var zero accountaudit.Event
		objects, role := state.binding.state.objects, state.binding.state.role
		row, err := objects.Using(scope).Filter(state.store.state.key.Exact(id)).Get(ctx)
		if err != nil {
			return zero, nil, err
		}
		prepared, err := objects.PrepareValues(&row, nil)
		if err != nil {
			return zero, nil, err
		}
		checked := *candidate
		checked.prepared, checked.row, checked.stored = prepared, row, true
		verify := func(ctx context.Context, scope db.Session) error {
			if err := verifyEmptyOwnedMemberships(ctx, scope, lifecycleMemberships, role.Model, id); err != nil {
				return err
			}
			memberships, err := state.store.state.relations.memberships(ctx, scope, id)
			if err != nil || len(memberships.groups)+len(memberships.direct)+len(memberships.grants) != 0 {
				return managementError(CodePersistence, err)
			}
			profile, err := readProfileMemberships(ctx, scope, lifecycleMemberships, id, nil)
			if err != nil {
				return err
			}
			for _, keys := range profile {
				if len(keys) != 0 {
					return managementError(CodePersistence, nil)
				}
			}
			return nil
		}
		if err := verify(ctx, scope); err != nil {
			return zero, nil, err
		}
		if err := state.checkCandidate(ctx, scope, domain, actor, ProfileRelationCreate, &checked, true, nil); err != nil {
			return zero, nil, err
		}
		event, err := accountaudit.Prepare(actor, role.Model.AppLabel+"."+role.Model.ModelName, id, admin.ActionAdd, state.auditFields, at)
		return event, verify, err
	}
	return result, nil
}

func (pending *throughPreparation) check(ctx context.Context, scope db.Queryer, actor auth.Principal, effects orm.CollectionChangeEffects, encoded bool) error {
	if actor.ID() != pending.actorID || !pending.authority.MatchesPrincipal(actor) {
		return managementError(CodePermission, nil)
	}
	deleted := make(map[ir.ModelIdentity][]int64)
	for _, effect := range effects.Deletions().Rows() {
		if effect.Deleted {
			deleted[effect.Model] = append(deleted[effect.Model], effect.Key)
		}
	}
	for _, candidate := range pending.ordered {
		if err := candidate.check(ctx, scope, actor, encoded, deleted[candidate.policy.model]); err != nil {
			return err
		}
	}
	return validateAccountSessionScope(ctx, scope)
}

func (pending *throughPreparation) finish(ctx context.Context, scope db.RelationSession, actor auth.Principal, applied orm.AppliedCollectionChanges, at time.Time) ([]lifecycleVerifier, error) {
	remaining := maps.Clone(pending.identities)
	var events []accountaudit.Event
	var verifiers []lifecycleVerifier
	for _, row := range applied.Insertions() {
		var identityField string
		for _, candidate := range pending.ordered {
			if candidate.policy.model == row.Model {
				identityField = candidate.policy.identity
				break
			}
		}
		if identityField == "" {
			continue // Ordinary through rows retain their common ORM owner.
		}
		value, valid := row.Values[identityField].String()
		key := throughIdentity{row.Model, value}
		candidate := remaining[key]
		if !valid || candidate == nil {
			return nil, managementError(CodePersistence, nil)
		}
		delete(remaining, key)
		event, verify, err := candidate.finish(ctx, scope, actor, row.Key, at)
		if err != nil {
			return nil, err
		}
		events, verifiers = append(events, event), append(verifiers, verify)
	}
	if len(remaining) != 0 {
		// An unexpected conflict/no-op cannot adopt an independently inserted
		// identity or publish a hash/audit for a row that this batch never made.
		return nil, managementError(CodeConflict, nil)
	}
	for _, event := range events {
		if _, err := accountaudit.AppendIn(ctx, scope, event); err != nil {
			return nil, err
		}
	}
	return verifiers, nil
}

func throughEffectPermissions(role ir.AccountRole, model ir.ModelIdentity, base []auth.Permission, related RelatedRowPermissions, values map[string]query.Value, memberships []deletionMembership, pending ...string) ([]auth.Permission, error) {
	required := slices.Clone(base)
	for _, membership := range memberships {
		if membership.through != model {
			continue
		}
		_, left := values[membership.source.Name()].Integer()
		_, right := values[membership.target.Name()].Integer()
		if (!left && !slices.Contains(pending, membership.source.Name())) || (!right && !slices.Contains(pending, membership.target.Name())) {
			continue
		}
		var permission DeletionPermissions
		switch membership.owner {
		case role.Model:
			permission = related.Account
		case role.Catalog.Group:
			permission = related.Group
		case role.Catalog.Permission:
			permission = related.Permission
		default:
			return nil, managementError(CodeInvalidConfig, nil)
		}
		if permission.Change == "" {
			return nil, managementError(CodePermission, nil)
		}
		required = append(required, permission.Change)
		required = append(required, permission.Additional...)
	}
	slices.Sort(required)
	required = slices.Compact(required)
	return required, nil
}
