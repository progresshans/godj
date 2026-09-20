package orm

import "github.com/progresshans/godj/query"

func resultOrdering[M any](expression query.ResultExpression, cause error, direction query.Direction) Ordering[M] {
	if cause != nil {
		return Ordering[M]{err: cause}
	}
	ordering, err := query.NewResultOrdering(expression, direction)
	return Ordering[M]{ordering: ordering, err: err}
}

func relatedOrdering[M any](path query.RelationPath, valid bool, cause error, direction query.Direction) Ordering[M] {
	expression, err := relatedResult(path, valid, cause)
	return resultOrdering[M](expression, err, direction)
}

func (f RelatedIntegerField[M]) Asc() Ordering[M] {
	return relatedOrdering[M](f.path, f.valid, f.configurationErr, query.Ascending)
}

func (f RelatedIntegerField[M]) Desc() Ordering[M] {
	return relatedOrdering[M](f.path, f.valid, f.configurationErr, query.Descending)
}

func (f RelatedStringField[M]) Asc() Ordering[M] {
	return relatedOrdering[M](f.path, f.valid, f.configurationErr, query.Ascending)
}

func (f RelatedStringField[M]) Desc() Ordering[M] {
	return relatedOrdering[M](f.path, f.valid, f.configurationErr, query.Descending)
}

func (f RelatedBooleanField[M]) Asc() Ordering[M] {
	return relatedOrdering[M](f.path, f.valid, f.configurationErr, query.Ascending)
}

func (f RelatedBooleanField[M]) Desc() Ordering[M] {
	return relatedOrdering[M](f.path, f.valid, f.configurationErr, query.Descending)
}

func (f RelatedFloatField[M]) Asc() Ordering[M] {
	return relatedOrdering[M](f.path, f.valid, f.configurationErr, query.Ascending)
}

func (f RelatedFloatField[M]) Desc() Ordering[M] {
	return relatedOrdering[M](f.path, f.valid, f.configurationErr, query.Descending)
}

func (f RelatedDecimalField[M]) Asc() Ordering[M] {
	return relatedOrdering[M](f.path, f.valid, f.configurationErr, query.Ascending)
}

func (f RelatedDecimalField[M]) Desc() Ordering[M] {
	return relatedOrdering[M](f.path, f.valid, f.configurationErr, query.Descending)
}

func (f RelatedDateTimeField[M]) Asc() Ordering[M] {
	return relatedOrdering[M](f.path, f.valid, f.configurationErr, query.Ascending)
}

func (f RelatedDateTimeField[M]) Desc() Ordering[M] {
	return relatedOrdering[M](f.path, f.valid, f.configurationErr, query.Descending)
}

func (f RelatedDateField[M]) Asc() Ordering[M] {
	return relatedOrdering[M](f.path, f.valid, f.configurationErr, query.Ascending)
}

func (f RelatedDateField[M]) Desc() Ordering[M] {
	return relatedOrdering[M](f.path, f.valid, f.configurationErr, query.Descending)
}

func (f RelatedTimeField[M]) Asc() Ordering[M] {
	return relatedOrdering[M](f.path, f.valid, f.configurationErr, query.Ascending)
}

func (f RelatedTimeField[M]) Desc() Ordering[M] {
	return relatedOrdering[M](f.path, f.valid, f.configurationErr, query.Descending)
}

func (f RelatedDurationField[M]) Asc() Ordering[M] {
	return relatedOrdering[M](f.path, f.valid, f.configurationErr, query.Ascending)
}

func (f RelatedDurationField[M]) Desc() Ordering[M] {
	return relatedOrdering[M](f.path, f.valid, f.configurationErr, query.Descending)
}

func (f RelatedUUIDField[M]) Asc() Ordering[M] {
	return relatedOrdering[M](f.path, f.valid, f.configurationErr, query.Ascending)
}

func (f RelatedUUIDField[M]) Desc() Ordering[M] {
	return relatedOrdering[M](f.path, f.valid, f.configurationErr, query.Descending)
}

func (f RelatedJSONField[M]) Asc() Ordering[M] {
	return relatedOrdering[M](f.path, f.valid, f.configurationErr, query.Ascending)
}

func (f RelatedJSONField[M]) Desc() Ordering[M] {
	return relatedOrdering[M](f.path, f.valid, f.configurationErr, query.Descending)
}
