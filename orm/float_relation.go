package orm

import (
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type RelatedFloatField[M any] struct {
	configurationErr error
	path             query.RelationPath
	valid            bool
	marker           [0]func(M)
}

func (relation ForwardRelation[S, T]) Float(field ReferenceField[T, float64]) (RelatedFloatField[S], error) {
	if err := relation.route.validate(); err != nil {
		return RelatedFloatField[S]{}, err
	}
	metadata, err := relatedScalarMetadata(relation.route.last().targetModel, field, true, ir.FieldFloat)
	if err != nil {
		return RelatedFloatField[S]{}, err
	}
	path, err := relation.route.path(fieldReference(metadata), query.RelationTerminalRelatedField)
	if err != nil {
		return RelatedFloatField[S]{}, err
	}
	return RelatedFloatField[S]{path: path, valid: true}, nil
}
func (relation ReverseRelation[Owner, Source]) Float(field ReferenceField[Source, float64]) (RelatedFloatField[Owner], error) {
	if err := validateReverseRelationState(relation.state); err != nil {
		return RelatedFloatField[Owner]{}, err
	}
	metadata, err := relatedScalarMetadata(relation.state.forward.sourceModel, field, relation.state.reverse.Cardinality == ir.RelationOneToOne, ir.FieldFloat)
	if err != nil {
		return RelatedFloatField[Owner]{}, err
	}
	path, err := relation.state.path(fieldReference(metadata))
	if err != nil {
		return RelatedFloatField[Owner]{}, err
	}
	return RelatedFloatField[Owner]{path: path, valid: true}, nil
}
func (field RelatedFloatField[M]) Exact(value float64) Predicate[M] {
	if field.configurationErr != nil {
		return Predicate[M]{err: field.configurationErr}
	}
	if !field.valid {
		return Predicate[M]{err: relationInvalidPlan("related float field is unbound")}
	}
	return predicateFromCondition[M](query.NewRelatedCondition(field.path, query.LookupExact, query.Float(value)), nil)
}

func (f RelatedFloatField[M]) GreaterThan(value float64) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupGreaterThan, query.Float(value))
}

func (f RelatedFloatField[M]) GreaterThanOrEqual(value float64) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupGreaterThanOrEqual, query.Float(value))
}

func (f RelatedFloatField[M]) LessThan(value float64) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupLessThan, query.Float(value))
}

func (f RelatedFloatField[M]) LessThanOrEqual(value float64) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupLessThanOrEqual, query.Float(value))
}

func (f RelatedFloatField[M]) IsNull(value bool) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupIsNull, query.Boolean(value))
}

func (f RelatedFloatField[M]) In(values ...float64) Predicate[M] {
	return relatedMembershipPredicate[M](f.path, f.valid, f.configurationErr, values, query.Float)
}
