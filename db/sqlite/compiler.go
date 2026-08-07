package sqlite

import (
	"fmt"
	"strings"

	"github.com/progresshans/godj/query"
)

func Compile(plan query.Plan) (string, []any, error) {
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
		argument, hasArgument, err := compileCondition(&sql, condition)
		if err != nil {
			return "", nil, err
		}
		if hasArgument {
			arguments = append(arguments, argument)
		}
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

func compileCondition(sql *strings.Builder, condition query.Condition) (any, bool, error) {
	field := condition.Field()
	value := condition.Value()
	switch condition.Lookup() {
	case query.LookupExact:
		if !valueMatchesField(value.Kind(), field.Kind()) {
			return nil, false, invalidPlan(fmt.Sprintf("exact value kind %q does not match field %q", value.Kind(), field.Name()))
		}
		argument, err := value.DatabaseValue()
		if err != nil {
			return nil, false, err
		}
		sql.WriteString(" = ?")
		return argument, true, nil
	case query.LookupIContains:
		text, ok := value.String()
		if field.Kind() != query.FieldString || !ok {
			return nil, false, unsupportedLookup(field, condition.Lookup())
		}
		sql.WriteString(" LIKE ? ESCAPE '\\'")
		return "%" + escapeLike(text) + "%", true, nil
	case query.LookupIsNull:
		isNull, ok := value.Boolean()
		if !ok {
			return nil, false, unsupportedLookup(field, condition.Lookup())
		}
		if isNull {
			sql.WriteString(" IS NULL")
		} else {
			sql.WriteString(" IS NOT NULL")
		}
		return nil, false, nil
	default:
		return nil, false, unsupportedLookup(field, condition.Lookup())
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
