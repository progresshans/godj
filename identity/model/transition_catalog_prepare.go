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
	source     CatalogCreationSource
	before     *M
	prepared   orm.PreparedModel[M]
	row        M
	changed    bool
	values     map[string]query.Value
	input      orm.ModelInput[M]
	references map[string]TransitionTargetReference
	resolve    func(context.Context, db.Queryer, auth.Principal, transitionTargetMappings) (catalogCreationCandidate[M], error)
}

func prepareCatalogCreation[G, P any](ctx context.Context, scope db.Queryer, actor auth.Principal, target *transitionCatalogTarget[G, P], groups []CatalogRowCreation[G], permissions []CatalogRowCreation[P], observed transitionCatalogObservation, mappings transitionTargetMappings) (preparedCatalogCreation, error) {
	var zero preparedCatalogCreation
	digest := &transitionDigestWriter{hash: sha256.New()}
	digest.part([]byte("godj.account-transition-catalog-creation.v1"))
	digest.part([]byte(observed.operation))
	digest.part([]byte("permissions"))
	permissionRows, err := prepareCatalogRows(ctx, scope, actor, target.permissions, permissions, observed.before.permissions, observed.after.permissions, "", mappings, digest)
	if err != nil {
		return zero, err
	}
	digest.part([]byte("groups"))
	groupRows, err := prepareCatalogRows(ctx, scope, actor, target.groups, groups, observed.before.groups, observed.after.groups, target.oldRole.Catalog.GroupRevision, mappings, digest)
	if err != nil {
		return zero, err
	}
	nodes := make([]transitionReferenceNode, 0, len(permissionRows)+len(groupRows))
	for _, row := range permissionRows {
		nodes = append(nodes, transitionReferenceNode{kind: TransitionReferencePermission, source: SourceAccount{ID: row.source.ID, Revision: row.source.Revision}, references: row.references})
	}
	for _, row := range groupRows {
		nodes = append(nodes, transitionReferenceNode{kind: TransitionReferenceGroup, source: SourceAccount{ID: row.source.ID, Revision: row.source.Revision}, references: row.references})
	}
	order, err := transitionReferenceOrder(nodes)
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
			return applyCatalogCreation(ctx, scope, actor, at, target, permissionRows, groupRows, observed, mappings, order)
		},
	}, nil
}

func prepareCatalogRows[M any](ctx context.Context, scope db.Queryer, actor auth.Principal, model transitionCatalogModel[M], requests []CatalogRowCreation[M], before, after map[int64]transitionCatalogRow, sharedRevision string, mappings transitionTargetMappings, digest *transitionDigestWriter) ([]catalogCreationCandidate[M], error) {
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
			candidate.input, candidate.references = request.Input, maps.Clone(request.References)
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
		candidate.values = values
		var err error
		candidate, err = prepareCatalogCreationCandidate(ctx, scope, actor, model, candidate, mappings)
		if err != nil {
			return nil, err
		}
		if err := writeCatalogCreationCandidate(digest, candidate); err != nil {
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

func prepareCatalogCreationCandidate[M any](ctx context.Context, scope db.Queryer, actor auth.Principal, model transitionCatalogModel[M], candidate catalogCreationCandidate[M], mappings transitionTargetMappings) (catalogCreationCandidate[M], error) {
	var zero catalogCreationCandidate[M]
	values := maps.Clone(candidate.values)
	if model.created {
		input, pending, err := bindTransitionReferences(model.objects, model.metadata, model.role, model.allowed, candidate.input, candidate.references, mappings)
		if err != nil {
			return zero, err
		}
		maps.Copy(values, input.Values())
		initial, err := orm.InitialModelValues(model.metadata)
		if err != nil {
			return zero, err
		}
		maps.Copy(initial, values)
		for name := range pending {
			if candidate.references[name].Kind == TransitionReferenceAccount {
				return zero, &query.Error{Category: query.CategoryQuery, Code: query.CodeUnsupported, Detail: "catalog creation referencing pending account keys requires a joint identity allocation plan"}
			}
			delete(initial, name)
		}
		if len(pending) != 0 {
			if err := checkCatalogScalars(model.metadata, model.name, model.code, initial); err != nil {
				return zero, err
			}
			if err := validateTransitionScalarValues(model.metadata, initial, true, pending); err != nil {
				return zero, err
			}
			known := slices.DeleteFunc(slices.Clone(model.relations), func(relation ProfileRelation[M]) bool { return pending[relation.state.field.Name] })
			if err := checkProfileRelations(ctx, scope, actor, ProfileRelationOrigin{operation: ProfileRelationTransition}, known, initial); err != nil {
				return zero, err
			}
			original := candidate
			candidate.values = initial
			candidate.resolve = func(ctx context.Context, scope db.Queryer, actor auth.Principal, actual transitionTargetMappings) (catalogCreationCandidate[M], error) {
				return prepareCatalogCreationCandidate(ctx, scope, actor, model, original, actual)
			}
			return candidate, nil
		}
	}
	prepared, err := model.objects.PrepareValues(candidate.before, values)
	if err != nil {
		return zero, managementError(CodeInvalidInput, err)
	}
	candidate.prepared = prepared
	candidate.row, err = prepared.Model()
	if err != nil {
		return zero, err
	}
	candidate.values, err = model.objects.ModelValues(candidate.row)
	if err != nil {
		return zero, err
	}
	if err := checkCatalogScalars(model.metadata, model.name, model.code, candidate.values); err != nil {
		return zero, err
	}
	if err := validateTransitionValues(ctx, scope, model.objects, candidate.row, candidate.values, model.created); err != nil {
		return zero, err
	}
	if err := validateCatalogCreationCandidate(ctx, scope, actor, model, candidate); err != nil {
		return zero, err
	}
	return candidate, nil
}

func writeCatalogCreationCandidate[M any](digest *transitionDigestWriter, row catalogCreationCandidate[M]) error {
	encoded, err := json.Marshal(row.source)
	if err != nil {
		return err
	}
	digest.part(encoded)
	if err := writeTransitionValues(digest, row.values); err != nil {
		return err
	}
	return writeTransitionTargetReferences(digest, row.references)
}

func validateCatalogCreationCandidate[M any](ctx context.Context, scope db.Queryer, actor auth.Principal, model transitionCatalogModel[M], candidate catalogCreationCandidate[M]) error {
	if err := checkProfileRelations(ctx, scope, actor, ProfileRelationOrigin{operation: ProfileRelationTransition}, model.relations, candidate.values); err != nil {
		return err
	}
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

func applyCatalogCreation[G, P any](ctx context.Context, scope db.RelationSession, actor auth.Principal, at time.Time, target *transitionCatalogTarget[G, P], permissions []catalogCreationCandidate[P], groups []catalogCreationCandidate[G], observed transitionCatalogObservation, references transitionTargetMappings, order []int) (createdCatalogState, error) {
	var zero createdCatalogState
	result := createdCatalogState{mapping: &CatalogMappings{Groups: make([]AccountMapping, len(groups)), Permissions: make([]AccountMapping, len(permissions))},
		before: cloneTransitionCatalogInventory(observed.before), after: cloneTransitionCatalogInventory(observed.after)}
	allocated := make(transitionTargetMappings, len(references))
	for kind, namespace := range references {
		namespace.targets = maps.Clone(namespace.targets)
		if namespace.targets == nil && kind != TransitionReferenceAccount {
			namespace.targets = make(map[int64]int64)
		}
		allocated[kind] = namespace
	}
	finalPermissions, finalGroups := slices.Clone(permissions), slices.Clone(groups)
	writePermission := func(index int) error {
		candidate := &finalPermissions[index]
		if err := resolveCatalogCreationCandidate(ctx, scope, actor, candidate, allocated); err != nil {
			return err
		}
		id, values, err := saveCatalogCreationCandidate(ctx, scope, target.permissions, candidate)
		if err != nil {
			return err
		}
		allocated[TransitionReferencePermission].targets[candidate.source.ID] = id
		if target.permissions.created {
			if _, err := addTransitionIdentity(ctx, scope, &result.identities, target.project, target.permissions.identity, target.permissions.objects, candidate.row); err != nil {
				return err
			}
		}
		result.mapping.Permissions[index] = AccountMapping{SourceID: candidate.source.ID, SourceRevision: candidate.source.Revision, TargetID: id, TargetRevision: candidate.source.TargetRevision}
		result.after.permissions[id] = transitionCatalogRow{revision: candidate.source.TargetRevision, code: observed.before.permissions[candidate.source.ID].code, values: values}
		return nil
	}
	writeGroup := func(index int) error {
		candidate := &finalGroups[index]
		if err := resolveCatalogCreationCandidate(ctx, scope, actor, candidate, allocated); err != nil {
			return err
		}
		id, _, err := saveCatalogCreationCandidate(ctx, scope, target.groups, candidate)
		if err != nil {
			return err
		}
		allocated[TransitionReferenceGroup].targets[candidate.source.ID] = id
		result.mapping.Groups[index] = AccountMapping{SourceID: candidate.source.ID, SourceRevision: candidate.source.Revision, TargetID: id, TargetRevision: candidate.source.TargetRevision}
		if target.groups.created {
			if _, err := addTransitionIdentity(ctx, scope, &result.identities, target.project, target.groups.identity, target.groups.objects, candidate.row); err != nil {
				return err
			}
		}
		return nil
	}
	for _, index := range order {
		var err error
		if index < len(finalPermissions) {
			err = writePermission(index)
		} else {
			err = writeGroup(index - len(finalPermissions))
		}
		if err != nil {
			return zero, err
		}
	}
	// Grants use the complete native key correspondence, regardless of which
	// catalog namespace supplied the first dependency-free identity.
	var changes []orm.CollectionChange
	for i, candidate := range finalGroups {
		id := result.mapping.Groups[i].TargetID
		keys, err := remapTransitionKeys(observed.before.groups[candidate.source.ID].permissions, allocated[TransitionReferencePermission].targets)
		if err != nil {
			return zero, err
		}
		result.after.groups[id] = transitionCatalogRow{revision: candidate.source.TargetRevision, permissions: keys, values: maps.Clone(candidate.values)}
		if !target.groups.created {
			old := result.before.groups[id]
			old.values = maps.Clone(candidate.values)
			old.revision, _ = candidate.values[target.oldRole.Catalog.GroupRevision].Integer()
			result.before.groups[id] = old
		}
		change, err := target.grants(candidate.row, keys)
		if err != nil {
			return zero, err
		}
		changes = append(changes, change)
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
		applied, err := batch.Apply(ctx, nil)
		if err != nil {
			return zero, err
		}
		result.insertions = append(result.insertions, applied)
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
			if err := validateTransitionValues(ctx, scope, target.permissions.objects, candidate.row, candidate.values, false); err != nil {
				return err
			}
			if err := validateCatalogCreationCandidate(ctx, scope, actor, target.permissions, candidate); err != nil {
				return err
			}
		}
		for _, candidate := range finalGroups {
			if err := validateTransitionValues(ctx, scope, target.groups.objects, candidate.row, candidate.values, false); err != nil {
				return err
			}
			if err := validateCatalogCreationCandidate(ctx, scope, actor, target.groups, candidate); err != nil {
				return err
			}
		}
		return nil
	}
	return result, result.verifyPolicy(ctx, scope)
}

func resolveCatalogCreationCandidate[M any](ctx context.Context, scope db.Queryer, actor auth.Principal, candidate *catalogCreationCandidate[M], mappings transitionTargetMappings) error {
	if candidate.resolve == nil {
		return nil
	}
	resolved, err := candidate.resolve(ctx, scope, actor, mappings)
	if err != nil {
		return err
	}
	if resolved.resolve != nil {
		return managementError(CodeConflict, nil)
	}
	*candidate = resolved
	return nil
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
	candidate.values = values
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
