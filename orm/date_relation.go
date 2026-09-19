package orm

import (
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type RelatedDateField[M any] struct {
	configurationErr error
	path             query.RelationPath
	valid            bool
	marker           [0]func(M)
}

func (relation ForwardRelation[S, T]) Date(field ReferenceField[T, calendar.Date]) (RelatedDateField[S], error) {
	if err := relation.route.validate(); err != nil {
		return RelatedDateField[S]{}, err
	}
	metadata, err := relatedScalarMetadata(relation.route.last().targetModel, field, true, ir.FieldDate)
	if err != nil {
		return RelatedDateField[S]{}, err
	}
	path, err := relation.route.path(fieldReference(metadata), query.RelationTerminalRelatedField)
	if err != nil {
		return RelatedDateField[S]{}, err
	}
	return RelatedDateField[S]{path: path, valid: true}, nil
}
func (relation ReverseRelation[Owner, Source]) Date(field DateField[Source]) (RelatedDateField[Owner], error) {
	if err := validateReverseRelationState(relation.state); err != nil {
		return RelatedDateField[Owner]{}, err
	}
	if field.err != nil {
		return RelatedDateField[Owner]{}, field.err
	}
	metadata, ok := matchingTerminalField(relation.state.forward.sourceModel, field.reference, ir.FieldDate)
	if !ok || metadata.Nullable {
		return RelatedDateField[Owner]{}, unknownRelatedField(field.reference.Name())
	}
	path, err := relation.state.path(fieldReference(metadata))
	if err != nil {
		return RelatedDateField[Owner]{}, err
	}
	return RelatedDateField[Owner]{path: path, valid: true}, nil
}
func (field RelatedDateField[M]) Exact(value calendar.Date) Predicate[M] {
	if field.configurationErr != nil {
		return Predicate[M]{err: field.configurationErr}
	}
	if !field.valid {
		return Predicate[M]{err: relationInvalidPlan("related date field is unbound")}
	}
	return predicateFromCondition[M](query.NewRelatedCondition(field.path, query.LookupExact, query.Date(value)), nil)
}

func (f RelatedDateField[M]) GreaterThan(value calendar.Date) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupGreaterThan, query.Date(value))
}

func (f RelatedDateField[M]) GreaterThanOrEqual(value calendar.Date) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupGreaterThanOrEqual, query.Date(value))
}

func (f RelatedDateField[M]) LessThan(value calendar.Date) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupLessThan, query.Date(value))
}

func (f RelatedDateField[M]) LessThanOrEqual(value calendar.Date) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupLessThanOrEqual, query.Date(value))
}

func (f RelatedDateField[M]) IsNull(value bool) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupIsNull, query.Boolean(value))
}

func (f RelatedDateField[M]) In(values ...calendar.Date) Predicate[M] {
	return relatedMembershipPredicate[M](f.path, f.valid, f.configurationErr, values, query.Date)
}
