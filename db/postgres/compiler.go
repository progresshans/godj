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
	if _, selected := plan.RelationProjection(); selected {
		return compileRelation(schema, plan)
	}
	for _, condition := range plan.Conditions() {
		if _, related := condition.RelationPath(); related {
			return compileRelation(schema, plan)
		}
	}
	return compileScalar(schema, plan)
}

func compileScalar(schema string, plan query.Plan) (string, []any, error) {
	if err := validateIdentifier(plan.Table()); err != nil {
		return "", nil, invalidPlan("table " + err.Error())
	}
	columns := plan.Columns()
	if len(columns) == 0 {
		return "", nil, invalidPlan("select columns are empty")
	}

	var statement strings.Builder
	statement.WriteString("SELECT ")
	for index, column := range columns {
		if index > 0 {
			statement.WriteString(", ")
		}
		quoted, err := quoteIdentifier(column.Column())
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

	conditions := plan.Conditions()
	arguments := make([]any, 0, len(conditions)+1)
	if len(conditions) > 0 {
		statement.WriteString(" WHERE ")
	}
	for index, condition := range conditions {
		if index > 0 {
			statement.WriteString(" AND ")
		}
		if !containsField(columns, condition.Field()) {
			return "", nil, invalidPlan(fmt.Sprintf("condition field %q is not selected model metadata", condition.Field().Name()))
		}
		field, err := quoteIdentifier(condition.Field().Column())
		if err != nil {
			return "", nil, err
		}
		statement.WriteString(field)
		conditionArguments, err := compileCondition(&statement, condition, len(arguments)+1)
		if err != nil {
			return "", nil, err
		}
		arguments = append(arguments, conditionArguments...)
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
	if limit, ok := plan.Limit(); ok {
		statement.WriteString(" LIMIT ")
		statement.WriteString(placeholder(len(arguments) + 1))
		arguments = append(arguments, int64(limit))
	}
	return statement.String(), arguments, nil
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

func compileRelation(schema string, plan query.Plan) (string, []any, error) {
	if err := validateIdentifier(plan.Table()); err != nil {
		return "", nil, invalidPlan("table " + err.Error())
	}
	columns := plan.Columns()
	if len(columns) == 0 {
		return "", nil, invalidPlan("select columns are empty")
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
			return "", nil, invalidPlan("PostgreSQL IN conditions cannot traverse a relation path")
		}
		hops := path.Hops()
		if len(hops) != 1 {
			return "", nil, invalidPlan("PostgreSQL relation compiler requires exactly one relation hop")
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
					return "", nil, unsupportedRelatedCondition(condition, "PostgreSQL relation compiler supports required forward many-to-one related-field paths only")
				}
			case query.RelationReverse:
				if err := validateReverseRelatedCondition(condition, hop); err != nil {
					return "", nil, err
				}
			default:
				return "", nil, invalidPlan("relation path has an unknown direction")
			}
			if condition.Lookup() != query.LookupExact {
				return "", nil, unsupportedRelatedCondition(condition, "PostgreSQL relation compiler supports exact related lookups only")
			}
			relatedConditions[index] = true
		case query.RelationTerminalSourceKey:
			if err := validateNullableSourceKeyCondition(columns, condition, hop); err != nil {
				return "", nil, err
			}
			sourceKeyHops = append(sourceKeyHops, hop)
			continue
		default:
			return "", nil, invalidPlan("relation path has an unknown terminal scope")
		}

		key := relationKey(hop)
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

	arguments := make([]any, 0, len(conditions)+1)
	if len(conditions) > 0 {
		statement.WriteString(" WHERE ")
	}
	for index, condition := range conditions {
		if index > 0 {
			statement.WriteString(" AND ")
		}
		alias := rootAlias
		if relatedConditions[index] {
			alias = joins[conditionKeys[index]].alias
		}
		field, err := quoteQualified(alias, condition.Field().Column())
		if err != nil {
			return "", nil, err
		}
		statement.WriteString(field)
		conditionArguments, err := compileCondition(&statement, condition, len(arguments)+1)
		if err != nil {
			return "", nil, err
		}
		arguments = append(arguments, conditionArguments...)
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
	if limit, ok := plan.Limit(); ok {
		statement.WriteString(" LIMIT ")
		statement.WriteString(placeholder(len(arguments) + 1))
		arguments = append(arguments, int64(limit))
	}
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
	if !containsField(plan.Columns(), sourceKey) {
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

func compileCondition(statement *strings.Builder, condition query.Condition, firstArgument int) ([]any, error) {
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
		statement.WriteString(" = ")
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
