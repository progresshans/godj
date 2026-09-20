package sqlite

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/query"
)

func Compile(plan query.Plan) (string, []any, error) {
	where, err := analyzeWhere(plan)
	if err != nil {
		return "", nil, err
	}
	selected := len(plan.RelationProjections()) != 0
	related := where.hasRelations || plan.ResultShape().HasRelations()
	if related && !selected && plan.ResultShape().IsCountAll() {
		inner, arguments, err := compileRelation(plan, where)
		if err != nil {
			return "", nil, err
		}
		return `SELECT COUNT(*) FROM (` + inner + `) AS "godj_count_source"`, arguments, nil
	}
	kind := plan.ResultShape().Kind()
	if selected && kind != query.ResultModel {
		return "", nil, unsupportedResult("SQLite scalar results cannot combine with related-object projection")
	}
	if related && kind != query.ResultModel && kind != query.ResultProjection {
		return "", nil, unsupportedResult("SQLite non-count aggregates cannot combine with relation filters")
	}
	if selected {
		return compileRelation(plan, where)
	}
	if related {
		return compileRelation(plan, where)
	}
	return compileScalar(plan, where)
}

// compileScalar is the single-table scalar compiler. Keep
// relation-specific qualification and validation out of this path so plans
// built before relation traversal retain their exact SQL and error behavior.
func compileScalar(plan query.Plan, where *sqliteWhereAnalysis) (string, []any, error) {
	if plan.Table() == "" {
		return "", nil, invalidPlan("table is empty")
	}
	sourceFields := plan.SourceFields()
	if err := validateReadSourceFields(sourceFields); err != nil {
		return "", nil, err
	}
	if err := bindScalarWhere(where, sourceFields); err != nil {
		return "", nil, err
	}
	result := plan.ResultShape()
	switch result.Kind() {
	case query.ResultModel:
		if len(result.Expressions()) != 0 {
			return "", nil, invalidPlan("model result contains explicit expressions")
		}
		return compileScalarRows(plan, queryplan.FieldExpressions(sourceFields), sourceFields, where)
	case query.ResultProjection:
		projection, err := queryplan.ProjectionExpressions(result, sourceFields)
		if err != nil {
			return "", nil, err
		}
		return compileScalarRows(plan, projection, sourceFields, where)
	case query.ResultAggregate:
		return compileScalarAggregate(plan, result, sourceFields, where)
	default:
		return "", nil, invalidPlan("query result kind is invalid")
	}
}

func compileScalarRows(plan query.Plan, selected []query.ResultExpression, sourceFields []query.FieldRef, where *sqliteWhereAnalysis) (string, []any, error) {
	if len(selected) == 0 {
		return "", nil, invalidPlan("select columns are empty")
	}

	var sql strings.Builder
	sql.WriteString("SELECT ")
	if plan.Distinct() {
		sql.WriteString("DISTINCT ")
	}
	arguments, err := appendSelectedExpressions(&sql, selected, "", nil)
	if err != nil {
		return "", nil, err
	}
	sql.WriteString(" FROM ")
	table, err := quoteIdentifier(plan.Table())
	if err != nil {
		return "", nil, err
	}
	sql.WriteString(table)

	whereArguments, err := appendWhere(&sql, where)
	if err != nil {
		return "", nil, err
	}
	arguments = append(arguments, whereArguments...)

	orderings := plan.Orderings()
	if len(orderings) > 0 {
		sql.WriteString(" ORDER BY ")
	}
	for index, ordering := range orderings {
		if err := queryplan.ValidateOrdering(ordering.Field()); err != nil {
			return "", nil, err
		}
		if index > 0 {
			sql.WriteString(", ")
		}
		if !queryplan.ContainsField(sourceFields, ordering.Field()) {
			return "", nil, invalidPlan(fmt.Sprintf("ordering field %q is not selected model metadata", ordering.Field().Name()))
		}
		field, err := quoteIdentifier(ordering.Field().Column())
		if err != nil {
			return "", nil, err
		}
		sql.WriteString(field)
		switch ordering.Direction() {
		case query.Ascending:
			sql.WriteString(" ASC")
		case query.Descending:
			sql.WriteString(" DESC")
		default:
			return "", nil, invalidPlan("unknown ordering direction")
		}
		if plan.Distinct() && plan.ResultShape().Kind() == query.ResultProjection &&
			!queryplan.ProjectsWholeField(selected, ordering.Field()) {
			return "", nil, unsupportedDistinctOrdering(ordering.Field())
		}
	}
	arguments = appendPagination(&sql, arguments, plan)
	return sql.String(), arguments, nil
}

func compileScalarAggregate(plan query.Plan, result query.ResultShape, sourceFields []query.FieldRef, where *sqliteWhereAnalysis) (string, []any, error) {
	expressions := result.Expressions()
	if len(expressions) == 0 {
		return "", nil, invalidPlan("aggregate result is empty")
	}
	_, limited := plan.Limit()
	_, offset := plan.Offset()
	if !plan.Distinct() && !limited && !offset {
		return compileDirectScalarAggregate(plan, expressions, sourceFields, where)
	}
	innerSQL, arguments, err := compileScalarRows(plan, queryplan.FieldExpressions(sourceFields), sourceFields, where)
	if err != nil {
		return "", nil, err
	}

	const sourceAlias = "godj_aggregate_source"
	quotedAlias, err := quoteIdentifier(sourceAlias)
	if err != nil {
		return "", nil, err
	}
	var sql strings.Builder
	sql.WriteString("SELECT ")
	if err := appendScalarAggregateExpressions(&sql, expressions, sourceFields, sourceAlias); err != nil {
		return "", nil, err
	}
	sql.WriteString(" FROM (")
	sql.WriteString(innerSQL)
	sql.WriteString(") AS ")
	sql.WriteString(quotedAlias)
	return sql.String(), arguments, nil
}

func compileDirectScalarAggregate(plan query.Plan, expressions []query.ResultExpression, sourceFields []query.FieldRef, where *sqliteWhereAnalysis) (string, []any, error) {
	table, err := quoteIdentifier(plan.Table())
	if err != nil {
		return "", nil, err
	}

	var sql strings.Builder
	sql.WriteString("SELECT ")
	if err := appendScalarAggregateExpressions(&sql, expressions, sourceFields, ""); err != nil {
		return "", nil, err
	}
	sql.WriteString(" FROM ")
	sql.WriteString(table)

	arguments, err := appendWhere(&sql, where)
	if err != nil {
		return "", nil, err
	}
	if err := queryplan.OmittedOrderings(plan.Orderings(), sourceFields, quoteIdentifier); err != nil {
		return "", nil, err
	}
	return sql.String(), arguments, nil
}

func appendScalarAggregateExpressions(sql *strings.Builder, expressions []query.ResultExpression, sourceFields []query.FieldRef, sourceAlias string) error {
	return queryplan.AppendAggregates(sql, expressions, sourceFields, func(function string, field query.FieldRef) (string, error) {
		var column string
		var err error
		if sourceAlias != "" {
			column, err = quoteQualified(sourceAlias, field.Column())
		} else {
			column, err = quoteIdentifier(field.Column())
		}
		return function + "(" + column + ")", err
	})
}

func appendPagination(sql *strings.Builder, arguments []any, plan query.Plan) []any {
	limit, limited := plan.Limit()
	if limited {
		sql.WriteString(" LIMIT ?")
		arguments = append(arguments, int64(limit))
	}
	if offset, ok := plan.Offset(); ok {
		if !limited {
			sql.WriteString(" LIMIT -1")
		}
		sql.WriteString(" OFFSET ?")
		arguments = append(arguments, int64(offset))
	}
	return arguments
}

func compileRelation(plan query.Plan, where *sqliteWhereAnalysis) (string, []any, error) {
	if plan.ResultShape().Kind() != query.ResultModel && plan.ResultShape().Kind() != query.ResultProjection && !plan.ResultShape().IsCountAll() {
		return "", nil, unsupportedResult("SQLite relation compilation requires model rows, a root projection, or COUNT(*)")
	}
	if plan.Table() == "" {
		return "", nil, invalidPlan("table is empty")
	}
	columns := plan.SourceFields()
	if err := validateReadSourceFields(columns); err != nil {
		return "", nil, err
	}

	for _, leaf := range where.leaves {
		condition := leaf.condition
		if _, related := condition.RelationPath(); !related {
			if !queryplan.ContainsField(columns, condition.Field()) {
				return "", nil, invalidPlan("condition field is not selected model metadata")
			}
			if right, ok := condition.RHSField(); ok && !queryplan.ContainsField(columns, right) {
				return "", nil, invalidPlan("condition right-hand-side field is not selected model metadata")
			}
		}
	}
	prepared, err := queryplan.PrepareJoins(plan, "SQLite")
	if err != nil {
		return "", nil, err
	}
	// SQLite's optimizer has one bit per joined table; the root counts too.
	// https://www.sqlite.org/limits.html#max_join
	if len(prepared.Keys) > 63 {
		return "", nil, unsupportedResult("SQLite supports at most 64 joined tables including the root")
	}

	keys, joins := prepared.Keys, prepared.ByKey

	const rootAlias = "t0"
	var sql strings.Builder
	sql.WriteString("SELECT ")
	if plan.Distinct() {
		sql.WriteString("DISTINCT ")
	}
	selected := queryplan.FieldExpressions(columns)
	if plan.ResultShape().Kind() == query.ResultProjection {
		selected, err = queryplan.ProjectionExpressions(plan.ResultShape(), columns)
		if err != nil {
			return "", nil, err
		}
	}
	arguments, err := appendSelectedExpressions(&sql, selected, rootAlias, joins)
	if err != nil {
		return "", nil, err
	}
	for _, projection := range plan.RelationProjections() {
		alias := joins[queryplan.KeyForPath(projection.Path().Hops())].Alias
		for _, column := range projection.TargetColumns() {
			sql.WriteString(", ")
			qualified, err := quoteQualified(alias, column.Column())
			if err != nil {
				return "", nil, err
			}
			sql.WriteString(qualified)
		}
	}
	table, err := quoteIdentifier(plan.Table())
	if err != nil {
		return "", nil, err
	}
	quotedRootAlias, err := quoteIdentifier(rootAlias)
	if err != nil {
		return "", nil, err
	}
	sql.WriteString(" FROM ")
	sql.WriteString(table)
	sql.WriteString(" AS ")
	sql.WriteString(quotedRootAlias)
	for _, key := range keys {
		join := joins[key]
		joinedTable, err := quoteIdentifier(join.Table)
		if err != nil {
			return "", nil, err
		}
		alias, err := quoteIdentifier(join.Alias)
		if err != nil {
			return "", nil, err
		}
		rootColumn, err := quoteQualified(join.FromAlias, join.FromColumn)
		if err != nil {
			return "", nil, err
		}
		joinedColumn, err := quoteQualified(join.Alias, join.Column)
		if err != nil {
			return "", nil, err
		}
		if join.LeftOuter {
			sql.WriteString(" LEFT OUTER JOIN ")
		} else {
			sql.WriteString(" INNER JOIN ")
		}
		sql.WriteString(joinedTable)
		sql.WriteString(" AS ")
		sql.WriteString(alias)
		sql.WriteString(" ON ")
		sql.WriteString(rootColumn)
		sql.WriteString(" = ")
		sql.WriteString(joinedColumn)
	}

	for _, leaf := range where.leaves {
		alias := rootAlias
		if path, related := leaf.condition.RelationPath(); related {
			if key, joined := queryplan.ConditionJoinKey(path); joined {
				relation, found := joins[key]
				if !found {
					return "", nil, invalidPlan("relation predicate join metadata is missing")
				}
				alias = relation.Alias
			}
		}
		field, err := quoteQualified(alias, leaf.condition.Field().Column())
		if err != nil {
			return "", nil, err
		}
		leaf.fieldSQL = field
		if right, ok := leaf.condition.RHSField(); ok {
			rhsField, err := quoteQualified(rootAlias, right.Column())
			if err != nil {
				return "", nil, err
			}
			leaf.rhsFieldSQL = rhsField
		}
	}
	whereArguments, err := appendWhere(&sql, where)
	if err != nil {
		return "", nil, err
	}

	arguments = append(arguments, whereArguments...)

	orderings := plan.Orderings()
	if len(orderings) > 0 {
		sql.WriteString(" ORDER BY ")
	}
	for index, ordering := range orderings {
		if err := queryplan.ValidateOrdering(ordering.Field()); err != nil {
			return "", nil, err
		}
		if index > 0 {
			sql.WriteString(", ")
		}
		if !queryplan.ContainsField(columns, ordering.Field()) {
			return "", nil, invalidPlan(fmt.Sprintf("ordering field %q is not selected model metadata", ordering.Field().Name()))
		}
		if plan.Distinct() && plan.ResultShape().Kind() == query.ResultProjection && !queryplan.ProjectsWholeField(selected, ordering.Field()) {
			return "", nil, unsupportedDistinctOrdering(ordering.Field())
		}
		field, err := quoteQualified(rootAlias, ordering.Field().Column())
		if err != nil {
			return "", nil, err
		}
		sql.WriteString(field)
		switch ordering.Direction() {
		case query.Ascending:
			sql.WriteString(" ASC")
		case query.Descending:
			sql.WriteString(" DESC")
		default:
			return "", nil, invalidPlan("unknown ordering direction")
		}
	}
	arguments = appendPagination(&sql, arguments, plan)
	return sql.String(), arguments, nil
}

func quoteQualified(alias, column string) (string, error) {
	quotedAlias, err := quoteIdentifier(alias)
	if err != nil {
		return "", err
	}
	quotedColumn, err := quoteIdentifier(column)
	if err != nil {
		return "", err
	}
	return quotedAlias + "." + quotedColumn, nil
}

func compileCondition(sql *strings.Builder, condition query.Condition, rhsFieldSQL string, inValues []query.Value) ([]any, error) {
	field := condition.Field()
	if field.Kind() == query.FieldDecimal && !field.ValidType() {
		return nil, invalidPlan("condition has invalid field type parameters")
	}
	if right, ok := condition.RHSField(); ok {
		if !right.ValidType() {
			return nil, invalidPlan("comparison has invalid right field type parameters")
		}
		if rhsFieldSQL == "" {
			return nil, invalidPlan("SQLite field comparison is missing its right-hand-side binding")
		}
		if field.Kind() != right.Kind() ||
			(field.Kind() != query.FieldInteger && field.Kind() != query.FieldFloat && field.Kind() != query.FieldDecimal && field.Kind() != query.FieldUUID && (field.Kind() != query.FieldJSON || condition.Lookup() != query.LookupExact) && field.Kind() != query.FieldString && field.Kind() != query.FieldDateTime && field.Kind() != query.FieldDate && (field.Kind() != query.FieldTime && field.Kind() != query.FieldDuration)) {
			return nil, invalidPlan("SQLite field comparison requires same-kind supported scalar fields")
		}
		operator, ok := comparisonOperator(condition.Lookup())
		if !ok {
			return nil, invalidPlan("SQLite field comparison requires exact or ordered comparison")
		}
		sql.WriteByte(' ')
		sql.WriteString(operator)
		sql.WriteByte(' ')
		sql.WriteString(rhsFieldSQL)
		return nil, nil
	}
	if rhsFieldSQL != "" {
		return nil, invalidPlan("SQLite literal condition contains an unexpected right-hand-side field binding")
	}
	value := condition.Value()
	switch condition.Lookup() {
	case query.LookupHasKey, query.LookupHasKeys, query.LookupHasAnyKeys:
		keys, ok := condition.JSONKeys()
		if !ok {
			return nil, invalidPlan("JSON key presence operand is missing")
		}
		encoded, err := json.Marshal(keys.Values())
		if err != nil {
			return nil, err
		}
		mode := int64(0)
		if condition.Lookup() == query.LookupHasKeys {
			mode = 1
		}
		sql.WriteString(", ?, ?)")
		return []any{string(encoded), mode}, nil
	case query.LookupExact:
		if !queryplan.ValueMatchesField(value.Kind(), field.Kind()) {
			return nil, invalidPlan(fmt.Sprintf("exact value kind %q does not match field %q", value.Kind(), field.Name()))
		}
		argument, err := sqliteValue(value)
		if err != nil {
			return nil, err
		}
		sql.WriteString(" = ?")
		return []any{argument}, nil
	case query.LookupGreaterThan, query.LookupGreaterThanOrEqual, query.LookupLessThan, query.LookupLessThanOrEqual:
		if !queryplan.OrderedValueMatchesField(value.Kind(), field.Kind()) {
			return nil, invalidPlan(fmt.Sprintf("ordered value kind %q does not match field %q", value.Kind(), field.Name()))
		}
		argument, err := sqliteValue(value)
		if err != nil {
			return nil, err
		}
		operator, _ := comparisonOperator(condition.Lookup())
		sql.WriteByte(' ')
		sql.WriteString(operator)
		sql.WriteString(" ?")
		return []any{argument}, nil
	case query.LookupIContains:
		text, ok := value.String()
		if field.Kind() != query.FieldString || !ok {
			return nil, unsupportedLookup(field, condition.Lookup())
		}
		sql.WriteString(" LIKE ? ESCAPE '\\'")
		return []any{"%" + queryplan.EscapeLike(text) + "%"}, nil
	case query.LookupIsNull:
		isNull, ok := value.Boolean()
		if !ok {
			return nil, unsupportedLookup(field, condition.Lookup())
		}
		if isNull {
			sql.WriteString(" IS NULL")
		} else {
			sql.WriteString(" IS NOT NULL")
		}
		return nil, nil
	case query.LookupIn:
		if len(inValues) == 0 {
			return nil, invalidPlan("SQLite IN requires a valid scalar list-backed condition")
		}
		sql.WriteString(" IN (")
		arguments := make([]any, len(inValues))
		for index, item := range inValues {
			if index > 0 {
				sql.WriteString(", ")
			}
			sql.WriteByte('?')
			argument, err := sqliteValue(item)
			if err != nil {
				return nil, err
			}
			arguments[index] = argument
		}
		sql.WriteByte(')')
		return arguments, nil
	default:
		return nil, unsupportedLookup(field, condition.Lookup())
	}
}

func comparisonOperator(lookup query.Lookup) (string, bool) {
	switch lookup {
	case query.LookupExact:
		return "=", true
	case query.LookupGreaterThan:
		return ">", true
	case query.LookupGreaterThanOrEqual:
		return ">=", true
	case query.LookupLessThan:
		return "<", true
	case query.LookupLessThanOrEqual:
		return "<=", true
	default:
		return "", false
	}
}

func quoteIdentifier(identifier string) (string, error) {
	if identifier == "" || strings.ContainsRune(identifier, 0) {
		return "", invalidPlan("identifier is empty or contains NUL")
	}
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`, nil
}

// validateReadSourceFields closes the decoder metadata domain without
// narrowing SQLite's existing quoted-identifier policy. Logical names remain
// exact GoDj names, while physical columns use SQLite's ASCII case folding.
func validateReadSourceFields(fields []query.FieldRef) error {
	if len(fields) == 0 {
		return invalidPlan("select columns are empty")
	}
	names := make(map[string]struct{}, len(fields))
	columns := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if field.Kind() == query.FieldDecimal && !field.ValidType() {
			return invalidPlan("field has invalid type parameters")
		}
		if _, err := quoteIdentifier(field.Name()); err != nil {
			return invalidPlan(fmt.Sprintf("field name %q is empty or contains NUL", field.Name()))
		}
		if _, err := quoteIdentifier(field.Column()); err != nil {
			return invalidPlan(fmt.Sprintf("field column %q is empty or contains NUL", field.Column()))
		}
		switch field.Kind() {
		case query.FieldInteger, query.FieldFloat, query.FieldDecimal, query.FieldUUID, query.FieldJSON, query.FieldString, query.FieldBoolean, query.FieldDateTime, query.FieldDate, query.FieldTime, query.FieldDuration:
		default:
			return invalidPlan(fmt.Sprintf("field %q has unsupported kind %q", field.Name(), field.Kind()))
		}
		if _, duplicate := names[field.Name()]; duplicate {
			return invalidPlan(fmt.Sprintf("field name %q is duplicated", field.Name()))
		}
		columnKey := sqliteIdentifierKey(field.Column())
		if _, duplicate := columns[columnKey]; duplicate {
			return invalidPlan(fmt.Sprintf("field column %q is duplicated under SQLite identifier rules", field.Column()))
		}
		names[field.Name()] = struct{}{}
		columns[columnKey] = struct{}{}
	}
	return nil
}

func invalidPlan(detail string) error {
	return &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan, Detail: detail}
}

func unsupportedLookup(field query.FieldRef, lookup query.Lookup) error {
	return &query.Error{
		Category: query.CategoryBackend,
		Code:     query.CodeUnsupported,
		Field:    field.Name(),
		Lookup:   string(lookup),
		Detail:   "SQLite compiler cannot compile this condition",
	}
}

func unsupportedRelatedCondition(condition query.Condition, detail string) error {
	return &query.Error{
		Category: query.CategoryBackend,
		Code:     query.CodeUnsupported,
		Field:    condition.Field().Name(),
		Lookup:   string(condition.Lookup()),
		Detail:   detail,
	}
}

func unsupportedResult(detail string) error {
	return &query.Error{
		Category: query.CategoryBackend,
		Code:     query.CodeUnsupported,
		Detail:   detail,
	}
}

func unsupportedDistinctOrdering(field query.FieldRef) error {
	return &query.Error{
		Category: query.CategoryBackend,
		Code:     query.CodeUnsupported,
		Field:    field.Name(),
		Detail:   "SQLite DISTINCT projection requires every ordering field in the result shape",
	}
}

// Every scalar cell, including a root cell selected after relationship filters,
// uses the same rendering and parameter order. Joins only supply qualification.
func appendSelectedExpressions(sql *strings.Builder, selected []query.ResultExpression, alias string, joins map[queryplan.RelationKey]queryplan.Join) ([]any, error) {
	arguments := make([]any, 0)
	for index, expression := range selected {
		if index > 0 {
			sql.WriteString(", ")
		}
		field, _ := expression.Field()
		selectedAlias := alias
		if path, related := expression.RelationPath(); related {
			joined, ok := joins[queryplan.KeyForPath(path.Hops())]
			if !ok {
				return nil, invalidPlan("selected relation was not materialized")
			}
			selectedAlias = joined.Alias
		}
		var column string
		var err error
		if selectedAlias == "" {
			column, err = quoteIdentifier(field.Column())
		} else {
			column, err = quoteQualified(selectedAlias, field.Column())
		}
		if err != nil {
			return nil, err
		}
		if path, ok := expression.JSONPath(); ok {
			sql.WriteString("godj_json_at(" + column + ", ?)")
			arguments = append(arguments, sqliteJSONPathArgument(path))
		} else {
			sql.WriteString(column)
		}
	}
	return arguments, nil
}
