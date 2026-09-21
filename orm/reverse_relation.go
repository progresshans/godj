package orm

import (
	"reflect"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// ReverseRelation is a query-only, one-hop reverse ForeignKey relation. It
// deliberately has no owner-instance or primary-key requirement.
type ReverseRelation[Owner, Source any] struct {
	state        reverseRelationState
	ownerMarker  [0]func(Owner)
	sourceMarker [0]func(Source)
}

type reverseRelationState struct {
	snapshot *projectBindingSnapshot
	owner    ir.ModelIdentity
	reverse  ReverseRelationMetadata
	forward  forwardRelationState
	valid    bool
}

// BindReverse resolves a named reverse namespace and reconstructs its
// canonical physical ForeignKey declaration from the same project snapshot.
func BindReverse[Owner, Source any](
	owner BoundModel[Owner],
	reverseName string,
	source BoundModel[Source],
) (ReverseRelation[Owner, Source], error) {
	state, err := bindReverseRelationState(owner, reverseName, source)
	if err != nil {
		return ReverseRelation[Owner, Source]{}, err
	}
	return ReverseRelation[Owner, Source]{state: state}, nil
}

func bindReverseRelationState[Owner, Source any](
	owner BoundModel[Owner],
	reverseName string,
	source BoundModel[Source],
) (reverseRelationState, error) {
	if err := validateBoundModel(owner); err != nil {
		return reverseRelationState{}, err
	}
	if err := validateBoundModel(source); err != nil {
		return reverseRelationState{}, err
	}
	if owner.snapshot != source.snapshot {
		return reverseRelationState{}, relationInvalidPlan("owner and source models belong to different project snapshots")
	}

	state, err := resolveReverseRelationState(owner.snapshot, owner.identity, owner.model, reverseName)
	if err != nil {
		return reverseRelationState{}, err
	}
	if state.reverse.Target != source.identity || !reflect.DeepEqual(state.forward.sourceModel, source.model) {
		return reverseRelationState{}, relationInvalidPlan("reverse relation source does not match bound source model")
	}
	return state, nil
}

func resolveReverseRelationState(
	snapshot *projectBindingSnapshot,
	owner ir.ModelIdentity,
	ownerModel ir.Model,
	reverseName string,
) (reverseRelationState, error) {
	if snapshot == nil {
		return reverseRelationState{}, relationInvalidPlan("project binding is unbound")
	}
	reverse, ok := findReverseRelation(snapshot, owner, reverseName)
	if !ok {
		return reverseRelationState{}, &query.Error{
			Category: query.CategoryField,
			Code:     query.CodeUnknownRelation,
			Field:    reverseName,
			Detail:   "reverse relation is not present on the owner model",
		}
	}
	if (reverse.Cardinality != ir.RelationOneToMany && reverse.Cardinality != ir.RelationOneToOne) || reverse.Owner != owner {
		return reverseRelationState{}, &query.Error{
			Category: query.CategoryField,
			Code:     query.CodeUnsupportedLookup,
			Field:    reverseName,
			Detail:   "reverse relation requires a named one-to-many or one-to-one edge",
		}
	}
	sourceModel, ok := snapshot.models[reverse.Target]
	if !ok {
		return reverseRelationState{}, relationInvalidPlan("reverse relation source model is missing")
	}
	forward, err := resolveForwardRelationState(snapshot, reverse.Target, sourceModel, reverse.SourceField)
	if err != nil {
		return reverseRelationState{}, relationInvalidPlan("reverse relation declaration cannot be reconstructed")
	}
	wantForward := ir.RelationManyToOne
	if reverse.Cardinality == ir.RelationOneToOne {
		wantForward = ir.RelationOneToOne
	}
	if forward.metadata.Target != owner ||
		forward.metadata.Cardinality != wantForward ||
		forward.metadata.Reverse.Disabled ||
		forward.metadata.Reverse.Name != reverseName ||
		!reflect.DeepEqual(forward.targetModel, ownerModel) {
		return reverseRelationState{}, relationInvalidPlan("reverse namespace disagrees with its forward declaration")
	}
	return reverseRelationState{
		snapshot: snapshot,
		owner:    owner,
		reverse:  reverse,
		forward:  forward,
		valid:    true,
	}, nil
}

func findReverseRelation(
	snapshot *projectBindingSnapshot,
	owner ir.ModelIdentity,
	name string,
) (ReverseRelationMetadata, bool) {
	if snapshot == nil {
		return ReverseRelationMetadata{}, false
	}
	for _, relation := range snapshot.reverse {
		if relation.Owner == owner && relation.Name == name {
			return relation, true
		}
	}
	return ReverseRelationMetadata{}, false
}

func validateReverseRelationState(state reverseRelationState) error {
	if !state.valid || state.snapshot == nil {
		return relationInvalidPlan("reverse relation is unbound")
	}
	ownerModel, ok := state.snapshot.models[state.owner]
	if !ok || !reflect.DeepEqual(ownerModel, state.forward.targetModel) {
		return relationInvalidPlan("reverse relation owner does not match its project snapshot")
	}
	reverse, ok := findReverseRelation(state.snapshot, state.owner, state.reverse.Name)
	if !ok || reverse != state.reverse {
		return relationInvalidPlan("reverse relation namespace does not match its project snapshot")
	}
	if err := validateForwardState(state.forward); err != nil {
		return err
	}
	return nil
}

func (state reverseRelationState) path(terminal query.FieldRef) (query.RelationPath, error) {
	return query.NewReverseRelationPath(
		state.forward.sourceIdentity,
		state.forward.sourceModel.DBTable,
		state.forward.metadata.Field,
		state.forward.metadata.Column,
		state.owner,
		state.forward.targetModel.DBTable,
		state.forward.targetPrimaryKey.Column,
		state.reverse.Name,
		state.forward.metadata.Nullable,
		terminal, state.reverse.Cardinality,
	)
}

func (r ReverseRelation[Owner, Source]) Integer(
	field ReferenceField[Source, int64],
) (RelatedIntegerField[Owner], error) {
	if err := validateReverseRelationState(r.state); err != nil {
		return RelatedIntegerField[Owner]{}, err
	}
	metadata, err := relatedScalarMetadata(r.state.forward.sourceModel, field, r.state.reverse.Cardinality == ir.RelationOneToOne, ir.FieldAuto, ir.FieldInteger)
	if err != nil {
		return RelatedIntegerField[Owner]{}, err
	}
	path, err := r.state.path(fieldReference(metadata))
	if err != nil {
		return RelatedIntegerField[Owner]{}, err
	}
	return RelatedIntegerField[Owner]{path: path, valid: true}, nil
}

func (r ReverseRelation[Owner, Source]) String(
	field ReferenceField[Source, string],
) (RelatedStringField[Owner], error) {
	if err := validateReverseRelationState(r.state); err != nil {
		return RelatedStringField[Owner]{}, err
	}
	metadata, err := relatedScalarMetadata(r.state.forward.sourceModel, field, r.state.reverse.Cardinality == ir.RelationOneToOne, ir.FieldChar, ir.FieldText)
	if err != nil {
		return RelatedStringField[Owner]{}, err
	}
	path, err := r.state.path(fieldReference(metadata))
	if err != nil {
		return RelatedStringField[Owner]{}, err
	}
	return RelatedStringField[Owner]{path: path, valid: true}, nil
}

// IsNull tests whether a OneToOne child exists, independently of its nullable
// fields. Collection absence has different query semantics and is not admitted.
func (r ReverseRelation[Owner, Source]) IsNull(value bool) Predicate[Owner] {
	if err := validateReverseRelationState(r.state); err != nil {
		return Predicate[Owner]{err: err}
	}
	_, path, err := r.state.presencePath()
	if err != nil {
		return Predicate[Owner]{err: err}
	}
	return predicateFromCondition[Owner](query.NewRelatedCondition(path, query.LookupIsNull, query.Boolean(value)), nil)
}

func (state reverseRelationState) presencePath() (ir.Field, query.RelationPath, error) {
	if state.reverse.Cardinality != ir.RelationOneToOne {
		return ir.Field{}, query.RelationPath{}, unsupportedRelationLookup(state.reverse.Name, query.LookupIsNull, "reverse presence requires a one-to-one edge")
	}
	key, ok := relationAutoPrimaryKey(state.forward.sourceModel)
	if !ok {
		return ir.Field{}, query.RelationPath{}, relationInvalidPlan("reverse presence requires a canonical child primary key")
	}
	path, err := state.path(fieldReference(key))
	return key, path, err
}

func (r ReverseRelation[Owner, Source]) Boolean(field BooleanLookupField[Source]) (RelatedBooleanField[Owner], error) {
	if err := validateReverseRelationState(r.state); err != nil {
		return RelatedBooleanField[Owner]{}, err
	}
	if r.state.reverse.Cardinality != ir.RelationOneToOne {
		return RelatedBooleanField[Owner]{}, unsupportedRelationLookup(r.state.reverse.Name, query.LookupExact, "reverse Boolean fields require a one-to-one edge")
	}
	if interfaceIsNil(field) {
		return RelatedBooleanField[Owner]{}, relationInvalidPlan("related Boolean field is nil")
	}
	var source Source
	reference, err := field.booleanLookupField(source)
	if err != nil {
		return RelatedBooleanField[Owner]{}, err
	}
	metadata, found := matchingTerminalField(r.state.forward.sourceModel, reference, ir.FieldBoolean)
	if !found {
		return RelatedBooleanField[Owner]{}, unknownRelatedField(reference.Name())
	}
	path, err := r.state.path(fieldReference(metadata))
	if err != nil {
		return RelatedBooleanField[Owner]{}, err
	}
	return RelatedBooleanField[Owner]{path: path, valid: true}, nil
}
