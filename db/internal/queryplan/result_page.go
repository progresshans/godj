package queryplan

import (
	"slices"
	"strconv"
	"strings"

	"github.com/progresshans/godj/query"
)

type PageDialect struct {
	ParameterLimit int
	OrderValue     func(query.FieldKind, string) string
	Pagination     func(*strings.Builder, *[]any, query.Plan) error
}

// CompilePage counts and slices one materialized input within one statement.
// CTE column lists assign names without rewriting input SQL or placeholders.
// Hidden order cells participate in ordering, never the published result.
func CompilePage(plan query.Plan, inputSQL string, inputArguments []any, dialect PageDialect) (string, []any, error) {
	if err := plan.ValidatePage(); err != nil {
		return "", nil, err
	}
	input, orderings, present := plan.PageInput()
	if !present || inputSQL == "" || dialect.Pagination == nil || dialect.ParameterLimit <= 0 ||
		len(inputSQL) > 4<<20 || len(inputArguments) > dialect.ParameterLimit {
		return "", nil, invalidPlan("page compiler has an invalid input or dialect")
	}
	arguments := slices.Clone(inputArguments)
	rows := `"` + unusedSQLIdentifier("godj_page_rows", inputSQL) + `"`
	page := `"` + unusedSQLIdentifier("godj_page_slice", inputSQL) + `"`
	selected := input.ResultShape().Expressions()
	appendOrder := func(statement *strings.Builder, prefix string) {
		statement.WriteString(" ORDER BY ")
		for index, order := range orderings {
			if index > 0 {
				statement.WriteString(", ")
			}
			value := prefix + `"c` + strconv.Itoa(order.Index()) + `"`
			if dialect.OrderValue != nil {
				value = dialect.OrderValue(selected[order.Index()].ResultValueKind(), value)
			}
			statement.WriteString(value)
			if order.Direction() == query.Ascending {
				statement.WriteString(" ASC")
			} else {
				statement.WriteString(" DESC")
			}
			switch order.Nulls() {
			case query.NullsFirst:
				statement.WriteString(" NULLS FIRST")
			case query.NullsLast:
				statement.WriteString(" NULLS LAST")
			}
		}
	}
	var statement strings.Builder
	statement.WriteString("WITH " + rows + " (")
	appendGroupColumns(&statement, len(selected), "")
	statement.WriteString(") AS MATERIALIZED (" + inputSQL + "), " + page + " AS (SELECT ")
	appendGroupColumns(&statement, len(selected), "")
	statement.WriteString(`, 1 AS "godj_present" FROM ` + rows)
	appendOrder(&statement, "")
	if err := dialect.Pagination(&statement, &arguments, plan); err != nil {
		return "", nil, err
	}
	statement.WriteString(`) SELECT "godj_totals"."godj_total", ` + page + `."godj_present", `)
	appendGroupColumns(&statement, len(plan.ResultShape().Expressions()), page+".")
	statement.WriteString(` FROM (SELECT COUNT(*) AS "godj_total" FROM ` + rows + `) AS "godj_totals" LEFT OUTER JOIN ` + page + ` ON 1 = 1`)
	appendOrder(&statement, page+".")
	if statement.Len() > 4<<20 || len(arguments) > dialect.ParameterLimit {
		return "", nil, invalidPlan("result page exceeds its SQL or parameter budget")
	}
	return statement.String(), arguments, nil
}
