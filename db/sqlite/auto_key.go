package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/db/internal/txscope"
	"github.com/progresshans/godj/query"
)

var (
	_ db.AutoKeyAllocator = (*transactionSession)(nil)
	_ db.AutoKeyAllocator = (*relationSession)(nil)
	_ db.AutoKeyAllocator = (*writeSession)(nil)
)

func (session *transactionSession) ReserveAutoKeys(ctx context.Context, plan query.AutoKeyPlan) ([]int64, error) {
	if err := session.validate(ctx); err != nil {
		return nil, err
	}
	return txscope.Do(session.scope, ctx, func(work context.Context) ([]int64, error) {
		return reserveSQLiteAutoKeys(work, session.transaction, plan, func() {})
	})
}

func (session *relationSession) ReserveAutoKeys(ctx context.Context, plan query.AutoKeyPlan) ([]int64, error) {
	if err := session.validate(ctx); err != nil {
		return nil, err
	}
	return txscope.Do(session.scope, ctx, func(work context.Context) ([]int64, error) {
		return reserveSQLiteAutoKeys(work, session.state.connection, plan, func() { session.state.mutationPossible.Store(true) })
	})
}

func (session *writeSession) ReserveAutoKeys(ctx context.Context, plan query.AutoKeyPlan) ([]int64, error) {
	if session == nil || session.session == nil {
		return nil, inactiveWriteSessionError()
	}
	return session.session.ReserveAutoKeys(ctx, plan)
}

func sqliteAutoKeyMetadataFailure(detail string) error {
	return &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: detail}
}

// reserveSQLiteAutoKeys runs under one txscope operation. The metadata reads
// and conditional sqlite_sequence write use the same native transaction. A
// deferred transaction that cannot upgrade its snapshot must fail with BUSY;
// neither a second connection nor a retry may allocate against different data.
func reserveSQLiteAutoKeys(ctx context.Context, executor queryplan.IntegerReturningExecutor, plan query.AutoKeyPlan, willWrite func()) ([]int64, error) {
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	table, err := quoteIdentifier(plan.Table())
	if err != nil {
		return nil, err
	}
	key, err := quoteIdentifier(plan.Key().Column())
	if err != nil {
		return nil, err
	}
	classify := func(err error) error { return classifySQLiteWriteError(ctx, "reserve auto keys", err) }
	if err := inspectSQLiteAutoKey(ctx, executor, plan.Table(), plan.Key().Column(), table); err != nil {
		return nil, classify(err)
	}

	var present bool
	var rowID, highWater int64
	err = readSQLiteAutoKeyRows(ctx, executor,
		`SELECT rowid, name, typeof(name), seq, typeof(seq) FROM main.sqlite_sequence WHERE CAST(name AS TEXT) COLLATE NOCASE = ? LIMIT 2`, []any{plan.Table()}, func(rows *sql.Rows) error {
			for rows.Next() {
				if present {
					return sqliteAutoKeyMetadataFailure("auto-key allocator has duplicate sequence rows")
				}
				var name, nameType, valueType string
				if err := rows.Scan(&rowID, &name, &nameType, &highWater, &valueType); err != nil {
					return err
				}
				if name != plan.Table() || nameType != "text" || valueType != "integer" || highWater < 0 {
					return sqliteAutoKeyMetadataFailure("auto-key allocator has an invalid sequence name or high-water value")
				}
				present = true
			}
			return nil
		})
	if err != nil {
		return nil, classify(err)
	}
	maximum, err := queryplan.ExecuteIntegerReturning(ctx, executor, "SELECT COALESCE(MAX("+key+"), 0) FROM main."+table, nil, 1, 1, classify)
	if err != nil {
		return nil, err
	}
	// SQLite does not advance sqlite_sequence when a primary key is UPDATEd.
	// Both sources therefore participate in the allocator's authoritative floor.
	base := max(highWater, maximum[0])
	if base > math.MaxInt64-int64(plan.Count()) {
		return nil, &query.Error{Category: query.CategoryBackend, Code: query.CodeAutoKeyExhausted, Detail: "SQLite auto-key allocator is exhausted"}
	}
	end := base + int64(plan.Count())
	statement := `INSERT INTO main.sqlite_sequence(name, seq) SELECT ?, ? WHERE NOT EXISTS (SELECT 1 FROM main.sqlite_sequence WHERE CAST(name AS TEXT) COLLATE NOCASE = ?) RETURNING seq`
	arguments := []any{plan.Table(), end, plan.Table()}
	if present {
		statement = `UPDATE main.sqlite_sequence SET seq = ? WHERE rowid = ? AND typeof(name) = 'text' AND name = ? COLLATE BINARY AND typeof(seq) = 'integer' AND seq = ? RETURNING seq`
		arguments = []any{end, rowID, plan.Table(), highWater}
	}
	willWrite()
	reserved, err := queryplan.ExecuteIntegerReturning(ctx, executor, statement, arguments, 1, 1, classify)
	if err != nil {
		return nil, err
	}
	if reserved[0] != end {
		return nil, sqliteAutoKeyMetadataFailure("native allocator did not reserve the complete requested interval")
	}
	keys := make([]int64, plan.Count())
	for index := range keys {
		keys[index] = end - int64(plan.Count()-index-1)
	}
	return keys, nil
}

func readSQLiteAutoKeyRows(ctx context.Context, executor queryplan.IntegerReturningExecutor, statement string, arguments []any, inspect func(*sql.Rows) error) (resultErr error) {
	rows, err := executor.QueryContext(ctx, statement, arguments...)
	if err != nil {
		if rows != nil {
			err = errors.Join(err, rows.Close())
		}
		return err
	}
	if rows == nil {
		return sqliteAutoKeyMetadataFailure("auto-key metadata returned nil rows")
	}
	defer func() { resultErr = errors.Join(resultErr, rows.Err(), rows.Close(), ctx.Err()) }()
	return inspect(rows)
}

func inspectSQLiteAutoKey(ctx context.Context, executor queryplan.IntegerReturningExecutor, tableName, keyName, table string) error {
	const maximumSQLBytes = 1 << 20
	const maximumTokens = 65536
	if err := readSQLiteAutoKeyRows(ctx, executor,
		`SELECT name, type, length(CAST(sql AS BLOB)), substr(sql, 1, ?) FROM main.sqlite_schema WHERE name COLLATE NOCASE = ? AND type IN ('table', 'view') LIMIT 2`, []any{maximumSQLBytes, tableName}, func(rows *sql.Rows) error {
			count := 0
			for rows.Next() {
				count++
				var name, kind, statement string
				var size int
				if err := rows.Scan(&name, &kind, &size, &statement); err != nil {
					return err
				}
				if count != 1 || name != tableName || kind != "table" || size < 1 || size > maximumSQLBytes {
					return sqliteAutoKeyMetadataFailure("auto-key reservation requires one exact bounded main table")
				}
				tokens, err := tokenizeSQLiteRelationSQL(statement, maximumTokens, true)
				if err != nil {
					return err
				}
				word := func(index int, value string) bool {
					return index < len(tokens) && !tokens[index].quoted && !tokens[index].literal && tokens[index].value == value
				}
				if !word(0, "create") || !word(1, "table") {
					return sqliteAutoKeyMetadataFailure("auto-key reservation requires an ordinary table")
				}
				found := 0
				for index := 2; index+2 < len(tokens); index++ {
					if word(index, "primary") && word(index+1, "key") && word(index+2, "autoincrement") {
						found++
					}
				}
				if found != 1 {
					return sqliteAutoKeyMetadataFailure("auto-key reservation requires native AUTOINCREMENT")
				}
			}
			if count != 1 {
				return sqliteAutoKeyMetadataFailure("auto-key reservation table is missing")
			}
			return nil
		}); err != nil {
		return err
	}
	if err := readSQLiteAutoKeyRows(ctx, executor,
		`SELECT 1 FROM temp.sqlite_schema WHERE name COLLATE NOCASE = ? AND type IN ('table', 'view') LIMIT 1`, []any{tableName}, func(rows *sql.Rows) error {
			if rows.Next() {
				return sqliteAutoKeyMetadataFailure("a temporary object shadows the reserved table")
			}
			return nil
		}); err != nil {
		return err
	}
	return readSQLiteAutoKeyRows(ctx, executor, `PRAGMA main.table_xinfo(`+table+`)`, nil, func(rows *sql.Rows) error {
		count, matched, primaryKeys := 0, 0, 0
		for rows.Next() {
			count++
			if count > maximumTokens {
				return sqliteAutoKeyMetadataFailure("auto-key table exceeds the column inspection bound")
			}
			var position, nonnull, primary, hidden int
			var name, declaredType string
			var defaultValue sql.NullString
			if err := rows.Scan(&position, &name, &declaredType, &nonnull, &defaultValue, &primary, &hidden); err != nil {
				return err
			}
			if primary != 0 {
				primaryKeys++
			}
			if name == keyName {
				matched++
				if declaredType != "INTEGER" || nonnull != 1 || primary != 1 || hidden != 0 || defaultValue.Valid {
					return sqliteAutoKeyMetadataFailure("auto-key field is not an exact INTEGER NOT NULL primary key")
				}
			}
		}
		if matched != 1 || primaryKeys != 1 {
			return sqliteAutoKeyMetadataFailure("auto-key field is not the single declared primary key")
		}
		return nil
	})
}
