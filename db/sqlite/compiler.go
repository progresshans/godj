package sqlite

import (
	"fmt"
	"sort"
	"strings"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func Compile(plan query.Plan) (string, []any, error) {
	for _, condition := range plan.Conditions() {
		if _, related := condition.RelationPath(); related {
			return compileRelation(plan)
		}
	}
	return compileScalar(plan)
}

// compileScalar is the compatibility-preserving single-table compiler. Keep
// relation-specific qualification and validation out of this path so plans
// built before relation traversal retain their exact SQL and error behavior.
func compileScalar(plan query.Plan) (string, []any, error) {
	if plan.Table() == "" {
		return "", nil, invalidPlan("table is empty")
	}
	columns := plan.Columns()
	if len(columns) == 0 {
		return "", nil, invalidPlan("select columns are empty")
	}

	var sql strings.Builder
	sql.WriteString("SELECT ")
	for index, column := range columns {
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
		if !containsField(columns, condition.Field()) {
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
		if !containsField(columns, ordering.Field()) {
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
	}
	if limit, ok := plan.Limit(); ok {
		sql.WriteString(" LIMIT ?")
		arguments = append(arguments, int64(limit))
	}
	return sql.String(), arguments, nil
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
	hop   query.RelationHop
	alias string
}

func compileRelation(plan query.Plan) (string, []any, error) {
	if plan.Table() == "" {
		return "", nil, invalidPlan("table is empty")
	}
	columns := plan.Columns()
	if len(columns) == 0 {
		return "", nil, invalidPlan("select columns are empty")
	}

	conditions := plan.Conditions()
	joinsByKey := make(map[relationJoinKey]query.RelationHop)
	conditionKeys := make([]relationJoinKey, len(conditions))
	relatedConditions := make([]bool, len(conditions))
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

	keys := make([]relationJoinKey, 0, len(joinsByKey))
	for key := range joinsByKey {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(left, right int) bool {
		return compareRelationJoinKey(keys[left], keys[right]) < 0
	})
	joins := make(map[relationJoinKey]relationJoin, len(keys))
	for index, key := range keys {
		joins[key] = relationJoin{hop: joinsByKey[key], alias: fmt.Sprintf("t%d", index+1)}
	}

	const rootAlias = "t0"
	var sql strings.Builder
	sql.WriteString("SELECT ")
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
		sql.WriteString(" INNER JOIN ")
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
	if limit, ok := plan.Limit(); ok {
		sql.WriteString(" LIMIT ?")
		arguments = append(arguments, int64(limit))
	}
	return sql.String(), arguments, nil
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
