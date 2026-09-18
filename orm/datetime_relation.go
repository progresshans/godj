package orm

import (
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"time"
)

type RelatedDateTimeField[M any] struct {
	path   query.RelationPath
	valid  bool
	marker [0]func(M)
}

func (relation ForwardRelation[S, T]) DateTime(field DateTimeField[T]) (RelatedDateTimeField[S], error) {
	if err := validateForwardState(relation.state); err != nil {
		return RelatedDateTimeField[S]{}, err
	}
	if field.err != nil {
		return RelatedDateTimeField[S]{}, field.err
	}
	metadata, ok := matchingTerminalField(relation.state.targetModel, field.reference, ir.FieldDateTime)
	if !ok || metadata.Nullable {
		return RelatedDateTimeField[S]{}, unknownRelatedField(field.reference.Name())
	}
	path, err := relation.state.path(fieldReference(metadata))
	if err != nil {
		return RelatedDateTimeField[S]{}, err
	}
	return RelatedDateTimeField[S]{path: path, valid: true}, nil
}
func (relation ReverseRelation[Owner, Source]) DateTime(field DateTimeField[Source]) (RelatedDateTimeField[Owner], error) {
	if err := validateReverseRelationState(relation.state); err != nil {
		return RelatedDateTimeField[Owner]{}, err
	}
	if field.err != nil {
		return RelatedDateTimeField[Owner]{}, field.err
	}
	metadata, ok := matchingTerminalField(relation.state.forward.sourceModel, field.reference, ir.FieldDateTime)
	if !ok || metadata.Nullable {
		return RelatedDateTimeField[Owner]{}, unknownRelatedField(field.reference.Name())
	}
	path, err := relation.state.path(fieldReference(metadata))
	if err != nil {
		return RelatedDateTimeField[Owner]{}, err
	}
	return RelatedDateTimeField[Owner]{path: path, valid: true}, nil
}
func (field RelatedDateTimeField[M]) Exact(value time.Time) Predicate[M] {
	if !field.valid {
		return Predicate[M]{err: relationInvalidPlan("related datetime field is unbound")}
	}
	return predicateFromCondition[M](query.NewRelatedCondition(field.path, query.LookupExact, query.DateTime(value)), nil)
}
