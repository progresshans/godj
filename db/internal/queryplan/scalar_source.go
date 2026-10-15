package queryplan

import "github.com/progresshans/godj/query"

// ScalarSource keeps CASE predicates, root values and related values in the
// same prepared read. A write may supply Field to resolve its original row.
// No callback creates source authority from an expression's own leaves.
type ScalarSource struct {
	Plan  query.Plan
	Alias string
	Joins map[RelationKey]Join
	Field func(query.FieldRef) (string, error)
	// Aggregate binds value and CASE/FILTER routes to prepared aggregate joins.
	// It never changes the source plan or authorizes a row WHERE expression.
	Aggregate bool
}

func (source ScalarSource) Validate(value query.ScalarExpression) error {
	if source.Aggregate {
		return source.Plan.ValidateAggregateScalar(value)
	}
	return source.Plan.ValidateScalar(value)
}

func (source ScalarSource) ValidatePredicate(value query.Expression) error {
	if source.Aggregate {
		return source.Plan.ValidateAggregatePredicate(value)
	}
	return source.Plan.ValidatePredicate(value)
}

func (source ScalarSource) ResolveField(field query.FieldRef, quote func(string) (string, error), qualify func(string, string) (string, error)) (string, error) {
	if source.Field != nil {
		return source.Field(field)
	}
	if source.Alias == "" {
		return quote(field.Column())
	}
	return qualify(source.Alias, field.Column())
}

func (source ScalarSource) ResolveRelated(path query.RelationPath, qualify func(string, string) (string, error)) (string, error) {
	alias := source.Alias
	if key, joined := ConditionJoinKey(path); joined {
		join, present := source.Joins[key]
		if !present {
			return "", invalidPlan("scalar relation was not materialized in its source")
		}
		alias = join.Alias
	}
	return qualify(alias, path.Terminal().Column())
}

func (source ScalarSource) ResolveCondition(condition query.Condition, quote func(string) (string, error), qualify func(string, string) (string, error)) (string, error) {
	if path, related := condition.RelationPath(); related {
		return source.ResolveRelated(path, qualify)
	}
	return source.ResolveField(condition.Field(), quote, qualify)
}
