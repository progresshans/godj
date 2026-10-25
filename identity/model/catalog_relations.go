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
	binding := store.state.binding
	role, account := binding.state.role, binding.state.model
	accountKey, accountObjects := store.state.key, binding.state.objects
	groupObjects, permissionObjects := groups.resource.Manager(), permissions.resource.Manager()
	accountGroup, err := catalogRelation(account, role.Groups, group, groups.key)
	if err != nil {
		return err
	}
	permissionGroup, err := catalogInverse(group, role.Catalog.GroupPermissions, permission, groups.key)
	if err != nil {
		return err
	}
	groupAccount, err := catalogInverse(account, role.Groups, group, accountKey)
	if err != nil {
		return err
	}
	permissionAccount, err := catalogInverse(account, role.Permissions, permission, accountKey)
	if err != nil {
		return err
	}
	groupRevision, present := namedField(groups.model, groups.revision)
	if !present {
		return managementError(CodeInvalidConfig, nil)
	}
	groupRevisionField := orm.NewIntegerField[G](groupRevision)
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
	checkAccount := func(ctx context.Context, scope db.Queryer, namespace auth.CredentialDomain, row M, key int64) error {
		grants, err := store.state.permissions(ctx, scope, key)
		if err != nil {
			return err
		}
		_, err = binding.prepareCredentialForDomain(namespace, row, grants)
		return err
	}
	groups.checkMembers = func(ctx context.Context, scope db.Session, namespace auth.CredentialDomain, id int64, proposed []int64) error {
		return walkCatalogRows(ctx, accountObjects, accountObjects.Using(scope).Filter(accountGroup.Exact(id)), accountKey, func(row M, key int64) error {
			if err := checkAccount(ctx, scope, namespace, row, key); err != nil {
				return err
			}
			memberships, err := catalogMembershipKeys(ctx, groupObjects.Using(scope).Filter(groupAccount.Exact(key)), groups.key, groupRevisionField, MaximumAccountGroups)
			if err != nil {
				return err
			}
			position, found := slices.BinarySearch(memberships, id)
			if !found {
				return managementError(CodePersistence, nil)
			}
			otherGroups := append(memberships[:position:position], memberships[position+1:]...)
			rows, err := readPermissions(ctx, permissionObjects.Using(scope).Filter(orm.Or(permissionAccount.Exact(key), permissionGroup.In(otherGroups...), permissions.key.In(proposed...))))
			if err != nil {
				return err
			}
			if len(rows) > auth.MaximumPermissions {
				return profileInputError(groups.collection, "max_items")
			}
			return nil
		})
	}
	return nil
}

// Every page is materialized and its rowset closed before nested inspection or
// writes. The first page has no lower-bound predicate: zero/negative stored
// keys are ordinary owners. Strict advancement prevents a truncated/infinite
// walk when a backend violates ordered distinct model materialization.
func walkCatalogRows[M any](ctx context.Context, objects orm.Manager[M], source orm.QuerySet[M], key orm.AutoField[M], visit func(M, int64) error) error {
	var last int64
	started := false
	for {
		page := source.OrderBy(key.Asc())
		if started {
			page = page.Filter(key.GreaterThan(last))
		}
		bounded, err := page.Limit(catalogOwnerBatchSize)
		if err != nil {
			return err
		}
		rows, err := bounded.All(ctx)
		if err != nil {
			return err
		}
		if len(rows) > catalogOwnerBatchSize {
			return managementError(CodePersistence, nil)
		}
		for _, row := range rows {
			value, present, err := objects.PrimaryKey(row)
			id, integer := value.Integer()
			if err != nil || !present || !integer || started && id <= last {
				return managementError(CodePersistence, err)
			}
			if err := visit(row, id); err != nil {
				return err
			}
			last, started = id, true
		}
		if len(rows) < catalogOwnerBatchSize {
			return nil
		}
	}
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

// Catalog endpoint deletion has a complete lifecycle collector. Collection
// replacement is a separate operation: its multiple relation mutations still
// require a single combined lifecycle owner before critical effects are safe.
func validateCatalogCollectionTopology(project orm.ProjectBinding, role ir.AccountRole) error {
	var roots []ir.ModelIdentity
	for _, relation := range project.ManyToManyRelations() {
		if relation.Source == role.Catalog.Group && relation.Field == role.Catalog.GroupPermissions {
			if relation.Through.Model == role.Model || relation.Through.Model == role.Catalog.Group || relation.Through.Model == role.Catalog.Permission {
				return &query.Error{Category: query.CategoryQuery, Code: query.CodeUnsupported, Detail: "catalog membership storage requires another account or catalog lifecycle owner"}
			}
			roots = append(roots, relation.Through.Model)
		}
	}
	return validateAccountDeleteRoots(project, role, roots)
}

// SetKeys may delete an explicit through row even when its endpoint FKs use
// PROTECT. Inspect that root directly; endpoint deletion reachability alone
// cannot prove that collection replacement preserves other account owners.
func validateRoleDeleteTopology(project orm.ProjectBinding, role ir.AccountRole, fields []string) error {
	var roots []ir.ModelIdentity
	for _, relation := range project.ManyToManyRelations() {
		if relation.Source == role.Model && (relation.Field == role.Groups || relation.Field == role.Permissions) && slices.Contains(fields, relation.Field) {
			root := relation.Through.Model
			if root == role.Model || root == role.Catalog.Group || root == role.Catalog.Permission {
				return &query.Error{Category: query.CategoryQuery, Code: query.CodeUnsupported, Detail: "account membership storage requires another account or catalog lifecycle owner"}
			}
			roots = append(roots, root)
		}
	}
	expected := 0
	for _, field := range []string{role.Groups, role.Permissions} {
		if slices.Contains(fields, field) {
			expected++
		}
	}
	if len(roots) != expected {
		return managementError(CodeInvalidConfig, nil)
	}
	return validateAccountDeleteRoots(project, role, roots)
}

func validateAccountDeleteRoots(project orm.ProjectBinding, role ir.AccountRole, roots []ir.ModelIdentity) error {
	critical := map[ir.ModelIdentity]bool{role.Model: true, role.Catalog.Group: true, role.Catalog.Permission: true}
	relations := project.ForwardRelations()
	for _, root := range roots {
		pending, visited := []ir.ModelIdentity{root}, make(map[ir.ModelIdentity]bool)
		for len(pending) != 0 {
			current := pending[0]
			pending = pending[1:]
			if visited[current] {
				continue
			}
			visited[current] = true
			for _, relation := range relations {
				if relation.Target != current || relation.OnDelete == ir.DeleteProtect {
					continue
				}
				if critical[relation.Source] {
					return &query.Error{Category: query.CategoryQuery, Code: query.CodeUnsupported, Detail: "relation deletion requires lifecycle coordination for additional account or catalog row effects"}
				}
				if relation.OnDelete == ir.DeleteCascade {
					pending = append(pending, relation.Source)
				}
			}
		}
	}
	return nil
}
