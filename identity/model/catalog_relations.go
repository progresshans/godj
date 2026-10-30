package model

import (
	"context"
	"math"
	"slices"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func catalogRelation[S, T any](source orm.BoundModel[S], name string, target orm.BoundModel[T], key orm.AutoField[T]) (orm.RelatedIntegerField[S], error) {
	relation, err := orm.BindQueryRelation(source, name, target)
	if err != nil {
		return orm.RelatedIntegerField[S]{}, err
	}
	return relation.Integer(key)
}
func catalogInverse[O, T any](owner orm.BoundModel[O], name string, target orm.BoundModel[T], key orm.AutoField[O]) (orm.RelatedIntegerField[T], error) {
	relation, err := orm.BindInverseManyToManyQuery(owner, name, target)
	if err != nil {
		return orm.RelatedIntegerField[T]{}, err
	}
	return relation.Integer(key)
}

func bindCatalogRelations[M, G, P any](store *CredentialStore[M], group orm.BoundModel[G], permission orm.BoundModel[P], groups *catalogModelState[G], permissions *catalogModelState[P]) error {
	role := store.state.binding.state.role
	permissionObjects := permissions.resource.Manager()
	permissionGroup, err := catalogInverse(group, role.Catalog.GroupPermissions, permission, groups.key)
	if err != nil {
		return err
	}
	readPermissions := func(ctx context.Context, source orm.QuerySet[P]) ([]P, error) {
		bounded, err := source.Distinct().OrderBy(permissions.key.Asc()).Limit(auth.MaximumPermissions + 1)
		if err != nil {
			return nil, err
		}
		rows, err := bounded.All(ctx)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if _, _, _, err := permissions.inspect(row, true); err != nil {
				return nil, err
			}
		}
		return rows, nil
	}
	groups.readKeys = func(ctx context.Context, scope db.Queryer, id int64) ([]int64, error) {
		rows, err := readPermissions(ctx, permissionObjects.Using(scope).Filter(permissionGroup.Exact(id)))
		if err != nil {
			return nil, err
		}
		if len(rows) > auth.MaximumPermissions {
			return nil, managementError(CodePersistence, nil)
		}
		keys := make([]int64, len(rows))
		for index, row := range rows {
			_, keys[index], _, err = permissions.inspect(row, true)
			if err != nil || index > 0 && keys[index-1] >= keys[index] {
				return nil, managementError(CodePersistence, err)
			}
		}
		return keys, nil
	}
	groups.checkKeys = func(ctx context.Context, scope db.Queryer, keys []int64) error {
		rows, err := readPermissions(ctx, permissionObjects.Using(scope).Filter(permissions.key.In(keys...)))
		if err != nil {
			return err
		}
		if len(rows) != len(keys) {
			return profileInputError(groups.collection, "invalid_choice")
		}
		for index, row := range rows {
			_, key, _, err := permissions.inspect(row, true)
			if err != nil {
				return err
			}
			if key != keys[index] {
				return profileInputError(groups.collection, "invalid_choice")
			}
		}
		return nil
	}

	return nil
}

func catalogMembershipKeys[M any](ctx context.Context, source orm.QuerySet[M], key orm.AutoField[M], revision orm.IntegerField[M], maximum int) ([]int64, error) {
	type membership struct{ key, revision int64 }
	projection := orm.Project2(key, revision, func(key, revision int64) membership { return membership{key, revision} })
	selected, err := orm.SelectQuery(source.OrderBy(key.Asc()).Distinct(), projection)
	if err != nil {
		return nil, err
	}
	selected, err = selected.Limit(maximum + 1)
	if err != nil {
		return nil, err
	}
	rows, err := selected.All(ctx)
	if err != nil {
		return nil, err
	}
	if len(rows) > maximum {
		return nil, managementError(CodePersistence, nil)
	}
	keys := make([]int64, len(rows))
	for index, row := range rows {
		if row.revision <= 0 || index > 0 && rows[index-1].key >= row.key {
			return nil, managementError(CodePersistence, nil)
		}
		keys[index] = row.key
	}
	return keys, nil
}

func advanceCatalogRevision[M any](ctx context.Context, scope db.Session, objects orm.Manager[M], row M, name string) error {
	values, err := objects.ModelValues(row)
	if err != nil {
		return err
	}
	revision, valid := values[name].Integer()
	if !valid || revision <= 0 || revision == math.MaxInt64 {
		return managementError(CodePersistence, nil)
	}
	prepared, err := objects.PrepareValues(&row, map[string]query.Value{name: query.Integer(revision + 1)})
	if err != nil {
		return err
	}
	value, err := prepared.Model()
	if err != nil {
		return err
	}
	return prepared.Save(ctx, scope, &value)
}

// Through rows that are themselves selected account/catalog models also need
// an identity-creating insertion owner. The shared lifecycle handles incoming
// effects on those models; that distinct storage/creation composition remains
// explicit and is not inferred from generic through defaults.
func validateCatalogCollectionStorage(project orm.ProjectBinding, role ir.AccountRole) error {
	for _, relation := range project.ManyToManyRelations() {
		if relation.Source == role.Catalog.Group && relation.Field == role.Catalog.GroupPermissions {
			return validateLifecycleThroughStorage(role, relation.Through.Model)
		}
	}
	return managementError(CodeInvalidConfig, nil)
}

func validateAccountCollectionStorage(project orm.ProjectBinding, role ir.AccountRole, fields []string) error {
	found := make(map[string]bool)
	for _, relation := range project.ManyToManyRelations() {
		if relation.Source == role.Model && slices.Contains(fields, relation.Field) {
			if err := validateLifecycleThroughStorage(role, relation.Through.Model); err != nil {
				return err
			}
			found[relation.Field] = true
		}
	}
	// Scalar fields are checked by their model input owner; the two role
	// relations are always required when selected. Extra M2M capabilities are
	// bound and checked by prepareProfileCollections.
	for _, field := range []string{role.Groups, role.Permissions} {
		if slices.Contains(fields, field) && !found[field] {
			return managementError(CodeInvalidConfig, nil)
		}
	}
	return nil
}

func validateLifecycleThroughStorage(role ir.AccountRole, through ir.ModelIdentity) error {
	if through == role.Model || through == role.Catalog.Group || through == role.Catalog.Permission {
		return &query.Error{Category: query.CategoryQuery, Code: query.CodeUnsupported, Detail: "selected-model through insertion requires an explicit identity creation owner"}
	}
	return nil
}
