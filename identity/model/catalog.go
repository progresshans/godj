package model

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/crud"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

const MaximumCatalogPageSize = 100
const catalogOwnerBatchSize = 256

// CatalogPermissions declares independent operation grants. Reads accept view
// OR change, then require every Additional grant. A failed authority lookup or
// overlay is never treated as a denial that permits the alternative.
type CatalogPermissions struct {
	View, Add, Change, Delete auth.Permission
	Additional                []auth.Permission
}

// CatalogValidator receives detached current/candidate values after current
// authorization. Current is nil during creation. The borrowed reader may be
// used for current choices but must not be retained, mutated or re-entered.
// Validators and clocks must be safe for concurrent service calls.
type CatalogValidator[M any] func(context.Context, db.Queryer, *M, M) (validation.Errors, error)

// CatalogModelConfig is an explicit transport-independent exposure policy.
// Core name/code fields must be writable; revision/keys are always server
// owned. Additional scalar fields retain their actual generated model types.
// GroupPermissions is the only supported writable/readable M2M selection;
// arbitrary relations and file lifetimes need their own management owners.
type CatalogModelConfig[M any] struct {
	ReadFields, WriteFields []string
	Permissions             CatalogPermissions
	Validate                CatalogValidator[M]
}

type CatalogConfig[G, P any] struct {
	Group      CatalogModelConfig[G]
	Permission CatalogModelConfig[P]
	// AccountPermissions applies when the host deletion graph directly
	// deletes or nulls selected account rows. Implicit membership revisions
	// remain consequences of the authorized catalog deletion.
	AccountPermissions DeletionPermissions
	Now                func() time.Time
}

// Catalog composes the selected account's current authority with its actual
// Group/Permission models. Construction verifies every capability against the
// complete host source. It owns no account copies, SQL adapters or global cache.
type Catalog[G, P any] struct{ state *catalogState[G, P] }
type catalogState[G, P any] struct {
	groups      CatalogResource[G]
	permissions CatalogResource[P]
}

func (Catalog[G, P]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.Catalog{redacted}"))
}

func (catalog *Catalog[G, P]) Groups() CatalogResource[G] {
	if catalog == nil || catalog.state == nil {
		return CatalogResource[G]{}
	}
	return catalog.state.groups
}
func (catalog *Catalog[G, P]) Permissions() CatalogResource[P] {
	if catalog == nil || catalog.state == nil {
		return CatalogResource[P]{}
	}
	return catalog.state.permissions
}

// CatalogResource is an immutable typed management capability supplied only by
// a Catalog. It rechecks current account/domain authority for every operation.
type CatalogResource[M any] struct{ state *catalogModelState[M] }
type catalogModelState[M any] struct {
	shared                                    *catalogScope
	resource                                  crud.Resource[M]
	model                                     ir.Model
	identity                                  ir.ModelIdentity
	key                                       orm.AutoField[M]
	keyName, name, code, revision, collection string
	readFields, listFields, writeFields       []string
	readCollection                            bool
	allowed                                   map[string]bool
	permissions                               catalogPermissions
	validate                                  CatalogValidator[M]
	readKeys                                  func(context.Context, db.Queryer, int64) ([]int64, error)
	checkKeys                                 func(context.Context, db.Queryer, []int64) error
	checkMembers                              func(context.Context, db.Session, auth.CredentialDomain, int64, []int64) error
	saveKeys                                  func(context.Context, db.Session, M, []int64) error
	delete                                    func(context.Context, db.Session, auth.Principal, M) (int64, int, error)
}
type catalogScope struct {
	reader    db.SnapshotReader
	writer    db.CoordinatedRelationAtomic
	authorize func(context.Context, db.Queryer, auth.Principal, []auth.Permission) (auth.Principal, bool, error)
	now       func() time.Time
}
type catalogPermissions struct{ view, add, change, delete []auth.Permission }

func (CatalogResource[M]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.CatalogResource{redacted}"))
}
func (resource CatalogResource[M]) ReadFields() []string {
	if resource.state == nil {
		return nil
	}
	return slices.Clone(resource.state.readFields)
}

// ListFields excludes collections: a page does not run per-row grant queries.
func (resource CatalogResource[M]) ListFields() []string {
	if resource.state == nil {
		return nil
	}
	return slices.Clone(resource.state.listFields)
}
func (resource CatalogResource[M]) WriteFields() []string {
	if resource.state == nil {
		return nil
	}
	return slices.Clone(resource.state.writeFields)
}

// Manager provides the same pure typed model source for Form/API declarations.
// It has no backend binding and grants no read or mutation authority.
func (resource CatalogResource[M]) Manager() orm.Manager[M] {
	if resource.state == nil {
		return orm.Manager[M]{}
	}
	return resource.state.resource.Manager()
}

func NewCatalog[M, G, P, L any](sessions *SessionStore[M], expected Binding[M], groups orm.RelationObjectDescriptor[G], permissions orm.RelationObjectDescriptor[P], grants orm.ManyToMany[G, P, L], deleteGroups orm.RelationDeleter[G], deletePermissions orm.RelationDeleter[P], overlay auth.Authorizer, config CatalogConfig[G, P]) (*Catalog[G, P], error) {
	if sessions == nil || sessions.state == nil {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	store := sessions.state.credentials
	if err := store.valid(); err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	if nilModelValue(overlay) || !zeroModelDescriptor(groups) || !zeroModelDescriptor(permissions) {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	writer, ok := store.state.backend.(db.CoordinatedRelationAtomic)
	if !ok || nilModelValue(writer) {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	binding, role := store.state.binding, store.state.binding.state.role
	project := binding.state.project
	group, err := orm.BindModel(project, role.Catalog.Group, groups)
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	permission, err := orm.BindModel(project, role.Catalog.Permission, permissions)
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	for _, err := range []error{
		grants.ValidateSource(project, role.Catalog.Group, role.Catalog.GroupPermissions),
		deleteGroups.ValidateSource(project, role.Catalog.Group),
		deletePermissions.ValidateSource(project, role.Catalog.Permission),
	} {
		if err != nil {
			return nil, managementError(CodeInvalidConfig, err)
		}
	}
	if err := validateCatalogCollectionTopology(project, role); err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	lifecycle, err := newDeletionLifecycle(sessions, expected, groups, permissions, overlay, AccountDeleteConfig{
		Account:    config.AccountPermissions,
		Group:      DeletionPermissions{Delete: config.Group.Permissions.Delete, Change: config.Group.Permissions.Change, Additional: config.Group.Permissions.Additional},
		Permission: DeletionPermissions{Delete: config.Permission.Permissions.Delete, Change: config.Permission.Permissions.Change, Additional: config.Permission.Permissions.Additional},
		Now:        config.Now,
	})
	if err != nil {
		return nil, err
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	shared := &catalogScope{reader: store.state.backend, writer: writer, now: now,
		authorize: func(ctx context.Context, scope db.Queryer, actor auth.Principal, required []auth.Permission) (auth.Principal, bool, error) {
			return store.AuthorizeIn(ctx, scope, actor, overlay, required[0], required[1:]...)
		},
	}
	groupState, err := newCatalogModel(shared, orm.NewManager[G](groups), role.Catalog.Group, role.Catalog.GroupName, "", role.Catalog.GroupRevision, role.Catalog.GroupPermissions, config.Group)
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	permissionState, err := newCatalogModel(shared, orm.NewManager[P](permissions), role.Catalog.Permission, role.Catalog.PermissionName, role.Catalog.PermissionCode, role.Catalog.PermissionRevision, "", config.Permission)
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	groupSource, groupPresent := project.Model(role.Catalog.Group)
	permissionSource, permissionPresent := project.Model(role.Catalog.Permission)
	if !groupPresent || !permissionPresent || !groupState.model.Equal(groupSource) || !permissionState.model.Equal(permissionSource) {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	groupState.delete = func(ctx context.Context, scope db.Session, actor auth.Principal, value G) (int64, int, error) {
		relations, ok := scope.(db.RelationSession)
		if !ok || nilModelValue(relations) {
			return 0, 0, managementError(CodeInvalidConfig, nil)
		}
		revoked := 0
		count, err := deleteGroups.DeleteWithEffectsInSession(ctx, relations, value, func(work context.Context, effects orm.RelationDeleteEffects) error {
			var err error
			revoked, err = lifecycle.apply(work, relations, actor, effects)
			return err
		})
		return count, revoked, err
	}
	permissionState.delete = func(ctx context.Context, scope db.Session, actor auth.Principal, value P) (int64, int, error) {
		relations, ok := scope.(db.RelationSession)
		if !ok || nilModelValue(relations) {
			return 0, 0, managementError(CodeInvalidConfig, nil)
		}
		revoked := 0
		count, err := deletePermissions.DeleteWithEffectsInSession(ctx, relations, value, func(work context.Context, effects orm.RelationDeleteEffects) error {
			var err error
			revoked, err = lifecycle.apply(work, relations, actor, effects)
			return err
		})
		return count, revoked, err
	}
	groupState.saveKeys = func(ctx context.Context, scope db.Session, owner G, keys []int64) error {
		collection, err := grants.InSession(scope, owner)
		if err != nil {
			return err
		}
		return collection.SetKeys(ctx, keys)
	}
	if err := bindCatalogRelations(store, group, permission, groupState, permissionState); err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	return &Catalog[G, P]{state: &catalogState[G, P]{groups: CatalogResource[G]{state: groupState}, permissions: CatalogResource[P]{state: permissionState}}}, nil
}

func newCatalogModel[M any](shared *catalogScope, objects orm.Manager[M], identity ir.ModelIdentity, name, code, revision, collection string, config CatalogModelConfig[M]) (*catalogModelState[M], error) {
	metadata, err := objects.Metadata()
	if err != nil {
		return nil, err
	}
	key, valid := primaryField(metadata)
	if !valid || len(config.ReadFields) == 0 || len(config.WriteFields) == 0 || len(config.WriteFields) > admin.MaximumChangedFields {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	state := &catalogModelState[M]{shared: shared, model: metadata, identity: identity, key: orm.NewAutoField[M](key), keyName: key.Name,
		name: name, code: code, revision: revision, collection: collection, validate: config.Validate, allowed: make(map[string]bool)}
	for _, declaration := range []struct {
		permission auth.Permission
		target     *[]auth.Permission
	}{
		{config.Permissions.View, &state.permissions.view}, {config.Permissions.Add, &state.permissions.add},
		{config.Permissions.Change, &state.permissions.change}, {config.Permissions.Delete, &state.permissions.delete},
	} {
		*declaration.target, err = auth.RequiredPermissions(declaration.permission, config.Permissions.Additional...)
		if err != nil {
			return nil, err
		}
	}
	selected := make(map[string]bool)
	for _, fieldName := range config.ReadFields {
		if selected[fieldName] {
			return nil, managementError(CodeInvalidConfig, nil)
		}
		selected[fieldName] = true
		if fieldName == collection && collection != "" {
			state.readCollection = true
			continue
		}
		field, found := namedField(metadata, fieldName)
		if !found || field.Kind.IsFile() {
			return nil, managementError(CodeInvalidConfig, nil)
		}
		state.listFields = append(state.listFields, fieldName)
	}
	state.readFields = slices.Clone(config.ReadFields)
	for _, fieldName := range config.WriteFields {
		if state.allowed[fieldName] {
			return nil, managementError(CodeInvalidConfig, nil)
		}
		if fieldName != collection || collection == "" {
			field, found := namedField(metadata, fieldName)
			if !found || fieldName == revision || field.PrimaryKey || field.NonEditable || field.Relation != nil || field.Kind.IsFile() {
				return nil, managementError(CodeInvalidConfig, nil)
			}
		}
		state.allowed[fieldName] = true
	}
	if !state.allowed[name] || code != "" && !state.allowed[code] {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	// Stable field order is the model's order, never caller map iteration.
	for _, field := range metadata.Fields {
		if state.allowed[field.Name] {
			state.writeFields = append(state.writeFields, field.Name)
		}
	}
	if state.allowed[collection] {
		state.writeFields = append(state.writeFields, collection)
	}
	if _, err := admin.PrepareEventTemplate("catalog-startup", identity.AppLabel+"."+identity.ModelName, admin.ActionChange, state.writeFields, ""); err != nil {
		return nil, err
	}
	state.resource, err = crud.New(objects, crud.Config{Fields: state.writeFields})
	if err != nil {
		return nil, err
	}
	return state, nil
}
