package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"math"
	"slices"
	"time"

	accountaudit "github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/rowset"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

type catalogCreationCandidate[M any] struct {
	source   CatalogCreationSource
	before   *M
	prepared orm.PreparedModel[M]
	row      M
	changed  bool
}

func prepareCatalogCreation[G, P any](ctx context.Context, scope db.Queryer, target *transitionCatalogTarget[G, P], groups []CatalogRowCreation[G], permissions []CatalogRowCreation[P], observed transitionCatalogObservation) (preparedCatalogCreation, error) {
	var zero preparedCatalogCreation
	digest := &transitionDigestWriter{hash: sha256.New()}
	digest.part([]byte("godj.account-transition-catalog-creation.v1"))
	digest.part([]byte(observed.operation))
	digest.part([]byte("permissions"))
	permissionRows, err := prepareCatalogRows(ctx, scope, target.permissions, permissions, observed.before.permissions, observed.after.permissions, "", digest)
	if err != nil {
		return zero, err
	}
	digest.part([]byte("groups"))
	groupRows, err := prepareCatalogRows(ctx, scope, target.groups, groups, observed.before.groups, observed.after.groups, target.oldRole.Catalog.GroupRevision, digest)
	if err != nil {
		return zero, err
	}
	manifest := CatalogCreationManifest{CreateGroups: target.groups.created, CreatePermissions: target.permissions.created,
		Groups: make([]CatalogCreationSource, len(groupRows)), Permissions: make([]CatalogCreationSource, len(permissionRows))}
	for i, row := range permissionRows {
		manifest.Permissions[i] = row.source
	}
	for i, row := range groupRows {
		manifest.Groups[i] = row.source
	}
	if err := validateCatalogCreationManifest(&manifest); err != nil {
		return zero, err
	}
	if digest.err != nil {
		return zero, digest.err
	}
	return preparedCatalogCreation{manifest: manifest, digest: hex.EncodeToString(digest.hash.Sum(nil)), bytes: digest.size,
		apply: func(ctx context.Context, scope db.RelationSession, actor auth.Principal, at time.Time) (createdCatalogState, error) {
			return applyCatalogCreation(ctx, scope, actor, at, target, permissionRows, groupRows, observed)
		},
	}, nil
}

func prepareCatalogRows[M any](ctx context.Context, scope db.Queryer, model transitionCatalogModel[M], requests []CatalogRowCreation[M], before, after map[int64]transitionCatalogRow, sharedRevision string, digest *transitionDigestWriter) ([]catalogCreationCandidate[M], error) {
	if model.created {
		if len(after) != 0 {
			return nil, managementError(CodeNotFresh, nil)
		}
		if len(before) != len(requests) {
			return nil, managementError(CodeConflict, nil)
		}
		if _, _, err := model.objects.Using(scope).OrderBy(model.key.Asc()).First(ctx); err != nil {
			return nil, err
		}
		if err := requireEmptyTransitionIncoming(ctx, scope, model.incoming); err != nil {
			return nil, err
		}
	} else if len(requests) != 0 || len(before) != len(after) {
		return nil, managementError(CodeConflict, nil)
	}
	keys := slices.Sorted(maps.Keys(before))
	result := make([]catalogCreationCandidate[M], len(keys))
	for i, id := range keys {
		source := before[id]
		candidate := catalogCreationCandidate[M]{source: CatalogCreationSource{ID: id, Revision: source.revision}}
		var values map[string]query.Value
		if model.created {
			request := requests[i]
			if request.Source.ID != id || request.Source.Revision != source.revision {
				return nil, managementError(CodeConflict, nil)
			}
			values = request.Input.Values()
			values[model.revision] = query.Integer(1)
			if model.code != "" {
				values[model.code] = query.String(string(source.code))
			}
			candidate.changed, candidate.source.TargetRevision = true, 1
		} else {
			existing, present := after[id]
			if !present || existing.code != source.code {
				return nil, managementError(CodeConflict, nil)
			}
			row, err := model.objects.Using(scope).Filter(model.key.Exact(id)).Get(ctx)
			if err != nil {
				return nil, err
			}
			actual, err := model.objects.ModelValues(row)
			if err != nil || !maps.Equal(actual, existing.values) {
				return nil, managementError(CodeConflict, err)
			}
			candidate.before = &row
			candidate.source.TargetRevision = existing.revision
			values = make(map[string]query.Value)
			// Only a shared Group can change: its new permission relation is
			// empty until the changed Permission model has actual target keys.
			candidate.changed = sharedRevision != "" && len(source.permissions) != 0
			if candidate.changed {
				for _, name := range []string{sharedRevision, model.revision} {
					revision, valid := actual[name].Integer()
					if !valid || revision <= 0 || revision == math.MaxInt64 {
						return nil, managementError(CodeInvalidInput, nil)
					}
					values[name] = query.Integer(revision + 1)
				}
				candidate.source.TargetRevision++
			}
		}
		prepared, err := model.objects.PrepareValues(candidate.before, values)
		if err != nil {
			return nil, managementError(CodeInvalidInput, err)
		}
		candidate.prepared = prepared
		candidate.row, err = prepared.Model()
		if err != nil {
			return nil, err
		}
		values, err = model.objects.ModelValues(candidate.row)
		if err != nil {
			return nil, err
		}
		if err := checkCatalogScalars(model.metadata, model.name, model.code, values); err != nil {
			return nil, err
		}
		if err := validateTransitionValues(ctx, scope, model.objects, candidate.row, values, model.created); err != nil {
			return nil, err
		}
		if err := validateCatalogCreationCandidate(ctx, scope, model, candidate); err != nil {
			return nil, err
		}
		if err := writeCatalogCreationCandidate(digest, model, candidate); err != nil {
			return nil, err
		}
		result[i] = candidate
	}
	return result, nil
}

func requireEmptyTransitionIncoming(ctx context.Context, scope db.Queryer, plans []query.Plan) error {
	for _, plan := range plans {
		found := false
		err := rowset.Scan(ctx, scope, plan, func(err error) error { return managementError(CodePersistence, err) }, func(rows db.Rows) (bool, error) {
			var key int64
			if err := rows.Scan(&key); err != nil {
				return false, err
			}
			found = true
			return false, nil
		})
		if err != nil {
			return err
		}
		if found {
			return managementError(CodeNotFresh, nil)
		}
	}
	return validateAccountSessionScope(ctx, scope)
}

func writeCatalogCreationCandidate[M any](digest *transitionDigestWriter, model transitionCatalogModel[M], row catalogCreationCandidate[M]) error {
	encoded, err := json.Marshal(row.source)
	if err != nil {
		return err
	}
	digest.part(encoded)
	values, err := model.objects.ModelValues(row.row)
	if err != nil {
		return err
	}
	return writeTransitionValues(digest, values)
}

func validateCatalogCreationCandidate[M any](ctx context.Context, scope db.Queryer, model transitionCatalogModel[M], candidate catalogCreationCandidate[M]) error {
	if model.validate == nil {
		return validateAccountSessionScope(ctx, scope)
	}
	var before *M
	if candidate.before != nil {
		row, err := model.objects.ApplyValues(*candidate.before, nil)
		if err != nil {
			return err
		}
		before = &row
	}
	row, err := model.objects.ApplyValues(candidate.row, nil)
	if err != nil {
		return err
	}
	failures, err := model.validate(ctx, scope, before, row)
	if err := errors.Join(err, validateAccountSessionScope(ctx, scope)); err != nil {
		return err
	}
	return validation.Reject(failures, nil)
}

func applyCatalogCreation[G, P any](ctx context.Context, scope db.RelationSession, actor auth.Principal, at time.Time, target *transitionCatalogTarget[G, P], permissions []catalogCreationCandidate[P], groups []catalogCreationCandidate[G], observed transitionCatalogObservation) (createdCatalogState, error) {
	var zero createdCatalogState
	result := createdCatalogState{mapping: &CatalogMappings{Groups: []AccountMapping{}, Permissions: []AccountMapping{}},
		before: cloneTransitionCatalogInventory(observed.before), after: cloneTransitionCatalogInventory(observed.after), newLinks: make(map[transitionCatalogPair]bool)}
	permissionKeys := make(map[int64]int64, len(permissions))
	finalPermissions, finalGroups := slices.Clone(permissions), slices.Clone(groups)
	for i := range finalPermissions {
		candidate := &finalPermissions[i]
		id, values, err := saveCatalogCreationCandidate(ctx, scope, target.permissions, candidate)
		if err != nil {
			return zero, err
		}
		permissionKeys[candidate.source.ID] = id
		result.mapping.Permissions = append(result.mapping.Permissions, AccountMapping{SourceID: candidate.source.ID, SourceRevision: candidate.source.Revision, TargetID: id, TargetRevision: candidate.source.TargetRevision})
		result.after.permissions[id] = transitionCatalogRow{revision: candidate.source.TargetRevision, code: observed.before.permissions[candidate.source.ID].code, values: values}
	}
	var changes []orm.CollectionChange
	for i := range finalGroups {
		candidate := &finalGroups[i]
		id, values, err := saveCatalogCreationCandidate(ctx, scope, target.groups, candidate)
		if err != nil {
			return zero, err
		}
		keys, err := remapTransitionKeys(observed.before.groups[candidate.source.ID].permissions, permissionKeys)
		if err != nil {
			return zero, err
		}
		result.mapping.Groups = append(result.mapping.Groups, AccountMapping{SourceID: candidate.source.ID, SourceRevision: candidate.source.Revision, TargetID: id, TargetRevision: candidate.source.TargetRevision})
		result.after.groups[id] = transitionCatalogRow{revision: candidate.source.TargetRevision, permissions: keys, values: values}
		if !target.groups.created {
			old := result.before.groups[id]
			old.values = maps.Clone(values)
			old.revision, _ = values[target.oldRole.Catalog.GroupRevision].Integer()
			result.before.groups[id] = old
		}
		change, err := target.grants(candidate.row, keys)
		if err != nil {
			return zero, err
		}
		changes = append(changes, change)
		for _, permission := range keys {
			result.newLinks[transitionCatalogPair{id, permission}] = true
		}
	}
	if len(changes) != 0 {
		batch, err := orm.PrepareCollectionChangesInSession(ctx, scope, changes...)
		if err != nil {
			return zero, err
		}
		if err := batch.ValidateSource(target.project); err != nil {
			return zero, err
		}
		if len(batch.Effects().Deletions().Rows()) != 0 {
			return zero, managementError(CodeConflict, nil)
		}
		if _, err := batch.Apply(ctx, nil); err != nil {
			return zero, err
		}
	}
	for _, candidate := range finalPermissions {
		if err := auditCatalogCreationCandidate(ctx, scope, actor, at, target.permissions, candidate); err != nil {
			return zero, err
		}
	}
	for _, candidate := range finalGroups {
		if err := auditCatalogCreationCandidate(ctx, scope, actor, at, target.groups, candidate); err != nil {
			return zero, err
		}
	}
	result.verifyPolicy = func(ctx context.Context, scope db.Queryer) error {
		for _, candidate := range finalPermissions {
			if err := validateCatalogCreationCandidate(ctx, scope, target.permissions, candidate); err != nil {
				return err
			}
		}
		for _, candidate := range finalGroups {
			if err := validateCatalogCreationCandidate(ctx, scope, target.groups, candidate); err != nil {
				return err
			}
		}
		return nil
	}
	return result, result.verifyPolicy(ctx, scope)
}

func saveCatalogCreationCandidate[M any](ctx context.Context, scope db.RelationSession, model transitionCatalogModel[M], candidate *catalogCreationCandidate[M]) (int64, map[string]query.Value, error) {
	if candidate.changed {
		if err := candidate.prepared.Save(ctx, scope, &candidate.row); err != nil {
			return 0, nil, err
		}
	}
	key, present, err := model.objects.PrimaryKey(candidate.row)
	id, integer := key.Integer()
	if err != nil || !present || !integer {
		return 0, nil, managementError(CodePersistence, err)
	}
	values, err := model.objects.ModelValues(candidate.row)
	if err != nil {
		return 0, nil, err
	}
	revision, valid := values[model.revision].Integer()
	if !valid || revision != candidate.source.TargetRevision || !model.created && id != candidate.source.ID {
		return 0, nil, managementError(CodePersistence, nil)
	}
	return id, values, nil
}

func auditCatalogCreationCandidate[M any](ctx context.Context, scope db.RelationSession, actor auth.Principal, at time.Time, model transitionCatalogModel[M], candidate catalogCreationCandidate[M]) error {
	if !candidate.changed {
		return nil
	}
	action := admin.ActionChange
	if model.created {
		action = admin.ActionAdd
	}
	key, _, err := model.objects.PrimaryKey(candidate.row)
	id, valid := key.Integer()
	if err != nil || !valid {
		return managementError(CodePersistence, err)
	}
	event, err := accountaudit.Prepare(actor, model.identity.AppLabel+"."+model.identity.ModelName, id, action, model.auditFields, at)
	if err != nil {
		return err
	}
	_, err = accountaudit.AppendIn(ctx, scope, event)
	return err
}

func cloneTransitionCatalogInventory(input transitionCatalogInventory) transitionCatalogInventory {
	clone := func(rows map[int64]transitionCatalogRow) map[int64]transitionCatalogRow {
		result := make(map[int64]transitionCatalogRow, len(rows))
		for id, row := range rows {
			row.values = maps.Clone(row.values)
			row.permissions = slices.Clone(row.permissions)
			result[id] = row
		}
		return result
	}
	return transitionCatalogInventory{groups: clone(input.groups), permissions: clone(input.permissions), links: slices.Clone(input.links)}
}
