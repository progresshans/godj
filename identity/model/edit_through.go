package model

import (
	"context"
	"slices"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

func attachThroughDefinitions[M any](store *CredentialStore[M], definitions map[string]*throughCreationState) (map[string]*throughCreationState, error) {
	result := make(map[string]*throughCreationState, len(definitions))
	attached := make(map[*throughCreationState]*throughCreationState)
	for name, policy := range definitions {
		if bound := attached[policy]; bound != nil {
			result[name] = bound
			continue
		}
		bound, err := policy.attach(store)
		if err != nil {
			return nil, err
		}
		result[name] = bound
		attached[policy] = bound
	}
	return result, nil
}

func prepareThroughCreations[M any](store *CredentialStore[M], fields []string, configured map[string]ThroughCreation, limit int) (map[string]*throughCreationState, []string, error) {
	definitions, ordinary, err := prepareThroughDefinitions(store.state.binding, fields, configured, limit)
	if err != nil {
		return nil, nil, err
	}
	attached, err := attachThroughDefinitions(store, definitions)
	return attached, ordinary, err
}

func prepareThroughDefinitions[M any](binding Binding[M], fields []string, configured map[string]ThroughCreation, limit int) (map[string]*throughCreationState, []string, error) {
	return prepareOwnedThroughDefinitions(binding.state.project, binding.state.roleFingerprint, binding.state.role.Model, fields, configured, limit)
}

func prepareOwnedThroughDefinitions(project orm.ProjectBinding, role [32]byte, owner ir.ModelIdentity, fields []string, configured map[string]ThroughCreation, limit int) (map[string]*throughCreationState, []string, error) {
	var ordinary []string
	if len(configured) == 0 && limit != 0 || len(configured) != 0 && (limit < 1 || limit > MaximumThroughCreations) {
		return nil, nil, managementError(CodeInvalidConfig, nil)
	}
	source, err := project.SourceSHA256()
	if err != nil {
		return nil, nil, err
	}
	result := make(map[string]*throughCreationState, len(configured))
	for name, configured := range configured {
		policy := configured.state
		if policy == nil || policy.role != role || policy.attach == nil || policy.endpoints == nil || !slices.Contains(fields, name) {
			return nil, nil, managementError(CodeInvalidConfig, nil)
		}
		actual, err := policy.project.SourceSHA256()
		if err != nil || actual != source {
			return nil, nil, managementError(CodeInvalidConfig, err)
		}
		matched := false
		for _, relation := range project.ManyToManyRelations() {
			if relation.Source == owner && relation.Field == name && relation.Through.Model == policy.model {
				if err := policy.endpoints(relation.Through.SourceField, relation.Through.TargetField); err != nil {
					return nil, nil, err
				}
				matched = true
			}
		}
		if !matched {
			return nil, nil, managementError(CodeInvalidConfig, nil)
		}
		result[name] = policy
	}
	for _, name := range fields {
		if result[name] == nil {
			ordinary = append(ordinary, name)
		}
	}
	return result, ordinary, nil
}

type accountEditRead[M any] struct {
	current, candidate M
	model              orm.PreparedModel[M]
	values             map[string]query.Value
	memberships        accountMemberships
	wasActive, active  bool
}

func (state *accountEditState[M]) readInput(ctx context.Context, scope db.Queryer, actor auth.Principal, id, revision int64, input orm.ModelInput[M]) (auth.Principal, accountEditRead[M], error) {
	var zero accountEditRead[M]
	store := state.sessions.state.credentials
	binding, role := store.state.binding, store.state.binding.state.role
	currentActor, allowed, err := store.AuthorizeIn(ctx, scope, actor, state.overlay, state.required[0], state.required[1:]...)
	if err != nil {
		return auth.Principal{}, zero, err
	}
	if !allowed {
		return auth.Principal{}, zero, managementError(CodePermission, nil)
	}
	current, found, err := state.resource.Get(ctx, scope, id)
	if err != nil {
		return auth.Principal{}, zero, err
	}
	if !found {
		return auth.Principal{}, zero, managementError(CodeNotFound, nil)
	}
	objects := state.resource.Manager()
	before, err := objects.ModelValues(current)
	if err != nil {
		return auth.Principal{}, zero, err
	}
	actual, valid := before[role.Revision].Integer()
	if !valid || actual <= 0 {
		return auth.Principal{}, zero, invalidAccountCredential()
	}
	if actual != revision {
		return auth.Principal{}, zero, managementError(CodeConflict, nil)
	}
	memberships, err := store.state.relations.memberships(ctx, scope, id)
	if err != nil {
		return auth.Principal{}, zero, err
	}
	namespace, _ := currentActor.CredentialDomain()
	if _, err := binding.prepareCredentialForDomain(namespace, current, memberships.grants); err != nil {
		return auth.Principal{}, zero, err
	}
	model, err := objects.PrepareInput(input, &current)
	if err != nil {
		return auth.Principal{}, zero, err
	}
	candidate, err := model.Model()
	if err != nil {
		return auth.Principal{}, zero, err
	}
	values, err := objects.ModelValues(candidate)
	if err != nil {
		return auth.Principal{}, zero, err
	}
	wasActive, err := credentialBoolean(before, role.Active)
	if err != nil {
		return auth.Principal{}, zero, err
	}
	active, err := credentialBoolean(values, role.Active)
	if err != nil {
		return auth.Principal{}, zero, err
	}
	return currentActor, accountEditRead[M]{current: current, candidate: candidate, model: model, values: values, memberships: memberships, wasActive: wasActive, active: active}, nil
}

func (state *accountEditState[M]) collectionChanges(ctx context.Context, scope db.Queryer, actor auth.Principal, current M, input orm.ModelInput[M], pending *throughPreparation, final bool) ([]orm.CollectionChange, error) {
	role := state.sessions.state.credentials.state.binding.state.role
	var changes []orm.CollectionChange
	appendChange := func(field string, makeChange func(M, []int64) (orm.CollectionChange, error)) error {
		keys, selected := input.Collection(field)
		if !selected {
			return nil
		}
		change, err := makeChange(current, keys)
		if err != nil {
			return err
		}
		if policy := state.through[field]; policy != nil {
			if pending == nil {
				return managementError(CodeInvalidConfig, nil)
			}
			change, err = policy.bind(ctx, scope, actor, pending, final, change)
			if err != nil {
				return err
			}
		}
		changes = append(changes, change)
		return nil
	}
	if err := appendChange(role.Groups, state.groupsChange); err != nil {
		return nil, err
	}
	if err := appendChange(role.Permissions, state.permissionsChange); err != nil {
		return nil, err
	}
	for _, collection := range state.collections {
		if err := appendChange(collection.state.field.Name, collection.state.prepare); err != nil {
			return nil, err
		}
	}
	return changes, nil
}

type accountEditFinal[M any] struct {
	row     M
	profile map[string][]int64
}

func (state *accountEditState[M]) checkEdit(ctx context.Context, scope db.Queryer, actor auth.Principal, id int64, read accountEditRead[M], plan *lifecyclePlan[M]) (accountEditFinal[M], error) {
	var zero accountEditFinal[M]
	role := state.sessions.state.credentials.state.binding.state.role
	final := map[string]query.Value{role.Revision: query.Integer(plan.rootRevision)}
	for _, field := range plan.root.nulls {
		final[field] = query.Null()
	}
	candidate, err := state.resource.Manager().ApplyValues(read.candidate, final)
	if err != nil {
		return zero, err
	}
	if len(state.relations) != 0 {
		values, err := state.resource.Manager().ModelValues(candidate)
		if err != nil {
			return zero, err
		}
		if err := checkProfileRelations(ctx, scope, actor, ProfileRelationOrigin{operation: ProfileRelationChange, id: id}, state.relations, values); err != nil {
			return zero, err
		}
	}
	groups := plan.projection.keys(role.Model, id, role.Groups, read.memberships.groups)
	direct := plan.projection.keys(role.Model, id, role.Permissions, read.memberships.direct)
	profile, err := readProfileMemberships(ctx, scope, state.lifecycle.memberships, id, plan.projection)
	if err != nil {
		return zero, err
	}
	if err := checkProfileCollections(ctx, scope, actor, ProfileRelationOrigin{operation: ProfileRelationChange, id: id}, state.collections, profile); err != nil {
		return zero, err
	}
	if err := state.validateCandidate(ctx, scope, read.current, candidate, groups, direct, profile); err != nil {
		return zero, err
	}
	return accountEditFinal[M]{row: candidate, profile: profile}, nil
}

func (state *accountEditState[M]) prepareThrough(ctx context.Context, actor auth.Principal, id, revision int64, input orm.ModelInput[M]) (*throughPreparation, error) {
	selected := false
	for name := range input.Collections() {
		selected = selected || state.through[name] != nil
	}
	if !selected {
		return nil, nil
	}
	store, role := state.sessions.state.credentials, state.sessions.state.credentials.state.binding.state.role
	pending, err := managementPreparationSnapshot(ctx, store.state.backend, func(work context.Context, scope db.Queryer) (*throughPreparation, error) {
		current, read, err := state.readInput(work, scope, actor, id, revision, input)
		if err != nil {
			return nil, err
		}
		domain, _ := current.CredentialDomain()
		pending := &throughPreparation{domain: domain, authority: domain, actorID: current.ID(), limit: state.throughLimit,
			pairs: make(map[throughPairKey]*throughCandidate), identities: make(map[throughIdentity]*throughCandidate)}
		changes, err := state.collectionChanges(work, scope, current, read.current, input, pending, false)
		if err != nil {
			return nil, err
		}
		pending.observation, err = orm.ObserveCollectionChangesInReadScope(work, scope, changes...)
		if err != nil {
			return nil, err
		}
		effects := pending.observation.Effects()
		plan, err := state.lifecycle.prepare(work, scope, current, effects.Deletions(), effects.Insertions(), &lifecycleEdit{
			key: deletionKey{role.Model, id}, revision: revision, required: state.required, fields: read.model.ChangedFields(), values: read.values, deactivate: read.wasActive && !read.active,
		})
		if err != nil {
			return nil, err
		}
		final, err := state.checkEdit(work, scope, current, id, read, plan)
		if err != nil {
			return nil, err
		}
		var deleted []int64
		for _, effect := range effects.Deletions().Rows() {
			if effect.Deleted && effect.Model == role.Model {
				deleted = append(deleted, effect.Key)
			}
		}
		failures, err := read.model.ValidateUniqueAfterDeletion(work, scope, final.row, deleted)
		if err != nil {
			return nil, err
		}
		if err := validation.Reject(failures, nil); err != nil {
			return nil, err
		}
		if err := pending.check(work, scope, current, effects, false); err != nil {
			return nil, err
		}
		return pending, nil
	})
	if err != nil {
		return nil, err
	}
	for _, candidate := range pending.ordered {
		if err := candidate.encode(ctx); err != nil {
			return nil, err
		}
	}
	return pending, nil
}
