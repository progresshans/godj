package dbstate

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// PostgresCatalog contains direct physical observations. Neither the product
// migration reader nor expected profiles participate in collecting this state.
type PostgresCatalog struct {
	Tables                    []string
	OtherRelations            []PostgresRelation
	Columns                   []PostgresColumn
	Constraints               []PostgresConstraint
	Indexes                   []PostgresIndex
	Sequences                 []PostgresSequence
	Triggers, Policies, Rules int
}

type PostgresColumn struct {
	Table            string
	Ordinal          int
	Name             string
	DataType         string
	NotNull          bool
	Identity         string
	Generated        string
	HasDefault       bool
	DefaultCollation bool
	Primary          bool
}

type PostgresConstraint struct {
	Table            string
	Name             string
	Kind             string
	Deferrable       bool
	Deferred         bool
	Validated        bool
	Key              string
	IndexName        string
	InternalTriggers int
}

type PostgresIndex struct {
	Table          string
	Name           string
	Primary        bool
	Unique         bool
	Valid          bool
	Ready          bool
	Live           bool
	KeyCount       int
	AttributeCount int
	Keys           string
	Method         string
	HasPredicate   bool
	HasExpressions bool
	Exclusion      bool
}

type PostgresRelation struct {
	Name string
	Kind string
}

type PostgresSequence struct {
	Name        string
	Kind        string
	Persistence string
	DataType    string
	Start       int64
	Increment   int64
	Minimum     int64
	Maximum     int64
	Cache       int64
	Cycle       bool
	OwnerTable  string
	OwnerColumn string
	Dependency  string
	Last        int64
	Called      bool
}

// CapturePostgresCatalog reads the supplied schema through the caller's raw
// connection. The caller owns consistency and connection lifetime; every row
// stream is fully consumed and closed before the next query.
func CapturePostgresCatalog(ctx context.Context, connection *pgx.Conn, schema string) (PostgresCatalog, error) {
	var snapshot PostgresCatalog
	relations, err := postgresCatalogRows(ctx, connection, schema, postgresRelationsSQL, func(row pgx.CollectableRow) (PostgresRelation, error) {
		var value PostgresRelation
		err := row.Scan(&value.Name, &value.Kind)
		return value, err
	})
	if err != nil {
		return PostgresCatalog{}, fmt.Errorf("read relations: %w", err)
	}
	for _, relation := range relations {
		switch relation.Kind {
		case "r":
			snapshot.Tables = append(snapshot.Tables, relation.Name)
		case "i", "S":
		default:
			snapshot.OtherRelations = append(snapshot.OtherRelations, relation)
		}
	}
	snapshot.Columns, err = postgresCatalogRows(ctx, connection, schema, postgresColumnsSQL, func(row pgx.CollectableRow) (PostgresColumn, error) {
		var value PostgresColumn
		err := row.Scan(&value.Table, &value.Ordinal, &value.Name, &value.DataType, &value.NotNull, &value.Identity, &value.Generated, &value.HasDefault, &value.DefaultCollation, &value.Primary)
		return value, err
	})
	if err != nil {
		return PostgresCatalog{}, fmt.Errorf("read columns: %w", err)
	}
	snapshot.Constraints, err = postgresCatalogRows(ctx, connection, schema, postgresConstraintsSQL, func(row pgx.CollectableRow) (PostgresConstraint, error) {
		var value PostgresConstraint
		err := row.Scan(&value.Table, &value.Name, &value.Kind, &value.Deferrable, &value.Deferred, &value.Validated, &value.Key, &value.IndexName, &value.InternalTriggers)
		return value, err
	})
	if err != nil {
		return PostgresCatalog{}, fmt.Errorf("read constraints: %w", err)
	}
	snapshot.Indexes, err = postgresCatalogRows(ctx, connection, schema, postgresIndexesSQL, func(row pgx.CollectableRow) (PostgresIndex, error) {
		var value PostgresIndex
		err := row.Scan(&value.Table, &value.Name, &value.Primary, &value.Unique, &value.Valid, &value.Ready, &value.Live, &value.KeyCount, &value.AttributeCount, &value.Keys, &value.Method, &value.HasPredicate, &value.HasExpressions, &value.Exclusion)
		return value, err
	})
	if err != nil {
		return PostgresCatalog{}, fmt.Errorf("read indexes: %w", err)
	}
	snapshot.Sequences, err = postgresCatalogRows(ctx, connection, schema, postgresSequencesSQL, func(row pgx.CollectableRow) (PostgresSequence, error) {
		var value PostgresSequence
		err := row.Scan(&value.Name, &value.Kind, &value.Persistence, &value.DataType, &value.Start, &value.Increment, &value.Minimum, &value.Maximum, &value.Cache, &value.Cycle, &value.OwnerTable, &value.OwnerColumn, &value.Dependency)
		return value, err
	})
	if err != nil {
		return PostgresCatalog{}, fmt.Errorf("read sequences: %w", err)
	}
	if err := connection.QueryRow(ctx, postgresObjectCountsSQL, schema).Scan(&snapshot.Triggers, &snapshot.Policies, &snapshot.Rules); err != nil {
		return PostgresCatalog{}, fmt.Errorf("read trigger, policy and rule counts: %w", err)
	}
	for i := range snapshot.Sequences {
		sequence := &snapshot.Sequences[i]
		qualified := pgx.Identifier{schema, sequence.Name}.Sanitize()
		if err := connection.QueryRow(ctx, "SELECT last_value, is_called FROM "+qualified).Scan(&sequence.Last, &sequence.Called); err != nil {
			return PostgresCatalog{}, fmt.Errorf("read sequence state: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return PostgresCatalog{}, err
	}
	return snapshot, nil
}

func postgresCatalogRows[T any](ctx context.Context, connection *pgx.Conn, schema, statement string, scan pgx.RowToFunc[T]) ([]T, error) {
	rows, err := connection.Query(ctx, statement, schema)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scan)
}

const postgresRelationsSQL = `SELECT "c"."relname", "c"."relkind"::text
		FROM "pg_catalog"."pg_class" AS "c"
		JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "c"."relnamespace"
		WHERE "n"."nspname" = $1 ORDER BY "c"."relkind", "c"."relname"`

const postgresColumnsSQL = `
		SELECT "c"."relname", "a"."attnum", "a"."attname",
		       "pg_catalog"."format_type"("a"."atttypid", "a"."atttypmod"),
		       "a"."attnotnull", "a"."attidentity"::text, "a"."attgenerated"::text,
		       ("d"."oid" IS NOT NULL),
		       COALESCE("a"."attcollation" = "type"."typcollation", "a"."attcollation" = 0),
		       EXISTS (
		         SELECT 1 FROM "pg_catalog"."pg_index" AS "i"
		         WHERE "i"."indrelid" = "c"."oid" AND "i"."indisprimary"
		           AND "a"."attnum" = ANY("i"."indkey")
		       )
		FROM "pg_catalog"."pg_class" AS "c"
		JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "c"."relnamespace"
		JOIN "pg_catalog"."pg_attribute" AS "a" ON "a"."attrelid" = "c"."oid"
		JOIN "pg_catalog"."pg_type" AS "type" ON "type"."oid" = "a"."atttypid"
		LEFT JOIN "pg_catalog"."pg_attrdef" AS "d"
		  ON "d"."adrelid" = "a"."attrelid" AND "d"."adnum" = "a"."attnum"
		WHERE "n"."nspname" = $1 AND "c"."relkind" = 'r'
		  AND "a"."attnum" > 0 AND NOT "a"."attisdropped"
		ORDER BY "c"."relname", "a"."attnum"`

const postgresConstraintsSQL = `
		SELECT "table"."relname", "constraint"."conname", "constraint"."contype"::text,
		       "constraint"."condeferrable", "constraint"."condeferred", "constraint"."convalidated",
		       COALESCE("pg_catalog"."array_to_string"("constraint"."conkey", ','), ''),
		       COALESCE("constraint_index"."relname", ''),
		       (SELECT COUNT(*) FROM "pg_catalog"."pg_trigger" AS "trigger"
		        WHERE "trigger"."tgconstraint" = "constraint"."oid" AND "trigger"."tgisinternal")
		FROM "pg_catalog"."pg_constraint" AS "constraint"
		JOIN "pg_catalog"."pg_class" AS "table" ON "table"."oid" = "constraint"."conrelid"
		JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "table"."relnamespace"
		LEFT JOIN "pg_catalog"."pg_class" AS "constraint_index"
		  ON "constraint_index"."oid" = "constraint"."conindid"
		WHERE "n"."nspname" = $1
		ORDER BY "table"."relname", "constraint"."conname"`

const postgresIndexesSQL = `
		SELECT "table"."relname", "index_class"."relname",
		       "index"."indisprimary", "index"."indisunique", "index"."indisvalid",
		       "index"."indisready", "index"."indislive",
		       "index"."indnkeyatts"::integer, "index"."indnatts"::integer,
		       COALESCE((
		         SELECT "pg_catalog"."string_agg"("attribute"."attname", ',' ORDER BY "key"."ordinality")
		         FROM "pg_catalog"."unnest"("index"."indkey"::smallint[]) WITH ORDINALITY
		           AS "key"("attribute_number", "ordinality")
		         JOIN "pg_catalog"."pg_attribute" AS "attribute"
		           ON "attribute"."attrelid" = "index"."indrelid"
		          AND "attribute"."attnum" = "key"."attribute_number"
		         WHERE "key"."ordinality" <= "index"."indnkeyatts"
		       ), ''),
		       "access_method"."amname",
		       ("index"."indpred" IS NOT NULL), ("index"."indexprs" IS NOT NULL),
		       "index"."indisexclusion"
		FROM "pg_catalog"."pg_index" AS "index"
		JOIN "pg_catalog"."pg_class" AS "table" ON "table"."oid" = "index"."indrelid"
		JOIN "pg_catalog"."pg_class" AS "index_class" ON "index_class"."oid" = "index"."indexrelid"
		JOIN "pg_catalog"."pg_am" AS "access_method" ON "access_method"."oid" = "index_class"."relam"
		JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "table"."relnamespace"
		WHERE "n"."nspname" = $1
		ORDER BY "table"."relname", "index_class"."relname"`

const postgresSequencesSQL = `
		SELECT "c"."relname", "c"."relkind"::text, "c"."relpersistence"::text,
		       "pg_catalog"."format_type"("s"."seqtypid", NULL),
		       "s"."seqstart", "s"."seqincrement", "s"."seqmin",
		       "s"."seqmax", "s"."seqcache", "s"."seqcycle",
		       COALESCE("owner_table"."relname", ''), COALESCE("owner_column"."attname", ''),
		       COALESCE("dependency"."deptype"::text, '')
		FROM "pg_catalog"."pg_sequence" AS "s"
		JOIN "pg_catalog"."pg_class" AS "c" ON "c"."oid" = "s"."seqrelid"
		JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "c"."relnamespace"
		LEFT JOIN "pg_catalog"."pg_depend" AS "dependency"
		  ON "dependency"."classid" = 'pg_catalog.pg_class'::regclass
		 AND "dependency"."objid" = "c"."oid"
		 AND "dependency"."refclassid" = 'pg_catalog.pg_class'::regclass
		 AND "dependency"."deptype" IN ('a', 'i')
		LEFT JOIN "pg_catalog"."pg_class" AS "owner_table"
		  ON "owner_table"."oid" = "dependency"."refobjid"
		LEFT JOIN "pg_catalog"."pg_attribute" AS "owner_column"
		  ON "owner_column"."attrelid" = "dependency"."refobjid"
		 AND "owner_column"."attnum" = "dependency"."refobjsubid"
		WHERE "n"."nspname" = $1
		ORDER BY "c"."relname"`

const postgresObjectCountsSQL = `
		SELECT
		  (SELECT COUNT(*)
		   FROM "pg_catalog"."pg_trigger" AS "trigger"
		   JOIN "pg_catalog"."pg_class" AS "table" ON "table"."oid" = "trigger"."tgrelid"
		   JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "table"."relnamespace"
		   WHERE "n"."nspname" = $1),
		  (SELECT COUNT(*)
		   FROM "pg_catalog"."pg_policy" AS "policy"
		   JOIN "pg_catalog"."pg_class" AS "table" ON "table"."oid" = "policy"."polrelid"
		   JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "table"."relnamespace"
		   WHERE "n"."nspname" = $1),
		  (SELECT COUNT(*)
		   FROM "pg_catalog"."pg_rewrite" AS "rule"
		   JOIN "pg_catalog"."pg_class" AS "table" ON "table"."oid" = "rule"."ev_class"
		   JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "table"."relnamespace"
		   WHERE "n"."nspname" = $1)`
