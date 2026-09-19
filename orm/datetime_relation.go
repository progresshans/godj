package orm

import (
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"time"
)

type RelatedDateTimeField[M any] struct {
	configurationErr error
	path             query.RelationPath
	valid            bool
	marker           [0]func(M)
}

func (relation ForwardRelation[S, T]) DateTime(field ReferenceField[T, time.Time]) (RelatedDateTimeField[S], error) {
	if err := relation.route.validate(); err != nil {
		return RelatedDateTimeField[S]{}, err
	}
	metadata, err := relatedScalarMetadata(relation.route.last().targetModel, field, true, ir.FieldDateTime)
	if err != nil {
		return RelatedDateTimeField[S]{}, err
	}
	path, err := relation.route.path(fieldReference(metadata), query.RelationTerminalRelatedField)
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
	if field.configurationErr != nil {
		return Predicate[M]{err: field.configurationErr}
	}
	if !field.valid {
		return Predicate[M]{err: relationInvalidPlan("related datetime field is unbound")}
	}
	return predicateFromCondition[M](query.NewRelatedCondition(field.path, query.LookupExact, query.DateTime(value)), nil)
}
