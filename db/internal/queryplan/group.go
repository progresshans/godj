package queryplan

import (
	"strconv"
	"strings"

	"github.com/progresshans/godj/query"
)

// NeedsAggregateCompiler identifies selected aggregates requiring the common
// grouping path instead of the legacy unfiltered scalar aggregate grammar.
func NeedsAggregateCompiler(plan query.Plan) bool {
	if plan.ResultShape().Kind() == query.ResultGrouped {
		return true
	}
	if plan.ResultShape().Kind() != query.ResultAggregate {
		return false
	}
	for _, expression := range plan.ResultShape().Expressions() {
		_, filter := expression.Filter()
		_, related := expression.RelationPath()
		if filter || related || expression.Kind() == query.ResultCount {
			return true
		}
		if expression.Kind() == query.ResultSum || expression.Kind() == query.ResultAvg {
			if where, present := plan.Where(); present && where.HasRelations() {
				return true
			}
		}
	}
	return false
}

// AppendGroupSource shares only SQL's relation grammar. Dialects retain all
// identifier, schema, physical value, argument and expression decisions.
func AppendGroupSource(statement *strings.Builder, plan query.Plan, prepared Joins,
	quoteTable, quoteIdentifier func(string) (string, error), qualify func(string, string) (string, error)) error {
	table, err := quoteTable(plan.Table())
	if err != nil {
		return err
	}
	statement.WriteString(" FROM " + table + ` AS "t0"`)
	for _, key := range prepared.Keys {
		join := prepared.ByKey[key]
		table, err := quoteTable(join.Table)
		if err != nil {
			return err
		}
		alias, err := quoteIdentifier(join.Alias)
		if err != nil {
			return err
		}
		left, err := qualify(join.FromAlias, join.FromColumn)
		if err != nil {
			return err
		}
		right, err := qualify(join.Alias, join.Column)
		if err != nil {
			return err
		}
		if join.LeftOuter {
			statement.WriteString(" LEFT OUTER JOIN ")
		} else {
			statement.WriteString(" INNER JOIN ")
		}
		statement.WriteString(table + " AS " + alias + " ON " + left + " = " + right)
	}
	return nil
}

func GroupConditionAlias(condition query.Condition, joins map[RelationKey]Join) (string, error) {
	alias := "t0"
	if path, related := condition.RelationPath(); related {
		if key, joined := ConditionJoinKey(path); joined {
			entry, found := joins[key]
			if !found {
				return "", invalidPlan("aggregate condition relation is not materialized")
			}
			alias = entry.Alias
		}
	}
	return alias, nil
}

// FinishGroups filters the completed grouped relation. This is HAVING's
// post-group semantics expressed using a derived table, so filtered aggregates
// are computed once and parameter traversal is deterministic.
// Page uses the same grouped CTE for count and rows, including an empty page.
func FinishGroups(base string, plan query.Plan, placeholder func(query.Value) (string, error),
	page func(*strings.Builder) error) (string, error) {
	shape := plan.ResultShape()
	if shape.Kind() != query.ResultGrouped {
		return base, nil
	}
	expressions := shape.Expressions()
	var filtered strings.Builder
	filtered.WriteString("SELECT ")
	appendGroupColumns(&filtered, len(expressions), "")
	filtered.WriteString(" FROM (" + base + `) AS "godj_group_values"`)
	if having, present := shape.GroupHaving(); present {
		filtered.WriteString(" WHERE ")
		if err := appendGroupPredicate(&filtered, having, expressions, placeholder, false); err != nil {
			return "", err
		}
	}
	if shape.GroupMode() == query.GroupPage {
		var statement strings.Builder
		statement.WriteString(`WITH "godj_groups" AS (` + filtered.String() + `), "godj_page" AS (SELECT `)
		appendGroupColumns(&statement, len(expressions), "")
		statement.WriteString(`, 1 AS "godj_present" FROM "godj_groups"`)
		appendGroupOrderings(&statement, shape, "")
		if err := page(&statement); err != nil {
			return "", err
		}
		statement.WriteString(`) SELECT "godj_totals"."godj_total", "godj_page"."godj_present", `)
		appendGroupColumns(&statement, len(expressions), `"godj_page".`)
		statement.WriteString(` FROM (SELECT COUNT(*) AS "godj_total" FROM "godj_groups") AS "godj_totals" LEFT OUTER JOIN "godj_page" ON 1 = 1`)
		appendGroupOrderings(&statement, shape, `"godj_page".`)
		return statement.String(), nil
	}
	appendGroupOrderings(&filtered, shape, "")
	if err := page(&filtered); err != nil {
		return "", err
	}
	if shape.GroupMode() == query.GroupCount {
		return `SELECT COUNT(*) FROM (` + filtered.String() + `) AS "godj_group_count"`, nil
	}
	return filtered.String(), nil
}

func appendGroupColumns(statement *strings.Builder, count int, prefix string) {
	for index := 0; index < count; index++ {
		if index > 0 {
			statement.WriteString(", ")
		}
		statement.WriteString(prefix + `"c` + strconv.Itoa(index) + `"`)
	}
}
func appendGroupOrderings(statement *strings.Builder, shape query.ResultShape, prefix string) {
	order := shape.GroupOrderings()
	if len(order) == 0 {
		return
	}
	statement.WriteString(" ORDER BY ")
	for index, item := range order {
		if index > 0 {
			statement.WriteString(", ")
		}
		statement.WriteString(prefix + `"c` + strconv.Itoa(ExpressionIndex(shape.Expressions(), item.Expression())) + `"`)
		if item.Direction() == query.Ascending {
			statement.WriteString(" ASC")
		} else {
			statement.WriteString(" DESC")
		}
		switch item.Nulls() {
		case query.NullsFirst:
			statement.WriteString(" NULLS FIRST")
		case query.NullsLast:
			statement.WriteString(" NULLS LAST")
		}
	}
}
func appendGroupPredicate(statement *strings.Builder, expression query.GroupExpression, selected []query.ResultExpression,
	placeholder func(query.Value) (string, error), negated bool) error {
	statement.WriteByte('(')
	if value, lookup, literal, leaf := expression.Leaf(); leaf {
		column := `"c` + strconv.Itoa(ExpressionIndex(selected, value)) + `"`
		statement.WriteString(column)
		if lookup == query.LookupIsNull {
			isNull, _ := literal.Boolean()
			if isNull {
				statement.WriteString(" IS NULL")
			} else {
				statement.WriteString(" IS NOT NULL")
			}
		} else {
			switch lookup {
			case query.LookupExact:
				statement.WriteString(" = ")
			case query.LookupGreaterThan:
				statement.WriteString(" > ")
			case query.LookupGreaterThanOrEqual:
				statement.WriteString(" >= ")
			case query.LookupLessThan:
				statement.WriteString(" < ")
			case query.LookupLessThanOrEqual:
				statement.WriteString(" <= ")
			default:
				return invalidPlan("group predicate comparison is invalid")
			}
			parameter, err := placeholder(literal)
			if err != nil {
				return err
			}
			statement.WriteString(parameter)
			if !value.IsAggregate() && negated {
				field, _ := value.Field()
				_, related := value.RelationPath()
				if field.Nullable() || related {
					statement.WriteString(" AND " + column + " IS NOT NULL")
				}
			}
		}
	} else {
		children := expression.Children()
		switch expression.Kind() {
		case query.ExpressionNot:
			statement.WriteString("NOT ")
			if err := appendGroupPredicate(statement, children[0], selected, placeholder, !negated); err != nil {
				return err
			}
		case query.ExpressionAnd, query.ExpressionOr:
			for index, child := range children {
				if index > 0 {
					if expression.Kind() == query.ExpressionAnd {
						statement.WriteString(" AND ")
					} else {
						statement.WriteString(" OR ")
					}
				}
				if err := appendGroupPredicate(statement, child, selected, placeholder, negated); err != nil {
					return err
				}
			}
		default:
			return invalidPlan("group predicate is invalid")
		}
	}
	statement.WriteByte(')')
	return nil
}
