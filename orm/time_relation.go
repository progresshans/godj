package orm

import (
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type RelatedTimeField[M any] struct {
	configurationErr error
	path             query.RelationPath
	valid            bool
	marker           [0]func(M)
}

func (relation ForwardRelation[S, T]) Time(field ReferenceField[T, clock.Time]) (RelatedTimeField[S], error) {
	if err := relation.route.validate(); err != nil {
		return RelatedTimeField[S]{}, err
	}
	metadata, err := relatedScalarMetadata(relation.route.last().targetModel, field, true, ir.FieldTime)
	if err != nil {
		return RelatedTimeField[S]{}, err
	}
	path, err := relation.route.path(fieldReference(metadata), query.RelationTerminalRelatedField)
	if err != nil {
		return RelatedTimeField[S]{}, err
	}
	return RelatedTimeField[S]{path: path, valid: true}, nil
}
func (relation ReverseRelation[Owner, Source]) Time(field ReferenceField[Source, clock.Time]) (RelatedTimeField[Owner], error) {
	if err := validateReverseRelationState(relation.state); err != nil {
		return RelatedTimeField[Owner]{}, err
	}
	metadata, err := relatedScalarMetadata(relation.state.forward.sourceModel, field, relation.state.reverse.Cardinality == ir.RelationOneToOne, ir.FieldTime)
	if err != nil {
		return RelatedTimeField[Owner]{}, err
	}
	path, err := relation.state.path(fieldReference(metadata))
	if err != nil {
		return RelatedTimeField[Owner]{}, err
	}
	return RelatedTimeField[Owner]{path: path, valid: true}, nil
}
func (field RelatedTimeField[M]) Exact(value clock.Time) Predicate[M] {
	if field.configurationErr != nil {
		return Predicate[M]{err: field.configurationErr}
	}
	if !field.valid {
		return Predicate[M]{err: relationInvalidPlan("related time field is unbound")}
	}
	return predicateFromCondition[M](query.NewRelatedCondition(field.path, query.LookupExact, query.Time(value)), nil)
}

func (f RelatedTimeField[M]) GreaterThan(value clock.Time) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupGreaterThan, query.Time(value))
}

func (f RelatedTimeField[M]) GreaterThanOrEqual(value clock.Time) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupGreaterThanOrEqual, query.Time(value))
}

func (f RelatedTimeField[M]) LessThan(value clock.Time) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupLessThan, query.Time(value))
}

func (f RelatedTimeField[M]) LessThanOrEqual(value clock.Time) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupLessThanOrEqual, query.Time(value))
}

func (f RelatedTimeField[M]) IsNull(value bool) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupIsNull, query.Boolean(value))
}

func (f RelatedTimeField[M]) In(values ...clock.Time) Predicate[M] {
	return relatedMembershipPredicate[M](f.path, f.valid, f.configurationErr, values, query.Time)
}
