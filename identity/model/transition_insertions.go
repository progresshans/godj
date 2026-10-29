package model

import (
	"context"
	"crypto/sha256"
	"maps"
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
	batches    []orm.AppliedCollectionChanges
	rows       []orm.CollectionInsertedRow
	bytes      int
	identities []func(context.Context) error
	keys       map[deletionKey]bool
}

func (state *transitionInsertions) add(source orm.ProjectBinding, applied orm.AppliedCollectionChanges) error {
	if state.bytes < 0 || state.bytes > MaximumTransitionStateBytes {
		return managementError(CodeInvalidInput, nil)
	}
	if err := applied.ValidateSource(source); err != nil {
		return managementError(CodeInvalidConfig, err)
	}
	if err := state.addRows(source, applied.Insertions()); err != nil {
		return err
	}
	state.batches = append(state.batches, applied)
	return nil
}

func (state *transitionInsertions) include(source orm.ProjectBinding, other transitionInsertions) error {
	if err := state.addRows(source, other.rows); err != nil {
		return err
	}
	state.identities = append(state.identities, other.identities...)
	state.batches = append(state.batches, other.batches...)
	return nil
}

// Direct account/catalog INSERTs are typed identity writes, not collection
// inserts. Their actual keys and complete payloads still own physical through
// projections, and remain bound to this same native scope until final readback.
func addTransitionIdentity[M any](ctx context.Context, scope db.RelationSession, state *transitionInsertions, source orm.ProjectBinding, identity ir.ModelIdentity, objects orm.Manager[M], row M) (orm.CollectionInsertedRow, error) {
	var zero orm.CollectionInsertedRow
	model, found := source.Model(identity)
	metadata, err := objects.Metadata()
	if err != nil || !found || !metadata.Equal(model) {
		return zero, managementError(CodeInvalidConfig, err)
	}
	field, valid := primaryField(model)
	value, present, err := objects.PrimaryKey(row)
	key, integer := value.Integer()
	if err != nil || !valid || !present || !integer {
		return zero, managementError(CodePersistence, err)
	}
	values, err := objects.ModelValues(row)
	if err != nil {
		return zero, err
	}
	expected := maps.Clone(values)
	verify := func(ctx context.Context) error {
		if err := validateAccountSessionScope(ctx, scope); err != nil {
			return err
		}
		actual, err := objects.Using(scope).Filter(orm.NewAutoField[M](field).Exact(key)).Get(ctx)
		if err != nil {
			return err
		}
		values, err := objects.ModelValues(actual)
		if err != nil || !maps.Equal(values, expected) {
			return managementError(CodeConflict, err)
		}
		return validateAccountSessionScope(ctx, scope)
	}
	if err := verify(ctx); err != nil {
		return zero, err
	}
	inserted := orm.CollectionInsertedRow{Model: identity, Key: key, Values: values}
	if err := state.addRows(source, []orm.CollectionInsertedRow{inserted}); err != nil {
		return zero, err
	}
	state.identities = append(state.identities, verify)
	return inserted, nil
}

func (state *transitionInsertions) addRows(source orm.ProjectBinding, rows []orm.CollectionInsertedRow) error {
	if state.bytes < 0 || state.bytes > MaximumTransitionStateBytes {
		return managementError(CodeInvalidInput, nil)
	}
	if len(state.keys) != len(state.rows) {
		return managementError(CodeInvalidConfig, nil)
	}
	seen := make(map[deletionKey]bool, len(rows))
	budget := &transitionDigestWriter{hash: sha256.New(), size: state.bytes}
	for _, row := range rows {
		model, found := source.Model(row.Model)
		key, keyed := primaryField(model)
		value, present := row.Values[key.Name]
		actual, integer := value.Integer()
		identity := deletionKey{row.Model, row.Key}
		if !found || !keyed || !present || !integer || actual != row.Key || state.keys[identity] || seen[identity] {
			return managementError(CodeConflict, nil)
		}
		seen[identity] = true
		budget.part([]byte(row.Model.AppLabel))
		budget.part([]byte(row.Model.ModelName))
		if err := writeTransitionValues(budget, row.Values); err != nil {
			return err
		}
	}
	if state.keys == nil && len(rows) != 0 {
		state.keys = make(map[deletionKey]bool, len(rows))
	}
	for _, row := range rows {
		state.rows = append(state.rows, orm.CollectionInsertedRow{Model: row.Model, Key: row.Key, Values: maps.Clone(row.Values)})
		state.keys[deletionKey{row.Model, row.Key}] = true
	}
	state.bytes = budget.size
	return nil
}

func (state transitionInsertions) verify(ctx context.Context) error {
	for _, verify := range state.identities {
		if err := verify(ctx); err != nil {
			return err
		}
	}
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
