package orm

import (
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"time"
)

// RelatedBooleanField retains the source model type after target validation.
type RelatedBooleanField[M any] struct {
	configurationErr error
	path             query.RelationPath
	valid            bool
	marker           [0]func(M)
}

func (r ForwardRelation[S, T]) Boolean(field BooleanLookupField[T]) (RelatedBooleanField[S], error) {
	if err := r.route.validate(); err != nil {
		return RelatedBooleanField[S]{}, err
	}
	if interfaceIsNil(field) {
		return RelatedBooleanField[S]{}, relationInvalidPlan("related Boolean field is nil")
	}
	var model T
	reference, err := field.booleanLookupField(model)
	if err != nil {
		return RelatedBooleanField[S]{}, err
	}
	metadata, found := matchingTerminalField(r.route.last().targetModel, reference, ir.FieldBoolean)
	if !found {
		return RelatedBooleanField[S]{}, unknownRelatedField(reference.Name())
	}
	path, err := r.route.path(fieldReference(metadata), query.RelationTerminalRelatedField)
	if err != nil {
		return RelatedBooleanField[S]{}, err
	}
	return RelatedBooleanField[S]{path: path, valid: true}, nil
}

func relatedLookupError(path query.RelationPath, valid bool, lookup query.Lookup) error {
	if !valid {
		return relationInvalidPlan("related scalar field is unbound")
	}
	if lookup != query.LookupExact {
		hops := path.Hops()
		if len(hops) == 0 || hops[0].Direction() != query.RelationForward {
			return unsupportedRelationLookup(path.Terminal().Name(), lookup, "non-exact scalar lookups require a forward relation route")
		}
	}
	return nil
}

func relatedScalarPredicate[M any](path query.RelationPath, valid bool, cause error, lookup query.Lookup, value query.Value) Predicate[M] {
	if cause != nil {
		return Predicate[M]{err: cause}
	}
	if err := relatedLookupError(path, valid, lookup); err != nil {
		return Predicate[M]{err: err}
	}
	return predicateFromCondition[M](query.NewRelatedCondition(path, lookup, value), nil)
}

func relatedMembershipPredicate[M, V any](path query.RelationPath, valid bool, cause error, values []V, convert func(V) query.Value) Predicate[M] {
	if cause != nil {
		return Predicate[M]{err: cause}
	}
	if err := relatedLookupError(path, valid, query.LookupIn); err != nil {
		return Predicate[M]{err: err}
	}
	items := make([]query.Value, len(values))
	for index, value := range values {
		items[index] = convert(value)
	}
	condition, err := query.NewRelatedInCondition(path, items)
	return predicateFromCondition[M](condition, err)
}

func (f RelatedBooleanField[M]) Exact(value bool) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupExact, query.Boolean(value))
}

func (f RelatedIntegerField[M]) GreaterThan(value int64) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupGreaterThan, query.Integer(value))
}

func (f RelatedIntegerField[M]) GreaterThanOrEqual(value int64) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupGreaterThanOrEqual, query.Integer(value))
}

func (f RelatedIntegerField[M]) LessThan(value int64) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupLessThan, query.Integer(value))
}

func (f RelatedIntegerField[M]) LessThanOrEqual(value int64) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupLessThanOrEqual, query.Integer(value))
}

func (f RelatedIntegerField[M]) IsNull(value bool) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupIsNull, query.Boolean(value))
}

func (f RelatedIntegerField[M]) In(values ...int64) Predicate[M] {
	return relatedMembershipPredicate[M](f.path, f.valid, f.configurationErr, values, query.Integer)
}

func (f RelatedStringField[M]) GreaterThan(value string) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupGreaterThan, query.String(value))
}

func (f RelatedStringField[M]) GreaterThanOrEqual(value string) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupGreaterThanOrEqual, query.String(value))
}

func (f RelatedStringField[M]) LessThan(value string) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupLessThan, query.String(value))
}

func (f RelatedStringField[M]) LessThanOrEqual(value string) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupLessThanOrEqual, query.String(value))
}

func (f RelatedStringField[M]) IsNull(value bool) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupIsNull, query.Boolean(value))
}

func (f RelatedStringField[M]) In(values ...string) Predicate[M] {
	return relatedMembershipPredicate[M](f.path, f.valid, f.configurationErr, values, query.String)
}

func (f RelatedDateTimeField[M]) GreaterThan(value time.Time) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupGreaterThan, query.DateTime(value))
}

func (f RelatedDateTimeField[M]) GreaterThanOrEqual(value time.Time) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupGreaterThanOrEqual, query.DateTime(value))
}

func (f RelatedDateTimeField[M]) LessThan(value time.Time) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupLessThan, query.DateTime(value))
}

func (f RelatedDateTimeField[M]) LessThanOrEqual(value time.Time) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupLessThanOrEqual, query.DateTime(value))
}

func (f RelatedDateTimeField[M]) IsNull(value bool) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupIsNull, query.Boolean(value))
}

func (f RelatedDateTimeField[M]) In(values ...time.Time) Predicate[M] {
	return relatedMembershipPredicate[M](f.path, f.valid, f.configurationErr, values, query.DateTime)
}

func (f RelatedBooleanField[M]) IsNull(value bool) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupIsNull, query.Boolean(value))
}

func (f RelatedBooleanField[M]) In(values ...bool) Predicate[M] {
	return relatedMembershipPredicate[M](f.path, f.valid, f.configurationErr, values, query.Boolean)
}

func (f RelatedStringField[M]) IContains(value string) Predicate[M] {
	return relatedScalarPredicate[M](f.path, f.valid, f.configurationErr, query.LookupIContains, query.String(value))
}
