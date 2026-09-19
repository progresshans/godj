package orm

import (
	"context"
	"fmt"
	"reflect"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

// ForwardSelectQuery evaluates the selected forward target tree in one rowset. Its
// cache is independent of the source QuerySet and retains concrete target types.
type ForwardSelectQuery[S any] struct {
	backend          db.Queryer
	plan             query.Plan
	binding          BoundModel[S]
	sourceDescriptor ProjectionDescriptor[S]
	targets          []preparedForwardSelection[S]
	evaluation       *evaluationState[forwardSelectedValue[S]]
	configurationErr error
	marker           [0]func(S)
}
type forwardSelectedValue[S any] struct {
	source  S
	targets []cachedForwardTarget
}

// SelectForward prepares one or more targets without evaluating the source.
// Errors stay on the query so terminals can apply context precedence.
func SelectForward[S any](source QuerySet[S], selections ...ForwardSelection[S]) ForwardSelectQuery[S] {
	result := ForwardSelectQuery[S]{backend: source.backend, plan: source.plan, evaluation: newEvaluationState[forwardSelectedValue[S]]()}
	if source.configurationErr != nil {
		return result.WithConfigurationError(source.configurationErr)
	}
	if len(selections) == 0 {
		return result.WithConfigurationError(relationInvalidPlan("forward selection requires at least one target"))
	}
	if len(source.plan.RelationProjections()) != 0 {
		return result.WithConfigurationError(relationInvalidPlan("source QuerySet already contains eager projections"))
	}
	remaining := MaximumForwardSelectionNodes
	targets, err := prepareSelectionSet(selections, 1, &remaining)
	if err != nil {
		return result.WithConfigurationError(err)
	}
	result.targets = targets
	for index, target := range targets {
		path := target.path()
		if index == 0 {
			result.binding = path.source
			result.sourceDescriptor = path.sourceDescriptor
		}
		if path.source.snapshot != result.binding.snapshot || path.source.identity != result.binding.identity || reflect.TypeOf(path.sourceDescriptor) != reflect.TypeOf(result.sourceDescriptor) {
			return result.WithConfigurationError(relationInvalidPlan("selected targets do not share one source binding"))
		}
	}
	if source.evaluation == nil || descriptorIsNil(source.descriptor) || reflect.TypeOf(source.descriptor) != reflect.TypeOf(result.sourceDescriptor) || !reflect.DeepEqual(source.descriptor.Metadata(), result.binding.model) || source.plan.Table() != result.binding.model.DBTable || !reflect.DeepEqual(source.plan.SourceFields(), modelFieldReferences(result.binding.model)) {
		return result.WithConfigurationError(relationInvalidPlan("source QuerySet does not match the selected source binding"))
	}
	var projections []query.RelationProjection
	for _, target := range result.targets {
		items, err := target.projections(nil)
		if err != nil {
			return result.WithConfigurationError(err)
		}
		projections = append(projections, items...)
	}
	plan, err := source.plan.WithRelationProjections(projections...)
	if err != nil {
		return result.WithConfigurationError(err)
	}
	result.plan = plan
	return result
}

// Select starts with this bound target and may include other target types from
// the same source. All combinations use SelectForward's common runtime.
func (selection ForwardSelect[S, T]) Select(source QuerySet[S], others ...ForwardSelection[S]) ForwardSelectQuery[S] {
	targets := make([]ForwardSelection[S], 0, len(others)+1)
	targets = append(targets, selection)
	targets = append(targets, others...)
	return SelectForward(source, targets...)
}
func (q ForwardSelectQuery[S]) Plan() query.Plan    { return q.plan }
func (q ForwardSelectQuery[S]) Backend() db.Queryer { return q.backend }

// WithSourceBinding seals a generated object/facade query to its own project
// source. A raw QuerySet alone has no project snapshot with which to compare a
// selector, so the generated boundary supplies that ownership explicitly.
func (q ForwardSelectQuery[S]) WithSourceBinding(source BoundModel[S]) ForwardSelectQuery[S] {
	if q.configurationErr != nil {
		return q
	}
	if err := validateObjectBoundModel(source); err != nil {
		return q.WithConfigurationError(err)
	}
	if q.binding.snapshot != source.snapshot || q.binding.identity != source.identity || reflect.TypeOf(q.sourceDescriptor) != reflect.TypeOf(source.objectDescriptor) {
		return q.WithConfigurationError(relationInvalidPlan("selected query belongs to another source binding"))
	}
	return q
}
func (q ForwardSelectQuery[S]) ConfigurationError() error { return q.configurationErr }
func (q ForwardSelectQuery[S]) WithConfigurationError(err error) ForwardSelectQuery[S] {
	if err != nil {
		q.configurationErr = err
	}
	return q
}

type selectedForwardCache struct {
	projection query.RelationProjection
	related    any
}

// ForwardSelected owns a source clone and independent, typed target caches.
// Copying the struct does not transfer its ownership.
type ForwardSelected[S any] struct {
	source           S
	backend          db.Queryer
	binding          BoundModel[S]
	sourceDescriptor ProjectionDescriptor[S]
	targets          map[string]selectedForwardCache
	_self            *ForwardSelected[S]
	marker           [0]func(S)
}

func (s *ForwardSelected[S]) validate() error {
	if s == nil || s._self != s || interfaceIsNil(s.sourceDescriptor) || interfaceIsNil(s.backend) || len(s.targets) == 0 {
		return relationInvalidPlan("forward selected result is nil, zero, or copied")
	}
	for name, target := range s.targets {
		if name != target.projection.TerminalHop().Field() || interfaceIsNil(target.related) {
			return relationInvalidPlan("forward selected target cache is invalid")
		}
	}
	return nil
}

// ValidateSourceBinding protects generated graph wrappers from a same-shaped
// model belonging to a different sealed project snapshot.
func (s *ForwardSelected[S]) ValidateSourceBinding(source BoundModel[S]) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := validateObjectBoundModel(source); err != nil {
		return err
	}
	if s.binding.snapshot != source.snapshot || s.binding.identity != source.identity || reflect.TypeOf(s.sourceDescriptor) != reflect.TypeOf(source.objectDescriptor) {
		return relationInvalidPlan("selected graph belongs to another source binding")
	}
	return nil
}

// Backend retains the affinity of the evaluated graph, including lazy edges
// that a generated wrapper may later access.
func (s *ForwardSelected[S]) Backend() (db.Queryer, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	return s.backend, nil
}
func (s *ForwardSelected[S]) Source() (S, error) {
	var zero S
	if err := s.validate(); err != nil {
		return zero, err
	}
	return s.sourceDescriptor.CloneModel(s.source), nil
}

// HasSelection lets generated object bridges preserve lazy caches for targets
// that were not selected. It validates ownership before exposing membership.
func (s *ForwardSelected[S]) HasSelection(path string) (bool, error) {
	if err := s.validate(); err != nil {
		return false, err
	}
	_, ok := s.targets[path]
	return ok, nil
}

func (q ForwardSelectQuery[S]) validateTerminal(ctx context.Context) error {
	if interfaceIsNil(ctx) {
		return relationInvalidPlan("context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if q.configurationErr != nil {
		return q.configurationErr
	}
	if interfaceIsNil(q.backend) {
		return relationBackendInvalidPlan("backend is nil")
	}
	if q.evaluation == nil || len(q.targets) == 0 {
		return relationInvalidPlan("forward select evaluation is unbound")
	}
	if err := validateObjectBoundModel(q.binding); err != nil {
		return err
	}
	descriptor, err := projectionDescriptorFor(q.binding)
	if err != nil {
		return err
	}
	if reflect.TypeOf(descriptor) != reflect.TypeOf(q.sourceDescriptor) {
		return relationInvalidPlan("selected source descriptor changed")
	}
	projections := q.plan.RelationProjections()
	if q.plan.Table() != q.binding.model.DBTable || !reflect.DeepEqual(q.plan.SourceFields(), modelFieldReferences(q.binding.model)) {
		return relationInvalidPlan("forward select query plan is zero or changed")
	}
	var expected []query.RelationProjection
	for _, target := range q.targets {
		if interfaceIsNil(target) {
			return relationInvalidPlan("selected target is nil")
		}
		if err := target.validate(); err != nil {
			return err
		}
		path := target.path()
		if path.source.snapshot != q.binding.snapshot || path.source.identity != q.binding.identity || reflect.TypeOf(path.sourceDescriptor) != reflect.TypeOf(q.sourceDescriptor) {
			return relationInvalidPlan("selected projection or binding changed")
		}
	}
	for _, target := range q.targets {
		items, err := target.projections(nil)
		if err != nil {
			return err
		}
		expected = append(expected, items...)
	}
	if len(projections) != len(expected) {
		return relationInvalidPlan("selected projection roster changed")
	}
	for i, projection := range projections {
		if !projection.Equal(expected[i]) {
			return relationInvalidPlan("selected projection route changed")
		}
	}
	return nil
}

func (q ForwardSelectQuery[S]) All(ctx context.Context) ([]*ForwardSelected[S], error) {
	if err := q.validateTerminal(ctx); err != nil {
		return nil, err
	}
	values, err := q.evaluation.evaluate(ctx, func(ctx context.Context) ([]forwardSelectedValue[S], error) {
		values, err := q.scan(ctx, q.plan, 0)
		if err == nil {
			err = ctx.Err()
		}
		return values, err
	})
	if err != nil {
		return nil, err
	}
	result := make([]*ForwardSelected[S], len(values))
	for index, value := range values {
		result[index] = q.cloneSelection(value)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
func (q ForwardSelectQuery[S]) First(ctx context.Context) (*ForwardSelected[S], bool, error) {
	if err := q.validateTerminal(ctx); err != nil {
		return nil, false, err
	}
	if len(q.plan.Orderings()) == 0 {
		return nil, false, &query.Error{Category: query.CategoryQuery, Code: query.CodeUnorderedQuery, Detail: "First requires an explicit ordering"}
	}
	values, ready := q.evaluation.cachedValues()
	if !ready {
		var err error
		values, err = q.scan(ctx, planWithMaximumRows(q.plan, 1), 1)
		if err != nil {
			return nil, false, err
		}
	}
	if len(values) == 0 {
		return nil, false, ctx.Err()
	}
	result := q.cloneSelection(values[0])
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	return result, true, nil
}
func (q ForwardSelectQuery[S]) Count(ctx context.Context) (int64, error) {
	if err := q.validateTerminal(ctx); err != nil {
		return 0, err
	}
	if values, ready := q.evaluation.cachedValues(); ready {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		return int64(len(values)), nil
	}
	return newQuerySet[S](q.backend, q.sourceDescriptor, q.plan.WithoutRelationProjections()).Count(ctx)
}

type projectedForwardRow[S any] struct {
	source   S
	key      query.Value
	presence ProjectionPresence
	targets  []projectedForwardTarget[S]
}

func (q ForwardSelectQuery[S]) scan(ctx context.Context, plan query.Plan, maximum int) ([]forwardSelectedValue[S], error) {
	rows, err := openQueryRows(ctx, q.backend, plan)
	if err != nil {
		return nil, err
	}
	lifecycle := rowsLifecycle{rows: rows}
	defer lifecycle.close()
	projected := make([]projectedForwardRow[S], 0)
	sourceColumns := len(plan.SourceFields())
	for (maximum == 0 || len(projected) < maximum) && rows.Next() {
		if err = ctx.Err(); err != nil {
			break
		}
		sourceScan := q.sourceDescriptor.NewProjectionScan()
		if interfaceIsNil(sourceScan) {
			err = relationInvalidPlan("projection descriptor returned a nil scan")
			break
		}
		sourceDestinations := sourceScan.Destinations()
		if !validProjectionDestinations(sourceDestinations, sourceColumns) {
			err = relationInvalidPlan("source projection scan destinations do not match selected columns")
			break
		}
		destinations := append([]any(nil), sourceDestinations...)
		scans := make([]forwardTargetScan[S], len(q.targets))
		for index, target := range q.targets {
			scans[index] = target.newScan()
			if interfaceIsNil(scans[index]) {
				err = relationInvalidPlan("projection descriptor returned a nil scan")
				break
			}
			cells := scans[index].destinations()
			if !validProjectionDestinations(cells, target.columnCount()) {
				err = relationInvalidPlan("target projection scan destinations do not match selected columns")
				break
			}
			destinations = append(destinations, cells...)
		}
		if err != nil {
			break
		}
		if scanErr := rows.Scan(destinations...); scanErr != nil {
			err = fmt.Errorf("scan relation projection row: %w", scanErr)
			break
		}
		source, key, presence := sourceScan.Decode()
		row := projectedForwardRow[S]{source: q.sourceDescriptor.CloneModel(source), key: key, presence: presence, targets: make([]projectedForwardTarget[S], len(scans))}
		for index, scan := range scans {
			row.targets[index] = scan.snapshot()
		}
		projected = append(projected, row)
	}
	if err = lifecycle.finish(ctx, err); err != nil {
		return nil, err
	}
	values := make([]forwardSelectedValue[S], len(projected))
	for index, row := range projected {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if row.presence != ProjectionPresent || row.key.IsNull() {
			return nil, relationInvalidPlan("source projection did not decode one present model")
		}
		if _, ok := row.key.Integer(); !ok {
			return nil, relationInvalidPlan("source projection returned a non-integer primary key")
		}
		value := forwardSelectedValue[S]{source: q.sourceDescriptor.CloneModel(row.source), targets: make([]cachedForwardTarget, len(row.targets))}
		for index, target := range row.targets {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			ready, err := target.validate(row.source)
			if err != nil {
				return nil, err
			}
			value.targets[index] = ready
		}
		values[index] = value
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return values, nil
}
func validProjectionDestinations(destinations []any, expected int) bool {
	if len(destinations) != expected {
		return false
	}
	for _, destination := range destinations {
		if interfaceIsNil(destination) {
			return false
		}
	}
	return true
}
func (q ForwardSelectQuery[S]) cloneSelection(value forwardSelectedValue[S]) *ForwardSelected[S] {
	return cloneForwardSelection(q.backend, q.binding, q.sourceDescriptor, value)
}
func cloneForwardSelection[S any](backend db.Queryer, binding BoundModel[S], descriptor ProjectionDescriptor[S], value forwardSelectedValue[S]) *ForwardSelected[S] {
	selected := &ForwardSelected[S]{source: descriptor.CloneModel(value.source), backend: backend, binding: binding, sourceDescriptor: descriptor, targets: make(map[string]selectedForwardCache, len(value.targets))}
	for _, target := range value.targets {
		projection := target.projection()
		selected.targets[projection.TerminalHop().Field()] = selectedForwardCache{projection: projection, related: target.relatedObject(backend)}
	}
	selected._self = selected
	return selected
}
