package postgres

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/progresshans/godj/db/internal/queryplan"
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

	if where.hasRelations && !relationProjection && plan.ResultShape().IsCountAll() {
		inner, arguments, err := compileRelation(schema, plan, sourceFields, where)
		if err != nil {
			return "", nil, err
		}
		return `SELECT COUNT(*) FROM (` + inner + `) AS "godj_count_source"`, arguments, nil
	}
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
	selectedFields := sourceFields
	if plan.ResultShape().Kind() == query.ResultProjection {
		var err error
		selectedFields, err = queryplan.ProjectionFields(plan.ResultShape(), sourceFields)
		if err != nil {
			return "", nil, err
		}
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
		if !queryplan.ContainsField(sourceFields, ordering.Field()) {
			return "", nil, invalidPlan(fmt.Sprintf("ordering field %q is not selected model metadata", ordering.Field().Name()))
		}
		if plan.Distinct() && plan.ResultShape().Kind() == query.ResultProjection &&
			!queryplan.ContainsField(selectedFields, ordering.Field()) {
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
	if err := queryplan.OmittedOrderings(plan.Orderings(), sourceFields, quoteIdentifier); err != nil {
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
// query constructors. Identifier, capability and source metadata checks remain
// an independent boundary before database I/O.
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
		if !queryplan.ContainsField(a.sourceFields, condition.Field()) {
			return whereLeaf{}, invalidPlan(fmt.Sprintf("condition field %q is not selected model metadata", condition.Field().Name()))
		}
		if right, ok := condition.RHSField(); ok && !queryplan.ContainsField(a.sourceFields, right) {
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
			if err := queryplan.ReverseCondition(condition, hop, "PostgreSQL"); err != nil {
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
		if err := queryplan.NullableSourceKey(a.sourceFields, condition, hop, "PostgreSQL"); err != nil {
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
		if !queryplan.ValueMatchesField(condition.Value().Kind(), field.Kind()) {
			return invalidPlan(fmt.Sprintf("exact value kind %q does not match field %q", condition.Value().Kind(), field.Name()))
		}
	case query.LookupGreaterThan, query.LookupGreaterThanOrEqual, query.LookupLessThan, query.LookupLessThanOrEqual:
		if !queryplan.OrderedValueMatchesField(condition.Value().Kind(), field.Kind()) {
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

func appendAggregateExpressions(statement *strings.Builder, expressions []query.ResultExpression, sourceFields []query.FieldRef, sourceAlias string) error {
	return queryplan.AppendAggregates(statement, expressions, sourceFields, func(field query.FieldRef) (string, error) {
		if sourceAlias != "" {
			return quoteQualified(sourceAlias, field.Column())
		}
		return quoteIdentifier(field.Column())
	})
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

func compileRelation(
	schema string,
	plan query.Plan,
	columns []query.FieldRef,
	where whereAnalysis,
) (string, []any, error) {
	joinsByKey := make(map[queryplan.RelationKey]query.RelationHop)
	sourceKeyHops := make([]query.RelationHop, 0, len(where.leaves))
	for _, leaf := range where.leaves {
		if !leaf.related {
			continue
		}
		if !leaf.usesJoin {
			sourceKeyHops = append(sourceKeyHops, leaf.hop)
			continue
		}
		key := queryplan.KeyForRelation(leaf.hop)
		if previous, exists := joinsByKey[key]; exists && !previous.Equal(leaf.hop) {
			return "", nil, invalidPlan(fmt.Sprintf("relation edge %s.%s.%s has inconsistent metadata", key.SourceApp, key.SourceModel, key.Field))
		}
		joinsByKey[key] = leaf.hop
	}

	prepared, err := queryplan.PrepareJoins(plan, joinsByKey, sourceKeyHops, "PostgreSQL")
	if err != nil {
		return "", nil, err
	}
	keys, joins, projectionKey := prepared.Keys, prepared.ByKey, prepared.ProjectionKey
	projection, selected := plan.RelationProjection()

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
		alias := joins[projectionKey].Alias
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
		joinedTable, err := quoteTable(schema, join.Table)
		if err != nil {
			return "", nil, err
		}
		alias, _ := quoteIdentifier(join.Alias)
		rootColumn, err := quoteQualified(rootAlias, join.RootColumn)
		if err != nil {
			return "", nil, err
		}
		joinedColumn, err := quoteQualified(join.Alias, join.Column)
		if err != nil {
			return "", nil, err
		}
		if join.LeftOuter {
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
			join, ok := joins[queryplan.KeyForRelation(hops[0])]
			if !ok {
				return "", invalidPlan("relation predicate join metadata is missing")
			}
			alias = join.Alias
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
		if !queryplan.ContainsField(columns, ordering.Field()) {
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
		matches := queryplan.ValueMatchesField(value.Kind(), field.Kind())
		if condition.Lookup() != query.LookupExact {
			matches = queryplan.OrderedValueMatchesField(value.Kind(), field.Kind())
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
	if !queryplan.CanonicalIdentifier(identifier) {
		return errors.New("identifier must match [a-z_][a-z0-9_]*")
	}
	if len(identifier) > postgresIdentifierMaxBytes {
		return fmt.Errorf("identifier exceeds PostgreSQL's %d-byte limit", postgresIdentifierMaxBytes)
	}
	return nil
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
