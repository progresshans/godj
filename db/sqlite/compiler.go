package sqlite

import (
	"fmt"
	"sort"
	"strings"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func Compile(plan query.Plan) (string, []any, error) {
	_, selected := plan.RelationProjection()
	related := false
	for _, condition := range plan.Conditions() {
		if _, ok := condition.RelationPath(); ok {
			related = true
			break
		}
	}
	if plan.ResultShape().Kind() != query.ResultModel && (selected || related) {
		return "", nil, unsupportedResult("SQLite projection and aggregate results cannot combine with relation paths or relation projection")
	}
	if selected {
		return compileRelation(plan)
	}
	if related {
		return compileRelation(plan)
	}
	return compileScalar(plan)
}

// compileScalar is the single-table scalar compiler. Keep
// relation-specific qualification and validation out of this path so plans
// built before relation traversal retain their exact SQL and error behavior.
func compileScalar(plan query.Plan) (string, []any, error) {
	if plan.Table() == "" {
		return "", nil, invalidPlan("table is empty")
	}
	sourceFields := plan.SourceFields()
	if err := validateReadSourceFields(sourceFields); err != nil {
		return "", nil, err
	}
	result := plan.ResultShape()
	switch result.Kind() {
	case query.ResultModel:
		if len(result.Expressions()) != 0 {
			return "", nil, invalidPlan("model result contains explicit expressions")
		}
		return compileScalarRows(plan, sourceFields, sourceFields)
	case query.ResultProjection:
		projectionFields, err := scalarProjectionFields(result, sourceFields)
		if err != nil {
			return "", nil, err
		}
		return compileScalarRows(plan, projectionFields, sourceFields)
	case query.ResultAggregate:
		return compileScalarAggregate(plan, result, sourceFields)
	default:
		return "", nil, invalidPlan("query result kind is invalid")
	}
}

func compileScalarRows(plan query.Plan, selectedFields, sourceFields []query.FieldRef) (string, []any, error) {
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

	conditions := plan.Conditions()
	arguments := make([]any, 0, len(conditions)+1)
	if len(conditions) > 0 {
		sql.WriteString(" WHERE ")
	}
	for index, condition := range conditions {
		if index > 0 {
			sql.WriteString(" AND ")
		}
		if !containsField(sourceFields, condition.Field()) {
			return "", nil, invalidPlan(fmt.Sprintf("condition field %q is not selected model metadata", condition.Field().Name()))
		}
		field, err := quoteIdentifier(condition.Field().Column())
		if err != nil {
			return "", nil, err
		}
		sql.WriteString(field)
		conditionArguments, err := compileCondition(&sql, condition)
		if err != nil {
			return "", nil, err
		}
		arguments = append(arguments, conditionArguments...)
	}

	orderings := plan.Orderings()
	if len(orderings) > 0 {
		sql.WriteString(" ORDER BY ")
	}
	for index, ordering := range orderings {
		if index > 0 {
			sql.WriteString(", ")
		}
		if !containsField(sourceFields, ordering.Field()) {
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
			!containsField(selectedFields, ordering.Field()) {
			return "", nil, unsupportedDistinctOrdering(ordering.Field())
		}
	}
	arguments = appendPagination(&sql, arguments, plan)
	return sql.String(), arguments, nil
}

func scalarProjectionFields(result query.ResultShape, sourceFields []query.FieldRef) ([]query.FieldRef, error) {
	expressions := result.Expressions()
	if len(expressions) == 0 {
		return nil, invalidPlan("projection result is empty")
	}
	fields := make([]query.FieldRef, len(expressions))
	for index, expression := range expressions {
		field, ok := expression.Field()
		if expression.Kind() != query.ResultField || !ok || !containsField(sourceFields, field) {
			return nil, invalidPlan("projection result contains a field outside the plan source metadata")
		}
		fields[index] = field
	}
	return fields, nil
}

func compileScalarAggregate(plan query.Plan, result query.ResultShape, sourceFields []query.FieldRef) (string, []any, error) {
	expressions := result.Expressions()
	if len(expressions) == 0 {
		return "", nil, invalidPlan("aggregate result is empty")
	}
	_, limited := plan.Limit()
	_, offset := plan.Offset()
	if !plan.Distinct() && !limited && !offset {
		return compileDirectScalarAggregate(plan, expressions, sourceFields)
	}
	innerSQL, arguments, err := compileScalarRows(plan, sourceFields, sourceFields)
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

func compileDirectScalarAggregate(plan query.Plan, expressions []query.ResultExpression, sourceFields []query.FieldRef) (string, []any, error) {
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

	conditions := plan.Conditions()
	arguments := make([]any, 0, len(conditions))
	if len(conditions) > 0 {
		sql.WriteString(" WHERE ")
	}
	for index, condition := range conditions {
		if index > 0 {
			sql.WriteString(" AND ")
		}
		if !containsField(sourceFields, condition.Field()) {
			return "", nil, invalidPlan(fmt.Sprintf("condition field %q is not selected model metadata", condition.Field().Name()))
		}
		field, err := quoteIdentifier(condition.Field().Column())
		if err != nil {
			return "", nil, err
		}
		sql.WriteString(field)
		conditionArguments, err := compileCondition(&sql, condition)
		if err != nil {
			return "", nil, err
		}
		arguments = append(arguments, conditionArguments...)
	}
	if err := validateOmittedScalarOrderings(plan.Orderings(), sourceFields); err != nil {
		return "", nil, err
	}
	return sql.String(), arguments, nil
}

func validateOmittedScalarOrderings(orderings []query.Ordering, sourceFields []query.FieldRef) error {
	for _, ordering := range orderings {
		if !containsField(sourceFields, ordering.Field()) {
			return invalidPlan(fmt.Sprintf("ordering field %q is not selected model metadata", ordering.Field().Name()))
		}
		if _, err := quoteIdentifier(ordering.Field().Column()); err != nil {
			return err
		}
		switch ordering.Direction() {
		case query.Ascending, query.Descending:
		default:
			return invalidPlan("unknown ordering direction")
		}
	}
	return nil
}

func appendScalarAggregateExpressions(sql *strings.Builder, expressions []query.ResultExpression, sourceFields []query.FieldRef, sourceAlias string) error {
	for index, expression := range expressions {
		if index > 0 {
			sql.WriteString(", ")
		}
		switch expression.Kind() {
		case query.ResultCountAll:
			if _, ok := expression.Field(); ok {
				return invalidPlan("COUNT(*) result contains a field")
			}
			sql.WriteString("COUNT(*)")
		case query.ResultMax:
			field, ok := expression.Field()
			if !ok || !containsField(sourceFields, field) ||
				(field.Kind() != query.FieldInteger && field.Kind() != query.FieldString) {
				return invalidPlan("MAX result requires an integer or string source field")
			}
			quoted, err := quoteIdentifier(field.Column())
			if sourceAlias != "" {
				quoted, err = quoteQualified(sourceAlias, field.Column())
			}
			if err != nil {
				return err
			}
			sql.WriteString("MAX(")
			sql.WriteString(quoted)
			sql.WriteByte(')')
		default:
			return invalidPlan("aggregate result contains an unsupported expression")
		}
	}
	return nil
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

type relationJoinKey struct {
	sourceApp   string
	sourceModel string
	field       string
	targetApp   string
	targetModel string
	direction   query.RelationDirection
}

type relationJoin struct {
	hop       query.RelationHop
	alias     string
	leftOuter bool
}

func compileRelation(plan query.Plan) (string, []any, error) {
	if plan.ResultShape().Kind() != query.ResultModel {
		return "", nil, unsupportedResult("SQLite relation compilation requires a model result")
	}
	if plan.Table() == "" {
		return "", nil, invalidPlan("table is empty")
	}
	columns := plan.SourceFields()
	if err := validateReadSourceFields(columns); err != nil {
		return "", nil, err
	}

	conditions := plan.Conditions()
	joinsByKey := make(map[relationJoinKey]query.RelationHop)
	conditionKeys := make([]relationJoinKey, len(conditions))
	relatedConditions := make([]bool, len(conditions))
	sourceKeyHops := make([]query.RelationHop, 0, len(conditions))
	for index, condition := range conditions {
		path, related := condition.RelationPath()
		if !related {
			if !containsField(columns, condition.Field()) {
				return "", nil, invalidPlan(fmt.Sprintf("condition field %q is not selected model metadata", condition.Field().Name()))
			}
			continue
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
				if err := validateReverseRelatedCondition(condition, hop); err != nil {
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
			if err := validateNullableSourceKeyCondition(columns, condition, hop); err != nil {
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
		key := relationJoinKey{
			sourceApp:   hop.Source().AppLabel,
			sourceModel: hop.Source().ModelName,
			field:       hop.Field(),
			targetApp:   hop.Target().AppLabel,
			targetModel: hop.Target().ModelName,
			direction:   hop.Direction(),
		}
		if previous, exists := joinsByKey[key]; exists && !previous.Equal(hop) {
			return "", nil, invalidPlan(fmt.Sprintf("relation edge %s.%s.%s has inconsistent metadata", key.sourceApp, key.sourceModel, key.field))
		}
		joinsByKey[key] = hop
		conditionKeys[index] = key
	}

	projection, selected := plan.RelationProjection()
	var projectionKey relationJoinKey
	if selected {
		var err error
		projectionKey, err = validateRelationProjection(plan, projection)
		if err != nil {
			return "", nil, err
		}
		hop := projection.Hop()
		for _, sourceKeyHop := range sourceKeyHops {
			if sameRelationSourceEdge(sourceKeyHop, hop) && !sourceKeyHop.Equal(hop) {
				return "", nil, invalidPlan("relation projection source-key provenance does not match the selected edge")
			}
		}
		if previous, exists := joinsByKey[projectionKey]; exists && !previous.Equal(hop) {
			return "", nil, invalidPlan(fmt.Sprintf(
				"relation edge %s.%s.%s has inconsistent predicate and projection metadata",
				projectionKey.sourceApp,
				projectionKey.sourceModel,
				projectionKey.field,
			))
		}
		joinsByKey[projectionKey] = hop
		for key := range joinsByKey {
			if key != projectionKey {
				return "", nil, invalidPlan("SQLite relation projection cannot combine unrelated relation joins")
			}
		}
	}

	keys := make([]relationJoinKey, 0, len(joinsByKey))
	for key := range joinsByKey {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(left, right int) bool {
		return compareRelationJoinKey(keys[left], keys[right]) < 0
	})
	joins := make(map[relationJoinKey]relationJoin, len(keys))
	for index, key := range keys {
		hop := joinsByKey[key]
		joins[key] = relationJoin{
			hop:       hop,
			alias:     fmt.Sprintf("t%d", index+1),
			leftOuter: selected && key == projectionKey && hop.Nullable(),
		}
	}

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
		alias := joins[projectionKey].alias
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
		joinedTableName := join.hop.TargetTable()
		rootColumnName := join.hop.SourceColumn()
		joinedColumnName := join.hop.TargetPrimaryKeyColumn()
		if join.hop.Direction() == query.RelationReverse {
			joinedTableName = join.hop.SourceTable()
			rootColumnName = join.hop.TargetPrimaryKeyColumn()
			joinedColumnName = join.hop.SourceColumn()
		}
		joinedTable, err := quoteIdentifier(joinedTableName)
		if err != nil {
			return "", nil, err
		}
		alias, err := quoteIdentifier(join.alias)
		if err != nil {
			return "", nil, err
		}
		rootColumn, err := quoteQualified(rootAlias, rootColumnName)
		if err != nil {
			return "", nil, err
		}
		joinedColumn, err := quoteQualified(join.alias, joinedColumnName)
		if err != nil {
			return "", nil, err
		}
		if join.leftOuter {
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

	arguments := make([]any, 0, len(conditions)+1)
	if len(conditions) > 0 {
		sql.WriteString(" WHERE ")
	}
	for index, condition := range conditions {
		if index > 0 {
			sql.WriteString(" AND ")
		}
		alias := rootAlias
		if relatedConditions[index] {
			alias = joins[conditionKeys[index]].alias
		}
		field, err := quoteQualified(alias, condition.Field().Column())
		if err != nil {
			return "", nil, err
		}
		sql.WriteString(field)
		conditionArguments, err := compileCondition(&sql, condition)
		if err != nil {
			return "", nil, err
		}
		arguments = append(arguments, conditionArguments...)
	}

	orderings := plan.Orderings()
	if len(orderings) > 0 {
		sql.WriteString(" ORDER BY ")
	}
	for index, ordering := range orderings {
		if index > 0 {
			sql.WriteString(", ")
		}
		if !containsField(columns, ordering.Field()) {
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

func validateRelationProjection(plan query.Plan, projection query.RelationProjection) (relationJoinKey, error) {
	hop := projection.Hop()
	if hop.Direction() != query.RelationForward || hop.Cardinality() != ir.RelationManyToOne || hop.ReverseName() != "" {
		return relationJoinKey{}, invalidPlan("SQLite relation projection requires one direct forward many-to-one hop")
	}
	if hop.SourceTable() != plan.Table() {
		return relationJoinKey{}, invalidPlan(fmt.Sprintf(
			"relation projection source table %q does not match plan root table %q",
			hop.SourceTable(),
			plan.Table(),
		))
	}
	if !canonicalRelationIdentity(hop.Source()) || !canonicalRelationIdentity(hop.Target()) ||
		!canonicalRelationIdentifier(hop.SourceTable()) || !canonicalRelationIdentifier(hop.Field()) ||
		!canonicalRelationIdentifier(hop.SourceColumn()) || !canonicalRelationIdentifier(hop.TargetTable()) ||
		!canonicalRelationIdentifier(hop.TargetPrimaryKeyColumn()) {
		return relationJoinKey{}, invalidPlan("relation projection contains non-canonical metadata")
	}
	sourceKey := query.NewFieldRef(hop.Field(), hop.SourceColumn(), query.FieldInteger, hop.Nullable())
	if !containsField(plan.SourceFields(), sourceKey) {
		return relationJoinKey{}, invalidPlan(fmt.Sprintf(
			"relation projection source key %q is not selected model metadata",
			hop.Field(),
		))
	}
	targetColumns := projection.TargetColumns()
	if len(targetColumns) == 0 {
		return relationJoinKey{}, invalidPlan("relation projection target columns are empty")
	}
	primaryKeyCount := 0
	names := make(map[string]struct{}, len(targetColumns))
	columns := make(map[string]struct{}, len(targetColumns))
	for _, field := range targetColumns {
		if !canonicalRelationIdentifier(field.Name()) || !canonicalRelationIdentifier(field.Column()) ||
			(field.Kind() != query.FieldInteger && field.Kind() != query.FieldString && field.Kind() != query.FieldBoolean) {
			return relationJoinKey{}, invalidPlan("relation projection contains an unsupported target field")
		}
		if _, exists := names[field.Name()]; exists {
			return relationJoinKey{}, invalidPlan("relation projection contains a duplicate target field")
		}
		if _, exists := columns[field.Column()]; exists {
			return relationJoinKey{}, invalidPlan("relation projection contains a duplicate target column")
		}
		names[field.Name()] = struct{}{}
		columns[field.Column()] = struct{}{}
		if field.Column() == hop.TargetPrimaryKeyColumn() {
			if field.Kind() != query.FieldInteger || field.Nullable() {
				return relationJoinKey{}, invalidPlan("relation projection target primary key must be a non-null integer")
			}
			primaryKeyCount++
		}
	}
	if primaryKeyCount != 1 {
		return relationJoinKey{}, invalidPlan("relation projection must contain its target primary key exactly once")
	}
	return relationJoinKey{
		sourceApp:   hop.Source().AppLabel,
		sourceModel: hop.Source().ModelName,
		field:       hop.Field(),
		targetApp:   hop.Target().AppLabel,
		targetModel: hop.Target().ModelName,
		direction:   hop.Direction(),
	}, nil
}

func sameRelationSourceEdge(left, right query.RelationHop) bool {
	return left.Field() == right.Field() &&
		left.SourceColumn() == right.SourceColumn()
}

func validateNullableSourceKeyCondition(
	columns []query.FieldRef,
	condition query.Condition,
	hop query.RelationHop,
) error {
	field := condition.Field()
	if hop.Direction() != query.RelationForward || hop.Cardinality() != ir.RelationManyToOne || !hop.Nullable() {
		return unsupportedRelatedCondition(condition, "SQLite source-key isnull requires a nullable forward many-to-one path")
	}
	if !canonicalRelationIdentity(hop.Source()) || !canonicalRelationIdentity(hop.Target()) ||
		!canonicalRelationIdentifier(hop.SourceTable()) || !canonicalRelationIdentifier(hop.Field()) ||
		!canonicalRelationIdentifier(hop.SourceColumn()) || !canonicalRelationIdentifier(hop.TargetTable()) ||
		!canonicalRelationIdentifier(hop.TargetPrimaryKeyColumn()) {
		return invalidPlan("nullable source-key relation path contains non-canonical metadata")
	}
	if field.Kind() != query.FieldInteger || !field.Nullable() ||
		field.Name() != hop.Field() || field.Column() != hop.SourceColumn() {
		return invalidPlan("nullable source-key relation terminal does not match the hop source key")
	}
	if !containsField(columns, field) {
		return invalidPlan(fmt.Sprintf("relation source key %q is not selected model metadata", field.Name()))
	}
	if condition.Lookup() != query.LookupIsNull {
		return unsupportedRelatedCondition(condition, "SQLite source-key relation paths support isnull only")
	}
	if _, ok := condition.Value().Boolean(); !ok {
		return invalidPlan("SQLite source-key isnull requires a Boolean value")
	}
	return nil
}

func validateReverseRelatedCondition(condition query.Condition, hop query.RelationHop) error {
	if hop.Cardinality() != ir.RelationOneToMany {
		return unsupportedRelatedCondition(condition, "SQLite reverse related-field paths require one-to-many traversal")
	}
	if !canonicalRelationIdentity(hop.Source()) || !canonicalRelationIdentity(hop.Target()) ||
		!canonicalRelationIdentifier(hop.SourceTable()) || !canonicalRelationIdentifier(hop.Field()) ||
		!canonicalRelationIdentifier(hop.SourceColumn()) || !canonicalRelationIdentifier(hop.TargetTable()) ||
		!canonicalRelationIdentifier(hop.TargetPrimaryKeyColumn()) || !canonicalRelationIdentifier(hop.ReverseName()) {
		return invalidPlan("reverse relation path contains non-canonical metadata")
	}
	field := condition.Field()
	if !canonicalRelationIdentifier(field.Name()) || !canonicalRelationIdentifier(field.Column()) || field.Nullable() ||
		(field.Kind() != query.FieldInteger && field.Kind() != query.FieldString) {
		return invalidPlan("reverse relation terminal is non-canonical or unsupported")
	}
	return nil
}

func canonicalRelationIdentity(identity ir.ModelIdentity) bool {
	return canonicalRelationIdentifier(identity.AppLabel) && canonicalRelationIdentifier(identity.ModelName)
}

func canonicalRelationIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for index, character := range value {
		if character == '_' || character >= 'a' && character <= 'z' || index > 0 && character >= '0' && character <= '9' {
			continue
		}
		return false
	}
	return true
}

func compareRelationJoinKey(left, right relationJoinKey) int {
	leftParts := [...]string{left.sourceApp, left.sourceModel, left.field, left.targetApp, left.targetModel, string(left.direction)}
	rightParts := [...]string{right.sourceApp, right.sourceModel, right.field, right.targetApp, right.targetModel, string(right.direction)}
	for index := range leftParts {
		if comparison := strings.Compare(leftParts[index], rightParts[index]); comparison != 0 {
			return comparison
		}
	}
	return 0
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

func compileCondition(sql *strings.Builder, condition query.Condition) ([]any, error) {
	field := condition.Field()
	value := condition.Value()
	switch condition.Lookup() {
	case query.LookupExact:
		if !valueMatchesField(value.Kind(), field.Kind()) {
			return nil, invalidPlan(fmt.Sprintf("exact value kind %q does not match field %q", value.Kind(), field.Name()))
		}
		argument, err := value.DatabaseValue()
		if err != nil {
			return nil, err
		}
		sql.WriteString(" = ?")
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

func containsField(columns []query.FieldRef, candidate query.FieldRef) bool {
	for _, column := range columns {
		if column.Equal(candidate) {
			return true
		}
	}
	return false
}

func valueMatchesField(value query.ValueKind, field query.FieldKind) bool {
	return (value == query.ValueInteger && field == query.FieldInteger) ||
		(value == query.ValueString && field == query.FieldString) ||
		(value == query.ValueBoolean && field == query.FieldBoolean)
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
