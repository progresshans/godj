package sqlite

import (
	"fmt"
	"strings"

	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func Compile(plan query.Plan) (string, []any, error) {
	where, err := analyzeWhere(plan)
	if err != nil {
		return "", nil, err
	}
	_, selected := plan.RelationProjection()
	related := where.hasRelations
	if related && !selected && plan.ResultShape().IsCountAll() {
		inner, arguments, err := compileRelation(plan, where)
		if err != nil {
			return "", nil, err
		}
		return `SELECT COUNT(*) FROM (` + inner + `) AS "godj_count_source"`, arguments, nil
	}
	if plan.ResultShape().Kind() != query.ResultModel && (selected || related) {
		return "", nil, unsupportedResult("SQLite projection and aggregate results cannot combine with relation paths or relation projection")
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
		return compileScalarRows(plan, sourceFields, sourceFields, where)
	case query.ResultProjection:
		projectionFields, err := queryplan.ProjectionFields(result, sourceFields)
		if err != nil {
			return "", nil, err
		}
		return compileScalarRows(plan, projectionFields, sourceFields, where)
	case query.ResultAggregate:
		return compileScalarAggregate(plan, result, sourceFields, where)
	default:
		return "", nil, invalidPlan("query result kind is invalid")
	}
}

func compileScalarRows(plan query.Plan, selectedFields, sourceFields []query.FieldRef, where *sqliteWhereAnalysis) (string, []any, error) {
	if len(selectedFields) == 0 {
		return "", nil, invalidPlan("select columns are empty")
	}

	var sql strings.Builder
	sql.WriteString("SELECT ")
	if plan.Distinct() {
		sql.WriteString("DISTINCT ")
	}
	for index, column := range selectedFields {
		if index > 0 {
			sql.WriteString(", ")
		}
		quoted, err := quoteIdentifier(column.Column())
		if err != nil {
			return "", nil, err
		}
		sql.WriteString(quoted)
	}
	sql.WriteString(" FROM ")
	table, err := quoteIdentifier(plan.Table())
	if err != nil {
		return "", nil, err
	}
	sql.WriteString(table)

	arguments, err := appendWhere(&sql, where)
	if err != nil {
		return "", nil, err
	}

	orderings := plan.Orderings()
	if len(orderings) > 0 {
		sql.WriteString(" ORDER BY ")
	}
	for index, ordering := range orderings {
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
			!queryplan.ContainsField(selectedFields, ordering.Field()) {
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
	innerSQL, arguments, err := compileScalarRows(plan, sourceFields, sourceFields, where)
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
	return queryplan.AppendAggregates(sql, expressions, sourceFields, func(field query.FieldRef) (string, error) {
		if sourceAlias != "" {
			return quoteQualified(sourceAlias, field.Column())
		}
		return quoteIdentifier(field.Column())
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
	if plan.ResultShape().Kind() != query.ResultModel && !plan.ResultShape().IsCountAll() {
		return "", nil, unsupportedResult("SQLite relation compilation requires a model result or COUNT(*)")
	}
	if plan.Table() == "" {
		return "", nil, invalidPlan("table is empty")
	}
	columns := plan.SourceFields()
	if err := validateReadSourceFields(columns); err != nil {
		return "", nil, err
	}

	joinsByKey := make(map[queryplan.RelationKey]query.RelationHop)
	conditionKeys := make([]queryplan.RelationKey, len(where.leaves))
	relatedConditions := make([]bool, len(where.leaves))
	sourceKeyHops := make([]query.RelationHop, 0, len(where.leaves))
	for index, leaf := range where.leaves {
		condition := leaf.condition
		path, related := condition.RelationPath()
		if !related {
			if !queryplan.ContainsField(columns, condition.Field()) {
				return "", nil, invalidPlan(fmt.Sprintf("condition field %q is not selected model metadata", condition.Field().Name()))
			}
			if right, ok := condition.RHSField(); ok && !queryplan.ContainsField(columns, right) {
				return "", nil, invalidPlan(fmt.Sprintf("condition right-hand-side field %q is not selected model metadata", right.Name()))
			}
			continue
		}
		if _, ok := condition.RHSField(); ok {
			return "", nil, invalidPlan("SQLite relation conditions cannot use a field-reference right-hand side")
		}
		if condition.Lookup() == query.LookupIn {
			return "", nil, invalidPlan("SQLite IN conditions cannot traverse a relation path")
		}
		hops := path.Hops()
		if len(hops) != 1 {
			return "", nil, invalidPlan("SQLite relation compiler requires exactly one relation hop")
		}
		hop := hops[0]
		if hop.Direction() == query.RelationForward && hop.SourceTable() != plan.Table() {
			return "", nil, invalidPlan(fmt.Sprintf("relation source table %q does not match plan root table %q", hop.SourceTable(), plan.Table()))
		}
		if hop.Direction() == query.RelationReverse && hop.TargetTable() != plan.Table() {
			return "", nil, invalidPlan(fmt.Sprintf("relation target table %q does not match reverse plan root table %q", hop.TargetTable(), plan.Table()))
		}
		if !condition.Field().Equal(path.Terminal()) {
			return "", nil, invalidPlan("related condition field does not match relation path terminal")
		}

		switch path.TerminalScope() {
		case query.RelationTerminalRelatedField:
			switch hop.Direction() {
			case query.RelationForward:
				if hop.Cardinality() != ir.RelationManyToOne || hop.Nullable() {
					return "", nil, unsupportedRelatedCondition(condition, "SQLite relation compiler supports required forward many-to-one related-field paths only")
				}
			case query.RelationReverse:
				if err := queryplan.ReverseCondition(condition, hop, "SQLite"); err != nil {
					return "", nil, err
				}
			default:
				return "", nil, invalidPlan("relation path has an unknown direction")
			}
			if condition.Lookup() != query.LookupExact {
				return "", nil, unsupportedRelatedCondition(condition, "SQLite relation compiler supports exact related lookups only")
			}
			relatedConditions[index] = true
		case query.RelationTerminalSourceKey:
			if err := queryplan.NullableSourceKey(columns, condition, hop, "SQLite"); err != nil {
				return "", nil, err
			}
			sourceKeyHops = append(sourceKeyHops, hop)
			// A nullable source-key path retains relation provenance in the
			// plan, but compiles against the root alias without allocating a
			// JOIN. Its condition key intentionally remains the zero value.
			continue
		default:
			return "", nil, invalidPlan("relation path has an unknown terminal scope")
		}
		key := queryplan.KeyForRelation(hop)
		if previous, exists := joinsByKey[key]; exists && !previous.Equal(hop) {
			return "", nil, invalidPlan(fmt.Sprintf("relation edge %s.%s.%s has inconsistent metadata", key.SourceApp, key.SourceModel, key.Field))
		}
		joinsByKey[key] = hop
		conditionKeys[index] = key
	}

	prepared, err := queryplan.PrepareJoins(plan, joinsByKey, sourceKeyHops, "SQLite")
	if err != nil {
		return "", nil, err
	}
	keys, joins, projectionKey := prepared.Keys, prepared.ByKey, prepared.ProjectionKey
	projection, selected := plan.RelationProjection()

	const rootAlias = "t0"
	var sql strings.Builder
	sql.WriteString("SELECT ")
	if plan.Distinct() {
		sql.WriteString("DISTINCT ")
	}
	for index, column := range columns {
		if index > 0 {
			sql.WriteString(", ")
		}
		qualified, err := quoteQualified(rootAlias, column.Column())
		if err != nil {
			return "", nil, err
		}
		sql.WriteString(qualified)
	}
	if selected {
		alias := joins[projectionKey].Alias
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
		rootColumn, err := quoteQualified(rootAlias, join.RootColumn)
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

	for index, leaf := range where.leaves {
		alias := rootAlias
		if relatedConditions[index] {
			alias = joins[conditionKeys[index]].Alias
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
	arguments, err := appendWhere(&sql, where)
	if err != nil {
		return "", nil, err
	}

	orderings := plan.Orderings()
	if len(orderings) > 0 {
		sql.WriteString(" ORDER BY ")
	}
	for index, ordering := range orderings {
		if index > 0 {
			sql.WriteString(", ")
		}
		if !queryplan.ContainsField(columns, ordering.Field()) {
			return "", nil, invalidPlan(fmt.Sprintf("ordering field %q is not selected model metadata", ordering.Field().Name()))
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

func compileCondition(sql *strings.Builder, condition query.Condition, rhsFieldSQL string) ([]any, error) {
	field := condition.Field()
	if right, ok := condition.RHSField(); ok {
		if rhsFieldSQL == "" {
			return nil, invalidPlan("SQLite field comparison is missing its right-hand-side binding")
		}
		if field.Kind() != right.Kind() ||
			(field.Kind() != query.FieldInteger && field.Kind() != query.FieldString) {
			return nil, invalidPlan("SQLite field comparison requires same-kind Integer or String fields")
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
	case query.LookupExact:
		if !queryplan.ValueMatchesField(value.Kind(), field.Kind()) {
			return nil, invalidPlan(fmt.Sprintf("exact value kind %q does not match field %q", value.Kind(), field.Name()))
		}
		argument, err := value.DatabaseValue()
		if err != nil {
			return nil, err
		}
		sql.WriteString(" = ?")
		return []any{argument}, nil
	case query.LookupGreaterThan, query.LookupGreaterThanOrEqual, query.LookupLessThan, query.LookupLessThanOrEqual:
		if !queryplan.OrderedValueMatchesField(value.Kind(), field.Kind()) {
			return nil, invalidPlan(fmt.Sprintf("ordered value kind %q does not match field %q", value.Kind(), field.Name()))
		}
		argument, err := value.DatabaseValue()
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
		return []any{"%" + escapeLike(text) + "%"}, nil
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
		values, ok := condition.Values()
		if !ok {
			return nil, invalidPlan("SQLite IN requires a valid root-table list-backed condition")
		}
		sql.WriteString(" IN (")
		arguments := make([]any, len(values))
		for index, item := range values {
			if index > 0 {
				sql.WriteString(", ")
			}
			sql.WriteByte('?')
			argument, err := item.DatabaseValue()
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
		if _, err := quoteIdentifier(field.Name()); err != nil {
			return invalidPlan(fmt.Sprintf("field name %q is empty or contains NUL", field.Name()))
		}
		if _, err := quoteIdentifier(field.Column()); err != nil {
			return invalidPlan(fmt.Sprintf("field column %q is empty or contains NUL", field.Column()))
		}
		switch field.Kind() {
		case query.FieldInteger, query.FieldString, query.FieldBoolean:
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

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
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
