package model

import (
	"context"
	"crypto/sha256"
	"fmt"
	"slices"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// TransitionCatalogModelConfig describes the target's typed field intent.
// Permission authorizes Add for a new model, or Change for a shared Group whose
// new permission relation is populated. Keys, revision, permission codes and
// grant selections belong to the transition. Shared rows accept no field input.
type TransitionCatalogModelConfig[M any] struct {
	Fields                []string
	Relations             []TransitionCatalogRelation[M]
	Permission            auth.Permission
	AdditionalPermissions []auth.Permission
	Validate              CatalogValidator[M]
}

type TransitionCatalogTargetConfig[G, P any] struct {
	Group      TransitionCatalogModelConfig[G]
	Permission TransitionCatalogModelConfig[P]
}

// CatalogRowCreation pairs a reviewed source with cleaned typed target values.
// Its target key is allocated by the native INSERT, never selected in advance.
// References selects reviewed account/catalog correspondences for FK/OneToOne
// fields omitted from Input. Creation copies the maps; Prepare verifies them.
// Account targets must already exist under their explicit transition mapping.
type CatalogRowCreation[M any] struct {
	Source     SourceAccount
	Input      orm.ModelInput[M]
	References map[string]TransitionTargetReference
}

func (CatalogRowCreation[M]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.CatalogRowCreation{redacted}"))
}

type TransitionCatalogTarget[G, P any] struct {
	state *transitionCatalogTarget[G, P]
}

func (TransitionCatalogTarget[G, P]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.TransitionCatalogTarget{redacted}"))
}

type transitionCatalogTarget[G, P any] struct {
	project           orm.ProjectBinding
	source            string
	oldRole, nextRole ir.AccountRole
	oldHash, nextHash [sha256.Size]byte
	groups            transitionCatalogModel[G]
	permissions       transitionCatalogModel[P]
	required          []auth.Permission
	grants            func(G, []int64) (orm.CollectionChange, error)
	accounts          func(context.Context, db.Queryer, int64, []int64, []int64) ([]orm.CollectionChange, error)
}

type transitionCatalogModel[M any] struct {
	objects              orm.Manager[M]
	metadata             ir.Model
	role                 ir.AccountRole
	relations            []ProfileRelation[M]
	identity             ir.ModelIdentity
	key                  orm.AutoField[M]
	name, revision, code string
	created              bool
	allowed              map[string]bool
	auditFields          []string
	required             []auth.Permission
	validate             CatalogValidator[M]
	incoming             []query.Plan
}

// NewTransitionCatalogTarget binds both roles and all three typed collection
// capabilities to the complete host source. A changed catalog model must be
// empty at preparation; a shared model retains its row identity. No I/O occurs.
func NewTransitionCatalogTarget[S, T, G, P, GP, TG, TP any](source Binding[S], target Binding[T], group orm.RelationObjectDescriptor[G], permission orm.RelationObjectDescriptor[P], grants orm.ManyToMany[G, P, GP], accountGroups orm.ManyToMany[T, G, TG], accountPermissions orm.ManyToMany[T, P, TP], config TransitionCatalogTargetConfig[G, P]) (TransitionCatalogTarget[G, P], error) {
	var zero TransitionCatalogTarget[G, P]
	if source.state == nil || target.state == nil || !zeroModelDescriptor(group) || !zeroModelDescriptor(permission) {
		return zero, managementError(CodeInvalidConfig, nil)
	}
	left, right := source.state.role, target.state.role
	project := target.state.project
	sourceHash, err := source.state.project.SourceSHA256()
	if err != nil {
		return zero, managementError(CodeInvalidConfig, err)
	}
	targetHash, err := project.SourceSHA256()
	if err != nil || sourceHash != targetHash || left.Model == right.Model || left.Catalog == right.Catalog {
		return zero, managementError(CodeInvalidConfig, err)
	}
	for _, catalog := range []ir.AccountCatalog{left.Catalog, right.Catalog} {
		if left.Model == catalog.Group || left.Model == catalog.Permission || right.Model == catalog.Group || right.Model == catalog.Permission {
			return zero, managementError(CodeInvalidConfig, nil)
		}
	}
	if left.Catalog.Group == right.Catalog.Permission || left.Catalog.Permission == right.Catalog.Group ||
		left.Catalog.Group == right.Catalog.Group && left.Catalog.Permission == right.Catalog.Permission {
		return zero, managementError(CodeInvalidConfig, nil)
	}
	if _, err := orm.BindModel(project, right.Catalog.Group, group); err != nil {
		return zero, managementError(CodeInvalidConfig, err)
	}
	if _, err := orm.BindModel(project, right.Catalog.Permission, permission); err != nil {
		return zero, managementError(CodeInvalidConfig, err)
	}
	for _, err := range []error{
		grants.ValidateSource(project, right.Catalog.Group, right.Catalog.GroupPermissions),
		accountGroups.ValidateSource(project, right.Model, right.Groups),
		accountPermissions.ValidateSource(project, right.Model, right.Permissions),
		validateCatalogCollectionStorage(project, right),
		validateAccountCollectionStorage(project, right, []string{right.Groups, right.Permissions}),
	} {
		if err != nil {
			return zero, managementError(CodeInvalidConfig, err)
		}
	}
	// The old selected identities are lifecycle owners too. A new role must
	// not disguise an INSERT into one of them as an ordinary through row.
	for _, relation := range project.ManyToManyRelations() {
		selected := relation.Source == right.Model && (relation.Field == right.Groups || relation.Field == right.Permissions) ||
			relation.Source == right.Catalog.Group && relation.Field == right.Catalog.GroupPermissions
		if selected {
			if err := validateLifecycleThroughStorage(left, relation.Through.Model); err != nil {
				return zero, err
			}
		}
	}
	groups, err := newTransitionCatalogModel(project, right, target.state.roleFingerprint, orm.NewManager[G](group), right.Catalog.Group, right.Catalog.GroupName, "", right.Catalog.GroupRevision, right.Catalog.GroupPermissions, left.Catalog.Group != right.Catalog.Group, config.Group)
	if err != nil {
		return zero, err
	}
	permissions, err := newTransitionCatalogModel(project, right, target.state.roleFingerprint, orm.NewManager[P](permission), right.Catalog.Permission, right.Catalog.PermissionName, right.Catalog.PermissionCode, right.Catalog.PermissionRevision, "", left.Catalog.Permission != right.Catalog.Permission, config.Permission)
	if err != nil {
		return zero, err
	}
	required := slices.Clone(groups.required)
	for _, permission := range permissions.required {
		if !slices.Contains(required, permission) {
			required = append(required, permission)
		}
	}
	if _, err := auth.RequiredPermissions(required[0], required[1:]...); err != nil {
		return zero, managementError(CodeInvalidConfig, err)
	}
	accountKey, err := target.state.objects.PrimaryKeyField()
	if err != nil {
		return zero, managementError(CodeInvalidConfig, err)
	}
	key := orm.NewAutoField[T](accountKey)
	return TransitionCatalogTarget[G, P]{&transitionCatalogTarget[G, P]{
		project: project, source: sourceHash, oldRole: left, nextRole: right, oldHash: source.state.roleFingerprint, nextHash: target.state.roleFingerprint,
		groups: groups, permissions: permissions, required: required,
		grants: func(row G, keys []int64) (orm.CollectionChange, error) { return grants.SetKeysChange(row, keys) },
		accounts: func(ctx context.Context, scope db.Queryer, id int64, groups, permissions []int64) ([]orm.CollectionChange, error) {
			row, err := target.state.objects.Using(scope).Filter(key.Exact(id)).Get(ctx)
			if err != nil {
				return nil, err
			}
			groupChange, err := accountGroups.SetKeysChange(row, groups)
			if err != nil {
				return nil, err
			}
			permissionChange, err := accountPermissions.SetKeysChange(row, permissions)
			if err != nil {
				return nil, err
			}
			return []orm.CollectionChange{groupChange, permissionChange}, nil
		},
	}}, nil
}

func newTransitionCatalogModel[M any](project orm.ProjectBinding, role ir.AccountRole, roleHash [32]byte, objects orm.Manager[M], identity ir.ModelIdentity, name, code, revision, collection string, created bool, config TransitionCatalogModelConfig[M]) (transitionCatalogModel[M], error) {
	var zero transitionCatalogModel[M]
	metadata, err := objects.Metadata()
	if err != nil {
		return zero, managementError(CodeInvalidConfig, err)
	}
	key, valid := primaryField(metadata)
	if !valid || len(config.Fields) >= admin.MaximumChangedFields || !created && len(config.Fields) != 0 {
		return zero, managementError(CodeInvalidConfig, nil)
	}
	result := transitionCatalogModel[M]{objects: objects, metadata: metadata, role: role, identity: identity, key: orm.NewAutoField[M](key), name: name, revision: revision, code: code,
		created: created, allowed: make(map[string]bool), validate: config.Validate}
	if created || collection != "" {
		result.required, err = auth.RequiredPermissions(config.Permission, config.AdditionalPermissions...)
		if err != nil {
			return zero, managementError(CodeInvalidConfig, err)
		}
	} else if config.Permission != "" || len(config.AdditionalPermissions) != 0 || config.Validate != nil {
		return zero, managementError(CodeInvalidConfig, nil)
	}
	for _, selected := range config.Fields {
		field, found := namedField(metadata, selected)
		if !found || result.allowed[selected] || selected == revision || selected == code || field.PrimaryKey || field.NonEditable || field.Kind.IsFile() {
			return zero, managementError(CodeInvalidConfig, nil)
		}
		result.allowed[selected] = true
	}
	result.relations, err = prepareTransitionCatalogRelations(project, roleHash, identity, metadata, config.Fields, config.Relations)
	if err != nil {
		return zero, err
	}
	if created && name != code && !result.allowed[name] {
		return zero, managementError(CodeInvalidConfig, nil)
	}
	if created {
		result.incoming, err = transitionIncomingPlans(project, identity)
		if err != nil {
			return zero, err
		}
		initial, err := orm.InitialModelValues(metadata)
		if err != nil {
			return zero, managementError(CodeInvalidConfig, err)
		}
		for _, field := range metadata.Fields {
			if field.PrimaryKey || field.Name == revision || field.Name == code || result.allowed[field.Name] {
				continue
			}
			value := initial[field.Name]
			text, textual := value.String()
			if value.IsNull() && !field.Nullable || textual && text == "" && !field.Blank && field.Default == nil || field.Relation != nil && !value.IsNull() {
				return zero, managementError(CodeInvalidConfig, nil)
			}
		}
		for _, field := range metadata.ManyToMany {
			if field.Name != collection && !field.Blank {
				return zero, managementError(CodeInvalidConfig, nil)
			}
		}
	}
	for _, field := range metadata.Fields {
		if result.allowed[field.Name] || created && field.Name == code {
			result.auditFields = append(result.auditFields, field.Name)
		}
	}
	if collection != "" {
		result.auditFields = append(result.auditFields, collection)
	}
	if len(result.required) != 0 {
		action := admin.ActionChange
		if created {
			action = admin.ActionAdd
		}
		if _, err := admin.PrepareEventTemplate("catalog-transition", identity.AppLabel+"."+identity.ModelName, action, result.auditFields, ""); err != nil {
			return zero, managementError(CodeInvalidConfig, err)
		}
	}
	return result, nil
}
