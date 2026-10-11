package sqlite

import (
	"strconv"
	"strings"

	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/query"
)

func compileGrouped(plan query.Plan) (string, []any, error) {
	if err := validateReadSourceFields(plan.SourceFields()); err != nil {
		return "", nil, err
	}
	if plan.ResultShape().Kind() == query.ResultAggregate {
		_, limited := plan.Limit()
		_, offset := plan.Offset()
		if limited || offset || plan.Distinct() || len(plan.RelationProjections()) != 0 {
			return "", nil, unsupportedResult("filtered or related aggregate requires an unsliced, non-distinct scalar source")
		}
	}
	prepared, err := queryplan.PrepareJoins(plan, "SQLite")
	if err != nil {
		return "", nil, err
	}
	if len(prepared.Keys) > 63 {
		return "", nil, unsupportedResult("SQLite supports at most 64 joined tables including the root")
	}
	where, err := analyzeWhere(plan)
	if err != nil {
		return "", nil, err
	}
	if err := bindGroupedWhere(plan, where, prepared); err != nil {
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
	// Qualified main tables cannot be shadowed by the page CTE names.
	quoteTable := func(table string) (string, error) { return quoteQualified("main", table) }
	if err := queryplan.AppendGroupSource(&statement, plan, prepared, quoteTable, quoteIdentifier, quoteQualified); err != nil {
		return "", nil, err
	}
	whereArguments, err := appendWhere(&statement, where)
	if err != nil {
		return "", nil, err
	}
	arguments = append(arguments, whereArguments...)
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
		argument, err := sqliteValue(value)
		if err != nil {
			return "", err
		}
		arguments = append(arguments, argument)
		return "?", nil
	}, func(sql *strings.Builder) error { arguments = appendPagination(sql, arguments, plan); return nil })
	if err != nil {
		return "", nil, err
	}
	if len(arguments) > 32766 || len(result) > 4<<20 {
		return "", nil, unsupportedResult("group query exceeds SQLite statement or parameter budget")
	}
	return result, arguments, nil
}

func appendGroupedValue(statement *strings.Builder, expression query.ResultExpression, plan query.Plan, prepared queryplan.Joins, arguments *[]any) error {
	if !expression.IsAggregate() {
		return appendResultValue(statement, expression, "t0", prepared.ByKey, arguments, false)
	}
	operandStart := len(*arguments)
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
		analysis, err := analyzeWhere(filterPlan)
		if err != nil {
			return err
		}
		filterJoins := prepared
		filterJoins.Exists = nil
		if err := bindGroupedWhere(filterPlan, analysis, filterJoins); err != nil {
			return err
		}
		filterSQL.WriteString(" FILTER (WHERE ")
		if err := appendWhereNode(&filterSQL, analysis.root, false, false, arguments); err != nil {
			return err
		}
		filterSQL.WriteByte(')')
	}
	rendered, err := renderAggregate(expression, operand.String(), filterSQL.String())
	if err != nil {
		return err
	}
	if expression.OperandKind() == query.FieldFloat && (expression.Kind() == query.ResultSum || expression.Kind() == query.ResultAvg) {
		*arguments = append(*arguments, (*arguments)[operandStart:]...)
	}
	statement.WriteString(rendered)
	return nil
}

func bindGroupedWhere(plan query.Plan, analysis *sqliteWhereAnalysis, prepared queryplan.Joins) error {
	for index, leaf := range analysis.leaves {
		condition := leaf.condition
		if scalar, computed := condition.Scalar(); computed {
			if err := scalar.ValidateSource(plan.SourceFields()); err != nil {
				return err
			}
			leaf.scalarAlias = "t0"
			continue
		}
		if path, related := condition.RelationPath(); related {
			if err := queryplan.RelationCondition(plan, condition, path, "SQLite"); err != nil {
				return err
			}
		} else if !queryplan.ContainsField(plan.SourceFields(), condition.Field()) {
			return invalidPlan("group predicate field is not source metadata")
		}
		alias := "t0"
		var err error
		if exists, present := prepared.Exists[index]; present {
			if len(exists.Joins) > 63 {
				return unsupportedResult("SQLite collection existence query exceeds 64 tables")
			}
			prefix, err := queryplan.CollectionExistsPrefix(exists, func(table string) (string, error) { return quoteQualified("main", table) }, quoteIdentifier, quoteQualified)
			if err != nil {
				return err
			}
			leaf.existsPrefix, alias = prefix, exists.TerminalAlias
		} else {
			alias, err = queryplan.GroupConditionAlias(condition, prepared.ByKey)
			if err != nil {
				return err
			}
		}
		leaf.fieldSQL, err = quoteQualified(alias, condition.Field().Column())
		if err != nil {
			return err
		}
		if right, present := condition.RHSField(); present {
			if !queryplan.ContainsField(plan.SourceFields(), right) {
				return invalidPlan("group predicate right-hand field is not source metadata")
			}
			leaf.rhsFieldSQL, err = quoteQualified("t0", right.Column())
			if err != nil {
				return err
			}
		}
	}
	return nil
}
