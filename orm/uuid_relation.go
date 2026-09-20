package orm

import (
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/uuid"
)

type RelatedUUIDField[M any] struct {
	configurationErr error
	path             query.RelationPath
	valid            bool
	marker           [0]func(M)
}

func (relation ForwardRelation[S, T]) UUID(field ReferenceField[T, uuid.UUID]) (RelatedUUIDField[S], error) {
	if err := relation.route.validate(); err != nil {
		return RelatedUUIDField[S]{}, err
	}
	metadata, err := relatedScalarMetadata(relation.route.last().targetModel, field, true, ir.FieldUUID)
	if err != nil {
		return RelatedUUIDField[S]{}, err
	}
	path, err := relation.route.path(fieldReference(metadata), query.RelationTerminalRelatedField)
	if err != nil {
		return RelatedUUIDField[S]{}, err
	}
	return RelatedUUIDField[S]{path: path, valid: true}, nil
}
func (relation ReverseRelation[Owner, Source]) UUID(field UUIDField[Source]) (RelatedUUIDField[Owner], error) {
	if err := validateReverseRelationState(relation.state); err != nil {
		return RelatedUUIDField[Owner]{}, err
	}
	if field.err != nil {
		return RelatedUUIDField[Owner]{}, field.err
	}
	metadata, ok := matchingTerminalField(relation.state.forward.sourceModel, field.reference, ir.FieldUUID)
	if !ok || metadata.Nullable {
		return RelatedUUIDField[Owner]{}, unknownRelatedField(field.reference.Name())
	}
	path, err := relation.state.path(fieldReference(metadata))
	if err != nil {
		return RelatedUUIDField[Owner]{}, err
	}
	return RelatedUUIDField[Owner]{path: path, valid: true}, nil
}
func (field RelatedUUIDField[M]) Exact(value uuid.UUID) Predicate[M] {
	if field.configurationErr != nil {
		return Predicate[M]{err: field.configurationErr}
	}
	if !field.valid {
		return Predicate[M]{err: relationInvalidPlan("related UUID field is unbound")}
	}
	return predicateFromCondition[M](query.NewRelatedCondition(field.path, query.LookupExact, query.UUID(value)), nil)
}

func (f RelatedUUIDField[M]) GreaterThan(value uuid.UUID) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupGreaterThan, query.UUID(value))
}

func (f RelatedUUIDField[M]) GreaterThanOrEqual(value uuid.UUID) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupGreaterThanOrEqual, query.UUID(value))
}

func (f RelatedUUIDField[M]) LessThan(value uuid.UUID) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupLessThan, query.UUID(value))
}

func (f RelatedUUIDField[M]) LessThanOrEqual(value uuid.UUID) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupLessThanOrEqual, query.UUID(value))
}

func (f RelatedUUIDField[M]) IsNull(value bool) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupIsNull, query.Boolean(value))
}

func (f RelatedUUIDField[M]) In(values ...uuid.UUID) Predicate[M] {
	return relatedMembershipPredicate[M](f.path, f.valid, f.configurationErr, values, query.UUID)
}
