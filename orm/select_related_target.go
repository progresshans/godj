package orm

import (
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"reflect"
	"slices"
	"strings"
)

// ForwardSelection is a closed, source-typed eager target. Its implementation
// retains the concrete target model type through scanning, cloning and access.
type ForwardSelection[S any] interface {
	prepareForwardSelection(int, *int) (preparedForwardSelection[S], error)
}

type preparedForwardSelection[S any] interface {
	path() forwardSelectPathState[S]
	validate() error
	sameBinding(preparedForwardSelection[S]) bool
	merge(preparedForwardSelection[S]) (preparedForwardSelection[S], error)
	projections([]query.RelationHop) ([]query.RelationProjection, error)
	columnCount() int
	newScan() forwardTargetScan[S]
}
type forwardTargetScan[S any] interface {
	destinations() []any
	snapshot() projectedForwardTarget[S]
}
type projectedForwardTarget[S any] interface {
	validate(S) (cachedForwardTarget, error)
	validateAbsent() error
}
type cachedForwardTarget interface {
	projection() query.RelationProjection
	relatedObject(db.Queryer) any
}

type preparedForwardTarget[S, T any] struct {
	state    forwardSelectState[S, T]
	base     query.Plan
	children []preparedForwardSelection[T]
}

// WithChildren attaches source-typed descendants without expanding generated
// types by depth. Each call owns its input slice and preserves an earlier error.
func (selection ForwardSelect[S, T]) WithChildren(children ...ForwardSelection[T]) ForwardSelect[S, T] {
	copy := append([]ForwardSelection[T](nil), selection.children...)
	selection.children = append(copy, children...)
	return selection
}

// WithConfigurationError lets a generated typed selector retain ownership or
// dispatch failures while preserving the first error across child composition.
func (selection ForwardSelect[S, T]) WithConfigurationError(err error) ForwardSelect[S, T] {
	if selection.configurationErr == nil {
		selection.configurationErr = err
	}
	return selection
}

// SelectRequiredForward and SelectNullableForward derive selections directly
// from sealed object handles. A failure stays on the selection until query
// preparation; unrelated object-only use does not require projection support.
func SelectRequiredForward[S, T any](relation RequiredForwardObject[S, T]) ForwardSelect[S, T] {
	return selectionFromObject(relation.state, false)
}
func SelectNullableForward[S, T any](relation NullableForwardObject[S, T]) ForwardSelect[S, T] {
	return selectionFromObject(relation.state, true)
}
func selectionFromObject[S, T any](state forwardObjectState[S, T], nullable bool) ForwardSelect[S, T] {
	if !state.valid || interfaceIsNil(state.storage) {
		return ForwardSelect[S, T]{configurationErr: relationInvalidPlan("forward object selection is unbound")}
	}
	path, err := ResolveForwardSelectPath(state.source, state.storage.Field().Name)
	if err != nil {
		return ForwardSelect[S, T]{configurationErr: err}
	}
	selection, err := bindForwardSelect(path.state, state, nullable)
	if err != nil {
		return ForwardSelect[S, T]{configurationErr: err}
	}
	return selection
}

// MaximumForwardSelectionNodes bounds preparation work, including repeated inputs.
const MaximumForwardSelectionNodes = 1024

func prepareSelectionSet[S any](selections []ForwardSelection[S], depth int, remaining *int) ([]preparedForwardSelection[S], error) {
	if len(selections) > *remaining {
		return nil, relationInvalidPlan("forward selection exceeds 1024 input nodes")
	}
	prepared := make([]preparedForwardSelection[S], len(selections))
	for i, selection := range selections {
		if interfaceIsNil(selection) {
			return nil, relationInvalidPlan("forward selection is nil")
		}
		target, err := selection.prepareForwardSelection(depth, remaining)
		if err != nil {
			return nil, err
		}
		prepared[i] = target
	}
	return mergeSelectionSet(prepared)
}
func mergeSelectionSet[S any](selections []preparedForwardSelection[S]) ([]preparedForwardSelection[S], error) {
	unique := make(map[string]preparedForwardSelection[S], len(selections))
	for _, target := range selections {
		if interfaceIsNil(target) {
			return nil, relationInvalidPlan("prepared forward selection is nil")
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
	result := make([]preparedForwardSelection[S], 0, len(unique))
	for _, target := range unique {
		result = append(result, target)
	}
	slices.SortFunc(result, func(left, right preparedForwardSelection[S]) int {
		return strings.Compare(left.path().path, right.path().path)
	})
	return result, nil
}
func (selection ForwardSelect[S, T]) prepareForwardSelection(depth int, remaining *int) (preparedForwardSelection[S], error) {
	if selection.configurationErr != nil {
		return nil, selection.configurationErr
	}
	if depth > query.MaximumRelationHops || *remaining <= 0 {
		return nil, relationInvalidPlan("forward selection exceeds its depth or node bound")
	}
	*remaining--
	if err := validateForwardSelectState(selection.state); err != nil {
		return nil, err
	}
	children, err := prepareSelectionSet(selection.children, depth+1, remaining)
	if err != nil {
		return nil, err
	}
	for _, child := range children {
		path := child.path()
		if path.source.snapshot != selection.state.relation.target.snapshot || path.source.identity != selection.state.relation.target.identity || reflect.TypeOf(path.sourceDescriptor) != reflect.TypeOf(selection.state.targetDescriptor) {
			return nil, relationInvalidPlan("selected child belongs to a different parent model or project binding")
		}
	}
	target := selection.state.path.relation.targetModel
	base, err := query.NewPlan(target.DBTable, modelFieldReferences(target)).WithLimit(2)
	if err != nil {
		return nil, err
	}
	return preparedForwardTarget[S, T]{state: selection.state, base: base, children: children}, nil
}
func (target preparedForwardTarget[S, T]) path() forwardSelectPathState[S] { return target.state.path }
func (target preparedForwardTarget[S, T]) validate() error {
	if err := validateForwardSelectState(target.state); err != nil {
		return err
	}
	expected, err := query.NewPlan(target.state.path.relation.targetModel.DBTable, modelFieldReferences(target.state.path.relation.targetModel)).WithLimit(2)
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
		if path.source.snapshot != target.state.relation.target.snapshot || path.source.identity != target.state.relation.target.identity || reflect.TypeOf(path.sourceDescriptor) != reflect.TypeOf(target.state.targetDescriptor) {
			return relationInvalidPlan("selected child binding changed")
		}
	}
	return nil
}
func (target preparedForwardTarget[S, T]) sameBinding(other preparedForwardSelection[S]) bool {
	value, ok := other.(preparedForwardTarget[S, T])
	return ok && target.state.path.projection.Equal(value.state.path.projection) &&
		target.state.path.source.snapshot == value.state.path.source.snapshot &&
		reflect.TypeOf(target.state.sourceDescriptor) == reflect.TypeOf(value.state.sourceDescriptor) &&
		reflect.TypeOf(target.state.targetDescriptor) == reflect.TypeOf(value.state.targetDescriptor) &&
		reflect.TypeOf(target.state.relation.storage) == reflect.TypeOf(value.state.relation.storage)
}
func (target preparedForwardTarget[S, T]) merge(other preparedForwardSelection[S]) (preparedForwardSelection[S], error) {
	if !target.sameBinding(other) {
		return nil, relationInvalidPlan("repeated selected target has conflicting binding metadata")
	}
	value := other.(preparedForwardTarget[S, T])
	combined := append([]preparedForwardSelection[T](nil), target.children...)
	combined = append(combined, value.children...)
	children, err := mergeSelectionSet(combined)
	if err != nil {
		return nil, err
	}
	target.children = children
	return target, nil
}
func (target preparedForwardTarget[S, T]) projections(prefix []query.RelationHop) ([]query.RelationProjection, error) {
	hops := append([]query.RelationHop(nil), prefix...)
	hops = append(hops, target.state.path.projection.Path().Hops()...)
	local := target.state.path.projection
	projection, err := query.NewForwardChainProjection(hops, local.Path().Terminal(), local.TargetColumns())
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
func (target preparedForwardTarget[S, T]) columnCount() int {
	count := len(target.state.path.projection.TargetColumns())
	for _, child := range target.children {
		count += child.columnCount()
	}
	return count
}
func (target preparedForwardTarget[S, T]) newScan() forwardTargetScan[S] {
	scan := target.state.targetDescriptor.NewProjectionScan()
	if interfaceIsNil(scan) {
		return nil
	}
	children := make([]forwardTargetScan[T], len(target.children))
	for i, child := range target.children {
		children[i] = child.newScan()
		if interfaceIsNil(children[i]) {
			return nil
		}
	}
	return typedForwardTargetScan[S, T]{target: target, scan: scan, children: children}
}

type typedForwardTargetScan[S, T any] struct {
	target   preparedForwardTarget[S, T]
	scan     ProjectionScan[T]
	children []forwardTargetScan[T]
}

func (scan typedForwardTargetScan[S, T]) destinations() []any {
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
func (scan typedForwardTargetScan[S, T]) snapshot() projectedForwardTarget[S] {
	value, key, presence := scan.scan.Decode()
	children := make([]projectedForwardTarget[T], len(scan.children))
	for i, child := range scan.children {
		children[i] = child.snapshot()
	}
	return typedProjectedForwardTarget[S, T]{target: scan.target, value: scan.target.state.targetDescriptor.CloneModel(value), key: key, presence: presence, children: children}
}

type typedProjectedForwardTarget[S, T any] struct {
	target   preparedForwardTarget[S, T]
	value    T
	key      query.Value
	presence ProjectionPresence
	children []projectedForwardTarget[T]
}

func (row typedProjectedForwardTarget[S, T]) validateAbsent() error {
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
func (row typedProjectedForwardTarget[S, T]) validate(source S) (cachedForwardTarget, error) {
	if len(row.children) != len(row.target.children) {
		return nil, relationInvalidPlan("projected descendant roster changed")
	}
	state := row.target.state
	foreignKey, ok := state.relation.storage.Value(state.sourceDescriptor.CloneModel(source))
	if !ok {
		return nil, relationInvalidPlan("relation storage could not read the projected source key")
	}
	if row.presence != ProjectionAbsent && row.presence != ProjectionPresent {
		return nil, relationInvalidPlan("target projection decoded an invalid row shape")
	}
	if row.presence == ProjectionAbsent && !row.key.IsNull() {
		return nil, relationInvalidPlan("absent target projection did not return a NULL key")
	}
	result := typedCachedForwardTarget[T]{selected: state.path.projection, descriptor: state.targetDescriptor, binding: state.relation.target}
	if foreignKey.IsNull() {
		if !state.relation.nullable {
			return nil, relatedObjectProjectionError(state.path.sourceKey, "required source key is NULL")
		}
		if row.presence != ProjectionAbsent || !row.key.IsNull() {
			return nil, relatedObjectProjectionError(state.path.sourceKey, "nullable NULL source key has a projected target")
		}
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
	identifier, ok := foreignKey.Integer()
	if !ok {
		return nil, relationInvalidPlan("relation storage returned a non-integer projected source key")
	}
	if row.presence != ProjectionPresent {
		return nil, relatedObjectProjectionError(state.path.sourceKey, "non-NULL source key has no projected target")
	}
	targetID, ok := row.key.Integer()
	if !ok || row.key.IsNull() {
		return nil, relatedObjectProjectionError(state.path.sourceKey, "projected target primary key is absent or non-integer")
	}
	if targetID != identifier {
		return nil, relatedObjectProjectionError(state.path.sourceKey, "projected target primary key does not match the source key")
	}
	plan, err := row.target.base.WithConditions(query.NewCondition(fieldReference(state.relation.targetKey), query.LookupExact, query.Integer(targetID)))
	if err != nil {
		return nil, err
	}
	result.value = state.targetDescriptor.CloneModel(row.value)
	result.present = true
	result.plan = plan
	result.children = make([]cachedForwardTarget, len(row.children))
	for i, child := range row.children {
		if interfaceIsNil(child) {
			return nil, relationInvalidPlan("projected child is nil")
		}
		cached, err := child.validate(row.value)
		if err != nil {
			return nil, err
		}
		result.children[i] = cached
	}
	return result, nil
}

type typedCachedForwardTarget[T any] struct {
	selected   query.RelationProjection
	descriptor ProjectionDescriptor[T]
	value      T
	present    bool
	plan       query.Plan
	binding    BoundModel[T]
	children   []cachedForwardTarget
}

func (target typedCachedForwardTarget[T]) projection() query.RelationProjection {
	return target.selected
}
func (target typedCachedForwardTarget[T]) relatedObject(backend db.Queryer) any {
	if !target.present {
		return newAbsentRelatedObject[T]()
	}
	evaluation := newEvaluationState[T]()
	evaluation.values = []T{target.descriptor.CloneModel(target.value)}
	evaluation.ready = true
	related := newRelatedObject(QuerySet[T]{backend: backend, descriptor: target.descriptor, plan: target.plan, evaluation: evaluation})
	if len(target.children) > 0 {
		related.selected = &relatedSelectedState[T]{binding: target.binding, descriptor: target.descriptor, value: forwardSelectedValue[T]{source: target.descriptor.CloneModel(target.value), targets: append([]cachedForwardTarget(nil), target.children...)}}
	}
	return related
}

// Related retrieves this binding's prepared cache from a selected result. The
// erased storage is private; matching projection, snapshot and concrete T are
// required before any typed cache is returned.
func (selection ForwardSelect[S, T]) Related(selected *ForwardSelected[S]) (*RelatedObject[T], error) {
	if selection.configurationErr != nil {
		return nil, selection.configurationErr
	}
	if err := selected.validate(); err != nil {
		return nil, err
	}
	if err := validateForwardSelectState(selection.state); err != nil {
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
