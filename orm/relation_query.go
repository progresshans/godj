package orm

import (
	"reflect"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// BoundModel seals one generated descriptor to a model in an immutable
// project snapshot. Its zero value is unbound and fails all fallible bind
// operations with a structured invalid-plan error.
type BoundModel[M any] struct {
	snapshot         *projectBindingSnapshot
	identity         ir.ModelIdentity
	model            ir.Model
	objectDescriptor RelationObjectDescriptor[M]
	objectPlan       query.Plan
	marker           [0]func(M)
}

// BindModel verifies that a generated descriptor describes exactly the model
// published by binding. Descriptor state is read and cloned once; later
// descriptor or caller mutation cannot alter the bound model.
func BindModel[M any](
	binding ProjectBinding,
	identity ir.ModelIdentity,
	descriptor ModelDescriptor[M],
) (BoundModel[M], error) {
	if descriptorIsNil(descriptor) {
		return BoundModel[M]{}, relationInvalidPlan("descriptor is nil")
	}
	if binding.snapshot == nil {
		return BoundModel[M]{}, relationInvalidPlan("project binding is unbound")
	}
	model, ok := binding.snapshot.models[identity]
	if !ok {
		return BoundModel[M]{}, relationInvalidPlan("model identity is not present in project binding")
	}
	descriptorModel := descriptor.Metadata().Clone()
	if !reflect.DeepEqual(model, descriptorModel) {
		return BoundModel[M]{}, relationInvalidPlan("descriptor metadata does not match project model")
	}
	var objectDescriptor RelationObjectDescriptor[M]
	var objectPlan query.Plan
	if capable, ok := descriptor.(RelationObjectDescriptor[M]); ok {
		snapshot := capable.SnapshotRelationObjectDescriptor()
		if interfaceIsNil(snapshot) {
			return BoundModel[M]{}, relationInvalidPlan("relation object descriptor snapshot is nil")
		}
		if !immutableZeroStateValue(snapshot) {
			return BoundModel[M]{}, relationInvalidPlan("relation object descriptor snapshot must be a named non-pointer zero-size struct")
		}
		if snapshotModel := snapshot.Metadata().Clone(); !reflect.DeepEqual(model, snapshotModel) {
			return BoundModel[M]{}, relationInvalidPlan("relation object descriptor snapshot metadata does not match project model")
		}
		objectDescriptor = snapshot
		objectPlan = query.NewPlan(model.DBTable, modelFieldReferences(model))
	}
	return BoundModel[M]{
		snapshot:         binding.snapshot,
		identity:         identity,
		model:            model.Clone(),
		objectDescriptor: objectDescriptor,
		objectPlan:       objectPlan,
	}, nil
}

// ForwardRelation retains the root and terminal Go types of a forward route.
// Each declaration belongs to the same immutable project snapshot.
type ForwardRelation[S, T any] struct {
	route        forwardQueryRoute
	sourceMarker [0]func(S)
	targetMarker [0]func(T)
}

type forwardRelationState struct {
	snapshot         *projectBindingSnapshot
	sourceIdentity   ir.ModelIdentity
	sourceModel      ir.Model
	metadata         RelationMetadata
	targetModel      ir.Model
	targetPrimaryKey ir.Field
}

// BindForward resolves one required or nullable forward relation. It rejects
// independently built or zero snapshots before relation lookup so no partially
// valid path is published.
func BindForward[S, T any](
	source BoundModel[S],
	field string,
	target BoundModel[T],
) (ForwardRelation[S, T], error) {
	if err := validateBoundModel(source); err != nil {
		return ForwardRelation[S, T]{}, err
	}
	if err := validateBoundModel(target); err != nil {
		return ForwardRelation[S, T]{}, err
	}
	if source.snapshot != target.snapshot {
		return ForwardRelation[S, T]{}, relationInvalidPlan("source and target models belong to different project snapshots")
	}
	state, err := resolveForwardRelationState(source.snapshot, source.identity, source.model, field)
	if err != nil {
		return ForwardRelation[S, T]{}, err
	}
	if state.metadata.Target != target.identity || !reflect.DeepEqual(state.targetModel, target.model) {
		return ForwardRelation[S, T]{}, relationInvalidPlan("forward relation target does not match bound target model")
	}
	return ForwardRelation[S, T]{route: forwardQueryRoute{steps: []forwardRelationState{state}}}, nil
}

// RelatedIntegerField and RelatedStringField carry the source model type after
// the target model and terminal field have been validated.
type RelatedIntegerField[M any] struct {
	configurationErr error
	path             query.RelationPath
	valid            bool
	marker           [0]func(M)
}

type RelatedStringField[M any] struct {
	configurationErr error
	path             query.RelationPath
	valid            bool
	marker           [0]func(M)
}

func (r ForwardRelation[S, T]) Integer(field ReferenceField[T, int64]) (RelatedIntegerField[S], error) {
	if err := r.route.validate(); err != nil {
		return RelatedIntegerField[S]{}, err
	}
	metadata, err := relatedScalarMetadata(r.route.last().targetModel, field, true, ir.FieldAuto, ir.FieldInteger)
	if err != nil {
		return RelatedIntegerField[S]{}, err
	}
	path, err := r.route.path(fieldReference(metadata), query.RelationTerminalRelatedField)
	if err != nil {
		return RelatedIntegerField[S]{}, err
	}
	return RelatedIntegerField[S]{path: path, valid: true}, nil
}

// Comparison transport is shared by nullable and non-null storage. Exact
// metadata and supported kinds still come from the sealed project snapshot.
func relatedScalarMetadata[M, V any](model ir.Model, field ReferenceField[M, V], allowNullable bool, kinds ...ir.FieldKind) (ir.Field, error) {
	if interfaceIsNil(field) {
		return ir.Field{}, relationInvalidPlan("related scalar field is nil")
	}
	var modelValue M
	var scalarValue V
	reference, err := field.referenceField(modelValue, scalarValue)
	if err != nil {
		return ir.Field{}, err
	}
	for _, kind := range kinds {
		metadata, ok := matchingTerminalField(model, reference, kind)
		if ok && (allowNullable || !metadata.Nullable) {
			return metadata, nil
		}
	}
	return ir.Field{}, unknownRelatedField(reference.Name())
}

func (r ForwardRelation[S, T]) String(field ReferenceField[T, string]) (RelatedStringField[S], error) {
	if err := r.route.validate(); err != nil {
		return RelatedStringField[S]{}, err
	}
	metadata, err := relatedScalarMetadata(r.route.last().targetModel, field, true, ir.FieldChar, ir.FieldText)
	if err != nil {
		return RelatedStringField[S]{}, err
	}
	path, err := r.route.path(fieldReference(metadata), query.RelationTerminalRelatedField)
	if err != nil {
		return RelatedStringField[S]{}, err
	}
	return RelatedStringField[S]{path: path, valid: true}, nil
}

func matchingStringTerminalField(model ir.Model, reference query.FieldRef) (ir.Field, bool) {
	for _, kind := range []ir.FieldKind{ir.FieldChar, ir.FieldText} {
		if field, found := matchingTerminalField(model, reference, kind); found {
			return field, true
		}
	}
	return ir.Field{}, false
}

func (f RelatedIntegerField[M]) Exact(value int64) Predicate[M] {
	if f.configurationErr != nil {
		return Predicate[M]{err: f.configurationErr}
	}
	if !f.valid {
		return Predicate[M]{err: relationInvalidPlan("related integer field is unbound")}
	}
	return predicateFromCondition[M](query.NewRelatedCondition(f.path, query.LookupExact, query.Integer(value)), nil)
}

func (f RelatedStringField[M]) Exact(value string) Predicate[M] {
	if f.configurationErr != nil {
		return Predicate[M]{err: f.configurationErr}
	}
	if !f.valid {
		return Predicate[M]{err: relationInvalidPlan("related string field is unbound")}
	}
	return predicateFromCondition[M](query.NewRelatedCondition(f.path, query.LookupExact, query.String(value)), nil)
}

func validateBoundModel[M any](model BoundModel[M]) error {
	if model.snapshot == nil {
		return relationInvalidPlan("bound model is unbound")
	}
	snapshotModel, ok := model.snapshot.models[model.identity]
	if !ok || !reflect.DeepEqual(snapshotModel, model.model) {
		return relationInvalidPlan("bound model does not match its project snapshot")
	}
	return nil
}

func resolveForwardRelationState(
	snapshot *projectBindingSnapshot,
	source ir.ModelIdentity,
	sourceModel ir.Model,
	field string,
) (forwardRelationState, error) {
	if snapshot == nil {
		return forwardRelationState{}, relationInvalidPlan("project binding is unbound")
	}
	metadata, ok := ProjectBinding{snapshot: snapshot}.Relation(source, field)
	if !ok {
		return forwardRelationState{}, &query.Error{
			Category: query.CategoryField,
			Code:     query.CodeUnknownRelation,
			Field:    field,
			Detail:   "relation is not present on the source model",
		}
	}
	if !metadata.Cardinality.SingleValued() {
		return forwardRelationState{}, &query.Error{
			Category: query.CategoryField,
			Code:     query.CodeUnsupportedLookup,
			Field:    field,
			Detail:   "forward relation requires a single-valued cardinality",
		}
	}
	targetModel, ok := snapshot.models[metadata.Target]
	if !ok {
		return forwardRelationState{}, relationInvalidPlan("bound relation target model is missing")
	}
	primaryKey, ok := relationAutoPrimaryKey(targetModel)
	if !ok {
		return forwardRelationState{}, relationInvalidPlan("bound relation target does not have one AutoField primary key")
	}
	sourceField, ok := findField(sourceModel.Fields, metadata.Field)
	if !ok || sourceField.Relation == nil || sourceField.Column != metadata.Column ||
		sourceField.Relation.Target != metadata.Target {
		return forwardRelationState{}, relationInvalidPlan("bound source model disagrees with relation metadata")
	}
	return forwardRelationState{
		snapshot:         snapshot,
		sourceIdentity:   source,
		sourceModel:      sourceModel.Clone(),
		metadata:         metadata,
		targetModel:      targetModel.Clone(),
		targetPrimaryKey: primaryKey.Clone(),
	}, nil
}

func validateForwardState(state forwardRelationState) error {
	if state.snapshot == nil {
		return relationInvalidPlan("forward relation is unbound")
	}
	source, ok := state.snapshot.models[state.sourceIdentity]
	if !ok || !reflect.DeepEqual(source, state.sourceModel) {
		return relationInvalidPlan("forward relation source does not match its project snapshot")
	}
	target, ok := state.snapshot.models[state.metadata.Target]
	if !ok || !reflect.DeepEqual(target, state.targetModel) {
		return relationInvalidPlan("forward relation target does not match its project snapshot")
	}
	return nil
}

func (state forwardRelationState) path(terminal query.FieldRef) (query.RelationPath, error) {
	return query.NewForwardRelationPath(
		state.sourceIdentity,
		state.sourceModel.DBTable,
		state.metadata.Field,
		state.metadata.Column,
		state.metadata.Target,
		state.targetModel.DBTable,
		state.targetPrimaryKey.Column,
		state.metadata.Nullable,
		terminal, state.metadata.Cardinality,
	)
}

func matchingTerminalField(model ir.Model, reference query.FieldRef, kind ir.FieldKind) (ir.Field, bool) {
	field, ok := findField(model.Fields, reference.Name())
	if !ok || field.Kind != kind || !fieldReference(field).Equal(reference) {
		return ir.Field{}, false
	}
	return field, true
}

func relationAutoPrimaryKey(model ir.Model) (ir.Field, bool) {
	var result ir.Field
	found := false
	for _, field := range model.Fields {
		if !field.PrimaryKey {
			continue
		}
		if found || field.Kind != ir.FieldAuto || field.Nullable || field.Relation != nil {
			return ir.Field{}, false
		}
		result = field
		found = true
	}
	return result, found
}

func relationInvalidPlan(detail string) *query.Error {
	return &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan, Detail: detail}
}

func unknownRelatedField(field string) *query.Error {
	return &query.Error{
		Category: query.CategoryField,
		Code:     query.CodeUnknownRelatedField,
		Field:    field,
		Detail:   "field is not a supported scalar field on the related model",
	}
}
