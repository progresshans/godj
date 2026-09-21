package orm

import (
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// RelatedSelection is a closed, source-typed eager target. Its implementation
// retains the concrete target model type through scanning, cloning and access.
type RelatedSelection[S any] interface {
	prepareRelatedSelection(int, *int) (preparedRelatedSelection[S], error)
}

type preparedRelatedSelection[S any] interface {
	path() relatedSelectPathState[S]
	validate() error
	sameBinding(preparedRelatedSelection[S]) bool
	merge(preparedRelatedSelection[S]) (preparedRelatedSelection[S], error)
	projections([]query.RelationHop) ([]query.RelationProjection, error)
	columnCount() int
	newScan() relatedTargetScan[S]
}
type relatedTargetScan[S any] interface {
	destinations() []any
	snapshot() projectedRelatedTarget[S]
}
type projectedRelatedTarget[S any] interface {
	validate(S, selectedCardinality, string) (cachedRelatedTarget, error)
	validateAbsent() error
}
type cachedRelatedTarget interface {
	projection() query.RelationProjection
	relatedObject(db.Queryer) any
}

type preparedRelatedTarget[S, T any] struct {
	state    relatedSelectState[S, T]
	base     query.Plan
	children []preparedRelatedSelection[T]
}

// WithChildren attaches source-typed descendants without expanding generated
// types by depth. Each call owns its input slice and preserves an earlier error.
func (selection RelatedSelect[S, T]) WithChildren(children ...RelatedSelection[T]) RelatedSelect[S, T] {
	copy := append([]RelatedSelection[T](nil), selection.children...)
	selection.children = append(copy, children...)
	return selection
}

// WithConfigurationError lets a generated typed selector retain ownership or
// dispatch failures while preserving the first error across child composition.
func (selection RelatedSelect[S, T]) WithConfigurationError(err error) RelatedSelect[S, T] {
	if selection.configurationErr == nil {
		selection.configurationErr = err
	}
	return selection
}

// SelectRequiredForward and SelectNullableForward derive selections directly
// from sealed object handles. A failure stays on the selection until query
// preparation; unrelated object-only use does not require projection support.
func SelectRequiredForward[S, T any](relation RequiredForwardObject[S, T]) RelatedSelect[S, T] {
	return selectionFromObject(relation.state, false)
}
func SelectNullableForward[S, T any](relation NullableForwardObject[S, T]) RelatedSelect[S, T] {
	return selectionFromObject(relation.state, true)
}
func selectionFromObject[S, T any](state forwardObjectState[S, T], nullable bool) RelatedSelect[S, T] {
	if !state.valid || interfaceIsNil(state.storage) {
		return RelatedSelect[S, T]{configurationErr: relationInvalidPlan("forward object selection is unbound")}
	}
	path, err := ResolveRelatedSelectPath(state.source, state.storage.Field().Name)
	if err != nil {
		return RelatedSelect[S, T]{configurationErr: err}
	}
	selection, err := bindForwardSelect(path.state, state, nullable)
	if err != nil {
		return RelatedSelect[S, T]{configurationErr: err}
	}
	return selection
}

// SelectReverseOneToOne derives an optional selection from a sealed reverse
// handle and composes with the same typed descendants as related selections.
func SelectReverseOneToOne[S, T any](relation ReverseOneToOneObject[S, T]) RelatedSelect[S, T] {
	if err := relation.state.validate(); err != nil {
		return RelatedSelect[S, T]{configurationErr: err}
	}
	path, err := ResolveRelatedSelectPath(relation.state.owner, relation.state.sourceForeignKey.Relation.Reverse.Name)
	if err != nil {
		return RelatedSelect[S, T]{configurationErr: err}
	}
	selection, err := BindReverseOneToOneSelect(path, relation)
	if err != nil {
		return RelatedSelect[S, T]{configurationErr: err}
	}
	return selection
}

// MaximumRelatedSelectionNodes bounds preparation work, including repeated inputs.
const MaximumRelatedSelectionNodes = 1024

func prepareSelectionSet[S any](selections []RelatedSelection[S], depth int, remaining *int) ([]preparedRelatedSelection[S], error) {
	if len(selections) > *remaining {
		return nil, relationInvalidPlan("related selection exceeds 1024 input nodes")
	}
	prepared := make([]preparedRelatedSelection[S], len(selections))
	for i, selection := range selections {
		if interfaceIsNil(selection) {
			return nil, relationInvalidPlan("related selection is nil")
		}
		target, err := selection.prepareRelatedSelection(depth, remaining)
		if err != nil {
			return nil, err
		}
		prepared[i] = target
	}
	return mergeSelectionSet(prepared)
}
func mergeSelectionSet[S any](selections []preparedRelatedSelection[S]) ([]preparedRelatedSelection[S], error) {
	unique := make(map[string]preparedRelatedSelection[S], len(selections))
	for _, target := range selections {
		if interfaceIsNil(target) {
			return nil, relationInvalidPlan("prepared related selection is nil")
		}
		name := target.path().path
		if previous, exists := unique[name]; exists {
			merged, err := previous.merge(target)
			if err != nil {
				return nil, err
			}
			target = merged
		}
		unique[name] = target
	}
	result := make([]preparedRelatedSelection[S], 0, len(unique))
	for _, target := range unique {
		result = append(result, target)
	}
	slices.SortFunc(result, func(left, right preparedRelatedSelection[S]) int {
		return strings.Compare(left.path().path, right.path().path)
	})
	return result, nil
}
func (selection RelatedSelect[S, T]) prepareRelatedSelection(depth int, remaining *int) (preparedRelatedSelection[S], error) {
	if selection.configurationErr != nil {
		return nil, selection.configurationErr
	}
	if depth > query.MaximumRelationHops || *remaining <= 0 {
		return nil, relationInvalidPlan("related selection exceeds its depth or node bound")
	}
	*remaining--
	if err := validateRelatedSelectState(selection.state); err != nil {
		return nil, err
	}
	children, err := prepareSelectionSet(selection.children, depth+1, remaining)
	if err != nil {
		return nil, err
	}
	for _, child := range children {
		path := child.path()
		if path.source.snapshot != selection.state.target.snapshot || path.source.identity != selection.state.target.identity || reflect.TypeOf(path.sourceDescriptor) != reflect.TypeOf(selection.state.targetDescriptor) {
			return nil, relationInvalidPlan("selected child belongs to a different parent model or project binding")
		}
	}
	target := selection.state.path.targetModel
	base, err := query.NewPlan(target.DBTable, modelFieldReferences(target)).WithLimit(2)
	if err != nil {
		return nil, err
	}
	return preparedRelatedTarget[S, T]{state: selection.state, base: base, children: children}, nil
}
func (target preparedRelatedTarget[S, T]) path() relatedSelectPathState[S] { return target.state.path }
func (target preparedRelatedTarget[S, T]) validate() error {
	if err := validateRelatedSelectState(target.state); err != nil {
		return err
	}
	expected, err := query.NewPlan(target.state.path.targetModel.DBTable, modelFieldReferences(target.state.path.targetModel)).WithLimit(2)
	if err != nil || !expected.Equal(target.base) {
		return relationInvalidPlan("selected target source plan changed after binding")
	}
	for _, child := range target.children {
		if interfaceIsNil(child) {
			return relationInvalidPlan("selected child is nil")
		}
		if err := child.validate(); err != nil {
			return err
		}
		path := child.path()
		if path.source.snapshot != target.state.target.snapshot || path.source.identity != target.state.target.identity || reflect.TypeOf(path.sourceDescriptor) != reflect.TypeOf(target.state.targetDescriptor) {
			return relationInvalidPlan("selected child binding changed")
		}
	}
	return nil
}
func (target preparedRelatedTarget[S, T]) sameBinding(other preparedRelatedSelection[S]) bool {
	value, ok := other.(preparedRelatedTarget[S, T])
	return ok && target.state.path.projection.Equal(value.state.path.projection) &&
		target.state.path.source.snapshot == value.state.path.source.snapshot &&
		reflect.TypeOf(target.state.sourceDescriptor) == reflect.TypeOf(value.state.sourceDescriptor) &&
		reflect.TypeOf(target.state.targetDescriptor) == reflect.TypeOf(value.state.targetDescriptor) &&
		reflect.TypeOf(target.state.sourceStorage) == reflect.TypeOf(value.state.sourceStorage) &&
		reflect.TypeOf(target.state.targetStorage) == reflect.TypeOf(value.state.targetStorage)
}
func (target preparedRelatedTarget[S, T]) merge(other preparedRelatedSelection[S]) (preparedRelatedSelection[S], error) {
	if !target.sameBinding(other) {
		return nil, relationInvalidPlan("repeated selected target has conflicting binding metadata")
	}
	value := other.(preparedRelatedTarget[S, T])
	combined := append([]preparedRelatedSelection[T](nil), target.children...)
	combined = append(combined, value.children...)
	children, err := mergeSelectionSet(combined)
	if err != nil {
		return nil, err
	}
	target.children = children
	return target, nil
}
func (target preparedRelatedTarget[S, T]) projections(prefix []query.RelationHop) ([]query.RelationProjection, error) {
	hops := append([]query.RelationHop(nil), prefix...)
	hops = append(hops, target.state.path.projection.Path().Hops()...)
	local := target.state.path.projection
	projection, err := query.NewRelationProjection(hops, local.Path().Terminal(), local.TargetColumns())
	if err != nil {
		return nil, err
	}
	result := []query.RelationProjection{projection}
	for _, child := range target.children {
		nested, err := child.projections(hops)
		if err != nil {
			return nil, err
		}
		result = append(result, nested...)
	}
	return result, nil
}
func (target preparedRelatedTarget[S, T]) columnCount() int {
	count := len(target.state.path.projection.TargetColumns())
	for _, child := range target.children {
		count += child.columnCount()
	}
	return count
}
func (target preparedRelatedTarget[S, T]) newScan() relatedTargetScan[S] {
	scan := target.state.targetDescriptor.NewProjectionScan()
	if interfaceIsNil(scan) {
		return nil
	}
	children := make([]relatedTargetScan[T], len(target.children))
	for i, child := range target.children {
		children[i] = child.newScan()
		if interfaceIsNil(children[i]) {
			return nil
		}
	}
	return typedRelatedTargetScan[S, T]{target: target, scan: scan, children: children}
}

type typedRelatedTargetScan[S, T any] struct {
	target   preparedRelatedTarget[S, T]
	scan     ProjectionScan[T]
	children []relatedTargetScan[T]
}

func (scan typedRelatedTargetScan[S, T]) destinations() []any {
	own := scan.scan.Destinations()
	if !validProjectionDestinations(own, len(scan.target.state.path.projection.TargetColumns())) {
		return nil
	}
	result := append([]any(nil), own...)
	for i, child := range scan.children {
		cells := child.destinations()
		if !validProjectionDestinations(cells, scan.target.children[i].columnCount()) {
			return nil
		}
		result = append(result, cells...)
	}
	return result
}
func (scan typedRelatedTargetScan[S, T]) snapshot() projectedRelatedTarget[S] {
	value, key, presence := scan.scan.Decode()
	children := make([]projectedRelatedTarget[T], len(scan.children))
	for i, child := range scan.children {
		children[i] = child.snapshot()
	}
	return typedProjectedRelatedTarget[S, T]{target: scan.target, value: scan.target.state.targetDescriptor.CloneModel(value), key: key, presence: presence, children: children}
}

type typedProjectedRelatedTarget[S, T any] struct {
	target   preparedRelatedTarget[S, T]
	value    T
	key      query.Value
	presence ProjectionPresence
	children []projectedRelatedTarget[T]
}

func (row typedProjectedRelatedTarget[S, T]) validateAbsent() error {
	if len(row.children) != len(row.target.children) {
		return relationInvalidPlan("projected descendant roster changed")
	}
	if row.presence != ProjectionAbsent || !row.key.IsNull() {
		return relatedObjectProjectionError(row.target.state.path.sourceKey, "absent ancestor has a present or partial descendant")
	}
	for _, child := range row.children {
		if interfaceIsNil(child) {
			return relationInvalidPlan("projected child is nil")
		}
		if err := child.validateAbsent(); err != nil {
			return err
		}
	}
	return nil
}

type selectedCardinality map[string]map[int64]query.Value

func (row typedProjectedRelatedTarget[S, T]) validate(source S, seen selectedCardinality, prefix string) (cachedRelatedTarget, error) {
	if len(row.children) != len(row.target.children) {
		return nil, relationInvalidPlan("projected descendant roster changed")
	}
	state := row.target.state
	if row.presence != ProjectionAbsent && row.presence != ProjectionPresent {
		return nil, relationInvalidPlan("target projection decoded an invalid row shape")
	}
	if row.presence == ProjectionAbsent && !row.key.IsNull() {
		return nil, relationInvalidPlan("absent target projection did not return a NULL key")
	}
	result := typedCachedRelatedTarget[T]{selected: state.path.projection, descriptor: state.targetDescriptor, binding: state.target}
	route := prefix + strconv.Itoa(len(state.path.path)) + ":" + state.path.path
	if err := row.validateMembership(source, seen, route); err != nil {
		return nil, err
	}
	if state.path.projection.TerminalHop().Direction() == query.RelationReverse {
		descriptor := state.source.objectDescriptor.(PrimaryKeyObjectDescriptor[S])
		ownerKey, _ := descriptor.PrimaryKey(state.sourceDescriptor.CloneModel(source))
		var err error
		result.plan, err = row.target.base.WithConditions(query.NewCondition(fieldReference(state.path.sourceKey), query.LookupExact, ownerKey))
		if err != nil {
			return nil, err
		}
		result.plan = result.plan.WithOrderings(query.NewOrdering(fieldReference(state.targetKey), query.Ascending))
		result.allowMissing = true
	}
	if row.presence == ProjectionAbsent {
		for _, child := range row.children {
			if interfaceIsNil(child) {
				return nil, relationInvalidPlan("projected child is nil")
			}
			if err := child.validateAbsent(); err != nil {
				return nil, err
			}
		}
		return result, nil
	}
	targetID, ok := row.key.Integer()
	if !ok || row.key.IsNull() {
		return nil, relatedObjectProjectionError(state.path.sourceKey, "projected target primary key is absent or non-integer")
	}
	plan, err := row.target.base.WithConditions(query.NewCondition(fieldReference(state.targetKey), query.LookupExact, query.Integer(targetID)))
	if err != nil {
		return nil, err
	}
	result.value = state.targetDescriptor.CloneModel(row.value)
	result.present = true
	if !result.allowMissing {
		result.plan = plan
	}
	result.children = make([]cachedRelatedTarget, len(row.children))
	for i, child := range row.children {
		if interfaceIsNil(child) {
			return nil, relationInvalidPlan("projected child is nil")
		}
		cached, err := child.validate(row.value, seen, route)
		if err != nil {
			return nil, err
		}
		result.children[i] = cached
	}
	return result, nil
}

// Membership is the only direction-specific row check. Scanning, descendant
// validation, cloning, context and publication remain shared.
func (row typedProjectedRelatedTarget[S, T]) validateMembership(source S, seen selectedCardinality, route string) error {
	state := row.target.state
	failure := func(detail string) error { return relatedObjectProjectionError(state.path.sourceKey, detail) }
	if state.path.projection.TerminalHop().Direction() == query.RelationReverse {
		descriptor, ok := state.source.objectDescriptor.(PrimaryKeyObjectDescriptor[S])
		if !ok {
			return relationInvalidPlan("reverse selection owner has no primary key descriptor")
		}
		ownerKey, present := descriptor.PrimaryKey(state.sourceDescriptor.CloneModel(source))
		ownerID, integer := ownerKey.Integer()
		if !present || !integer || ownerKey.IsNull() {
			return failure("projected reverse owner has no explicit integer primary key")
		}
		owners := seen[route]
		if owners == nil {
			owners = map[int64]query.Value{}
			seen[route] = owners
		}
		if previous, exists := owners[ownerID]; exists && !previous.Equal(row.key) {
			return &query.Error{Category: query.CategoryIntegrity, Code: query.CodeRelatedObjectCardinality, Field: state.path.sourceKey.Name, Detail: "one reverse owner has conflicting projected children"}
		}
		owners[ownerID] = row.key
		if row.presence == ProjectionAbsent {
			return nil
		}
		foreignKey, ok := state.targetStorage.Value(state.targetDescriptor.CloneModel(row.value))
		if !ok {
			return relationInvalidPlan("reverse selection could not read child FK")
		}
		childOwner, integer := foreignKey.Integer()
		if !integer || foreignKey.IsNull() || childOwner != ownerID {
			return failure("projected child FK does not match the owner primary key")
		}
		return nil
	}
	foreignKey, ok := state.sourceStorage.Value(state.sourceDescriptor.CloneModel(source))
	if !ok {
		return relationInvalidPlan("relation storage could not read projected source FK")
	}
	if foreignKey.IsNull() {
		if !state.path.sourceKey.Nullable {
			return failure("required source key is NULL")
		}
		if row.presence != ProjectionAbsent {
			return failure("nullable NULL source key has a projected target")
		}
		return nil
	}
	identifier, ok := foreignKey.Integer()
	if !ok {
		return relationInvalidPlan("relation storage returned a non-integer projected source key")
	}
	if row.presence != ProjectionPresent {
		return failure("non-NULL source key has no projected target")
	}
	targetID, ok := row.key.Integer()
	if !ok || row.key.IsNull() || targetID != identifier {
		return failure("projected target primary key does not match the source key")
	}
	return nil
}

type typedCachedRelatedTarget[T any] struct {
	selected     query.RelationProjection
	descriptor   ProjectionDescriptor[T]
	value        T
	present      bool
	allowMissing bool
	plan         query.Plan
	binding      BoundModel[T]
	children     []cachedRelatedTarget
}

func (target typedCachedRelatedTarget[T]) projection() query.RelationProjection {
	return target.selected
}
func (target typedCachedRelatedTarget[T]) relatedObject(backend db.Queryer) any {
	if !target.present && !target.allowMissing {
		return newAbsentRelatedObject[T]()
	}
	evaluation := newEvaluationState[T]()
	if target.present {
		evaluation.values = []T{target.descriptor.CloneModel(target.value)}
	} else {
		evaluation.values = []T{}
	}
	evaluation.ready = true
	related := newRelatedObject(QuerySet[T]{backend: backend, descriptor: target.descriptor, plan: target.plan, evaluation: evaluation})
	related.allowMissing = target.allowMissing
	if len(target.children) > 0 {
		related.selected = &relatedSelectedState[T]{binding: target.binding, descriptor: target.descriptor, value: relatedSelectedValue[T]{source: target.descriptor.CloneModel(target.value), targets: append([]cachedRelatedTarget(nil), target.children...)}}
	}
	return related
}

// Related retrieves this binding's prepared cache from a selected result. The
// erased storage is private; matching projection, snapshot and concrete T are
// required before any typed cache is returned.
func (selection RelatedSelect[S, T]) Related(selected *RelatedSelected[S]) (*RelatedObject[T], error) {
	if selection.configurationErr != nil {
		return nil, selection.configurationErr
	}
	if err := selected.validate(); err != nil {
		return nil, err
	}
	if err := validateRelatedSelectState(selection.state); err != nil {
		return nil, err
	}
	if selected.binding.snapshot != selection.state.path.source.snapshot || selected.binding.identity != selection.state.path.source.identity || reflect.TypeOf(selected.sourceDescriptor) != reflect.TypeOf(selection.state.sourceDescriptor) {
		return nil, relationInvalidPlan("selected result belongs to a different source binding")
	}
	value, found := selected.targets[selection.state.path.path]
	if !found || !value.projection.Equal(selection.state.path.projection) {
		return nil, relationInvalidPlan("selected result does not contain this bound projection")
	}
	related, ok := value.related.(*RelatedObject[T])
	if !ok || related == nil {
		return nil, relationInvalidPlan("selected target has a different Go model type")
	}
	if err := related.validate(); err != nil {
		return nil, err
	}
	return related, nil
}
