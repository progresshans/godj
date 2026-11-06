package model

import (
	"context"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/rowset"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

// Capture the actual selected typed managers. No same-shaped substitute or
// synthetic namespace/backend is needed to inspect an unprovisioned database.
func prepareStartupProbe[M, G, P any](binding Binding[M], groups orm.RelationObjectDescriptor[G], permissions orm.RelationObjectDescriptor[P]) (func(context.Context, db.Queryer) (bool, error), error) {
	memberships, err := bindDeletionMemberships(binding.state.project, binding.state.role)
	if err != nil {
		return nil, err
	}
	account, err := startupModelProbe(binding.state.objects)
	if err != nil {
		return nil, err
	}
	group, err := startupModelProbe(orm.NewManager(groups))
	if err != nil {
		return nil, err
	}
	permission, err := startupModelProbe(orm.NewManager(permissions))
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, scope db.Queryer) (bool, error) {
		present, err := account(ctx, scope)
		if err != nil {
			return false, err
		}
		for _, probe := range []func(context.Context, db.Queryer) (bool, error){group, permission} {
			if _, err := probe(ctx, scope); err != nil {
				return false, err
			}
		}
		for _, membership := range memberships {
			plan, err := query.NewPlan(membership.table, []query.FieldRef{membership.key, membership.source, membership.target}).WithOrderings(query.NewOrdering(membership.key, query.Ascending)).WithLimit(1)
			if err != nil {
				return false, err
			}
			count := 0
			err = rowset.Scan(ctx, scope, plan, func(err error) error { return managementError(CodePersistence, err) }, func(rows db.Rows) (bool, error) {
				var key, source, target any
				if err := rows.Scan(&key, &source, &target); err != nil {
					return false, err
				}
				_, keyValid := key.(int64)
				_, sourceValid := source.(int64)
				_, targetValid := target.(int64)
				if !keyValid || !sourceValid && (source != nil || !membership.source.Nullable()) || !targetValid && (target != nil || !membership.target.Nullable()) {
					return false, managementError(CodePersistence, nil)
				}
				count++
				if count > 1 {
					return false, managementError(CodePersistence, nil)
				}
				return true, nil
			})
			if err != nil {
				return false, err
			}
		}
		return present, nil
	}, nil
}

func startupModelProbe[M any](objects orm.Manager[M]) (func(context.Context, db.Queryer) (bool, error), error) {
	metadata, err := objects.Metadata()
	if err != nil {
		return nil, err
	}
	key, ok := primaryField(metadata)
	if !ok {
		return nil, invalidCredentialStore()
	}
	field := orm.NewAutoField[M](key)
	return func(ctx context.Context, scope db.Queryer) (bool, error) {
		// First selects every scalar column even when the table is empty.
		// There is no lower key bound: zero/negative existing keys count too.
		_, present, err := objects.Using(scope).OrderBy(field.Asc()).First(ctx)
		return present, err
	}, nil
}
