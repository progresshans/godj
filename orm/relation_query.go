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
	}
	return BoundModel[M]{
		snapshot:         binding.snapshot,
		identity:         identity,
		model:            model.Clone(),
		objectDescriptor: objectDescriptor,
	}, nil
}

// ForwardRelation is a required one-hop many-to-one relation whose source and
// target descriptors have already been checked against one project snapshot.
type ForwardRelation[S, T any] struct {
	state        forwardRelationState
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

// BindForward resolves one required forward relation. It rejects independently
// built or zero snapshots before relation lookup so no partially valid path is
// published.
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
	state, err := resolveForwardRelation(source.snapshot, source.identity, source.model, field)
	if err != nil {
		return ForwardRelation[S, T]{}, err
	}
	if state.metadata.Target != target.identity || !reflect.DeepEqual(state.targetModel, target.model) {
		return ForwardRelation[S, T]{}, relationInvalidPlan("forward relation target does not match bound target model")
	}
	return ForwardRelation[S, T]{state: state}, nil
}

// RelatedIntegerField and RelatedStringField carry the source model type after
// the target model and terminal field have been validated.
type RelatedIntegerField[M any] struct {
	path   query.RelationPath
	valid  bool
	marker [0]func(M)
}

type RelatedStringField[M any] struct {
	path   query.RelationPath
	valid  bool
	marker [0]func(M)
}

func (r ForwardRelation[S, T]) Integer(field IntegerField[T]) (RelatedIntegerField[S], error) {
	if err := validateForwardState(r.state); err != nil {
		return RelatedIntegerField[S]{}, err
	}
	if field.err != nil {
		return RelatedIntegerField[S]{}, field.err
	}
	metadata, ok := matchingTerminalField(r.state.targetModel, field.reference, ir.FieldAuto)
	if !ok || metadata.Nullable {
		return RelatedIntegerField[S]{}, unknownRelatedField(field.reference.Name())
	}
	path, err := r.state.path(fieldReference(metadata))
	if err != nil {
		return RelatedIntegerField[S]{}, err
	}
	return RelatedIntegerField[S]{path: path, valid: true}, nil
}

func (r ForwardRelation[S, T]) String(field StringField[T]) (RelatedStringField[S], error) {
	if err := validateForwardState(r.state); err != nil {
		return RelatedStringField[S]{}, err
	}
	if field.err != nil {
		return RelatedStringField[S]{}, field.err
	}
	metadata, ok := matchingTerminalField(r.state.targetModel, field.reference, ir.FieldChar)
	if !ok || metadata.Nullable {
		return RelatedStringField[S]{}, unknownRelatedField(field.reference.Name())
	}
	path, err := r.state.path(fieldReference(metadata))
	if err != nil {
		return RelatedStringField[S]{}, err
	}
	return RelatedStringField[S]{path: path, valid: true}, nil
}

func (f RelatedIntegerField[M]) Exact(value int64) Predicate[M] {
	if !f.valid {
		return Predicate[M]{err: relationInvalidPlan("related integer field is unbound")}
	}
	return Predicate[M]{condition: query.NewRelatedCondition(f.path, query.LookupExact, query.Integer(value))}
}

func (f RelatedStringField[M]) Exact(value string) Predicate[M] {
	if !f.valid {
		return Predicate[M]{err: relationInvalidPlan("related string field is unbound")}
	}
	return Predicate[M]{condition: query.NewRelatedCondition(f.path, query.LookupExact, query.String(value))}
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

func resolveForwardRelation(
	snapshot *projectBindingSnapshot,
	source ir.ModelIdentity,
	sourceModel ir.Model,
	field string,
) (forwardRelationState, error) {
	state, err := resolveForwardRelationState(snapshot, source, sourceModel, field)
	if err != nil {
		return forwardRelationState{}, err
	}
	if state.metadata.Nullable {
		return forwardRelationState{}, &query.Error{
			Category: query.CategoryField,
			Code:     query.CodeUnsupportedLookup,
			Field:    field,
			Detail:   "nullable forward relation predicates are not supported",
		}
	}
	return state, nil
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
	if metadata.Cardinality != ir.RelationManyToOne {
		return forwardRelationState{}, &query.Error{
			Category: query.CategoryField,
			Code:     query.CodeUnsupportedLookup,
			Field:    field,
			Detail:   "only forward many-to-one relations are supported",
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
		terminal,
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
