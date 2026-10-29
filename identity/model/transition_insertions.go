package model

import (
	"context"
	"crypto/sha256"
	"slices"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// A physical through row may project into several role relations. Only actual
// source-bound insertions can extend their reviewed inventories; a matching
// logical pair is not evidence for a new physical identity or nullable row.
type transitionInsertions struct {
	batches []orm.AppliedCollectionChanges
	rows    []orm.CollectionInsertedRow
	bytes   int
}

func (state *transitionInsertions) add(source orm.ProjectBinding, applied orm.AppliedCollectionChanges) error {
	if state.bytes < 0 || state.bytes > MaximumTransitionStateBytes {
		return managementError(CodeInvalidInput, nil)
	}
	if err := applied.ValidateSource(source); err != nil {
		return managementError(CodeInvalidConfig, err)
	}
	rows := applied.Insertions()
	seen := make(map[deletionKey]bool, len(state.rows)+len(rows))
	for _, row := range state.rows {
		seen[deletionKey{row.Model, row.Key}] = true
	}
	budget := &transitionDigestWriter{hash: sha256.New(), size: state.bytes}
	for _, row := range rows {
		model, found := source.Model(row.Model)
		key, keyed := primaryField(model)
		value, present := row.Values[key.Name]
		actual, integer := value.Integer()
		identity := deletionKey{row.Model, row.Key}
		if !found || !keyed || !present || !integer || actual != row.Key || seen[identity] {
			return managementError(CodeConflict, nil)
		}
		seen[identity] = true
		budget.part([]byte(row.Model.AppLabel))
		budget.part([]byte(row.Model.ModelName))
		if err := writeTransitionValues(budget, row.Values); err != nil {
			return err
		}
	}
	state.rows = append(state.rows, rows...)
	state.batches = append(state.batches, applied)
	state.bytes = budget.size
	return nil
}

func (state transitionInsertions) verify(ctx context.Context) error {
	for _, batch := range state.batches {
		if err := batch.Verify(ctx); err != nil {
			return err
		}
	}
	return nil
}

func projectTransitionLinks(membership deletionMembership, before []deletionLink, inserted []orm.CollectionInsertedRow) ([]deletionLink, error) {
	result := slices.Clone(before)
	seen := make(map[int64]bool, len(before)+len(inserted))
	for _, row := range before {
		if seen[row.key] {
			return nil, managementError(CodeConflict, nil)
		}
		seen[row.key] = true
	}
	for _, row := range inserted {
		if row.Model != membership.through {
			continue
		}
		keyValue, present := row.Values[membership.key.Name()]
		key, integer := keyValue.Integer()
		if !present || !integer || key != row.Key || seen[key] {
			return nil, managementError(CodeConflict, nil)
		}
		endpoint := func(field query.FieldRef) (int64, bool, error) {
			value, present := row.Values[field.Name()]
			if !present {
				return 0, false, managementError(CodeConflict, nil)
			}
			key, integer := value.Integer()
			if !integer && (!field.Nullable() || !value.IsNull()) {
				return 0, false, managementError(CodeConflict, nil)
			}
			return key, integer, nil
		}
		left, leftValid, err := endpoint(membership.source)
		if err != nil {
			return nil, err
		}
		right, rightValid, err := endpoint(membership.target)
		if err != nil {
			return nil, err
		}
		seen[key] = true
		result = append(result, deletionLink{key: key, source: left, target: right, sourceValid: leftValid, targetValid: rightValid})
	}
	slices.SortFunc(result, func(a, b deletionLink) int { return compareInt64(a.key, b.key) })
	return result, nil
}

func projectTransitionCatalog(memberships []deletionMembership, role ir.AccountRole, before transitionCatalogInventory, inserted []orm.CollectionInsertedRow) (transitionCatalogInventory, error) {
	for _, membership := range memberships {
		if membership.owner != role.Catalog.Group || membership.field != role.Catalog.GroupPermissions {
			continue
		}
		result := cloneTransitionCatalogInventory(before)
		var err error
		result.links, err = projectTransitionLinks(membership, before.links, inserted)
		return result, err
	}
	return transitionCatalogInventory{}, managementError(CodeInvalidConfig, nil)
}

func projectTransitionAccountLinks(before []transitionRoleLinkInventory, inserted []orm.CollectionInsertedRow) ([]transitionRoleLinkInventory, error) {
	result := slices.Clone(before)
	for index := range result {
		var err error
		result[index].rows, err = projectTransitionLinks(result[index].membership, result[index].rows, inserted)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

// Materializing account/profile collections can add sparse rows to the same
// table that stores old and new catalog grants. The logical catalog must remain
// unchanged. Pin a fresh digest only after comparing its complete inventory to
// the independently projected exact rows, never by adopting a post-write read.
func (owner *Transition[S, T]) observeInsertedCatalog(ctx context.Context, scope db.Queryer, current *transitionObservation[S, T], rows []orm.CollectionInsertedRow) error {
	oldRole := owner.old.state.credentials.state.binding.state.role
	nextRole := owner.next.state.credentials.state.binding.state.role
	before, err := projectTransitionCatalog(owner.oldMemberships, oldRole, current.catalog.before, rows)
	if err != nil {
		return err
	}
	after, err := projectTransitionCatalog(owner.nextMemberships, nextRole, current.catalog.after, rows)
	if err != nil {
		return err
	}
	actual, err := owner.observeCatalogRows(ctx, scope, current.catalog.operation)
	if err != nil {
		return err
	}
	if !(sameTransitionCatalogInventory(before, actual.before) && sameTransitionCatalogInventory(after, actual.after)) {
		return managementError(CodeConflict, nil)
	}
	actual.mapping = cloneCatalogMappings(current.catalog.mapping)
	actual.groups, actual.direct = current.catalog.groups, current.catalog.direct
	current.catalog = actual
	return nil
}
