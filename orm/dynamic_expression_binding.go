package orm

import "github.com/progresshans/godj/query"

// A relation name is resolved only through the sealed project snapshot whose
// root model agrees with this QuerySet. Neither table names nor SQL aliases are
// accepted as caller-provided binding authority.
func dynamicExpressionBinding[M any](source QuerySet[M], binding *BoundModel[M]) (dynamicBinding, error) {
	if source.configurationErr != nil {
		return dynamicBinding{}, source.configurationErr
	}
	if source.prepared == nil {
		return dynamicBinding{}, invalidResultBuilder("expression binding requires model metadata")
	}
	bound := dynamicBinding{model: source.prepared}
	if binding != nil {
		if err := validateMaterializationSource(source, *binding); err != nil {
			return dynamicBinding{}, err
		}
		model := *binding
		bound.related = func(name string) (query.ScalarExpression, error) {
			path, err := dynamicValueRelation(model, name)
			if err != nil {
				return query.ScalarExpression{}, err
			}
			return query.RelatedExpression(path)
		}
	}
	return bound, nil
}

func dynamicValueRelation[M any](model BoundModel[M], name string) (query.RelationPath, error) {
	predicate, err := parseRelationInput(model, nil, LookupInput{Key: name + "__isnull", Value: true})
	if err != nil {
		return query.RelationPath{}, err
	}
	condition, leaf := predicate.expression.Condition()
	if !leaf {
		return query.RelationPath{}, invalidResultBuilder("dynamic value did not resolve to one related field")
	}
	path, related := condition.RelationPath()
	if !related {
		return query.RelationPath{}, invalidResultBuilder("dynamic value has no bound relation route")
	}
	return path, nil
}
