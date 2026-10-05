package queryplan

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/progresshans/godj/query"
)

// BulkUpdateSource is compiled by the dialect. A direct predicate preserves
// native UPDATE rechecks after a PostgreSQL lock wait; a joined selection keeps
// statement-snapshot membership. Where includes its leading WHERE, if present.
type BulkUpdateSource struct {
	Selection, Where, Alias string
	Arguments               []any
	Direct                  bool
}

// BulkUpdateParts binds the predicate or SELECT compiler to a native UPDATE
// without a read round trip. Reusable positional keys bind CASE and membership.
type BulkUpdateParts struct {
	spec                         query.BulkUpdateSpec
	Table, Key, Scope, Selection string
	Where, Alias                 string
	Direct                       bool
	Columns                      []string
	Arguments                    []any
	BatchSize                    int
}

func PrepareBulkUpdate(spec query.BulkUpdateSpec, parameterLimit int, compileSource func(query.Plan) (BulkUpdateSource, error), quoteTable, quoteIdentifier func(string) (string, error), columnKey func(string) string) (BulkUpdateParts, error) {
	var zero BulkUpdateParts
	if err := spec.Validate(); err != nil {
		return zero, err
	}
	if columnKey == nil {
		columnKey = func(value string) string { return value }
	}
	selection := spec.Selection()
	table, err := quoteTable(selection.Table())
	if err != nil {
		return zero, err
	}
	key, err := quoteIdentifier(spec.Key().Column())
	if err != nil {
		return zero, err
	}
	physical := make(map[string]bool)
	for _, field := range selection.SourceFields() {
		name := columnKey(field.Column())
		if physical[name] {
			return zero, invalidPlan("bulk update source repeats a physical column")
		}
		physical[name] = true
	}
	fields := spec.Fields()
	columns := make([]string, len(fields))
	for index, field := range fields {
		if columnKey(field.Column()) == columnKey(spec.Key().Column()) {
			return zero, invalidPlan("bulk update cannot assign its physical primary key")
		}
		columns[index], err = quoteIdentifier(field.Column())
		if err != nil {
			return zero, err
		}
	}
	source, err := compileSource(selection)
	if err != nil {
		return zero, err
	}
	if source.Direct && source.Selection != "" || !source.Direct && (source.Selection == "" || source.Where != "" || source.Alias != "") {
		return zero, invalidPlan("bulk update compiler returned an ambiguous source")
	}
	arguments := source.Arguments
	if parameterLimit <= 0 || len(arguments) >= parameterLimit {
		return zero, invalidPlan("bulk update predicate exhausts the backend parameter budget")
	}
	batchSize := min(query.MaximumBulkRows, query.MaximumBulkValues/(len(fields)+1), (parameterLimit-len(arguments))/(len(fields)+1))
	if batchSize < 1 {
		return zero, invalidPlan("bulk update has no parameter budget for one row")
	}
	// SQLite resolves unqualified table names against CTE names. Never shadow
	// a physical table retained by any predicate, including related tables.
	tables := map[string]bool{columnKey(selection.Table()): true}
	for _, condition := range selection.Conditions() {
		if path, ok := condition.RelationPath(); ok {
			for _, hop := range path.Hops() {
				tables[columnKey(hop.SourceTable())], tables[columnKey(hop.TargetTable())] = true, true
			}
		}
	}
	scope := "godj_bulk_targets"
	for index := 0; tables[columnKey(scope)]; index++ {
		scope = "godj_bulk_targets_" + strconv.Itoa(index)
	}
	scope, err = quoteIdentifier(scope)
	if err != nil {
		return zero, err
	}
	return BulkUpdateParts{spec: spec, Table: table, Key: key, Scope: scope, Selection: source.Selection, Where: source.Where, Alias: source.Alias, Direct: source.Direct, Columns: columns, Arguments: arguments, BatchSize: batchSize}, nil
}

func CompileBulkUpdate(plan query.BulkUpdatePlan, parts BulkUpdateParts, validateValue func(query.FieldRef, query.Value) error, encodeValue func(query.Value) (any, error), placeholder func(int) string) (string, []any, error) {
	if err := plan.Validate(); err != nil {
		return "", nil, err
	}
	if !parts.spec.Equal(plan.Spec()) || plan.RowCount() > parts.BatchSize || len(parts.Columns) != len(plan.Spec().Fields()) {
		return "", nil, invalidPlan("bulk update exceeds its native batch shape or parameter budget")
	}
	arguments := slices.Clone(parts.Arguments)
	keys, rows, fields := plan.Keys(), plan.Rows(), plan.Spec().Fields()
	keyParameters := make([]string, len(keys))
	for index, key := range keys {
		arguments = append(arguments, key)
		keyParameters[index] = placeholder(len(arguments))
	}
	var statement strings.Builder
	key := parts.Key
	if parts.Direct {
		statement.WriteString("UPDATE " + parts.Table)
		if parts.Alias != "" {
			statement.WriteString(" AS " + parts.Alias)
			key = parts.Alias + "." + key
		}
		statement.WriteString(" SET ")
	} else {
		statement.WriteString(`WITH ` + parts.Scope + ` ("godj_bulk_key") AS MATERIALIZED (SELECT DISTINCT "godj_bulk_input".` + parts.Key + ` FROM (` + parts.Selection + `) AS "godj_bulk_input" WHERE "godj_bulk_input".` + parts.Key + ` IN (` + strings.Join(keyParameters, ", ") + `)) UPDATE ` + parts.Table + ` SET `)
	}
	for column, field := range fields {
		if column > 0 {
			statement.WriteString(", ")
		}
		statement.WriteString(parts.Columns[column] + " = CASE " + key)
		for row := range rows {
			value := rows[row][column]
			if err := validateValue(field, value); err != nil {
				return "", nil, err
			}
			argument, err := encodeValue(value)
			if err != nil {
				return "", nil, err
			}
			arguments = append(arguments, argument)
			statement.WriteString(" WHEN " + keyParameters[row] + " THEN " + placeholder(len(arguments)))
		}
		// The existing column also provides a native type for an all-NULL CASE.
		statement.WriteString(" ELSE " + parts.Columns[column] + " END")
	}
	if parts.Direct {
		statement.WriteString(parts.Where)
		if parts.Where == "" {
			statement.WriteString(" WHERE ")
		} else {
			statement.WriteString(" AND ")
		}
		statement.WriteString(key + " IN (" + strings.Join(keyParameters, ", ") + ")")
	} else {
		statement.WriteString(" WHERE " + parts.Key + ` IN (SELECT "godj_bulk_key" FROM ` + parts.Scope + ")")
	}
	return statement.String(), arguments, nil
}

type BulkUpdateExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func ExecuteBulkUpdate(ctx context.Context, executor BulkUpdateExecutor, plan query.BulkUpdatePlan, statement string, arguments []any, classify func(error) error) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	// Compilation above must validate all metadata and input values first.
	if plan.Spec().Selection().EmptyResult() {
		return 0, nil
	}
	result, err := executor.ExecContext(ctx, statement, arguments...)
	if err != nil {
		return 0, classify(err)
	}
	if result == nil {
		return 0, invalidPlan("bulk update returned no result metadata")
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read bulk update affected rows: %w", err)
	}
	unique := make(map[int64]bool, plan.RowCount())
	for _, key := range plan.Keys() {
		unique[key] = true
	}
	if count < 0 || count > int64(len(unique)) {
		return 0, &query.Error{Category: query.CategoryBackend, Code: query.CodeUnexpectedRows, Detail: "bulk update affected count is outside its unique input keys"}
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return count, nil
}
