package postgres

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/db/internal/txscope"
	"github.com/progresshans/godj/query"
)

var _ db.GraphInserter = (*transactionSession)(nil)

type postgresInsertGraph struct {
	rows       []queryplan.InsertGraphRow
	statement  string
	arguments  []any
	readback   string
	readKeys   []any
	tables     []string
	quoted     map[string]string
	keyColumns map[string]string
}

// Each row is a distinct data-modifying CTE. PostgreSQL checks immediate FKs
// at the end of the complete statement. No CTE depends on observing another
// CTE's table snapshot: all references are already bound to explicit keys.
func compileInsertGraph(schema string, plan query.InsertGraphPlan) (postgresInsertGraph, error) {
	var zero postgresInsertGraph
	rows, err := queryplan.PrepareInsertGraph(plan, postgresBulkParameters, validateWriteValue, quoteIdentifier, nil, postgresValue)
	if err != nil {
		return zero, err
	}
	compiled := postgresInsertGraph{rows: rows, quoted: make(map[string]string), keyColumns: make(map[string]string)}
	ctes := make([]string, len(rows))
	results := make([]string, len(rows))
	readback := make([]string, len(rows))
	for index, row := range rows {
		table, err := quoteTable(schema, row.Insert.Table())
		if err != nil {
			return zero, err
		}
		if _, exists := compiled.quoted[row.Insert.Table()]; !exists {
			compiled.tables = append(compiled.tables, row.Insert.Table())
			compiled.quoted[row.Insert.Table()] = table
			key, _ := row.Insert.ReturningKey()
			compiled.keyColumns[row.Insert.Table()] = key.Column()
		}
		parameters := make([]string, len(row.Arguments))
		for column := range parameters {
			parameters[column] = placeholder(len(compiled.arguments) + column + 1)
		}
		compiled.arguments = append(compiled.arguments, row.Arguments...)
		ordinal := strconv.Itoa(index)
		name := "godj_graph_" + ordinal
		ctes[index] = name + " AS (INSERT INTO " + table + " (" + strings.Join(row.Columns, ", ") +
			") OVERRIDING SYSTEM VALUE VALUES (" + strings.Join(parameters, ", ") + ") RETURNING " + row.Key + " AS graph_key)"
		results[index] = "SELECT " + ordinal + " AS graph_ordinal, graph_key FROM " + name
		readback[index] = "SELECT " + ordinal + " AS graph_ordinal, " + row.Key + " AS graph_key FROM ONLY " + table +
			" WHERE " + row.Key + " = " + placeholder(index+1)
		compiled.readKeys = append(compiled.readKeys, row.Expected)
	}
	compiled.statement = "WITH " + strings.Join(ctes, ", ") + " SELECT graph_key FROM (" + strings.Join(results, " UNION ALL ") + ") AS graph_result ORDER BY graph_ordinal"
	compiled.readback = "SELECT graph_key FROM (" + strings.Join(readback, " UNION ALL ") + ") AS graph_result ORDER BY graph_ordinal"
	slices.Sort(compiled.tables)
	return compiled, nil
}

func (session *transactionSession) InsertGraphLimits(ctx context.Context) (db.InsertGraphLimits, error) {
	if err := session.validate(ctx); err != nil {
		return db.InsertGraphLimits{}, err
	}
	if session.readOnly {
		return db.InsertGraphLimits{}, unsupportedInsert("read-only snapshots cannot insert a graph")
	}
	return db.InsertGraphLimits{Rows: query.MaximumInsertGraphRows, Parameters: postgresBulkParameters}, nil
}

func (session *transactionSession) InsertGraph(ctx context.Context, plan query.InsertGraphPlan) ([]int64, error) {
	if _, err := session.InsertGraphLimits(ctx); err != nil {
		return nil, err
	}
	compiled, err := compileInsertGraph(session.backend.schema, plan)
	if err != nil {
		return nil, err
	}
	var result []int64
	err = session.scope.Savepoint(ctx, func(child *txscope.Scope) error {
		var err error
		result, err = txscope.Do(child, ctx, func(work context.Context) ([]int64, error) {
			classify := func(err error) error {
				table := ""
				var native *pgconn.PgError
				if errors.As(err, &native) {
					if _, own := compiled.quoted[native.TableName]; own {
						table = native.TableName
					}
				}
				return classifyDatabaseError(work, "insert", session.backend.schema, table, err)
			}
			for _, table := range compiled.tables {
				quoted := compiled.quoted[table]
				if _, err := session.transaction.ExecContext(work, "LOCK TABLE "+quoted+" IN ACCESS SHARE MODE"); err != nil {
					return nil, classify(err)
				}
				if _, err := inspectPostgresAutoKey(work, session.transaction, session.backend.schema, quoted, compiled.keyColumns[table]); err != nil {
					return nil, classify(err)
				}
			}
			keys, err := queryplan.ExecuteIntegerReturning(work, session.transaction, compiled.statement, compiled.arguments, len(compiled.rows), len(compiled.rows), classify)
			if err != nil {
				return nil, err
			}
			if err := queryplan.ValidateInsertGraphKeys(compiled.rows, keys); err != nil {
				return nil, err
			}
			stored, err := queryplan.ExecuteIntegerReturning(work, session.transaction, compiled.readback, compiled.readKeys, len(compiled.rows), len(compiled.rows), classify)
			if err != nil {
				return nil, err
			}
			if err := queryplan.ValidateInsertGraphKeys(compiled.rows, stored); err != nil {
				return nil, err
			}
			return keys, nil
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
