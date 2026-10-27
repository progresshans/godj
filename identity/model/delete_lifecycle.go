package model

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/sessions"
)

// DeletionPermissions covers direct row effects on one selected model. A
// cascaded deletion requires Delete; a surviving SET_NULL row requires Change.
// Additional grants apply to both. Removing a membership as a consequence of
// deleting its target does not require an independent edit grant on its owner.
type DeletionPermissions struct {
	Delete, Change auth.Permission
	Additional     []auth.Permission
}

// RelatedRowPermissions authorizes additional selected-model effects of an
// account edit. An omitted operation grants no authority for that effect.
// The edited root continues to require the editor's complete change policy.
type RelatedRowPermissions struct {
	Account, Group, Permission DeletionPermissions
}

// AccountDeleteConfig explicitly declares authority for all three selected
// lifecycle models. Ordinary host rows follow their declared relation policy.
// Now must be safe for concurrent calls and must not perform external effects.
type AccountDeleteConfig struct {
	Account, Group, Permission DeletionPermissions
	Now                        func() time.Time
}

type deletionKey struct {
	model ir.ModelIdentity
	id    int64
}
type deletionTouch struct {
	key                deletionKey
	deleted, direct    bool
	nulls, collections []string
	fields             []string
	root               bool
	created            bool
}
type deletionStored struct {
	revision    int64
	principalID string
	advance     func(context.Context, db.Session) error
	future      func(context.Context, db.Session, auth.CredentialDomain, *lifecycleProjection, *deletionTouch, map[string]query.Value) (lifecycleVerifier, error)
}
type deletionModel struct {
	metadata       ir.Model
	remove, change []auth.Permission
	load           func(context.Context, db.Session, auth.CredentialDomain, int64) (deletionStored, error)
}
type deletionLifecycle[M any] struct {
	credentials *CredentialStore[M]
	revoke      func(context.Context, db.Session, string, sessions.ID) (int, error)
	overlay     auth.Authorizer
	models      map[ir.ModelIdentity]deletionModel
	memberships []deletionMembership
	now         func() time.Time
}

func newDeletionLifecycle[M, G, P any](sessionStore *SessionStore[M], expected Binding[M], groups orm.RelationObjectDescriptor[G], permissions orm.RelationObjectDescriptor[P], overlay auth.Authorizer, config AccountDeleteConfig) (*deletionLifecycle[M], error) {
	return newLifecycle(sessionStore, expected, groups, permissions, overlay, config, true)
}

func newLifecycle[M, G, P any](sessionStore *SessionStore[M], expected Binding[M], groups orm.RelationObjectDescriptor[G], permissions orm.RelationObjectDescriptor[P], overlay auth.Authorizer, config AccountDeleteConfig, requireAll bool) (*deletionLifecycle[M], error) {
	if sessionStore == nil || sessionStore.state == nil {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	return newLifecycleFromCredentials(sessionStore.state.credentials, expected, groups, permissions, overlay, config, requireAll, sessionStore.RevokeIn)
}

// First-account provisioning has no older accounts in its new domain. It uses
// the same collector with no revocation capability; an unexpected revocation
// is an error, never an ignored effect or a fabricated session owner.
func newLifecycleFromCredentials[M, G, P any](store *CredentialStore[M], expected Binding[M], groups orm.RelationObjectDescriptor[G], permissions orm.RelationObjectDescriptor[P], overlay auth.Authorizer, config AccountDeleteConfig, requireAll bool, revoke func(context.Context, db.Session, string, sessions.ID) (int, error)) (*deletionLifecycle[M], error) {
	if expected.state == nil || nilModelValue(overlay) {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	if err := store.valid(); err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	binding := store.state.binding
	actualSource, err := binding.state.project.SourceSHA256()
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	expectedSource, err := expected.state.project.SourceSHA256()
	if err != nil || actualSource != expectedSource || binding.state.roleFingerprint != expected.state.roleFingerprint {
		return nil, managementError(CodeInvalidConfig, err)
	}
	if writer, ok := store.state.backend.(db.CoordinatedRelationAtomic); !ok || nilModelValue(writer) || !zeroModelDescriptor(groups) || !zeroModelDescriptor(permissions) {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	role, project := binding.state.role, binding.state.project
	group, err := orm.BindModel(project, role.Catalog.Group, groups)
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	permission, err := orm.BindModel(project, role.Catalog.Permission, permissions)
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	state := &deletionLifecycle[M]{credentials: store, revoke: revoke, overlay: overlay, models: make(map[ir.ModelIdentity]deletionModel), now: config.Now}
	if state.now == nil {
		state.now = time.Now
	}
	var allPermissions []auth.Permission
	for _, declaration := range []struct {
		identity ir.ModelIdentity
		policy   DeletionPermissions
	}{{role.Model, config.Account}, {role.Catalog.Group, config.Group}, {role.Catalog.Permission, config.Permission}} {
		metadata, present := project.Model(declaration.identity)
		if !present {
			return nil, managementError(CodeInvalidConfig, nil)
		}
		remove, err := lifecycleRequiredPermissions(declaration.policy.Delete, declaration.policy.Additional, requireAll)
		if err != nil {
			return nil, managementError(CodeInvalidConfig, err)
		}
		change, err := lifecycleRequiredPermissions(declaration.policy.Change, declaration.policy.Additional, requireAll)
		if err != nil {
			return nil, managementError(CodeInvalidConfig, err)
		}
		state.models[declaration.identity] = deletionModel{metadata: metadata, remove: remove, change: change}
		allPermissions = append(allPermissions, remove...)
		allPermissions = append(allPermissions, change...)
	}
	slices.Sort(allPermissions)
	allPermissions = slices.Compact(allPermissions)
	if len(allPermissions) != 0 {
		if _, err := auth.RequiredPermissions(allPermissions[0], allPermissions[1:]...); err != nil {
			return nil, managementError(CodeInvalidConfig, err)
		}
	}
	state.memberships, err = bindDeletionMemberships(project, role)
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	account := state.models[role.Model]
	account.load = func(ctx context.Context, scope db.Session, domain auth.CredentialDomain, id int64) (deletionStored, error) {
		row, err := binding.state.objects.Using(scope).Filter(store.state.key.Exact(id)).Get(ctx)
		if err != nil {
			return deletionStored{}, err
		}
		membership, err := store.state.relations.memberships(ctx, scope, id)
		if err != nil {
			return deletionStored{}, err
		}
		credential, err := binding.prepareCredentialForDomain(domain, row, membership.grants)
		if err != nil {
			return deletionStored{}, err
		}
		stored, err := prepareDeletionStored(binding.state.objects, row, id, role.Revision)
		stored.principalID = credential.Principal().ID()
		stored.future = accountLifecycleFuture(store, row, id, membership)
		return stored, err
	}
	state.models[role.Model] = account
	groupModel, permissionModel := state.models[role.Catalog.Group], state.models[role.Catalog.Permission]
	groupObjects, permissionObjects := orm.NewManager[G](groups), orm.NewManager[P](permissions)
	var emptyGroup G
	if _, err := groupObjects.ApplyValues(emptyGroup, nil); err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	var emptyPermission P
	if _, err := permissionObjects.ApplyValues(emptyPermission, nil); err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	groupKey, _ := primaryField(groupModel.metadata)
	permissionKey, _ := primaryField(permissionModel.metadata)
	permissionGroup, err := catalogInverse(group, role.Catalog.GroupPermissions, permission, orm.NewAutoField[G](groupKey))
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	inspectPermission := func(row P) (deletionStored, error) {
		_, id, _, err := inspectCatalogRow(permissionObjects, permissionModel.metadata, permissionKey.Name, role.Catalog.PermissionName, role.Catalog.PermissionCode, role.Catalog.PermissionRevision, row, true)
		if err != nil {
			return deletionStored{}, err
		}
		return prepareDeletionStored(permissionObjects, row, id, role.Catalog.PermissionRevision)
	}
	permissionModel.load = func(ctx context.Context, scope db.Session, _ auth.CredentialDomain, id int64) (deletionStored, error) {
		row, err := permissionObjects.Using(scope).Filter(orm.NewAutoField[P](permissionKey).Exact(id)).Get(ctx)
		if err != nil {
			return deletionStored{}, err
		}
		if _, err := prepareDeletionStored(permissionObjects, row, id, role.Catalog.PermissionRevision); err != nil {
			return deletionStored{}, err
		}
		stored, err := inspectPermission(row)
		stored.future = permissionLifecycleFuture(permissionObjects, permissionModel.metadata, row, id, role)
		return stored, err
	}
	groupModel.load = func(ctx context.Context, scope db.Session, _ auth.CredentialDomain, id int64) (deletionStored, error) {
		row, err := groupObjects.Using(scope).Filter(orm.NewAutoField[G](groupKey).Exact(id)).Get(ctx)
		if err != nil {
			return deletionStored{}, err
		}
		if _, _, _, err := inspectCatalogRow(groupObjects, groupModel.metadata, groupKey.Name, role.Catalog.GroupName, "", role.Catalog.GroupRevision, row, true); err != nil {
			return deletionStored{}, err
		}
		key := orm.NewAutoField[P](permissionKey)
		bounded, err := permissionObjects.Using(scope).Filter(permissionGroup.Exact(id)).Distinct().OrderBy(key.Asc()).Limit(auth.MaximumPermissions + 1)
		if err != nil {
			return deletionStored{}, err
		}
		rows, err := bounded.All(ctx)
		if err != nil || len(rows) > auth.MaximumPermissions {
			return deletionStored{}, managementError(CodePersistence, err)
		}
		var previous int64
		for index, grant := range rows {
			if _, err := inspectPermission(grant); err != nil {
				return deletionStored{}, err
			}
			value, present, err := permissionObjects.PrimaryKey(grant)
			id, integer := value.Integer()
			if err != nil || !present || !integer || index > 0 && id <= previous {
				return deletionStored{}, managementError(CodePersistence, err)
			}
			previous = id
		}
		stored, err := prepareDeletionStored(groupObjects, row, id, role.Catalog.GroupRevision)
		keys := make([]int64, len(rows))
		for index, grant := range rows {
			value, _, _ := permissionObjects.PrimaryKey(grant)
			keys[index], _ = value.Integer()
		}
		stored.future = groupLifecycleFuture(groupObjects, permissionObjects, groupModel.metadata, permissionModel.metadata, permissionGroup, row, id, keys, role)
		return stored, err
	}
	state.models[role.Catalog.Group], state.models[role.Catalog.Permission] = groupModel, permissionModel
	return state, nil
}

func prepareDeletionStored[M any](objects orm.Manager[M], row M, id int64, revisionName string) (deletionStored, error) {
	key, present, err := objects.PrimaryKey(row)
	actual, integer := key.Integer()
	if err != nil || !present || !integer || actual != id {
		return deletionStored{}, managementError(CodePersistence, err)
	}
	values, err := objects.ModelValues(row)
	if err != nil {
		return deletionStored{}, err
	}
	revision, valid := values[revisionName].Integer()
	if !valid || revision <= 0 {
		return deletionStored{}, managementError(CodePersistence, nil)
	}
	return deletionStored{revision: revision, advance: func(ctx context.Context, scope db.Session) error {
		return advanceCatalogRevision(ctx, scope, objects, row, revisionName)
	}}, nil
}

func compareDeletionKeys(a, b deletionKey) int {
	if order := cmp.Compare(a.model.AppLabel, b.model.AppLabel); order != 0 {
		return order
	}
	if order := cmp.Compare(a.model.ModelName, b.model.ModelName); order != 0 {
		return order
	}
	return cmp.Compare(a.id, b.id)
}
