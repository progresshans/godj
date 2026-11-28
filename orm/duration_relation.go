package orm

import (
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type RelatedDurationField[M any] struct {
	configurationErr error
	path             query.RelationPath
	valid            bool
	marker           [0]func(M)
}

func (relation QueryRelation[S, T]) Duration(field ReferenceField[T, duration.Duration]) (RelatedDurationField[S], error) {
	if err := relation.route.validate(); err != nil {
		return RelatedDurationField[S]{}, err
	}
	metadata, err := relatedScalarMetadata(relation.route.last().targetModel, field, true, ir.FieldDuration)
	if err != nil {
		return RelatedDurationField[S]{}, err
	}
	path, err := relation.route.path(fieldReference(metadata), query.RelationTerminalRelatedField)
	if err != nil {
		return RelatedDurationField[S]{}, err
	}
	return RelatedDurationField[S]{path: path, valid: true}, nil
}

func (field RelatedDurationField[M]) Exact(value duration.Duration) Predicate[M] {
	if field.configurationErr != nil {
		return Predicate[M]{err: field.configurationErr}
	}
	if !field.valid {
		return Predicate[M]{err: relationInvalidPlan("related duration field is unbound")}
	}
	return predicateFromCondition[M](query.NewRelatedCondition(field.path, query.LookupExact, query.Duration(value)), nil)
}

func (f RelatedDurationField[M]) GreaterThan(value duration.Duration) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupGreaterThan, query.Duration(value))
}

func (f RelatedDurationField[M]) GreaterThanOrEqual(value duration.Duration) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupGreaterThanOrEqual, query.Duration(value))
}

func (f RelatedDurationField[M]) LessThan(value duration.Duration) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupLessThan, query.Duration(value))
}

func (f RelatedDurationField[M]) LessThanOrEqual(value duration.Duration) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupLessThanOrEqual, query.Duration(value))
}

func (f RelatedDurationField[M]) IsNull(value bool) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupIsNull, query.Boolean(value))
}

func (f RelatedDurationField[M]) In(values ...duration.Duration) Predicate[M] {
	return relatedMembershipPredicate[M](f.path, f.valid, f.configurationErr, values, query.Duration)
}
