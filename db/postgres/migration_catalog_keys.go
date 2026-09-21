package postgres

import (
	"context"
	"database/sql"
)

const (
	// Each field can own a UNIQUE and an FK, plus model constraints and PK.
	postgresMigrationMaxCatalogConstraints = 3*postgresMigrationMaxFields + 1
	postgresMigrationMaxCatalogIndexes     = 2*postgresMigrationMaxFields + 1
)

type postgresMigrationIndexKey struct {
	attributeNumber      int
	columnOptions        int
	columnCollation      bool
	operatorClassSchema  string
	operatorClassName    string
	operatorClassDefault bool
	operatorClassMethod  bool
}

func readPostgresMigrationConstraintKeys(ctx context.Context, executor migrationSQLExecutor, tableOID int64, constraints []postgresMigrationConstraintCatalog) error {
	type member struct {
		oid                 int64
		position, attribute int
	}
	keys, err := queryPostgresCatalogRows(ctx, "constraint keys", postgresMigrationMaxNodes, func() (*sql.Rows, error) {
		return executor.QueryContext(ctx,
			`SELECT "k"."oid"::bigint, "member"."position"::integer, "member"."attribute"::integer `+
				`FROM "pg_catalog"."pg_constraint" AS "k" `+
				`CROSS JOIN LATERAL "pg_catalog"."unnest"("k"."conkey") WITH ORDINALITY AS "member"("attribute", "position") `+
				`WHERE "k"."conrelid" = $1 ORDER BY "k"."oid", "member"."position" LIMIT $2`, tableOID, postgresMigrationMaxNodes+1)
	}, func(rows *sql.Rows) (member, error) {
		var value member
		err := rows.Scan(&value.oid, &value.position, &value.attribute)
		return value, err
	})
	if err != nil {
		return err
	}
	owners := make(map[int64]int, len(constraints))
	for index, constraint := range constraints {
		if _, exists := owners[constraint.oid]; exists || constraint.oid <= 0 {
			return postgresMigrationCatalogDrift("", "constraint key owner is missing or duplicated")
		}
		owners[constraint.oid] = index
	}
	for _, key := range keys {
		index, exists := owners[key.oid]
		if !exists || key.position != len(constraints[index].sourceAttributes)+1 || key.position > postgresMigrationMaxFields {
			return postgresMigrationCatalogDrift("", "constraint key order or owner is inconsistent")
		}
		constraints[index].sourceAttributes = append(constraints[index].sourceAttributes, key.attribute)
	}
	return nil
}

func readPostgresMigrationIndexKeys(ctx context.Context, executor migrationSQLExecutor, tableOID int64, indexes []postgresMigrationIndexCatalog) error {
	type member struct {
		oid      int64
		position int
		key      postgresMigrationIndexKey
	}
	keys, err := queryPostgresCatalogRows(ctx, "index keys", postgresMigrationMaxNodes, func() (*sql.Rows, error) {
		return executor.QueryContext(ctx,
			`SELECT "i"."indexrelid"::bigint, "member"."position"::integer, "i"."indkey"["member"."position"]::integer, `+
				`COALESCE("i"."indoption"["member"."position"]::integer, -1), `+
				`COALESCE("i"."indcollation"["member"."position"] = "a"."attcollation", false), `+
				`COALESCE("on"."nspname", ''), COALESCE("o"."opcname", ''), COALESCE("o"."opcdefault", false), `+
				`COALESCE("o"."opcmethod" = "ic"."relam", false) `+
				`FROM "pg_catalog"."pg_index" AS "i" JOIN "pg_catalog"."pg_class" AS "ic" ON "ic"."oid" = "i"."indexrelid" `+
				`CROSS JOIN LATERAL "pg_catalog"."generate_subscripts"("i"."indkey", 1) AS "member"("position") `+
				`LEFT JOIN "pg_catalog"."pg_attribute" AS "a" ON "a"."attrelid" = "i"."indrelid" AND "a"."attnum" = "i"."indkey"["member"."position"] `+
				`LEFT JOIN "pg_catalog"."pg_opclass" AS "o" ON "o"."oid" = "i"."indclass"["member"."position"] `+
				`LEFT JOIN "pg_catalog"."pg_namespace" AS "on" ON "on"."oid" = "o"."opcnamespace" `+
				`WHERE "i"."indrelid" = $1 ORDER BY "i"."indexrelid", "member"."position" LIMIT $2`, tableOID, postgresMigrationMaxNodes+1)
	}, func(rows *sql.Rows) (member, error) {
		var value member
		err := rows.Scan(&value.oid, &value.position, &value.key.attributeNumber, &value.key.columnOptions, &value.key.columnCollation,
			&value.key.operatorClassSchema, &value.key.operatorClassName, &value.key.operatorClassDefault, &value.key.operatorClassMethod)
		return value, err
	})
	if err != nil {
		return err
	}
	owners := make(map[int64]int, len(indexes))
	for index, value := range indexes {
		if _, exists := owners[value.oid]; exists || value.oid <= 0 {
			return postgresMigrationCatalogDrift("", "index key owner is missing or duplicated")
		}
		owners[value.oid] = index
	}
	for _, key := range keys {
		index, exists := owners[key.oid]
		if !exists || key.position != len(indexes[index].keys) || key.position >= postgresMigrationMaxFields {
			return postgresMigrationCatalogDrift("", "index key order or owner is inconsistent")
		}
		indexes[index].keys = append(indexes[index].keys, key.key)
	}
	return nil
}
