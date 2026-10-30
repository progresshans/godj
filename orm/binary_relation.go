package orm

import (
	"github.com/progresshans/godj/binaryvalue"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type RelatedBinaryField[M any] struct {
	configurationErr error
	path             query.RelationPath
	valid            bool
	marker           [0]func(M)
}

func (relation QueryRelation[S, T]) Binary(field ReferenceField[T, binaryvalue.Value]) (RelatedBinaryField[S], error) {
	if err := relation.route.validate(); err != nil {
		return RelatedBinaryField[S]{}, err
	}
	metadata, err := relatedScalarMetadata(relation.route.last().targetModel, field, true, ir.FieldBinary)
	if err != nil {
		return RelatedBinaryField[S]{}, err
	}
	path, err := relation.route.path(fieldReference(metadata), query.RelationTerminalRelatedField)
	if err != nil {
		return RelatedBinaryField[S]{}, err
	}
	return RelatedBinaryField[S]{path: path, valid: true}, nil
}

func (field RelatedBinaryField[M]) Exact(value binaryvalue.Value) Predicate[M] {
	if field.configurationErr != nil {
		return Predicate[M]{err: field.configurationErr}
	}
	if !field.valid {
		return Predicate[M]{err: relationInvalidPlan("related Binary field is unbound")}
	}
	return predicateFromCondition[M](query.NewRelatedCondition(field.path, query.LookupExact, query.Binary(value)), nil)
}

func (f RelatedBinaryField[M]) GreaterThan(value binaryvalue.Value) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupGreaterThan, query.Binary(value))
}

func (f RelatedBinaryField[M]) GreaterThanOrEqual(value binaryvalue.Value) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupGreaterThanOrEqual, query.Binary(value))
}

func (f RelatedBinaryField[M]) LessThan(value binaryvalue.Value) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupLessThan, query.Binary(value))
}

func (f RelatedBinaryField[M]) LessThanOrEqual(value binaryvalue.Value) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupLessThanOrEqual, query.Binary(value))
}

func (f RelatedBinaryField[M]) IsNull(value bool) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupIsNull, query.Boolean(value))
}

func (f RelatedBinaryField[M]) In(values ...binaryvalue.Value) Predicate[M] {
	return relatedMembershipPredicate[M](f.path, f.valid, f.configurationErr, values, query.Binary)
}
