package orm

import (
	"reflect"
	"strings"

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
