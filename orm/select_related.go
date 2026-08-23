package orm

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// ProjectionPresence is the sealed row-shape result reported by an additive
// generated projection scanner. The zero value is deliberately invalid.
type ProjectionPresence uint8

const (
	ProjectionInvalid ProjectionPresence = iota
	ProjectionAbsent  ProjectionPresence = 1
	ProjectionPresent ProjectionPresence = 2
)

// ProjectionDescriptor is the additive joined-row scanning capability. It is
// implemented by generated companions on the same named descriptor used by
// ordinary root-only QuerySets.
type ProjectionDescriptor[M any] interface {
	ModelDescriptor[M]
	NewProjectionScan() ProjectionScan[M]
}

// ProjectionScan owns fresh per-row destinations and decodes their complete
// state after the runtime has called Row.Scan exactly once.
type ProjectionScan[M any] interface {
	Destinations() []any
	Decode() (model M, primaryKey query.Value, presence ProjectionPresence)
}

// ForwardSelectPath is one immutable, project-resolved direct forward eager
// path. Its zero value is invalid.
type ForwardSelectPath[S any] struct {
	state  forwardSelectPathState[S]
	marker [0]func(S)
}

type forwardSelectPathState[S any] struct {
	source           BoundModel[S]
	sourceDescriptor ProjectionDescriptor[S]
	relation         forwardRelationState
	sourceKey        ir.Field
	projection       query.RelationProjection
	path             string
	valid            bool
	marker           [0]func(S)
}

// ResolveForwardSelectPath resolves exactly one case-sensitive direct
// many-to-one path. Unknown, blank, multi-hop, and reverse names share the
// stable invalid_related_path taxonomy before any backend is involved.
func ResolveForwardSelectPath[S any](source BoundModel[S], path string) (ForwardSelectPath[S], error) {
	if err := validateObjectBoundModel(source); err != nil {
		return ForwardSelectPath[S]{}, err
	}
	if path == "" || strings.TrimSpace(path) == "" || strings.Contains(path, "__") {
		return ForwardSelectPath[S]{}, invalidRelatedPath(path)
	}
	metadata, ok := ProjectBinding{snapshot: source.snapshot}.Relation(source.identity, path)
	if !ok || metadata.Cardinality != ir.RelationManyToOne {
		return ForwardSelectPath[S]{}, invalidRelatedPath(path)
	}
	relation, err := resolveForwardRelationState(source.snapshot, source.identity, source.model, path)
	if err != nil {
		return ForwardSelectPath[S]{}, err
	}
	sourceKey, ok := findField(relation.sourceModel.Fields, relation.metadata.Field)
	if !ok || sourceKey.Kind != ir.FieldForeignKey || sourceKey.Nullable != relation.metadata.Nullable ||
		sourceKey.Relation == nil || sourceKey.Relation.Target != relation.metadata.Target ||
		sourceKey.Relation.Cardinality != ir.RelationManyToOne {
		return ForwardSelectPath[S]{}, relationInvalidPlan("forward select source key is not canonical")
	}
	sourceDescriptor, err := projectionDescriptorFor(source)
	if err != nil {
		return ForwardSelectPath[S]{}, err
	}
	targetColumns := make([]query.FieldRef, len(relation.targetModel.Fields))
	for index, field := range relation.targetModel.Fields {
		targetColumns[index] = fieldReference(field)
	}
	projection, err := query.NewForwardRelationProjection(
		relation.sourceIdentity,
		relation.sourceModel.DBTable,
		fieldReference(sourceKey),
		relation.metadata.Target,
		relation.targetModel.DBTable,
		fieldReference(relation.targetPrimaryKey),
		targetColumns,
	)
	if err != nil {
		return ForwardSelectPath[S]{}, err
	}
	return ForwardSelectPath[S]{state: forwardSelectPathState[S]{
		source:           source,
		sourceDescriptor: sourceDescriptor,
		relation:         relation,
		sourceKey:        sourceKey.Clone(),
		projection:       projection,
		path:             path,
		valid:            true,
	}}, nil
}

// ForwardSelect is a sealed source/target projection bound from an existing
// forward object handle. It reuses that handle's project snapshot, relation
// storage, target descriptor, and backend affinity rules.
type ForwardSelect[S, T any] struct {
	state        forwardSelectState[S, T]
	sourceMarker [0]func(S)
	targetMarker [0]func(T)
}

type forwardSelectState[S, T any] struct {
	path             forwardSelectPathState[S]
	relation         forwardObjectState[S, T]
	sourceDescriptor ProjectionDescriptor[S]
	targetDescriptor ProjectionDescriptor[T]
	valid            bool
	sourceMarker     [0]func(S)
	targetMarker     [0]func(T)
}

func BindRequiredForwardSelect[S, T any](
	path ForwardSelectPath[S],
	relation RequiredForwardObject[S, T],
) (ForwardSelect[S, T], error) {
	return bindForwardSelect(path.state, relation.state, false)
}

func BindNullableForwardSelect[S, T any](
	path ForwardSelectPath[S],
	relation NullableForwardObject[S, T],
) (ForwardSelect[S, T], error) {
	return bindForwardSelect(path.state, relation.state, true)
}

func bindForwardSelect[S, T any](
	path forwardSelectPathState[S],
	relation forwardObjectState[S, T],
	wantNullable bool,
) (ForwardSelect[S, T], error) {
	if err := validateForwardSelectPath(path); err != nil {
		return ForwardSelect[S, T]{}, err
	}
	if !relation.valid || relation.nullable != wantNullable {
		return ForwardSelect[S, T]{}, relationInvalidPlan("forward select object handle is unbound or has the wrong nullability")
	}
	if err := validateObjectBoundModel(relation.source); err != nil {
		return ForwardSelect[S, T]{}, err
	}
	if err := validateObjectBoundModel(relation.target); err != nil {
		return ForwardSelect[S, T]{}, err
	}
	if interfaceIsNil(relation.storage) || !immutableZeroStateValue(relation.storage) {
		return ForwardSelect[S, T]{}, relationInvalidPlan("forward select relation storage is unavailable or mutable")
	}
	if relation.source.snapshot != relation.target.snapshot || relation.source.snapshot != path.source.snapshot ||
		relation.source.identity != path.relation.sourceIdentity ||
		relation.target.identity != path.relation.metadata.Target ||
		!reflect.DeepEqual(relation.source.model, path.relation.sourceModel) ||
		!reflect.DeepEqual(relation.target.model, path.relation.targetModel) ||
		!reflect.DeepEqual(relation.targetKey, path.relation.targetPrimaryKey) ||
		!reflect.DeepEqual(relation.storage.Field(), path.sourceKey) {
		return ForwardSelect[S, T]{}, relationInvalidPlan("forward select path and object handle do not share one canonical project relation")
	}
	sourceDescriptor, err := projectionDescriptorFor(relation.source)
	if err != nil {
		return ForwardSelect[S, T]{}, err
	}
	targetDescriptor, err := projectionDescriptorFor(relation.target)
	if err != nil {
		return ForwardSelect[S, T]{}, err
	}
	if reflect.TypeOf(sourceDescriptor) != reflect.TypeOf(path.sourceDescriptor) {
		return ForwardSelect[S, T]{}, relationInvalidPlan("forward select source projection descriptor changed")
	}
	return ForwardSelect[S, T]{state: forwardSelectState[S, T]{
		path:             path,
		relation:         relation,
		sourceDescriptor: sourceDescriptor,
		targetDescriptor: targetDescriptor,
		valid:            true,
	}}, nil
}

func projectionDescriptorFor[M any](model BoundModel[M]) (ProjectionDescriptor[M], error) {
	if err := validateObjectBoundModel(model); err != nil {
		return nil, err
	}
	descriptor, ok := model.objectDescriptor.(ProjectionDescriptor[M])
	if !ok || interfaceIsNil(descriptor) {
		return nil, relationInvalidPlan("bound model does not provide a projection descriptor")
	}
	if !immutableZeroStateValue(descriptor) || !reflect.DeepEqual(descriptor.Metadata(), model.model) {
		return nil, relationInvalidPlan("projection descriptor is mutable or disagrees with the project model")
	}
	return descriptor, nil
}

func validateForwardSelectPath[S any](state forwardSelectPathState[S]) error {
	if !state.valid || state.path == "" {
		return relationInvalidPlan("forward select path is unbound")
	}
	if err := validateObjectBoundModel(state.source); err != nil {
		return err
	}
	if err := validateForwardState(state.relation); err != nil {
		return err
	}
	if state.source.snapshot != state.relation.snapshot || state.source.identity != state.relation.sourceIdentity ||
		!reflect.DeepEqual(state.source.model, state.relation.sourceModel) ||
		state.path != state.relation.metadata.Field ||
		!reflect.DeepEqual(state.sourceKey, mustFindField(state.relation.sourceModel.Fields, state.relation.metadata.Field)) {
		return relationInvalidPlan("forward select path changed after resolution")
	}
	projection, err := query.NewForwardRelationProjection(
		state.relation.sourceIdentity,
		state.relation.sourceModel.DBTable,
		fieldReference(state.sourceKey),
		state.relation.metadata.Target,
		state.relation.targetModel.DBTable,
		fieldReference(state.relation.targetPrimaryKey),
		modelFieldReferences(state.relation.targetModel),
	)
	if err != nil || !projection.Equal(state.projection) {
		return relationInvalidPlan("forward select projection changed after resolution")
	}
	descriptor, err := projectionDescriptorFor(state.source)
	if err != nil {
		return err
	}
	if reflect.TypeOf(descriptor) != reflect.TypeOf(state.sourceDescriptor) {
		return relationInvalidPlan("forward select path source descriptor changed")
	}
	return nil
}

func mustFindField(fields []ir.Field, name string) ir.Field {
	field, _ := findField(fields, name)
	return field
}

func modelFieldReferences(model ir.Model) []query.FieldRef {
	result := make([]query.FieldRef, len(model.Fields))
	for index, field := range model.Fields {
		result[index] = fieldReference(field)
	}
	return result
}

// ForwardSelectQuery is the independent All-only evaluation surface for one
// eager projection. It does not evaluate or populate the source QuerySet.
type ForwardSelectQuery[S, T any] struct {
	backend          db.Queryer
	plan             query.Plan
	selection        forwardSelectState[S, T]
	evaluation       *forwardSelectEvaluation[S, T]
	configurationErr error
	sourceMarker     [0]func(S)
	targetMarker     [0]func(T)
}

type forwardSelectEvaluation[S, T any] struct {
	mu     sync.Mutex
	ready  bool
	values []forwardSelectedValue[S, T]
	flight *evaluationFlight
}

type forwardSelectedValue[S, T any] struct {
	source        S
	target        T
	targetKey     query.Value
	targetPresent bool
	sourceMarker  [0]func(S)
	targetMarker  [0]func(T)
}

// Select preserves the source QuerySet plan while deriving an independent
// eager evaluation state. Configuration errors are stored for terminal
// precedence because this method performs no I/O.
func (s ForwardSelect[S, T]) Select(source QuerySet[S]) ForwardSelectQuery[S, T] {
	result := ForwardSelectQuery[S, T]{
		backend:    source.backend,
		plan:       source.plan,
		selection:  s.state,
		evaluation: &forwardSelectEvaluation[S, T]{},
	}
	if source.configurationErr != nil {
		result.configurationErr = source.configurationErr
		return result
	}
	if err := validateForwardSelectState(s.state); err != nil {
		result.configurationErr = err
		return result
	}
	if source.evaluation == nil || descriptorIsNil(source.descriptor) ||
		reflect.TypeOf(source.descriptor) != reflect.TypeOf(s.state.sourceDescriptor) ||
		!reflect.DeepEqual(source.descriptor.Metadata(), s.state.path.relation.sourceModel) ||
		source.plan.Table() != s.state.path.relation.sourceModel.DBTable ||
		!reflect.DeepEqual(source.plan.SourceFields(), modelFieldReferences(s.state.path.relation.sourceModel)) {
		result.configurationErr = relationInvalidPlan("source QuerySet does not match the resolved forward select source")
		return result
	}
	plan, err := source.plan.WithRelationProjection(s.state.path.projection)
	if err != nil {
		result.configurationErr = err
		return result
	}
	result.plan = plan
	return result
}

func validateForwardSelectState[S, T any](state forwardSelectState[S, T]) error {
	if !state.valid || interfaceIsNil(state.sourceDescriptor) || interfaceIsNil(state.targetDescriptor) {
		return relationInvalidPlan("forward select is unbound")
	}
	if err := validateForwardSelectPath(state.path); err != nil {
		return err
	}
	sourceDescriptor, err := projectionDescriptorFor(state.relation.source)
	if err != nil {
		return err
	}
	targetDescriptor, err := projectionDescriptorFor(state.relation.target)
	if err != nil {
		return err
	}
	if !state.relation.valid || interfaceIsNil(state.relation.storage) || !immutableZeroStateValue(state.relation.storage) ||
		state.relation.source.snapshot != state.path.source.snapshot ||
		!reflect.DeepEqual(state.relation.storage.Field(), state.path.sourceKey) ||
		reflect.TypeOf(state.sourceDescriptor) != reflect.TypeOf(state.path.sourceDescriptor) ||
		reflect.TypeOf(state.sourceDescriptor) != reflect.TypeOf(sourceDescriptor) ||
		reflect.TypeOf(state.targetDescriptor) != reflect.TypeOf(targetDescriptor) ||
		!reflect.DeepEqual(state.sourceDescriptor.Metadata(), state.path.relation.sourceModel) ||
		!reflect.DeepEqual(state.targetDescriptor.Metadata(), state.path.relation.targetModel) {
		return relationInvalidPlan("forward select state changed after binding")
	}
	return nil
}

func (q ForwardSelectQuery[S, T]) Plan() query.Plan    { return q.plan }
func (q ForwardSelectQuery[S, T]) Backend() db.Queryer { return q.backend }

// ForwardSelected owns one source clone and a ready related-object cache.
// Pointer identity prevents accidental value-copy ownership splits.
type ForwardSelected[S, T any] struct {
	source           S
	sourceDescriptor ProjectionDescriptor[S]
	related          *RelatedObject[T]
	_self            *ForwardSelected[S, T]
	sourceMarker     [0]func(S)
	targetMarker     [0]func(T)
}

func (s *ForwardSelected[S, T]) Source() (S, error) {
	var zero S
	if err := s.validate(); err != nil {
		return zero, err
	}
	return s.sourceDescriptor.CloneModel(s.source), nil
}

func (s *ForwardSelected[S, T]) Related() (*RelatedObject[T], error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	if err := s.related.validate(); err != nil {
		return nil, err
	}
	return s.related, nil
}

func (s *ForwardSelected[S, T]) validate() error {
	if s == nil || s._self != s || interfaceIsNil(s.sourceDescriptor) || s.related == nil {
		return relationInvalidPlan("forward selected result is nil, zero, or copied")
	}
	return nil
}

// All evaluates, validates, and atomically publishes one complete joined
// rowset. Failed evaluations never populate the successful cache.
func (q ForwardSelectQuery[S, T]) All(ctx context.Context) ([]*ForwardSelected[S, T], error) {
	if err := q.validateTerminal(ctx); err != nil {
		return nil, err
	}

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		state := q.evaluation
		state.mu.Lock()
		if state.ready {
			values := state.values
			state.mu.Unlock()
			result := q.cloneSelected(values)
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return result, nil
		}
		if flight := state.flight; flight != nil {
			state.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-flight.done:
			}
			if flight.err == nil || errors.Is(flight.err, context.Canceled) || errors.Is(flight.err, context.DeadlineExceeded) {
				continue
			}
			return nil, flight.err
		}

		flight := &evaluationFlight{done: make(chan struct{})}
		state.flight = flight
		state.mu.Unlock()

		values, err := q.scanAll(ctx)
		if err == nil {
			err = ctx.Err()
		}
		state.mu.Lock()
		if err == nil {
			state.values = values
			state.ready = true
		}
		flight.err = err
		state.flight = nil
		close(flight.done)
		state.mu.Unlock()

		if err != nil {
			return nil, err
		}
		result := q.cloneSelected(values)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return result, nil
	}
}

func (q ForwardSelectQuery[S, T]) validateTerminal(ctx context.Context) error {
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
	if q.evaluation == nil {
		return relationInvalidPlan("forward select evaluation state is nil")
	}
	if err := validateForwardSelectState(q.selection); err != nil {
		return err
	}
	projection, ok := q.plan.RelationProjection()
	if !ok || !projection.Equal(q.selection.path.projection) ||
		q.plan.Table() != q.selection.path.relation.sourceModel.DBTable ||
		!reflect.DeepEqual(q.plan.SourceFields(), modelFieldReferences(q.selection.path.relation.sourceModel)) {
		return relationInvalidPlan("forward select query plan is zero or changed")
	}
	return nil
}

type projectedRow[S, T any] struct {
	source         S
	sourceKey      query.Value
	sourcePresence ProjectionPresence
	target         T
	targetKey      query.Value
	targetPresence ProjectionPresence
}

func (q ForwardSelectQuery[S, T]) scanAll(ctx context.Context) ([]forwardSelectedValue[S, T], error) {
	rows, err := q.openRows(ctx)
	if err != nil {
		return nil, err
	}
	projected := make([]projectedRow[S, T], 0)
	for rows.Next() {
		if contextErr := ctx.Err(); contextErr != nil {
			err = contextErr
			break
		}
		sourceScan := q.selection.sourceDescriptor.NewProjectionScan()
		targetScan := q.selection.targetDescriptor.NewProjectionScan()
		if interfaceIsNil(sourceScan) || interfaceIsNil(targetScan) {
			err = relationInvalidPlan("projection descriptor returned a nil scan")
			break
		}
		sourceDestinations := sourceScan.Destinations()
		targetDestinations := targetScan.Destinations()
		if !validProjectionDestinations(sourceDestinations, len(q.plan.SourceFields())) ||
			!validProjectionDestinations(targetDestinations, len(q.selection.path.projection.TargetColumns())) {
			err = relationInvalidPlan("projection scan destinations do not match the selected columns")
			break
		}
		destinations := make([]any, 0, len(sourceDestinations)+len(targetDestinations))
		destinations = append(destinations, sourceDestinations...)
		destinations = append(destinations, targetDestinations...)
		if scanErr := rows.Scan(destinations...); scanErr != nil {
			err = fmt.Errorf("scan relation projection row: %w", scanErr)
			break
		}
		source, sourceKey, sourcePresence := sourceScan.Decode()
		target, targetKey, targetPresence := targetScan.Decode()
		projected = append(projected, projectedRow[S, T]{
			source:         q.selection.sourceDescriptor.CloneModel(source),
			sourceKey:      sourceKey,
			sourcePresence: sourcePresence,
			target:         q.selection.targetDescriptor.CloneModel(target),
			targetKey:      targetKey,
			targetPresence: targetPresence,
		})
	}
	err = finishRowsLifecycle(ctx, err, rows)
	if err != nil {
		return nil, err
	}

	values := make([]forwardSelectedValue[S, T], len(projected))
	for index, row := range projected {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		value, err := q.validateProjectedRow(row)
		if err != nil {
			return nil, err
		}
		values[index] = value
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

func (q ForwardSelectQuery[S, T]) openRows(ctx context.Context) (db.Rows, error) {
	rows, err := q.backend.Query(ctx, q.plan)
	if err != nil {
		if !interfaceIsNil(rows) {
			if closeErr := rows.Close(); closeErr != nil {
				return nil, errors.Join(err, fmt.Errorf("close rows returned with backend error: %w", closeErr))
			}
		}
		return nil, err
	}
	if interfaceIsNil(rows) {
		return nil, relationBackendInvalidPlan("backend returned nil rows without an error")
	}
	return rows, nil
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

func (q ForwardSelectQuery[S, T]) validateProjectedRow(row projectedRow[S, T]) (forwardSelectedValue[S, T], error) {
	if row.sourcePresence != ProjectionPresent || row.sourceKey.IsNull() {
		return forwardSelectedValue[S, T]{}, relationInvalidPlan("source projection did not decode one present model")
	}
	if _, ok := row.sourceKey.Integer(); !ok {
		return forwardSelectedValue[S, T]{}, relationInvalidPlan("source projection returned a non-integer primary key")
	}
	foreignKey, ok := q.selection.relation.storage.Value(q.selection.sourceDescriptor.CloneModel(row.source))
	if !ok {
		return forwardSelectedValue[S, T]{}, relationInvalidPlan("relation storage could not read the projected source key")
	}

	if row.targetPresence != ProjectionAbsent && row.targetPresence != ProjectionPresent {
		return forwardSelectedValue[S, T]{}, relationInvalidPlan("target projection decoded an invalid row shape")
	}
	if row.targetPresence == ProjectionAbsent && !row.targetKey.IsNull() {
		return forwardSelectedValue[S, T]{}, relationInvalidPlan("absent target projection did not return a NULL key")
	}
	if foreignKey.IsNull() {
		if !q.selection.relation.nullable {
			return forwardSelectedValue[S, T]{}, relatedObjectProjectionError(q.selection.path.sourceKey, "required source key is NULL")
		}
		if row.targetPresence != ProjectionAbsent || !row.targetKey.IsNull() {
			return forwardSelectedValue[S, T]{}, relatedObjectProjectionError(q.selection.path.sourceKey, "nullable NULL source key has a projected target")
		}
		return forwardSelectedValue[S, T]{
			source: q.selection.sourceDescriptor.CloneModel(row.source),
		}, nil
	}
	identifier, ok := foreignKey.Integer()
	if !ok {
		return forwardSelectedValue[S, T]{}, relationInvalidPlan("relation storage returned a non-integer projected source key")
	}
	if row.targetPresence != ProjectionPresent {
		return forwardSelectedValue[S, T]{}, relatedObjectProjectionError(q.selection.path.sourceKey, "non-NULL source key has no projected target")
	}
	targetIdentifier, ok := row.targetKey.Integer()
	if !ok || row.targetKey.IsNull() {
		return forwardSelectedValue[S, T]{}, relatedObjectProjectionError(q.selection.path.sourceKey, "projected target primary key is absent or non-integer")
	}
	if targetIdentifier != identifier {
		return forwardSelectedValue[S, T]{}, relatedObjectProjectionError(q.selection.path.sourceKey, "projected target primary key does not match the source key")
	}
	return forwardSelectedValue[S, T]{
		source:        q.selection.sourceDescriptor.CloneModel(row.source),
		target:        q.selection.targetDescriptor.CloneModel(row.target),
		targetKey:     query.Integer(targetIdentifier),
		targetPresent: true,
	}, nil
}

func (q ForwardSelectQuery[S, T]) cloneSelected(values []forwardSelectedValue[S, T]) []*ForwardSelected[S, T] {
	result := make([]*ForwardSelected[S, T], len(values))
	for index, value := range values {
		selected := &ForwardSelected[S, T]{
			source:           q.selection.sourceDescriptor.CloneModel(value.source),
			sourceDescriptor: q.selection.sourceDescriptor,
			related:          q.readyRelated(value),
		}
		selected._self = selected
		result[index] = selected
	}
	return result
}

func (q ForwardSelectQuery[S, T]) readyRelated(value forwardSelectedValue[S, T]) *RelatedObject[T] {
	if !value.targetPresent {
		return newAbsentRelatedObject[T]()
	}
	identifier, _ := value.targetKey.Integer()
	primaryKey := NewIntegerField[T](q.selection.relation.targetKey)
	querySet := NewManager[T](q.selection.targetDescriptor).
		Using(q.backend).
		Filter(primaryKey.Exact(identifier))
	limited, _ := querySet.Limit(2)
	evaluation := newEvaluationState[T]()
	evaluation.values = []T{q.selection.targetDescriptor.CloneModel(value.target)}
	evaluation.ready = true
	limited.evaluation = evaluation
	return newRelatedObject(limited)
}

func invalidRelatedPath(path string) *query.Error {
	return &query.Error{
		Category: query.CategoryField,
		Code:     query.CodeInvalidRelatedPath,
		Field:    path,
		Detail:   "path is not one direct forward many-to-one relation",
	}
}

func relatedObjectProjectionError(field ir.Field, detail string) *query.Error {
	return &query.Error{
		Category: query.CategoryIntegrity,
		Code:     query.CodeRelatedObjectProjection,
		Field:    field.Name,
		Detail:   detail,
	}
}
