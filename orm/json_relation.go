package orm

import (
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type RelatedJSONField[M any] struct {
	configurationErr error
	path             query.RelationPath
	valid            bool
	marker           [0]func(M)
}

func (relation ForwardRelation[S, T]) JSON(field ReferenceField[T, jsonvalue.Value]) (RelatedJSONField[S], error) {
	if err := relation.route.validate(); err != nil {
		return RelatedJSONField[S]{}, err
	}
	metadata, err := relatedScalarMetadata(relation.route.last().targetModel, field, true, ir.FieldJSON)
	if err != nil {
		return RelatedJSONField[S]{}, err
	}
	path, err := relation.route.path(fieldReference(metadata), query.RelationTerminalRelatedField)
	if err != nil {
		return RelatedJSONField[S]{}, err
	}
	return RelatedJSONField[S]{path: path, valid: true}, nil
}
func (relation ReverseRelation[Owner, Source]) JSON(field JSONField[Source]) (RelatedJSONField[Owner], error) {
	if err := validateReverseRelationState(relation.state); err != nil {
		return RelatedJSONField[Owner]{}, err
	}
	if field.err != nil {
		return RelatedJSONField[Owner]{}, field.err
	}
	metadata, ok := matchingTerminalField(relation.state.forward.sourceModel, field.reference, ir.FieldJSON)
	if !ok || metadata.Nullable {
		return RelatedJSONField[Owner]{}, unknownRelatedField(field.reference.Name())
	}
	path, err := relation.state.path(fieldReference(metadata))
	if err != nil {
		return RelatedJSONField[Owner]{}, err
	}
	return RelatedJSONField[Owner]{path: path, valid: true}, nil
}
func (field RelatedJSONField[M]) Exact(value jsonvalue.Value) Predicate[M] {
	if field.configurationErr != nil {
		return Predicate[M]{err: field.configurationErr}
	}
	if !field.valid {
		return Predicate[M]{err: relationInvalidPlan("related JSON field is unbound")}
	}
	return predicateFromCondition[M](query.NewRelatedCondition(field.path, query.LookupExact, query.JSON(value)), nil)
}

func (f RelatedJSONField[M]) IsNull(value bool) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupIsNull, query.Boolean(value))
}

func (f RelatedJSONField[M]) Contains(value jsonvalue.Value) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupContains, query.JSON(value))
}

func (f RelatedJSONField[M]) ContainedBy(value jsonvalue.Value) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupContainedBy, query.JSON(value))
}

func (f RelatedJSONField[M]) In(values ...jsonvalue.Value) Predicate[M] {
	return relatedMembershipPredicate[M](f.path, f.valid, f.configurationErr, values, query.JSON)
}

func (field RelatedJSONField[M]) WithConfigurationError(err error) RelatedJSONField[M] {
	if field.configurationErr == nil {
		field.configurationErr = err
	}
	return field
}

// JSON comparisons preserve the backend-specific whole-document and path
// comparison rules; they do not grant MIN/MAX or ordered F capabilities.
func (f RelatedJSONField[M]) GreaterThan(value jsonvalue.Value) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupGreaterThan, query.JSON(value))
}
func (f RelatedJSONField[M]) GreaterThanOrEqual(value jsonvalue.Value) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupGreaterThanOrEqual, query.JSON(value))
}
func (f RelatedJSONField[M]) LessThan(value jsonvalue.Value) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupLessThan, query.JSON(value))
}
func (f RelatedJSONField[M]) LessThanOrEqual(value jsonvalue.Value) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupLessThanOrEqual, query.JSON(value))
}
