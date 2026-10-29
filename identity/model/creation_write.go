package model

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

// Expected read refusals stay data until the snapshot has closed. A missing
// domain is eligible only in FirstAccount's explicit fresh-state policy, never
// inferred here from a failure's error chain.
func creationSnapshot(ctx context.Context, backend db.SnapshotReader, read func(context.Context, db.Queryer) error) error {
	type observation struct{ rejection error }
	observed, err := db.ReadSnapshotResult(ctx, backend, func(work context.Context, scope db.Queryer) (observation, error) {
		failure := read(work, scope)
		if _, rejected := validation.Rejected(failure); rejected && errors.Unwrap(failure) == nil {
			return observation{failure}, nil
		}
		if classified, ok := failure.(*Error); ok && classified.Unwrap() == nil && (classified.Code == CodePermission || classified.Code == CodeNotFresh) {
			return observation{failure}, nil
		}
		return observation{}, failure
	})
	if err != nil {
		return managementError(CodePersistence, err)
	}
	return observed.rejection
}

func (state *creationState[M]) encodeCandidate(ctx context.Context, namespace auth.CredentialDomain, candidate *creationCandidate[M]) error {
	encoded, err := state.encode(ctx, candidate.raw, candidate.unusable)
	if err != nil {
		return credentialPasswordRejection(err)
	}
	binding := state.store.state.binding
	candidate.row, err = binding.state.objects.ApplyValues(candidate.row, map[string]query.Value{binding.state.role.Password: query.String(encoded)})
	if err != nil {
		return managementError(CodeInvalidConfig, err)
	}
	credential, err := binding.prepareCandidateCredential(namespace, candidate.row, nil)
	if err != nil || credential.HasUsablePassword() == candidate.unusable {
		return managementError(CodeInvalidConfig, err)
	}
	if err := ctx.Err(); err != nil {
		return managementError(CodeInvalidInput, err)
	}
	return nil
}

// The typed INSERT supplies a key but is still provisional. All requested
// memberships are collected together, including deletions caused by a database
// rule. The same lifecycle validates affected account/catalog rows, revisions,
// sessions and audit; authority is the actor observed before this INSERT.
func (state *creationState[M]) insertIn(ctx context.Context, scope db.RelationSession, namespace auth.CredentialDomain, actor auth.Principal, candidate *creationCandidate[M], at time.Time, operator bool) (M, Creation, error) {
	var zero M
	if candidate == nil || candidate.form != nil || len(candidate.pendingFiles) != 0 || state.lifecycle == nil {
		return zero, Creation{}, managementError(CodeInvalidInput, nil)
	}
	objects := state.binding.state.objects
	row, err := objects.ApplyValues(candidate.row, nil)
	if err != nil {
		return zero, Creation{}, err
	}
	if err := candidate.prepared.Save(ctx, scope, &row); err != nil {
		return zero, Creation{}, err
	}
	credential, err := state.binding.prepareCredentialForDomain(namespace, row, nil)
	if err != nil {
		return zero, Creation{}, err
	}
	if operator {
		actor = credential.Principal()
		if !actor.Active() || !actor.Staff() || !actor.Superuser() || !credential.HasUsablePassword() {
			return zero, Creation{}, invalidAccountCredential()
		}
	}
	key, present, err := objects.PrimaryKey(row)
	id, integer := key.Integer()
	if err != nil || !present || !integer {
		return zero, Creation{}, invalidAccountCredential()
	}
	var changes []orm.CollectionChange
	for _, collection := range []struct {
		keys    []int64
		prepare func(M, []int64) (orm.CollectionChange, error)
	}{{candidate.groups, state.groupsChange}, {candidate.permissions, state.permissionsChange}} {
		change, err := collection.prepare(row, collection.keys)
		if err != nil {
			return zero, Creation{}, err
		}
		changes = append(changes, change)
	}
	for _, collection := range state.collections {
		change, err := collection.state.prepare(row, candidate.profile[collection.state.field.Name])
		if err != nil {
			return zero, Creation{}, err
		}
		changes = append(changes, change)
	}
	batch, err := orm.PrepareCollectionChangesInSession(ctx, scope, changes...)
	if err != nil {
		return zero, Creation{}, err
	}
	if err := batch.ValidateSource(state.binding.state.project); err != nil {
		return zero, Creation{}, err
	}
	values, err := objects.ModelValues(row)
	if err != nil {
		return zero, Creation{}, err
	}
	effects := batch.Effects()
	plan, err := state.lifecycle.prepare(ctx, scope, actor, effects.Deletions(), effects.Insertions(), &lifecycleEdit{
		key: deletionKey{state.binding.state.role.Model, id}, revision: 1, required: state.required,
		fields: slices.Clone(state.auditFields), values: values, created: true, operator: operator, at: at,
	})
	if err != nil {
		return zero, Creation{}, err
	}
	final := map[string]query.Value{state.binding.state.role.Revision: query.Integer(plan.rootRevision)}
	for _, field := range plan.root.nulls {
		final[field] = query.Null()
	}
	prepared, err := objects.PrepareValues(&row, final)
	if err != nil {
		return zero, Creation{}, err
	}
	finalRow, err := prepared.Model()
	if err != nil {
		return zero, Creation{}, err
	}
	revoked := 0
	if _, err := batch.Apply(ctx, func(work context.Context, _ orm.CollectionChangeEffects) error {
		var err error
		revoked, err = plan.apply(work, scope)
		return err
	}); err != nil {
		return zero, Creation{}, err
	}
	// Validators and uniqueness now see the final collected graph. The
	// candidate's key excludes its own provisional row from uniqueness.
	checked := *candidate
	checked.row, checked.prepared, checked.stored = finalRow, prepared, true
	checked.profile, err = readProfileMemberships(ctx, scope, state.lifecycle.memberships, id, plan.projection)
	if err != nil {
		return zero, Creation{}, err
	}
	actualMemberships, err := state.store.state.relations.memberships(ctx, scope, id)
	if err != nil {
		return zero, Creation{}, err
	}
	checked.groups, checked.permissions = actualMemberships.groups, actualMemberships.direct
	operation := ProfileRelationCreate
	if operator {
		operation = ProfileRelationFirstAccount
		actor = auth.Principal{}
	}
	if err := state.checkCandidate(ctx, scope, namespace, actor, operation, &checked, true); err != nil {
		return zero, Creation{}, err
	}
	if err := prepared.Save(ctx, scope, &finalRow); err != nil {
		return zero, Creation{}, err
	}
	if err := checkProfileCollections(ctx, scope, actor, ProfileRelationOrigin{operation: operation}, state.collections, checked.profile); err != nil {
		return zero, Creation{}, err
	}
	if err := plan.verify(ctx, scope); err != nil {
		return zero, Creation{}, err
	}
	return finalRow, Creation{ID: id, Revision: plan.rootRevision, RevokedSessions: revoked, ChangedFields: lifecycleChangedFields(state.metadata, plan.root)}, nil
}
