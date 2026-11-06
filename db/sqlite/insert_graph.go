package sqlite

import (
	"context"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/db/internal/txscope"
	"github.com/progresshans/godj/query"
)

// The gateway holds one parameter per column, matching the bundled SQLite
// profile's 2,000-column limit. A graph is indivisible; an excess is an error.
const sqliteInsertGraphParameters = 2000

var sqliteInsertGraphSequence atomic.Uint64

var (
	_ db.GraphInserter = (*transactionSession)(nil)
	_ db.GraphInserter = (*relationSession)(nil)
	_ db.GraphInserter = (*writeSession)(nil)
)

type sqliteInsertGraph struct {
	rows                     []queryplan.InsertGraphRow
	input, receipts, trigger string
	createInput              string
	createReceipts           string
	createTrigger            string
	statement                string
	arguments                []any
	readback                 string
	readKeys                 []any
}

// SQLite immediate FKs are checked at the end of the outer statement. A TEMP
// trigger on a connection-private gateway makes all target INSERTs part of
// that statement. Values remain bound parameters, with no gateway affinity;
// the actual target columns own conversion and every native constraint.
func compileInsertGraph(plan query.InsertGraphPlan) (sqliteInsertGraph, error) {
	var zero sqliteInsertGraph
	rows, err := queryplan.PrepareInsertGraph(plan, sqliteInsertGraphParameters, queryplan.WriteValue, quoteIdentifier, sqliteIdentifierKey, sqliteValue)
	if err != nil {
		return zero, err
	}
	sequence := sqliteInsertGraphSequence.Add(1)
	if sequence == 0 {
		return zero, invalidPlan("insert graph temporary identifier space is exhausted")
	}
	prefix := "godj_graph_" + strconv.FormatUint(sequence, 10)
	compiled := sqliteInsertGraph{rows: rows, input: prefix + "_input", receipts: prefix + "_receipts", trigger: prefix + "_insert"}
	input, _ := quoteIdentifier(compiled.input)
	receipts, _ := quoteIdentifier(compiled.receipts)
	trigger, _ := quoteIdentifier(compiled.trigger)
	columns := make([]string, 0, plan.ValueCount())
	parameters := make([]string, 0, plan.ValueCount())
	readback := make([]string, len(rows))
	var body strings.Builder
	for index, row := range rows {
		table, err := quoteIdentifier(row.Insert.Table())
		if err != nil {
			return zero, err
		}
		name := sqliteIdentifierKey(row.Insert.Table())
		if name == compiled.input || name == compiled.receipts {
			return zero, invalidPlan("insert graph target collides with its temporary gateway")
		}
		values := make([]string, len(row.Arguments))
		for column := range row.Arguments {
			gateway := "v" + strconv.Itoa(len(compiled.arguments)+column)
			columns = append(columns, gateway)
			parameters = append(parameters, "?")
			values[column] = "NEW." + gateway
		}
		compiled.arguments = append(compiled.arguments, row.Arguments...)
		body.WriteString("INSERT INTO " + table + " (" + strings.Join(row.Columns, ", ") + ") VALUES (" + strings.Join(values, ", ") + "); ")
		// A BEFORE trigger may suppress or replace an INSERT. Observe the
		// actual statement count and native rowid before our receipt changes
		// either connection-local value. Zero/negative rowids remain valid.
		body.WriteString("SELECT CASE WHEN changes() != 1 OR last_insert_rowid() != " + values[row.KeyIndex] +
			" THEN RAISE(ABORT, 'insert graph did not create its requested row') END; ")
		ordinal := strconv.Itoa(index)
		body.WriteString("INSERT INTO " + receipts + " (ordinal, graph_key) VALUES (" + ordinal + ", last_insert_rowid()); ")
		readback[index] = "SELECT " + ordinal + " AS graph_ordinal, " + row.Key + " AS graph_key FROM main." + table + " WHERE " + row.Key + " = ?"
		compiled.readKeys = append(compiled.readKeys, row.Expected)
	}
	compiled.createInput = "CREATE TEMP TABLE " + input + " (" + strings.Join(columns, ", ") + ")"
	compiled.createReceipts = "CREATE TEMP TABLE " + receipts + " (ordinal INTEGER NOT NULL PRIMARY KEY, graph_key INTEGER NOT NULL)"
	compiled.createTrigger = "CREATE TEMP TRIGGER " + trigger + " AFTER INSERT ON temp." + input + " BEGIN " + body.String() + "END"
	compiled.statement = "INSERT INTO temp." + input + " VALUES (" + strings.Join(parameters, ", ") + ")"
	compiled.readback = "SELECT graph_key FROM (" + strings.Join(readback, " UNION ALL ") + ") ORDER BY graph_ordinal"
	return compiled, nil
}

func executeInsertGraph(ctx context.Context, executor queryplan.BulkInsertExecutor, compiled sqliteInsertGraph, willWrite func()) ([]int64, error) {
	classify := func(err error) error { return classifySQLiteWriteError(ctx, "insert", err) }
	for _, pragma := range []struct {
		statement string
		want      int64
	}{{"PRAGMA foreign_keys", 1}, {"PRAGMA defer_foreign_keys", 0}} {
		values, err := queryplan.ExecuteIntegerReturning(ctx, executor, pragma.statement, nil, 1, 1, classify)
		if err != nil {
			return nil, err
		}
		if values[0] != pragma.want {
			return nil, invalidPlan("insert graph requires active foreign keys without connection-wide deferral")
		}
	}
	checked := make(map[string]bool)
	for _, row := range compiled.rows {
		name := row.Insert.Table()
		if checked[name] {
			continue
		}
		table, _ := quoteIdentifier(name)
		key, _ := row.Insert.ReturningKey()
		if err := inspectSQLiteAutoKey(ctx, executor, name, key.Column(), table); err != nil {
			return nil, classify(err)
		}
		checked[name] = true
	}
	// All DDL and target writes live inside the caller-created savepoint.
	// CREATE deliberately has no IF NOT EXISTS, and failure never drops an
	// object whose creation was not confirmed. Rollback removes our own DDL.
	willWrite()
	for _, statement := range []string{compiled.createInput, compiled.createReceipts, compiled.createTrigger} {
		if _, err := executor.ExecContext(ctx, statement); err != nil {
			return nil, classify(err)
		}
	}
	if _, err := executor.ExecContext(ctx, compiled.statement, compiled.arguments...); err != nil {
		return nil, classify(err)
	}
	receipts, _ := quoteIdentifier(compiled.receipts)
	keys, err := queryplan.ExecuteIntegerReturning(ctx, executor, "SELECT graph_key FROM temp."+receipts+" ORDER BY ordinal", nil, len(compiled.rows), len(compiled.rows), classify)
	if err != nil {
		return nil, err
	}
	if err := queryplan.ValidateInsertGraphKeys(compiled.rows, keys); err != nil {
		return nil, err
	}
	stored, err := queryplan.ExecuteIntegerReturning(ctx, executor, compiled.readback, compiled.readKeys, len(compiled.rows), len(compiled.rows), classify)
	if err != nil {
		return nil, err
	}
	if err := queryplan.ValidateInsertGraphKeys(compiled.rows, stored); err != nil {
		return nil, err
	}
	// Dropping the gateway drops its trigger. A cleanup failure is returned
	// through the savepoint, which rolls back target writes and all temp DDL.
	for _, name := range []string{compiled.input, compiled.receipts} {
		table, _ := quoteIdentifier(name)
		if _, err := executor.ExecContext(ctx, "DROP TABLE temp."+table); err != nil {
			return nil, classify(err)
		}
	}
	return keys, nil
}

func sqliteInsertGraphLimits() db.InsertGraphLimits {
	return db.InsertGraphLimits{Rows: query.MaximumInsertGraphRows, Parameters: sqliteInsertGraphParameters}
}

func (session *transactionSession) InsertGraphLimits(ctx context.Context) (db.InsertGraphLimits, error) {
	if err := session.validate(ctx); err != nil {
		return db.InsertGraphLimits{}, err
	}
	return sqliteInsertGraphLimits(), nil
}

func (session *relationSession) InsertGraphLimits(ctx context.Context) (db.InsertGraphLimits, error) {
	if err := session.validate(ctx); err != nil {
		return db.InsertGraphLimits{}, err
	}
	return sqliteInsertGraphLimits(), nil
}

func (session *writeSession) InsertGraphLimits(ctx context.Context) (db.InsertGraphLimits, error) {
	if session == nil || session.session == nil {
		return db.InsertGraphLimits{}, inactiveWriteSessionError()
	}
	return session.session.InsertGraphLimits(ctx)
}

func insertGraphInScope(ctx context.Context, scope *txscope.Scope, executor queryplan.BulkInsertExecutor, plan query.InsertGraphPlan, willWrite func()) ([]int64, error) {
	compiled, err := compileInsertGraph(plan)
	if err != nil {
		return nil, err
	}
	var result []int64
	err = scope.Savepoint(ctx, func(child *txscope.Scope) error {
		var err error
		result, err = txscope.Do(child, ctx, func(work context.Context) ([]int64, error) {
			return executeInsertGraph(work, executor, compiled, willWrite)
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (session *transactionSession) InsertGraph(ctx context.Context, plan query.InsertGraphPlan) ([]int64, error) {
	if err := session.validate(ctx); err != nil {
		return nil, err
	}
	return insertGraphInScope(ctx, session.scope, session.transaction, plan, func() {})
}

func (session *relationSession) InsertGraph(ctx context.Context, plan query.InsertGraphPlan) ([]int64, error) {
	if err := session.validate(ctx); err != nil {
		return nil, err
	}
	return insertGraphInScope(ctx, session.scope, session.state.connection, plan, func() { session.state.mutationPossible.Store(true) })
}

func (session *writeSession) InsertGraph(ctx context.Context, plan query.InsertGraphPlan) ([]int64, error) {
	if session == nil || session.session == nil {
		return nil, inactiveWriteSessionError()
	}
	return session.session.InsertGraph(ctx, plan)
}
