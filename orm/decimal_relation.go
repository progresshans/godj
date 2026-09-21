package orm

import (
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type RelatedDecimalField[M any] struct {
	configurationErr error
	path             query.RelationPath
	valid            bool
	marker           [0]func(M)
}

func (relation ForwardRelation[S, T]) Decimal(field ReferenceField[T, decimal.Decimal]) (RelatedDecimalField[S], error) {
	if err := relation.route.validate(); err != nil {
		return RelatedDecimalField[S]{}, err
	}
	metadata, err := relatedScalarMetadata(relation.route.last().targetModel, field, true, ir.FieldDecimal)
	if err != nil {
		return RelatedDecimalField[S]{}, err
	}
	path, err := relation.route.path(fieldReference(metadata), query.RelationTerminalRelatedField)
	if err != nil {
		return RelatedDecimalField[S]{}, err
	}
	return RelatedDecimalField[S]{path: path, valid: true}, nil
}
func (relation ReverseRelation[Owner, Source]) Decimal(field ReferenceField[Source, decimal.Decimal]) (RelatedDecimalField[Owner], error) {
	if err := validateReverseRelationState(relation.state); err != nil {
		return RelatedDecimalField[Owner]{}, err
	}
	metadata, err := relatedScalarMetadata(relation.state.forward.sourceModel, field, relation.state.reverse.Cardinality == ir.RelationOneToOne, ir.FieldDecimal)
	if err != nil {
		return RelatedDecimalField[Owner]{}, err
	}
	path, err := relation.state.path(fieldReference(metadata))
	if err != nil {
		return RelatedDecimalField[Owner]{}, err
	}
	return RelatedDecimalField[Owner]{path: path, valid: true}, nil
}
func (field RelatedDecimalField[M]) Exact(value decimal.Decimal) Predicate[M] {
	if field.configurationErr != nil {
		return Predicate[M]{err: field.configurationErr}
	}
	if !field.valid {
		return Predicate[M]{err: relationInvalidPlan("related decimal field is unbound")}
	}
	return predicateFromCondition[M](query.NewRelatedCondition(field.path, query.LookupExact, query.Decimal(value)), nil)
}

func (f RelatedDecimalField[M]) GreaterThan(value decimal.Decimal) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupGreaterThan, query.Decimal(value))
}

func (f RelatedDecimalField[M]) GreaterThanOrEqual(value decimal.Decimal) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupGreaterThanOrEqual, query.Decimal(value))
}

func (f RelatedDecimalField[M]) LessThan(value decimal.Decimal) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupLessThan, query.Decimal(value))
}

func (f RelatedDecimalField[M]) LessThanOrEqual(value decimal.Decimal) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupLessThanOrEqual, query.Decimal(value))
}

func (f RelatedDecimalField[M]) IsNull(value bool) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupIsNull, query.Boolean(value))
}

func (f RelatedDecimalField[M]) In(values ...decimal.Decimal) Predicate[M] {
	return relatedMembershipPredicate[M](f.path, f.valid, f.configurationErr, values, query.Decimal)
}
