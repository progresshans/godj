package model

import (
	"context"
	"errors"
	"slices"
	"unicode/utf8"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
)

// The normal credential path reads only bounded group keys and effective
// grants. Management additionally loads direct keys in the same native scope.
// No public inverse accessor, unbounded join materialization or cache is used.
type accountMemberships struct {
	groups, direct []int64
	grants         []auth.Permission
}
type accountRelationReader struct {
	permissions                   func(context.Context, db.Queryer, int64) ([]auth.Permission, error)
	memberships                   func(context.Context, db.Queryer, int64) (accountMemberships, error)
	proposed                      func(context.Context, db.Queryer, []int64, []int64) ([]auth.Permission, error)
	checkGroups, checkPermissions func(context.Context, db.Queryer, []int64) error
}

var errAccountGrantLimit = errors.New("account effective permission limit exceeded")

func bindAccountRelations[M, G, P any](binding Binding[M], groupDescriptor orm.RelationObjectDescriptor[G], permissionDescriptor orm.RelationObjectDescriptor[P]) (*accountRelationReader, error) {
	if !zeroModelDescriptor(groupDescriptor) || !zeroModelDescriptor(permissionDescriptor) {
		return nil, invalidCredentialStore()
	}
	project, role := binding.state.project, binding.state.role
	group, err := orm.BindModel(project, role.Catalog.Group, groupDescriptor)
	if err != nil {
		return nil, err
	}
	permission, err := orm.BindModel(project, role.Catalog.Permission, permissionDescriptor)
	if err != nil {
		return nil, err
	}
	accountMetadata, _ := project.Model(role.Model)
	accountKey, valid := primaryField(accountMetadata)
	if !valid {
		return nil, invalidCredentialStore()
	}
	memberKey := orm.NewAutoField[M](accountKey)
	direct, err := orm.BindInverseManyToManyQuery(binding.state.model, role.Permissions, permission)
	if err != nil {
		return nil, err
	}
	directMember, err := direct.Integer(memberKey)
	if err != nil {
		return nil, err
	}
	members, err := orm.BindInverseManyToManyQuery(binding.state.model, role.Groups, group)
	if err != nil {
		return nil, err
	}
	groupMember, err := members.Integer(memberKey)
	if err != nil {
		return nil, err
	}
	groupGrants, err := orm.BindInverseManyToManyQuery(group, role.Catalog.GroupPermissions, permission)
	if err != nil {
		return nil, err
	}
	viaGroup, err := orm.ChainRelations(groupGrants, members).Integer(memberKey)
	if err != nil {
		return nil, err
	}
	groupMetadata, _ := project.Model(role.Catalog.Group)
	permissionMetadata, _ := project.Model(role.Catalog.Permission)
	groupKey, ok := primaryField(groupMetadata)
	if !ok {
		return nil, invalidCredentialStore()
	}
	groupRevision, ok := namedField(groupMetadata, role.Catalog.GroupRevision)
	if !ok {
		return nil, invalidCredentialStore()
	}
	code, ok := namedField(permissionMetadata, role.Catalog.PermissionCode)
	if !ok {
		return nil, invalidCredentialStore()
	}
	revision, ok := namedField(permissionMetadata, role.Catalog.PermissionRevision)
	if !ok {
		return nil, invalidCredentialStore()
	}
	permissionKey, ok := primaryField(permissionMetadata)
	if !ok {
		return nil, invalidCredentialStore()
	}
	permissionKeyField := orm.NewAutoField[P](permissionKey)
	permissionGroup, err := groupGrants.Integer(orm.NewAutoField[G](groupKey))
	if err != nil {
		return nil, err
	}
	groupObjects, permissionObjects := orm.NewManager[G](groupDescriptor), orm.NewManager[P](permissionDescriptor)
	groupKeyField, groupRevisionField := orm.NewAutoField[G](groupKey), orm.NewIntegerField[G](groupRevision)
	codeField, revisionField := orm.NewStringField[P](code), orm.NewIntegerField[P](revision)

	type grant struct {
		code     string
		revision int64
	}
	projection := orm.Project2(codeField, revisionField, func(code string, revision int64) grant { return grant{code, revision} })
	type directGrant struct {
		key      int64
		code     string
		revision int64
	}
	directProjection := orm.Project3(permissionKeyField, codeField, revisionField, func(key int64, code string, revision int64) directGrant { return directGrant{key, code, revision} })
	validGrant := func(value string, revision int64) bool {
		canonical, err := auth.NewPermission(value)
		return err == nil && string(canonical) == value && revision > 0 && utf8.ValidString(value) && (code.MaxLength <= 0 || utf8.RuneCountInString(value) <= code.MaxLength)
	}
	readGroups := func(ctx context.Context, source orm.QuerySet[G]) ([]int64, error) {
		return catalogMembershipKeys(ctx, source, groupKeyField, groupRevisionField, MaximumAccountGroups)
	}
	readDirect := func(ctx context.Context, source orm.QuerySet[P]) ([]int64, error) {
		selected, err := orm.SelectQuery(source.OrderBy(permissionKeyField.Asc()).Distinct(), directProjection)
		if err != nil {
			return nil, err
		}
		selected, err = selected.Limit(auth.MaximumPermissions + 1)
		if err != nil {
			return nil, err
		}
		rows, err := selected.All(ctx)
		if err != nil {
			return nil, err
		}
		if len(rows) > auth.MaximumPermissions {
			return nil, invalidAccountCredential()
		}
		keys := make([]int64, len(rows))
		for i, row := range rows {
			if !validGrant(row.code, row.revision) || i > 0 && rows[i-1].key >= row.key {
				return nil, invalidAccountCredential()
			}
			keys[i] = row.key
		}
		return keys, nil
	}
	readGrants := func(ctx context.Context, source orm.QuerySet[P]) ([]auth.Permission, error) {
		selected, err := orm.SelectQuery(source.OrderBy(codeField.Asc()).Distinct(), projection)
		if err != nil {
			return nil, err
		}
		selected, err = selected.Limit(auth.MaximumPermissions + 1)
		if err != nil {
			return nil, err
		}
		rows, err := selected.All(ctx)
		if err != nil {
			return nil, err
		}
		if len(rows) > auth.MaximumPermissions {
			return nil, errAccountGrantLimit
		}
		result := make([]auth.Permission, len(rows))
		for i, row := range rows {
			if !validGrant(row.code, row.revision) || i > 0 && rows[i-1].code >= row.code {
				return nil, invalidAccountCredential()
			}
			result[i] = auth.Permission(row.code)
		}
		return result, nil
	}
	currentGrants := func(ctx context.Context, scope db.Queryer, key int64) ([]auth.Permission, error) {
		grants, err := readGrants(ctx, permissionObjects.Using(scope).Filter(orm.Or(directMember.Exact(key), viaGroup.Exact(key))))
		if errors.Is(err, errAccountGrantLimit) {
			return nil, invalidAccountCredential()
		}
		return grants, err
	}
	reader := &accountRelationReader{}
	reader.permissions = func(ctx context.Context, scope db.Queryer, key int64) ([]auth.Permission, error) {
		if _, err := readGroups(ctx, groupObjects.Using(scope).Filter(groupMember.Exact(key))); err != nil {
			return nil, err
		}
		return currentGrants(ctx, scope, key)
	}
	reader.memberships = func(ctx context.Context, scope db.Queryer, key int64) (accountMemberships, error) {
		groups, err := readGroups(ctx, groupObjects.Using(scope).Filter(groupMember.Exact(key)))
		if err != nil {
			return accountMemberships{}, err
		}
		direct, err := readDirect(ctx, permissionObjects.Using(scope).Filter(directMember.Exact(key)))
		if err != nil {
			return accountMemberships{}, err
		}
		grants, err := currentGrants(ctx, scope, key)
		if err != nil {
			return accountMemberships{}, err
		}
		return accountMemberships{groups, direct, grants}, nil
	}
	reader.checkGroups = func(ctx context.Context, scope db.Queryer, groups []int64) error {
		actualGroups, err := readGroups(ctx, groupObjects.Using(scope).Filter(groupKeyField.In(groups...)))
		if err != nil {
			return err
		}
		if !slices.Equal(groups, actualGroups) {
			return profileInputError(role.Groups, "invalid_choice")
		}
		return nil
	}
	reader.checkPermissions = func(ctx context.Context, scope db.Queryer, direct []int64) error {
		actualDirect, err := readDirect(ctx, permissionObjects.Using(scope).Filter(permissionKeyField.In(direct...)))
		if err != nil {
			return err
		}
		if !slices.Equal(direct, actualDirect) {
			return profileInputError(role.Permissions, "invalid_choice")
		}
		return nil
	}
	reader.proposed = func(ctx context.Context, scope db.Queryer, groups, direct []int64) ([]auth.Permission, error) {
		if err := reader.checkGroups(ctx, scope, groups); err != nil {
			return nil, err
		}
		if err := reader.checkPermissions(ctx, scope, direct); err != nil {
			return nil, err
		}
		return readGrants(ctx, permissionObjects.Using(scope).Filter(orm.Or(permissionKeyField.In(direct...), permissionGroup.In(groups...))))
	}
	return reader, nil
}
