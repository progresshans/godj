package model

import (
	"context"
	"errors"
	"slices"
	"time"

	accountaudit "github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
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
		if _, rejected := validation.Rejected(failure); rejected && (errors.Unwrap(failure) == nil || errors.Unwrap(failure) == passwordInputSource) {
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

// All results from insertIn are provisional. The owning operation must append
// audit and confirm the SAME native transaction before publishing anything.
func (state *creationState[M]) insertIn(ctx context.Context, scope db.RelationSession, namespace auth.CredentialDomain, candidate *creationCandidate[M]) (M, Creation, error) {
	var zero M
	objects := state.store.state.binding.state.objects
	row, err := objects.ApplyValues(candidate.row, nil)
	if err != nil {
		return zero, Creation{}, err
	}
	if err := candidate.prepared.Save(ctx, scope, &row); err != nil {
		return zero, Creation{}, err
	}
	if _, err := state.store.state.binding.prepareCredentialForDomain(namespace, row, nil); err != nil {
		return zero, Creation{}, err
	}
	if len(candidate.groups) != 0 {
		if err := state.saveGroups(ctx, scope, row, candidate.groups); err != nil {
			return zero, Creation{}, err
		}
	}
	if len(candidate.permissions) != 0 {
		if err := state.savePermissions(ctx, scope, row, candidate.permissions); err != nil {
			return zero, Creation{}, err
		}
	}
	key, present, err := objects.PrimaryKey(row)
	id, integer := key.Integer()
	if err != nil || !present || !integer {
		return zero, Creation{}, invalidAccountCredential()
	}
	return row, Creation{ID: id, Revision: 1, ChangedFields: slices.Clone(state.auditFields)}, nil
}
func (state *creationState[M]) auditIn(ctx context.Context, scope db.Session, actor auth.Principal, result Creation) error {
	return state.auditAtIn(ctx, scope, actor, result, state.config.Now())
}
func (state *creationState[M]) auditAtIn(ctx context.Context, scope db.Session, actor auth.Principal, result Creation, at time.Time) error {
	role := state.store.state.binding.state.role
	event, err := accountaudit.Prepare(actor, role.Model.AppLabel+"."+role.Model.ModelName, result.ID, admin.ActionAdd, result.ChangedFields, at)
	if err != nil {
		return err
	}
	_, err = accountaudit.AppendIn(ctx, scope, event)
	return err
}
