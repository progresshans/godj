package orm

import "github.com/progresshans/godj/query"

// CountField is the sealed model-specific capability for counting values.
// A collection field can be counted without becoming a scalar row selection.
type CountField[M any] interface {
	countAggregateField(M) (query.ResultExpression, error)
}

func aggregateFieldResult(kind query.ResultExpressionKind, field query.FieldRef, cause error) (query.ResultExpression, error) {
	if cause != nil {
		return query.ResultExpression{}, cause
	}
	return query.AggregateResult(kind, query.FieldResult(field))
}

func relatedAggregateResult(kind query.ResultExpressionKind, path query.RelationPath, valid bool, cause error) (query.ResultExpression, error) {
	if cause != nil {
		return query.ResultExpression{}, cause
	}
	if !valid {
		return query.ResultExpression{}, relationInvalidPlan("related aggregate field is unbound")
	}
	return query.RelatedAggregateResult(kind, path)
}

func (f field[M]) countAggregateField(M) (query.ResultExpression, error) {
	return aggregateFieldResult(query.ResultCount, f.reference, f.err)
}

func countScalarResult[M, V any](field ScalarField[M, V]) (query.ResultExpression, error) {
	value, _, err := scalarResult(field)
	if err != nil {
		return query.ResultExpression{}, err
	}
	return query.CountResult(value)
}

func (value ScalarExpression[M, V]) countAggregateField(M) (query.ResultExpression, error) {
	if value.err != nil {
		return query.ResultExpression{}, value.err
	}
	return query.ScalarAggregateResult(query.ResultCount, value.expression)
}

func (value FieldReference[M, V]) countAggregateField(M) (query.ResultExpression, error) {
	return countScalarResult(value)
}

func (value ResultColumn[M, V]) countAggregateField(M) (query.ResultExpression, error) {
	return countScalarResult(value)
}

func (value DynamicScalar[M]) countAggregateField(M) (query.ResultExpression, error) {
	return countScalarResult(value)
}

func (value JSONPathField[M]) countAggregateField(M) (query.ResultExpression, error) {
	return countScalarResult(value)
}
