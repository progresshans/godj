package postgres

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

const postgresIdentifierMaxBytes = 63

func compilePlan(schema string, plan query.Plan) (string, []any, error) {
	if err := validateSchemaIdentifier(schema); err != nil {
		return "", nil, invalidPlan(err.Error())
	}
	if err := validateIdentifier(plan.Table()); err != nil {
		return "", nil, invalidPlan("table " + err.Error())
	}
	sourceFields, err := validateReadSourceFields(plan.SourceFields())
	if err != nil {
		return "", nil, err
	}
	where, err := analyzeWhere(plan, sourceFields)
	if err != nil {
		return "", nil, err
	}
	_, relationProjection := plan.RelationProjection()
	hasRelation := relationProjection || where.hasRelations

	resultKind := plan.ResultShape().Kind()
	if resultKind != query.ResultModel && hasRelation {
		return "", nil, unsupportedResultShape(
			"PostgreSQL scalar projection and aggregate results cannot traverse or project relations",
		)
	}
	switch resultKind {
	case query.ResultModel, query.ResultProjection:
		if hasRelation {
			return compileRelation(schema, plan, sourceFields, where)
		}
		return compileScalar(schema, plan, sourceFields, where)
	case query.ResultAggregate:
		return compileAggregate(schema, plan, sourceFields, where)
	default:
		return "", nil, invalidPlan("query result kind is invalid")
	}
}

func compileScalar(
	schema string,
	plan query.Plan,
	sourceFields []query.FieldRef,
	where whereAnalysis,
) (string, []any, error) {
	selectedFields, err := scalarSelectedFields(plan, sourceFields)
	if err != nil {
		return "", nil, err
	}
	return compileScalarSelect(schema, plan, sourceFields, selectedFields, where)
}

func compileScalarSelect(
	schema string,
	plan query.Plan,
	sourceFields,
	selectedFields []query.FieldRef,
	where whereAnalysis,
) (string, []any, error) {
	var statement strings.Builder
	statement.WriteString("SELECT ")
	if plan.Distinct() {
		statement.WriteString("DISTINCT ")
	}
	for index, field := range selectedFields {
		if index > 0 {
			statement.WriteString(", ")
		}
		quoted, err := quoteIdentifier(field.Column())
		if err != nil {
			return "", nil, err
		}
		statement.WriteString(quoted)
	}
	statement.WriteString(" FROM ")
	table, err := quoteTable(schema, plan.Table())
	if err != nil {
		return "", nil, err
	}
	statement.WriteString(table)

	arguments, err := appendWhere(&statement, where, scalarWhereField, scalarWhereRHSField)
	if err != nil {
		return "", nil, err
	}

	orderings := plan.Orderings()
	if len(orderings) > 0 {
		statement.WriteString(" ORDER BY ")
	}
	for index, ordering := range orderings {
		if index > 0 {
			statement.WriteString(", ")
		}
		if !containsField(sourceFields, ordering.Field()) {
			return "", nil, invalidPlan(fmt.Sprintf("ordering field %q is not selected model metadata", ordering.Field().Name()))
		}
		if plan.Distinct() && plan.ResultShape().Kind() == query.ResultProjection &&
			!containsField(selectedFields, ordering.Field()) {
			return "", nil, unsupportedDistinctOrdering(ordering.Field())
		}
		field, err := quoteIdentifier(ordering.Field().Column())
		if err != nil {
			return "", nil, err
		}
		statement.WriteString(field)
		switch ordering.Direction() {
		case query.Ascending:
			statement.WriteString(" ASC")
		case query.Descending:
			statement.WriteString(" DESC")
		default:
			return "", nil, invalidPlan("unknown ordering direction")
		}
	}
	appendPagination(&statement, &arguments, plan)
	return statement.String(), arguments, nil
}

func compileAggregate(
	schema string,
	plan query.Plan,
	sourceFields []query.FieldRef,
	where whereAnalysis,
) (string, []any, error) {
	expressions := plan.ResultShape().Expressions()
	if len(expressions) == 0 {
		return "", nil, invalidPlan("aggregate result is empty")
	}
	_, limited := plan.Limit()
	_, offset := plan.Offset()
	if !plan.Distinct() && !limited && !offset {
		return compileDirectAggregate(schema, plan, expressions, sourceFields, where)
	}
	return compileDerivedAggregate(schema, plan, expressions, sourceFields, where)
}

func compileDerivedAggregate(
	schema string,
	plan query.Plan,
	expressions []query.ResultExpression,
	sourceFields []query.FieldRef,
	where whereAnalysis,
) (string, []any, error) {
	const sourceAlias = "godj_source"
	var statement strings.Builder
	statement.WriteString("SELECT ")
	if err := appendAggregateExpressions(&statement, expressions, sourceFields, sourceAlias); err != nil {
		return "", nil, err
	}

	inner, arguments, err := compileScalarSelect(schema, plan, sourceFields, sourceFields, where)
	if err != nil {
		return "", nil, err
	}
	quotedAlias, err := quoteIdentifier(sourceAlias)
	if err != nil {
		return "", nil, err
	}
	statement.WriteString(" FROM (")
	statement.WriteString(inner)
	statement.WriteString(") AS ")
	statement.WriteString(quotedAlias)
	return statement.String(), arguments, nil
}

func compileDirectAggregate(
	schema string,
	plan query.Plan,
	expressions []query.ResultExpression,
	sourceFields []query.FieldRef,
	where whereAnalysis,
) (string, []any, error) {
	table, err := quoteTable(schema, plan.Table())
	if err != nil {
		return "", nil, err
	}

	var statement strings.Builder
	statement.WriteString("SELECT ")
	if err := appendAggregateExpressions(&statement, expressions, sourceFields, ""); err != nil {
		return "", nil, err
	}
	statement.WriteString(" FROM ")
	statement.WriteString(table)

	arguments, err := appendWhere(&statement, where, scalarWhereField, scalarWhereRHSField)
	if err != nil {
		return "", nil, err
	}
	if err := validateOmittedAggregateOrderings(plan.Orderings(), sourceFields); err != nil {
		return "", nil, err
	}
	return statement.String(), arguments, nil
}

const (
	maximumWhereDepth = 64
	maximumWhereNodes = 1024
)

type whereLeaf struct {
	related  bool
	hop      query.RelationHop
	usesJoin bool
}

type whereAnalysis struct {
	expression   query.Expression
	present      bool
	hasRelations bool
	leaves       []whereLeaf
}

type whereAnalyzer struct {
	plan         query.Plan
	sourceFields []query.FieldRef
	visited      int
	leaves       []whereLeaf
}

// analyzeWhere validates the authoritative Boolean tree independently of the
// query constructors. That second boundary is intentional: WithConditions
// preserves malformed historical inputs so every backend must still fail
// closed before database I/O.
func analyzeWhere(plan query.Plan, sourceFields []query.FieldRef) (whereAnalysis, error) {
	expression, present := plan.Where()
	if !present {
		return whereAnalysis{}, nil
	}
	analyzer := whereAnalyzer{plan: plan, sourceFields: sourceFields}
	hasRelations, err := analyzer.walk(expression, 1, true)
	if err != nil {
		return whereAnalysis{}, err
	}
	if expression.HasRelations() != hasRelations {
		return whereAnalysis{}, invalidPlan("query expression relation metadata is malformed")
	}
	return whereAnalysis{
		expression:   expression,
		present:      true,
		hasRelations: hasRelations,
		leaves:       analyzer.leaves,
	}, nil
}

func (a *whereAnalyzer) walk(
	expression query.Expression,
	depth int,
	relationAtRootConjunction bool,
) (bool, error) {
	if depth > maximumWhereDepth {
		return false, invalidPlan("query expression exceeds the maximum depth of 64")
	}
	a.visited++
	if a.visited > maximumWhereNodes {
		return false, invalidPlan("query expression exceeds the maximum node count of 1024")
	}

	kind := expression.Kind()
	children := expression.Children()
	switch kind {
	case query.ExpressionLeaf:
		if len(children) != 0 {
			return false, invalidPlan("query expression leaf is malformed")
		}
		condition, ok := expression.Condition()
		if !ok {
			return false, invalidPlan("query expression leaf is malformed")
		}
		leaf, err := a.analyzeLeaf(condition, relationAtRootConjunction)
		if err != nil {
			return false, err
		}
		a.leaves = append(a.leaves, leaf)
		if expression.HasRelations() != leaf.related {
			return false, invalidPlan("query expression relation metadata is malformed")
		}
		return leaf.related, nil
	case query.ExpressionAnd, query.ExpressionOr:
		if len(children) < 2 {
			return false, invalidPlan("AND and OR query expressions require at least two children")
		}
	case query.ExpressionNot:
		if len(children) != 1 {
			return false, invalidPlan("NOT query expressions require exactly one child")
		}
	default:
		return false, invalidPlan("query expression is zero or malformed")
	}

	childRelationAtRoot := relationAtRootConjunction && kind == query.ExpressionAnd
	hasRelations := false
	for _, child := range children {
		childRelations, err := a.walk(child, depth+1, childRelationAtRoot)
		if err != nil {
			return false, err
		}
		hasRelations = hasRelations || childRelations
	}
	if expression.HasRelations() != hasRelations {
		return false, invalidPlan("query expression relation metadata is malformed")
	}
	return hasRelations, nil
}

func (a *whereAnalyzer) analyzeLeaf(condition query.Condition, relationAtRootConjunction bool) (whereLeaf, error) {
	if err := validateWhereCondition(condition); err != nil {
		return whereLeaf{}, err
	}
	path, related := condition.RelationPath()
	if !related {
		if !containsField(a.sourceFields, condition.Field()) {
			return whereLeaf{}, invalidPlan(fmt.Sprintf("condition field %q is not selected model metadata", condition.Field().Name()))
		}
		if right, ok := condition.RHSField(); ok && !containsField(a.sourceFields, right) {
			return whereLeaf{}, invalidPlan(fmt.Sprintf("condition right-hand-side field %q is not selected model metadata", right.Name()))
		}
		return whereLeaf{}, nil
	}
	if !relationAtRootConjunction {
		return whereLeaf{}, unsupportedBooleanRelation(condition)
	}
	if condition.Lookup() == query.LookupIn {
		return whereLeaf{}, invalidPlan("PostgreSQL IN conditions cannot traverse a relation path")
	}
	hops := path.Hops()
	if len(hops) != 1 {
		return whereLeaf{}, invalidPlan("PostgreSQL relation compiler requires exactly one relation hop")
	}
	hop := hops[0]
	if hop.Direction() == query.RelationForward && hop.SourceTable() != a.plan.Table() {
		return whereLeaf{}, invalidPlan(fmt.Sprintf("relation source table %q does not match plan root table %q", hop.SourceTable(), a.plan.Table()))
	}
	if hop.Direction() == query.RelationReverse && hop.TargetTable() != a.plan.Table() {
		return whereLeaf{}, invalidPlan(fmt.Sprintf("relation target table %q does not match reverse plan root table %q", hop.TargetTable(), a.plan.Table()))
	}
	if !condition.Field().Equal(path.Terminal()) {
		return whereLeaf{}, invalidPlan("related condition field does not match relation path terminal")
	}

	leaf := whereLeaf{related: true, hop: hop}
	switch path.TerminalScope() {
	case query.RelationTerminalRelatedField:
		switch hop.Direction() {
		case query.RelationForward:
			if hop.Cardinality() != ir.RelationManyToOne || hop.Nullable() {
				return whereLeaf{}, unsupportedRelatedCondition(condition, "PostgreSQL relation compiler supports required forward many-to-one related-field paths only")
			}
		case query.RelationReverse:
			if err := validateReverseRelatedCondition(condition, hop); err != nil {
				return whereLeaf{}, err
			}
		default:
			return whereLeaf{}, invalidPlan("relation path has an unknown direction")
		}
		if condition.Lookup() != query.LookupExact {
			return whereLeaf{}, unsupportedRelatedCondition(condition, "PostgreSQL relation compiler supports exact related lookups only")
		}
		leaf.usesJoin = true
	case query.RelationTerminalSourceKey:
		if err := validateNullableSourceKeyCondition(a.sourceFields, condition, hop); err != nil {
			return whereLeaf{}, err
		}
	default:
		return whereLeaf{}, invalidPlan("relation path has an unknown terminal scope")
	}
	return leaf, nil
}

func validateWhereCondition(condition query.Condition) error {
	field := condition.Field()
	if err := validateIdentifier(field.Name()); err != nil {
		return invalidPlan("condition field name " + err.Error())
	}
	if err := validateIdentifier(field.Column()); err != nil {
		return invalidPlan("condition field column " + err.Error())
	}
	switch field.Kind() {
	case query.FieldInteger, query.FieldString, query.FieldBoolean:
	default:
		return invalidPlan(fmt.Sprintf("condition field %q has unsupported kind %q", field.Name(), field.Kind()))
	}
	if right, ok := condition.RHSField(); ok {
		if _, related := condition.RelationPath(); related {
			return invalidPlan("PostgreSQL relation conditions cannot use a field right-hand side")
		}
		if err := validateIdentifier(right.Name()); err != nil {
			return invalidPlan("condition right-hand-side field name " + err.Error())
		}
		if err := validateIdentifier(right.Column()); err != nil {
			return invalidPlan("condition right-hand-side field column " + err.Error())
		}
		if right.Kind() != field.Kind() || (field.Kind() != query.FieldInteger && field.Kind() != query.FieldString) {
			return invalidPlan("PostgreSQL field comparison requires same-kind Integer or String fields")
		}
		if condition.Lookup() != query.LookupExact && !orderedComparisonLookup(condition.Lookup()) {
			return unsupportedLookup(field, condition.Lookup())
		}
		return nil
	}

	switch condition.Lookup() {
	case query.LookupExact:
		if !valueMatchesField(condition.Value().Kind(), field.Kind()) {
			return invalidPlan(fmt.Sprintf("exact value kind %q does not match field %q", condition.Value().Kind(), field.Name()))
		}
	case query.LookupGreaterThan, query.LookupGreaterThanOrEqual, query.LookupLessThan, query.LookupLessThanOrEqual:
		if !orderedValueMatchesField(condition.Value().Kind(), field.Kind()) {
			return unsupportedLookup(field, condition.Lookup())
		}
	case query.LookupIContains:
		if _, ok := condition.Value().String(); field.Kind() != query.FieldString || !ok {
			return unsupportedLookup(field, condition.Lookup())
		}
	case query.LookupIsNull:
		if _, ok := condition.Value().Boolean(); !ok {
			return unsupportedLookup(field, condition.Lookup())
		}
	case query.LookupIn:
		if _, related := condition.RelationPath(); related {
			return invalidPlan("PostgreSQL IN conditions cannot traverse a relation path")
		}
		if _, ok := condition.Values(); !ok {
			return invalidPlan("PostgreSQL IN requires a valid root-table list-backed condition")
		}
	default:
		return unsupportedLookup(field, condition.Lookup())
	}
	return nil
}

type whereFieldResolver func(query.Condition) (string, error)
type whereRHSFieldResolver func(query.FieldRef) (string, error)

func scalarWhereField(condition query.Condition) (string, error) {
	return quoteIdentifier(condition.Field().Column())
}

func scalarWhereRHSField(field query.FieldRef) (string, error) {
	return quoteIdentifier(field.Column())
}

func appendWhere(
	statement *strings.Builder,
	where whereAnalysis,
	resolveField whereFieldResolver,
	resolveRHSField whereRHSFieldResolver,
) ([]any, error) {
	arguments := make([]any, 0, len(where.leaves))
	if !where.present {
		return arguments, nil
	}
	statement.WriteString(" WHERE ")
	if err := appendWhereExpression(statement, where.expression, resolveField, resolveRHSField, &arguments, false); err != nil {
		return nil, err
	}
	return arguments, nil
}

func appendWhereExpression(
	statement *strings.Builder,
	expression query.Expression,
	resolveField whereFieldResolver,
	resolveRHSField whereRHSFieldResolver,
	arguments *[]any,
	negated bool,
) error {
	statement.WriteByte('(')
	switch expression.Kind() {
	case query.ExpressionLeaf:
		condition, ok := expression.Condition()
		if !ok {
			return invalidPlan("query expression leaf is malformed")
		}
		field, err := resolveField(condition)
		if err != nil {
			return err
		}
		rightField, hasRightField := condition.RHSField()
		right := ""
		if hasRightField {
			right, err = resolveRHSField(rightField)
			if err != nil {
				return err
			}
		}
		statement.WriteString(field)
		conditionArguments, err := compileCondition(statement, condition, right, len(*arguments)+1)
		if err != nil {
			return err
		}
		*arguments = append(*arguments, conditionArguments...)
		_, related := condition.RelationPath()
		if negated && !related && nullableNegationGuard(condition.Lookup()) {
			if condition.Field().Nullable() {
				statement.WriteString(" AND ")
				statement.WriteString(field)
				statement.WriteString(" IS NOT NULL")
			}
			if hasRightField && rightField.Nullable() && !rightField.Equal(condition.Field()) {
				statement.WriteString(" AND ")
				statement.WriteString(right)
				statement.WriteString(" IS NOT NULL")
			}
		}
	case query.ExpressionAnd, query.ExpressionOr:
		children := expression.Children()
		operator := " AND "
		if expression.Kind() == query.ExpressionOr {
			operator = " OR "
		}
		for index, child := range children {
			if index > 0 {
				statement.WriteString(operator)
			}
			if err := appendWhereExpression(statement, child, resolveField, resolveRHSField, arguments, negated); err != nil {
				return err
			}
		}
	case query.ExpressionNot:
		children := expression.Children()
		statement.WriteString("NOT ")
		if err := appendWhereExpression(statement, children[0], resolveField, resolveRHSField, arguments, !negated); err != nil {
			return err
		}
	default:
		return invalidPlan("query expression is zero or malformed")
	}
	statement.WriteByte(')')
	return nil
}

func nullableNegationGuard(lookup query.Lookup) bool {
	switch lookup {
	case query.LookupExact, query.LookupGreaterThan, query.LookupGreaterThanOrEqual,
		query.LookupLessThan, query.LookupLessThanOrEqual, query.LookupIContains, query.LookupIn:
		return true
	default:
		return false
	}
}

func validateOmittedAggregateOrderings(orderings []query.Ordering, sourceFields []query.FieldRef) error {
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

func appendAggregateExpressions(
	statement *strings.Builder,
	expressions []query.ResultExpression,
	sourceFields []query.FieldRef,
	sourceAlias string,
) error {
	for index, expression := range expressions {
		if index > 0 {
			statement.WriteString(", ")
		}
		switch expression.Kind() {
		case query.ResultCountAll:
			if _, hasField := expression.Field(); hasField {
				return invalidPlan("COUNT(*) result contains a field")
			}
			statement.WriteString("COUNT(*)")
		case query.ResultMax:
			field, ok := expression.Field()
			if !ok || !containsField(sourceFields, field) ||
				(field.Kind() != query.FieldInteger && field.Kind() != query.FieldString) {
				return invalidPlan("MAX result field is not supported source metadata")
			}
			quoted, err := quoteIdentifier(field.Column())
			if sourceAlias != "" {
				quoted, err = quoteQualified(sourceAlias, field.Column())
			}
			if err != nil {
				return err
			}
			statement.WriteString("MAX(")
			statement.WriteString(quoted)
			statement.WriteByte(')')
		default:
			return invalidPlan("aggregate result contains an unsupported expression")
		}
	}
	return nil
}

func scalarSelectedFields(plan query.Plan, sourceFields []query.FieldRef) ([]query.FieldRef, error) {
	switch plan.ResultShape().Kind() {
	case query.ResultModel:
		return sourceFields, nil
	case query.ResultProjection:
		expressions := plan.ResultShape().Expressions()
		if len(expressions) == 0 {
			return nil, invalidPlan("projection result is empty")
		}
		selected := make([]query.FieldRef, len(expressions))
		for index, expression := range expressions {
			field, ok := expression.Field()
			if expression.Kind() != query.ResultField || !ok || !containsField(sourceFields, field) {
				return nil, invalidPlan("projection result field is not supported source metadata")
			}
			selected[index] = field
		}
		return selected, nil
	default:
		return nil, invalidPlan("scalar compiler requires a model or projection result")
	}
}

func validateReadSourceFields(fields []query.FieldRef) ([]query.FieldRef, error) {
	if len(fields) == 0 {
		return nil, invalidPlan("select columns are empty")
	}
	names := make(map[string]struct{}, len(fields))
	columns := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if err := validateIdentifier(field.Name()); err != nil {
			return nil, invalidPlan("field name " + err.Error())
		}
		if err := validateIdentifier(field.Column()); err != nil {
			return nil, invalidPlan("field column " + err.Error())
		}
		switch field.Kind() {
		case query.FieldInteger, query.FieldString, query.FieldBoolean:
		default:
			return nil, invalidPlan(fmt.Sprintf("field %q has unsupported kind %q", field.Name(), field.Kind()))
		}
		if _, duplicate := names[field.Name()]; duplicate {
			return nil, invalidPlan(fmt.Sprintf("field name %q is duplicated", field.Name()))
		}
		if _, duplicate := columns[field.Column()]; duplicate {
			return nil, invalidPlan(fmt.Sprintf("field column %q is duplicated", field.Column()))
		}
		names[field.Name()] = struct{}{}
		columns[field.Column()] = struct{}{}
	}
	return fields, nil
}

func appendPagination(statement *strings.Builder, arguments *[]any, plan query.Plan) {
	if limit, ok := plan.Limit(); ok {
		statement.WriteString(" LIMIT ")
		statement.WriteString(placeholder(len(*arguments) + 1))
		*arguments = append(*arguments, int64(limit))
	}
	if offset, ok := plan.Offset(); ok {
		statement.WriteString(" OFFSET ")
		statement.WriteString(placeholder(len(*arguments) + 1))
		*arguments = append(*arguments, int64(offset))
	}
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

func compileRelation(
	schema string,
	plan query.Plan,
	columns []query.FieldRef,
	where whereAnalysis,
) (string, []any, error) {
	joinsByKey := make(map[relationJoinKey]query.RelationHop)
	sourceKeyHops := make([]query.RelationHop, 0, len(where.leaves))
	for _, leaf := range where.leaves {
		if !leaf.related {
			continue
		}
		if !leaf.usesJoin {
			sourceKeyHops = append(sourceKeyHops, leaf.hop)
			continue
		}
		key := relationKey(leaf.hop)
		if previous, exists := joinsByKey[key]; exists && !previous.Equal(leaf.hop) {
			return "", nil, invalidPlan(fmt.Sprintf("relation edge %s.%s.%s has inconsistent metadata", key.sourceApp, key.sourceModel, key.field))
		}
		joinsByKey[key] = leaf.hop
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
			return "", nil, invalidPlan("relation edge has inconsistent predicate and projection metadata")
		}
		joinsByKey[projectionKey] = hop
		for key := range joinsByKey {
			if key != projectionKey {
				return "", nil, invalidPlan("PostgreSQL relation projection cannot combine unrelated relation joins")
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
	var statement strings.Builder
	statement.WriteString("SELECT ")
	if plan.Distinct() {
		statement.WriteString("DISTINCT ")
	}
	for index, column := range columns {
		if index > 0 {
			statement.WriteString(", ")
		}
		qualified, err := quoteQualified(rootAlias, column.Column())
		if err != nil {
			return "", nil, err
		}
		statement.WriteString(qualified)
	}
	if selected {
		alias := joins[projectionKey].alias
		for _, column := range projection.TargetColumns() {
			statement.WriteString(", ")
			qualified, err := quoteQualified(alias, column.Column())
			if err != nil {
				return "", nil, err
			}
			statement.WriteString(qualified)
		}
	}
	rootTable, err := quoteTable(schema, plan.Table())
	if err != nil {
		return "", nil, err
	}
	quotedRootAlias, _ := quoteIdentifier(rootAlias)
	statement.WriteString(" FROM ")
	statement.WriteString(rootTable)
	statement.WriteString(" AS ")
	statement.WriteString(quotedRootAlias)
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
		joinedTable, err := quoteTable(schema, joinedTableName)
		if err != nil {
			return "", nil, err
		}
		alias, _ := quoteIdentifier(join.alias)
		rootColumn, err := quoteQualified(rootAlias, rootColumnName)
		if err != nil {
			return "", nil, err
		}
		joinedColumn, err := quoteQualified(join.alias, joinedColumnName)
		if err != nil {
			return "", nil, err
		}
		if join.leftOuter {
			statement.WriteString(" LEFT OUTER JOIN ")
		} else {
			statement.WriteString(" INNER JOIN ")
		}
		statement.WriteString(joinedTable)
		statement.WriteString(" AS ")
		statement.WriteString(alias)
		statement.WriteString(" ON ")
		statement.WriteString(rootColumn)
		statement.WriteString(" = ")
		statement.WriteString(joinedColumn)
	}

	resolveField := func(condition query.Condition) (string, error) {
		alias := rootAlias
		if path, related := condition.RelationPath(); related && path.TerminalScope() == query.RelationTerminalRelatedField {
			hops := path.Hops()
			join, ok := joins[relationKey(hops[0])]
			if !ok {
				return "", invalidPlan("relation predicate join metadata is missing")
			}
			alias = join.alias
		}
		return quoteQualified(alias, condition.Field().Column())
	}
	resolveRHSField := func(field query.FieldRef) (string, error) {
		return quoteQualified(rootAlias, field.Column())
	}
	arguments, err := appendWhere(&statement, where, resolveField, resolveRHSField)
	if err != nil {
		return "", nil, err
	}

	orderings := plan.Orderings()
	if len(orderings) > 0 {
		statement.WriteString(" ORDER BY ")
	}
	for index, ordering := range orderings {
		if index > 0 {
			statement.WriteString(", ")
		}
		if !containsField(columns, ordering.Field()) {
			return "", nil, invalidPlan(fmt.Sprintf("ordering field %q is not selected model metadata", ordering.Field().Name()))
		}
		field, err := quoteQualified(rootAlias, ordering.Field().Column())
		if err != nil {
			return "", nil, err
		}
		statement.WriteString(field)
		switch ordering.Direction() {
		case query.Ascending:
			statement.WriteString(" ASC")
		case query.Descending:
			statement.WriteString(" DESC")
		default:
			return "", nil, invalidPlan("unknown ordering direction")
		}
	}
	appendPagination(&statement, &arguments, plan)
	return statement.String(), arguments, nil
}

func relationKey(hop query.RelationHop) relationJoinKey {
	return relationJoinKey{
		sourceApp:   hop.Source().AppLabel,
		sourceModel: hop.Source().ModelName,
		field:       hop.Field(),
		targetApp:   hop.Target().AppLabel,
		targetModel: hop.Target().ModelName,
		direction:   hop.Direction(),
	}
}

func validateRelationProjection(plan query.Plan, projection query.RelationProjection) (relationJoinKey, error) {
	hop := projection.Hop()
	if hop.Direction() != query.RelationForward || hop.Cardinality() != ir.RelationManyToOne || hop.ReverseName() != "" {
		return relationJoinKey{}, invalidPlan("PostgreSQL relation projection requires one direct forward many-to-one hop")
	}
	if hop.SourceTable() != plan.Table() {
		return relationJoinKey{}, invalidPlan(fmt.Sprintf(
			"relation projection source table %q does not match plan root table %q",
			hop.SourceTable(), plan.Table(),
		))
	}
	if !canonicalRelationIdentity(hop.Source()) || !canonicalRelationIdentity(hop.Target()) ||
		!canonicalIdentifier(hop.SourceTable()) || !canonicalIdentifier(hop.Field()) ||
		!canonicalIdentifier(hop.SourceColumn()) || !canonicalIdentifier(hop.TargetTable()) ||
		!canonicalIdentifier(hop.TargetPrimaryKeyColumn()) {
		return relationJoinKey{}, invalidPlan("relation projection contains non-canonical metadata")
	}
	sourceKey := query.NewFieldRef(hop.Field(), hop.SourceColumn(), query.FieldInteger, hop.Nullable())
	if !containsField(plan.SourceFields(), sourceKey) {
		return relationJoinKey{}, invalidPlan(fmt.Sprintf("relation projection source key %q is not selected model metadata", hop.Field()))
	}
	targetColumns := projection.TargetColumns()
	if len(targetColumns) == 0 {
		return relationJoinKey{}, invalidPlan("relation projection target columns are empty")
	}
	primaryKeyCount := 0
	names := make(map[string]struct{}, len(targetColumns))
	columns := make(map[string]struct{}, len(targetColumns))
	for _, field := range targetColumns {
		if !canonicalIdentifier(field.Name()) || !canonicalIdentifier(field.Column()) ||
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
	return relationKey(hop), nil
}

func validateNullableSourceKeyCondition(columns []query.FieldRef, condition query.Condition, hop query.RelationHop) error {
	field := condition.Field()
	if hop.Direction() != query.RelationForward || hop.Cardinality() != ir.RelationManyToOne || !hop.Nullable() {
		return unsupportedRelatedCondition(condition, "PostgreSQL source-key isnull requires a nullable forward many-to-one path")
	}
	if !canonicalRelationIdentity(hop.Source()) || !canonicalRelationIdentity(hop.Target()) ||
		!canonicalIdentifier(hop.SourceTable()) || !canonicalIdentifier(hop.Field()) ||
		!canonicalIdentifier(hop.SourceColumn()) || !canonicalIdentifier(hop.TargetTable()) ||
		!canonicalIdentifier(hop.TargetPrimaryKeyColumn()) {
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
		return unsupportedRelatedCondition(condition, "PostgreSQL source-key relation paths support isnull only")
	}
	if _, ok := condition.Value().Boolean(); !ok {
		return invalidPlan("PostgreSQL source-key isnull requires a Boolean value")
	}
	return nil
}

func validateReverseRelatedCondition(condition query.Condition, hop query.RelationHop) error {
	if hop.Cardinality() != ir.RelationOneToMany {
		return unsupportedRelatedCondition(condition, "PostgreSQL reverse related-field paths require one-to-many traversal")
	}
	if !canonicalRelationIdentity(hop.Source()) || !canonicalRelationIdentity(hop.Target()) ||
		!canonicalIdentifier(hop.SourceTable()) || !canonicalIdentifier(hop.Field()) ||
		!canonicalIdentifier(hop.SourceColumn()) || !canonicalIdentifier(hop.TargetTable()) ||
		!canonicalIdentifier(hop.TargetPrimaryKeyColumn()) || !canonicalIdentifier(hop.ReverseName()) {
		return invalidPlan("reverse relation path contains non-canonical metadata")
	}
	field := condition.Field()
	if !canonicalIdentifier(field.Name()) || !canonicalIdentifier(field.Column()) || field.Nullable() ||
		(field.Kind() != query.FieldInteger && field.Kind() != query.FieldString) {
		return invalidPlan("reverse relation terminal is non-canonical or unsupported")
	}
	return nil
}

func sameRelationSourceEdge(left, right query.RelationHop) bool {
	return left.Field() == right.Field() && left.SourceColumn() == right.SourceColumn()
}

func canonicalRelationIdentity(identity ir.ModelIdentity) bool {
	return canonicalIdentifier(identity.AppLabel) && canonicalIdentifier(identity.ModelName)
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

func compileCondition(statement *strings.Builder, condition query.Condition, rightField string, firstArgument int) ([]any, error) {
	field := condition.Field()
	value := condition.Value()
	switch condition.Lookup() {
	case query.LookupExact, query.LookupGreaterThan, query.LookupGreaterThanOrEqual,
		query.LookupLessThan, query.LookupLessThanOrEqual:
		operator := comparisonOperator(condition.Lookup())
		if _, fieldRHS := condition.RHSField(); fieldRHS {
			if rightField == "" {
				return nil, invalidPlan("PostgreSQL field comparison binding is missing")
			}
			statement.WriteString(operator)
			statement.WriteString(rightField)
			return nil, nil
		}
		matches := valueMatchesField(value.Kind(), field.Kind())
		if condition.Lookup() != query.LookupExact {
			matches = orderedValueMatchesField(value.Kind(), field.Kind())
		}
		if !matches {
			return nil, invalidPlan(fmt.Sprintf("comparison value kind %q does not match field %q", value.Kind(), field.Name()))
		}
		argument, err := value.DatabaseValue()
		if err != nil {
			return nil, err
		}
		statement.WriteString(operator)
		statement.WriteString(placeholder(firstArgument))
		return []any{argument}, nil
	case query.LookupIContains:
		text, ok := value.String()
		if field.Kind() != query.FieldString || !ok {
			return nil, unsupportedLookup(field, condition.Lookup())
		}
		statement.WriteString(" ILIKE ")
		statement.WriteString(placeholder(firstArgument))
		statement.WriteString(` ESCAPE '\'`)
		return []any{"%" + escapeLike(text) + "%"}, nil
	case query.LookupIsNull:
		isNull, ok := value.Boolean()
		if !ok {
			return nil, unsupportedLookup(field, condition.Lookup())
		}
		if isNull {
			statement.WriteString(" IS NULL")
		} else {
			statement.WriteString(" IS NOT NULL")
		}
		return nil, nil
	case query.LookupIn:
		values, ok := condition.Values()
		if !ok {
			return nil, invalidPlan("PostgreSQL IN requires a valid root-table list-backed condition")
		}
		statement.WriteString(" IN (")
		arguments := make([]any, len(values))
		for index, item := range values {
			if index > 0 {
				statement.WriteString(", ")
			}
			statement.WriteString(placeholder(firstArgument + index))
			argument, err := item.DatabaseValue()
			if err != nil {
				return nil, err
			}
			arguments[index] = argument
		}
		statement.WriteByte(')')
		return arguments, nil
	default:
		return nil, unsupportedLookup(field, condition.Lookup())
	}
}

func orderedComparisonLookup(lookup query.Lookup) bool {
	switch lookup {
	case query.LookupGreaterThan, query.LookupGreaterThanOrEqual, query.LookupLessThan, query.LookupLessThanOrEqual:
		return true
	default:
		return false
	}
}

func orderedValueMatchesField(value query.ValueKind, field query.FieldKind) bool {
	return value == query.ValueInteger && field == query.FieldInteger ||
		value == query.ValueString && field == query.FieldString
}

func comparisonOperator(lookup query.Lookup) string {
	switch lookup {
	case query.LookupExact:
		return " = "
	case query.LookupGreaterThan:
		return " > "
	case query.LookupGreaterThanOrEqual:
		return " >= "
	case query.LookupLessThan:
		return " < "
	case query.LookupLessThanOrEqual:
		return " <= "
	default:
		return ""
	}
}

func validateSchemaIdentifier(identifier string) error {
	if err := validateIdentifier(identifier); err != nil {
		return fmt.Errorf("schema %s", err)
	}
	if identifier == "information_schema" || strings.HasPrefix(identifier, "pg_") {
		return errors.New("schema is reserved by PostgreSQL")
	}
	return nil
}

func validateIdentifier(identifier string) error {
	if !canonicalIdentifier(identifier) {
		return errors.New("identifier must match [a-z_][a-z0-9_]*")
	}
	if len(identifier) > postgresIdentifierMaxBytes {
		return fmt.Errorf("identifier exceeds PostgreSQL's %d-byte limit", postgresIdentifierMaxBytes)
	}
	return nil
}

func canonicalIdentifier(identifier string) bool {
	if identifier == "" {
		return false
	}
	for index, character := range identifier {
		if character == '_' || character >= 'a' && character <= 'z' || index > 0 && character >= '0' && character <= '9' {
			continue
		}
		return false
	}
	return true
}

func quoteIdentifier(identifier string) (string, error) {
	if err := validateIdentifier(identifier); err != nil {
		return "", invalidPlan(err.Error())
	}
	return `"` + identifier + `"`, nil
}

func quoteTable(schema, table string) (string, error) {
	if err := validateSchemaIdentifier(schema); err != nil {
		return "", invalidPlan(err.Error())
	}
	quotedSchema, _ := quoteIdentifier(schema)
	quotedTable, err := quoteIdentifier(table)
	if err != nil {
		return "", err
	}
	return quotedSchema + "." + quotedTable, nil
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

func placeholder(position int) string {
	return "$" + strconv.Itoa(position)
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
		Detail:   "PostgreSQL compiler cannot compile this condition",
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

func unsupportedBooleanRelation(condition query.Condition) error {
	return &query.Error{
		Category: query.CategoryBackend,
		Code:     query.CodeUnsupported,
		Field:    condition.Field().Name(),
		Lookup:   string(condition.Lookup()),
		Detail:   "PostgreSQL relation predicates under OR or NOT are not supported",
	}
}

func unsupportedResultShape(detail string) error {
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
		Detail:   fmt.Sprintf("PostgreSQL DISTINCT projection cannot order by unprojected field %q", field.Name()),
	}
}
