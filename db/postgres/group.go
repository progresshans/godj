package postgres

import (
	"strconv"
	"strings"

	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/query"
)

func compileGrouped(schema string, plan query.Plan) (string, []any, error) {
	if plan.ResultShape().Kind() == query.ResultAggregate {
		_, limited := plan.Limit()
		_, offset := plan.Offset()
		if limited || offset || plan.Distinct() || len(plan.RelationProjections()) != 0 {
			return "", nil, unsupportedResultShape("filtered or related aggregate requires an unsliced, non-distinct scalar source")
		}
	}
	prepared, err := queryplan.PrepareJoins(plan, "PostgreSQL")
	if err != nil {
		return "", nil, err
	}
	where, err := analyzeWhere(plan, plan.SourceFields())
	if err != nil {
		return "", nil, err
	}
	if err := bindGroupedExists(schema, plan, &where, prepared); err != nil {
		return "", nil, err
	}
	var statement strings.Builder
	arguments := make([]any, 0)
	statement.WriteString("SELECT ")
	for index, expression := range plan.ResultShape().Expressions() {
		if index > 0 {
			statement.WriteString(", ")
		}
		if err := appendGroupedValue(&statement, expression, plan, prepared, &arguments); err != nil {
			return "", nil, err
		}
		statement.WriteString(` AS "c` + strconv.Itoa(index) + `"`)
	}
	if err := queryplan.AppendGroupSource(&statement, plan, prepared, func(table string) (string, error) { return quoteTable(schema, table) }, quoteIdentifier, quoteQualified); err != nil {
		return "", nil, err
	}
	arguments, err = appendWhere(&statement, where, groupedFieldResolver(prepared), groupedRHSField, arguments)
	if err != nil {
		return "", nil, err
	}
	if keys := plan.ResultShape().GroupKeys(); len(keys) > 0 {
		statement.WriteString(" GROUP BY ")
		for index := range keys {
			if index > 0 {
				statement.WriteString(", ")
			}
			statement.WriteString(strconv.Itoa(index + 1))
		}
	}
	result, err := queryplan.FinishGroups(statement.String(), plan, func(value query.Value) (string, error) {
		argument, err := postgresValue(value)
		if err != nil {
			return "", err
		}
		arguments = append(arguments, argument)
		return placeholder(len(arguments)), nil
	}, func(sql *strings.Builder) error { appendPagination(sql, &arguments, plan); return nil })
	if err != nil {
		return "", nil, err
	}
	if len(arguments) > 65535 || len(result) > 4<<20 {
		return "", nil, unsupportedResultShape("group query exceeds PostgreSQL statement or parameter budget")
	}
	return result, arguments, nil
}

func appendGroupedValue(statement *strings.Builder, expression query.ResultExpression, plan query.Plan, prepared queryplan.Joins, arguments *[]any) error {
	if !expression.IsAggregate() {
		return appendResultValue(statement, expression, "t0", prepared.ByKey, arguments, false)
	}
	var operand, filterSQL strings.Builder
	if expression.Kind() != query.ResultCountAll {
		if err := appendResultValue(&operand, expression, "t0", prepared.ByKey, arguments, true); err != nil {
			return err
		}
	}
	if filter, present := expression.Filter(); present {
		filterPlan, err := query.NewPlan(plan.Table(), plan.SourceFields()).WithWhere(filter)
		if err != nil {
			return err
		}
		analysis, err := analyzeWhere(filterPlan, plan.SourceFields())
		if err != nil {
			return err
		}
		filterSQL.WriteString(" FILTER (WHERE ")
		leaves := analysis.leaves
		if err := appendWhereExpression(&filterSQL, analysis.expression, &leaves, groupedFieldResolver(prepared), groupedRHSField, arguments, false); err != nil {
			return err
		}
		if len(leaves) != 0 {
			return invalidPlan("aggregate filter has unused prepared leaves")
		}
		filterSQL.WriteByte(')')
	}
	rendered, err := renderAggregate(expression, operand.String(), filterSQL.String(), true)
	if err != nil {
		return err
	}
	statement.WriteString(rendered)
	return nil
}

func groupedFieldResolver(prepared queryplan.Joins) whereFieldResolver {
	return func(condition query.Condition) (string, error) {
		alias, err := queryplan.GroupConditionAlias(condition, prepared.ByKey)
		if err != nil {
			return "", err
		}
		return quoteQualified(alias, condition.Field().Column())
	}
}
func groupedRHSField(field query.FieldRef) (string, error) {
	return quoteQualified("t0", field.Column())
}
func bindGroupedExists(schema string, plan query.Plan, where *whereAnalysis, prepared queryplan.Joins) error {
	for index, exists := range prepared.Exists {
		prefix, err := queryplan.CollectionExistsPrefix(exists, func(table string) (string, error) { return quoteTable(schema, table) }, quoteIdentifier, quoteQualified)
		if err != nil {
			return err
		}
		field, err := quoteQualified(exists.TerminalAlias, plan.Conditions()[index].Field().Column())
		if err != nil {
			return err
		}
		where.leaves[index].existsPrefix, where.leaves[index].existsField = prefix, field
	}
	return nil
}
