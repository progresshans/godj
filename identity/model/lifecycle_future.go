package model

import (
	"context"
	"errors"
	"math"
	"slices"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type lifecycleVerifier func(context.Context, db.Session) error
type lifecycleFuture func(context.Context, db.Session, auth.CredentialDomain, *lifecycleProjection, *deletionTouch, map[string]query.Value) (lifecycleVerifier, error)

func lifecycleRequiredPermissions(primary auth.Permission, additional []auth.Permission, required bool) ([]auth.Permission, error) {
	if primary != "" || required {
		return auth.RequiredPermissions(primary, additional...)
	}
	// Optional effect policies can omit an operation, but malformed additional
	// grants are still configuration errors rather than silently ignored input.
	if len(additional) != 0 {
		if _, err := auth.RequiredPermissions(additional[0], additional[1:]...); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

func (touch *deletionTouch) changed() bool {
	return touch.created || touch.deleted || len(touch.nulls)+len(touch.collections)+len(touch.fields) != 0
}

func (touch *deletionTouch) changedFields() []string {
	result := append(slices.Clone(touch.fields), touch.nulls...)
	result = append(result, touch.collections...)
	slices.Sort(result)
	return slices.Compact(result)
}

// Prepare a detached final typed row and a private readback check. Internal
// readback verifies the authorized transaction, without requiring a new public
// view grant after self-deactivation, permission loss or actor deletion.
func prepareLifecycleFuture[M any](objects orm.Manager[M], row M, id int64, revision string, touch *deletionTouch, root map[string]query.Value, check func(M) (lifecycleVerifier, error)) (lifecycleVerifier, error) {
	metadata, err := objects.Metadata()
	if err != nil {
		return nil, err
	}
	key, found := primaryField(metadata)
	if !found {
		return nil, managementError(CodePersistence, nil)
	}
	queryKey := orm.NewAutoField[M](key)
	if touch.deleted {
		return func(ctx context.Context, scope db.Session) error {
			count, err := objects.Using(scope).Filter(queryKey.Exact(id)).Count(ctx)
			if err != nil || count != 0 {
				return managementError(CodePersistence, err)
			}
			return nil
		}, nil
	}
	values, err := objects.ModelValues(row)
	if err != nil {
		return nil, err
	}
	updates := make(map[string]query.Value, len(root)+len(touch.nulls)+1)
	for name, value := range root {
		if name == key.Name {
			if !value.Equal(values[name]) {
				return nil, managementError(CodePersistence, nil)
			}
			continue
		}
		updates[name] = value
	}
	for _, name := range touch.nulls {
		updates[name] = query.Null()
	}
	actual, valid := values[revision].Integer()
	if !valid || actual <= 0 || touch.changed() && actual == math.MaxInt64 {
		return nil, managementError(CodePersistence, nil)
	}
	if touch.changed() && !touch.created {
		actual++
	}
	updates[revision] = query.Integer(actual)
	prepared, err := objects.PrepareValues(&row, updates)
	if err != nil {
		return nil, err
	}
	expected, err := prepared.Model()
	if err != nil {
		return nil, err
	}
	verifyMore, err := check(expected)
	if err != nil {
		return nil, err
	}
	want, err := objects.ModelValues(expected)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, scope db.Session) error {
		actual, err := objects.Using(scope).Filter(queryKey.Exact(id)).Get(ctx)
		if err != nil {
			return managementError(CodePersistence, err)
		}
		values, err := objects.ModelValues(actual)
		if err != nil || len(values) != len(want) {
			return managementError(CodePersistence, err)
		}
		for field, expected := range want {
			if !expected.Equal(values[field]) {
				return managementError(CodePersistence, nil)
			}
		}
		if verifyMore != nil {
			return verifyMore(ctx, scope)
		}
		return nil
	}, nil
}

func projectedAccountGrants(ctx context.Context, scope db.Queryer, reader *accountRelationReader, role ir.AccountRole, projection *lifecycleProjection, groups, direct []int64) ([]auth.Permission, error) {
	var unchanged []int64
	combined := slices.Clone(direct)
	for _, id := range groups {
		if projection.deleted(role.Catalog.Group, id) {
			return nil, profileInputError(role.Groups, "invalid_choice")
		}
		set, changed := projection.sets[lifecycleMembershipKey{deletionKey{role.Catalog.Group, id}, role.Catalog.GroupPermissions}]
		if changed {
			combined = append(combined, set.after...)
		} else {
			unchanged = append(unchanged, id)
		}
	}
	slices.Sort(combined)
	combined = slices.Compact(combined)
	if len(combined) > auth.MaximumPermissions {
		return nil, errAccountGrantLimit
	}
	for _, id := range combined {
		if projection.deleted(role.Catalog.Permission, id) {
			return nil, profileInputError(role.Permissions, "invalid_choice")
		}
	}
	return reader.proposed(ctx, scope, unchanged, combined)
}

func accountLifecycleFuture[M any](store *CredentialStore[M], row M, id int64, current accountMemberships) lifecycleFuture {
	binding, role := store.state.binding, store.state.binding.state.role
	return func(ctx context.Context, scope db.Session, domain auth.CredentialDomain, projection *lifecycleProjection, touch *deletionTouch, root map[string]query.Value) (lifecycleVerifier, error) {
		return prepareLifecycleFuture(binding.state.objects, row, id, role.Revision, touch, root, func(candidate M) (lifecycleVerifier, error) {
			groups := projection.keys(role.Model, id, role.Groups, current.groups)
			direct := projection.keys(role.Model, id, role.Permissions, current.direct)
			grants, err := projectedAccountGrants(ctx, scope, store.state.relations, role, projection, groups, direct)
			if errors.Is(err, errAccountGrantLimit) {
				return nil, profileInputError(role.Permissions, "max_items")
			}
			if err != nil {
				return nil, err
			}
			if _, err := binding.prepareCredentialForDomain(domain, candidate, grants); err != nil {
				return nil, err
			}
			return func(ctx context.Context, scope db.Session) error {
				actual, err := store.state.relations.memberships(ctx, scope, id)
				if err != nil || !slices.Equal(actual.groups, groups) || !slices.Equal(actual.direct, direct) || !slices.Equal(actual.grants, grants) {
					return managementError(CodePersistence, err)
				}
				return nil
			}, nil
		})
	}
}

func permissionLifecycleFuture[P any](objects orm.Manager[P], metadata ir.Model, row P, id int64, role ir.AccountRole) lifecycleFuture {
	key, _ := primaryField(metadata)
	return func(_ context.Context, _ db.Session, _ auth.CredentialDomain, _ *lifecycleProjection, touch *deletionTouch, root map[string]query.Value) (lifecycleVerifier, error) {
		return prepareLifecycleFuture(objects, row, id, role.Catalog.PermissionRevision, touch, root, func(candidate P) (lifecycleVerifier, error) {
			_, _, _, err := inspectCatalogRow(objects, metadata, key.Name, role.Catalog.PermissionName, role.Catalog.PermissionCode, role.Catalog.PermissionRevision, candidate, true)
			return nil, err
		})
	}
}

func groupLifecycleFuture[G, P any](groups orm.Manager[G], permissions orm.Manager[P], metadata, permissionMetadata ir.Model, permissionGroup orm.RelatedIntegerField[P], row G, id int64, current []int64, role ir.AccountRole) lifecycleFuture {
	key, _ := primaryField(metadata)
	permissionKey, _ := primaryField(permissionMetadata)
	readKeys := func(ctx context.Context, source orm.QuerySet[P]) ([]int64, error) {
		key := orm.NewAutoField[P](permissionKey)
		bounded, err := source.Distinct().OrderBy(key.Asc()).Limit(auth.MaximumPermissions + 1)
		if err != nil {
			return nil, err
		}
		rows, err := bounded.All(ctx)
		if err != nil || len(rows) > auth.MaximumPermissions {
			return nil, managementError(CodePersistence, err)
		}
		keys := make([]int64, len(rows))
		for index, row := range rows {
			_, keys[index], _, err = inspectCatalogRow(permissions, permissionMetadata, permissionKey.Name, role.Catalog.PermissionName, role.Catalog.PermissionCode, role.Catalog.PermissionRevision, row, true)
			if err != nil || index > 0 && keys[index-1] >= keys[index] {
				return nil, managementError(CodePersistence, err)
			}
		}
		return keys, nil
	}
	return func(ctx context.Context, scope db.Session, _ auth.CredentialDomain, projection *lifecycleProjection, touch *deletionTouch, root map[string]query.Value) (lifecycleVerifier, error) {
		return prepareLifecycleFuture(groups, row, id, role.Catalog.GroupRevision, touch, root, func(candidate G) (lifecycleVerifier, error) {
			if _, _, _, err := inspectCatalogRow(groups, metadata, key.Name, role.Catalog.GroupName, "", role.Catalog.GroupRevision, candidate, true); err != nil {
				return nil, err
			}
			keys := projection.keys(role.Catalog.Group, id, role.Catalog.GroupPermissions, current)
			for _, key := range keys {
				if projection.deleted(role.Catalog.Permission, key) {
					return nil, profileInputError(role.Catalog.GroupPermissions, "invalid_choice")
				}
			}
			actual, err := readKeys(ctx, permissions.Using(scope).Filter(orm.NewAutoField[P](permissionKey).In(keys...)))
			if err != nil || !slices.Equal(actual, keys) {
				return nil, managementError(CodePersistence, err)
			}
			return func(ctx context.Context, scope db.Session) error {
				actual, err := readKeys(ctx, permissions.Using(scope).Filter(permissionGroup.Exact(id)))
				if err != nil || !slices.Equal(actual, keys) {
					return managementError(CodePersistence, err)
				}
				return nil
			}, nil
		})
	}
}
