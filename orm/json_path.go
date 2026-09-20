package orm

import (
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/query"
)

// JSONPathField exposes predicates on one literal JSON path. It deliberately
// has no write, order, F-reference or projection methods: missing paths have
// a different result domain from a whole model field.
type JSONPathField[M any] struct {
	field    query.FieldRef
	relation *query.RelationPath
	path     query.JSONPath
	err      error
	marker   [0]func(M)
}

func (f jsonField[M]) At(segments ...query.JSONPathSegment) JSONPathField[M] {
	path, err := query.NewJSONPath(segments...)
	if f.err != nil {
		err = f.err
	}
	return JSONPathField[M]{field: f.reference, path: path, err: err}
}

func (f RelatedJSONField[M]) At(segments ...query.JSONPathSegment) JSONPathField[M] {
	path, err := query.NewJSONPath(segments...)
	if cause := relatedLookupError(f.path, f.valid, query.LookupExact); cause != nil {
		err = cause
	}
	if f.configurationErr != nil {
		err = f.configurationErr
	}
	return JSONPathField[M]{field: f.path.Terminal(), relation: &f.path, path: path, err: err}
}

func (f JSONPathField[M]) Exact(value jsonvalue.Value) Predicate[M] {
	return f.predicate(query.LookupExact, query.JSON(value), nil)
}

// IsNull matches a missing path (including absent/wrong-type containers), not
// a stored JSON null. Exact(jsonvalue.Null()) matches the latter.
func (f JSONPathField[M]) IsNull(value bool) Predicate[M] {
	return f.predicate(query.LookupIsNull, query.Boolean(value), nil)
}

func (f JSONPathField[M]) In(values ...jsonvalue.Value) Predicate[M] {
	items := make([]query.Value, len(values))
	for i, value := range values {
		items[i] = query.JSON(value)
	}
	return f.predicate(query.LookupIn, query.Value{}, items)
}

func (f JSONPathField[M]) predicate(lookup query.Lookup, value query.Value, values []query.Value) Predicate[M] {
	if f.err != nil {
		return Predicate[M]{err: f.err}
	}
	var condition query.Condition
	var err error
	if f.relation != nil {
		if err = relatedLookupError(*f.relation, true, lookup); err != nil {
			return Predicate[M]{err: err}
		}
		if lookup == query.LookupIn {
			condition, err = query.NewRelatedInCondition(*f.relation, values)
		} else {
			condition = query.NewRelatedCondition(*f.relation, lookup, value)
		}
	} else if lookup == query.LookupIn {
		condition, err = query.NewInCondition(f.field, values)
	} else {
		condition = query.NewCondition(f.field, lookup, value)
	}
	if err == nil {
		condition, err = condition.WithJSONPath(f.path)
	}
	return predicateFromCondition[M](condition, err)
}

func dynamicJSONPath(condition query.Condition, segments []query.JSONPathSegment) (query.Condition, error) {
	if segments == nil {
		return condition, nil
	}
	path, err := query.NewJSONPath(segments...)
	if err != nil {
		return query.Condition{}, err
	}
	return condition.WithJSONPath(path)
}
