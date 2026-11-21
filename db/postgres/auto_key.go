package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/db/internal/txscope"
	"github.com/progresshans/godj/query"
)

var _ db.AutoKeyAllocator = (*transactionSession)(nil)

type postgresAutoKeyState struct {
	sequenceID                         int64
	namespace, name, identity          string
	increment, minimum, maximum, cache int64
}

func (session *transactionSession) ReserveAutoKeys(ctx context.Context, plan query.AutoKeyPlan) ([]int64, error) {
	if err := session.validate(ctx); err != nil {
		return nil, err
	}
	if session.readOnly {
		return nil, unsupportedInsert("read-only snapshots cannot reserve auto keys")
	}
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	table, err := quoteTable(session.backend.schema, plan.Table())
	if err != nil {
		return nil, err
	}
	key, err := quoteIdentifier(plan.Key().Column())
	if err != nil {
		return nil, err
	}
	return txscope.Do(session.scope, ctx, func(work context.Context) ([]int64, error) {
		classify := func(err error) error {
			if err == nil {
				return nil
			}
			var native *pgconn.PgError
			if work.Err() == nil && errors.As(err, &native) && native.Code == "2200H" {
				return &query.Error{Category: query.CategoryBackend, Code: query.CodeAutoKeyExhausted, Detail: "PostgreSQL identity sequence is exhausted", Cause: err}
			}
			return classifyDatabaseError(work, "reserve auto keys", session.backend.schema, plan.Table(), err)
		}
		// Pin the exact relation against DDL while inspecting its own identity
		// sequence. Ordinary concurrent inserts still use that same sequence.
		if _, err := session.transaction.ExecContext(work, "LOCK TABLE "+table+" IN ACCESS SHARE MODE"); err != nil {
			return nil, classify(err)
		}
		before, err := inspectPostgresAutoKey(work, session.transaction, session.backend.schema, table, plan.Key().Column())
		if err != nil {
			return nil, classify(err)
		}
		sequence, err := quoteTable(before.namespace, before.name)
		if err != nil {
			return nil, err
		}
		keys, err := queryplan.ExecuteIntegerReturning(work, session.transaction,
			"SELECT pg_catalog.nextval($1::regclass) FROM pg_catalog.generate_series(1, $2::integer) AS requested(ordinal) ORDER BY ordinal",
			[]any{sequence, plan.Count()}, plan.Count(), plan.Count(), classify)
		if err != nil {
			return nil, err
		}
		seen := make(map[int64]bool, len(keys))
		for _, id := range keys {
			if seen[id] {
				return nil, autoKeyMetadataFailure("native identity sequence returned duplicate keys")
			}
			seen[id] = true
		}
		// nextval owns the sequence's native transaction lock. Recheck its
		// parameters after allocation so an intervening ALTER cannot change a
		// reviewed noncycling allocator into a different physical capability.
		after, err := inspectPostgresAutoKey(work, session.transaction, session.backend.schema, table, plan.Key().Column())
		if err != nil {
			return nil, classify(err)
		}
		if after != before {
			return nil, autoKeyMetadataFailure("identity allocator changed during reservation")
		}
		occupied, err := queryplan.ExecuteIntegerReturning(work, session.transaction,
			"SELECT "+key+" FROM ONLY "+table+" WHERE "+key+" = ANY($1::bigint[]) LIMIT 1", []any{keys}, 0, 1, classify)
		if err != nil {
			return nil, err
		}
		if len(occupied) != 0 {
			return nil, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeUniquePrimaryKey, Detail: "reserved native key already belongs to a stored row"}
		}
		return keys, nil
	})
}

func autoKeyMetadataFailure(detail string) error {
	return &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: detail}
}

func inspectPostgresAutoKey(ctx context.Context, executor queryplan.IntegerReturningExecutor, namespace, table, column string) (result postgresAutoKeyState, resultErr error) {
	const statement = `SELECT a.attidentity::text, a.attnotnull, a.atttypid=20,
        pg_catalog.cardinality(p.conkey), p.conkey[1]=a.attnum,
        s.oid::bigint, n.nspname, s.relname, q.seqcycle,
        q.seqincrement, q.seqmin, q.seqmax, q.seqcache, q.seqtypid=20
      FROM pg_catalog.pg_class t
      JOIN pg_catalog.pg_attribute a ON a.attrelid=t.oid AND a.attname=$2 AND NOT a.attisdropped
      JOIN pg_catalog.pg_constraint p ON p.conrelid=t.oid AND p.contype='p'
      JOIN pg_catalog.pg_class s ON s.oid=pg_catalog.pg_get_serial_sequence($1,a.attname)::regclass AND s.relkind='S'
      JOIN pg_catalog.pg_namespace n ON n.oid=s.relnamespace
      JOIN pg_catalog.pg_sequence q ON q.seqrelid=s.oid
      WHERE t.oid=$1::regclass AND t.relkind='r' AND NOT t.relispartition
        AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_inherits i WHERE i.inhrelid=t.oid OR i.inhparent=t.oid)`
	rows, err := executor.QueryContext(ctx, statement, table, column)
	if err != nil {
		if rows != nil {
			err = errors.Join(err, rows.Close())
		}
		return result, err
	}
	if rows == nil {
		return result, autoKeyMetadataFailure("identity metadata returned nil rows")
	}
	defer func() {
		resultErr = errors.Join(resultErr, rows.Close(), ctx.Err())
		if resultErr != nil {
			result = postgresAutoKeyState{}
		}
	}()
	count := 0
	for rows.Next() {
		count++
		if count > 1 {
			return result, autoKeyMetadataFailure("identity metadata is not singular")
		}
		var nonnull, integer, exactKey, cycle, integerSequence bool
		var columns int
		if err := rows.Scan(&result.identity, &nonnull, &integer, &columns, &exactKey, &result.sequenceID, &result.namespace, &result.name,
			&cycle, &result.increment, &result.minimum, &result.maximum, &result.cache, &integerSequence); err != nil {
			return result, err
		}
		if (result.identity != "d" && result.identity != "a") || !nonnull || !integer || columns != 1 || !exactKey || result.sequenceID <= 0 || result.namespace != namespace ||
			cycle || !integerSequence || result.increment == 0 || result.minimum > result.maximum || result.cache < 1 {
			return result, autoKeyMetadataFailure("reservation requires the table's noncycling bigint identity primary key in its own schema")
		}
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	if count != 1 {
		return result, autoKeyMetadataFailure("reservation has no unique native identity allocator")
	}
	return result, nil
}
