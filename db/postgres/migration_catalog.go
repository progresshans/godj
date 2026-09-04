package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

var errPostgresMigrationPhysicalDrift = errors.New("PostgreSQL application schema is outside the current migration profile")

type postgresMigrationTableCatalog struct {
	oid              int64
	name             string
	kind             string
	persistence      string
	accessMethod     string
	isPartition      bool
	rowSecurity      bool
	forceRowSecurity bool
	hasSubclass      bool
	replicaIdentity  string
	options          int
	parentCount      int
	childCount       int
	userTriggers     int
	policies         int
	rules            int
	attributeSlots   int
	columns          []postgresMigrationColumnCatalog
	constraints      []postgresMigrationConstraintCatalog
	indexes          []postgresMigrationIndexCatalog
	sequences        []postgresMigrationSequenceCatalog
}

type postgresMigrationColumnCatalog struct {
	attributeNumber  int
	name             string
	typeSchema       string
	typeName         string
	typeModifier     int
	notNull          bool
	identity         string
	generated        string
	hasDefault       bool
	defaultCollation bool
}

type postgresMigrationConstraintCatalog struct {
	oid                   int64
	name                  string
	kind                  string
	deferrable            bool
	deferred              bool
	validated             bool
	sourceKeyCount        int
	sourceAttributeNumber int
	targetOID             int64
	targetSchema          string
	targetTable           string
	targetColumn          string
	targetKeyCount        int
	targetAttributeNumber int
	updateAction          string
	deleteAction          string
	matchType             string
	indexOID              int64
	internalTriggers      int
	enabledInternal       int
}

type postgresMigrationSequenceCatalog struct {
	oid                  int64
	schema               string
	name                 string
	kind                 string
	persistence          string
	typeSchema           string
	typeName             string
	start                int64
	increment            int64
	maximum              int64
	minimum              int64
	cache                int64
	cycle                bool
	ownerTableOID        int64
	ownerAttributeNumber int
	dependencyType       string
	tableDependencyCount int
}

type postgresMigrationIndexCatalog struct {
	oid                  int64
	name                 string
	primary              bool
	unique               bool
	valid                bool
	ready                bool
	live                 bool
	keyCount             int
	totalCount           int
	firstAttributeNumber int
	hasPredicate         bool
	hasExpressions       bool
}

func loadPostgresMigrationTableCatalog(
	ctx context.Context,
	executor migrationSQLExecutor,
	namespace,
	table string,
) (postgresMigrationTableCatalog, bool, error) {
	var catalog postgresMigrationTableCatalog
	err := executor.QueryRowContext(
		ctx,
		`SELECT "c"."oid"::bigint, "c"."relname", "c"."relkind"::text, `+
			`"c"."relpersistence"::text, COALESCE("am"."amname", ''), `+
			`"c"."relispartition", "c"."relrowsecurity", "c"."relforcerowsecurity", `+
			`"c"."relhassubclass", "c"."relreplident"::text, `+
			`COALESCE("pg_catalog"."array_length"("c"."reloptions", 1), 0), `+
			`(SELECT COUNT(*) FROM "pg_catalog"."pg_inherits" AS "parent" WHERE "parent"."inhrelid" = "c"."oid"), `+
			`(SELECT COUNT(*) FROM "pg_catalog"."pg_inherits" AS "child" WHERE "child"."inhparent" = "c"."oid"), `+
			`(SELECT COUNT(*) FROM "pg_catalog"."pg_trigger" AS "trigger" WHERE "trigger"."tgrelid" = "c"."oid" AND NOT "trigger"."tgisinternal"), `+
			`(SELECT COUNT(*) FROM "pg_catalog"."pg_policy" AS "policy" WHERE "policy"."polrelid" = "c"."oid"), `+
			`(SELECT COUNT(*) FROM "pg_catalog"."pg_rewrite" AS "rule" WHERE "rule"."ev_class" = "c"."oid"), `+
			`"c"."relnatts"::integer `+
			`FROM "pg_catalog"."pg_class" AS "c" `+
			`JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "c"."relnamespace" `+
			`LEFT JOIN "pg_catalog"."pg_am" AS "am" ON "am"."oid" = "c"."relam" `+
			`WHERE "n"."nspname" = $1 AND "c"."relname" = $2`,
		namespace,
		table,
	).Scan(
		&catalog.oid,
		&catalog.name,
		&catalog.kind,
		&catalog.persistence,
		&catalog.accessMethod,
		&catalog.isPartition,
		&catalog.rowSecurity,
		&catalog.forceRowSecurity,
		&catalog.hasSubclass,
		&catalog.replicaIdentity,
		&catalog.options,
		&catalog.parentCount,
		&catalog.childCount,
		&catalog.userTriggers,
		&catalog.policies,
		&catalog.rules,
		&catalog.attributeSlots,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return postgresMigrationTableCatalog{}, false, nil
	}
	if err != nil {
		return postgresMigrationTableCatalog{}, false, classifyPostgresMigrationIO(ctx, "inspect PostgreSQL application table "+table, err)
	}
	if catalog.oid <= 0 || catalog.name != table {
		return postgresMigrationTableCatalog{}, false, postgresMigrationCapability("PostgreSQL catalog returned an invalid application table identity", errPostgresMigrationPhysicalDrift)
	}

	columns, err := readPostgresMigrationColumns(ctx, executor, catalog.oid)
	if err != nil {
		return postgresMigrationTableCatalog{}, false, err
	}
	constraints, err := readPostgresMigrationConstraints(ctx, executor, catalog.oid)
	if err != nil {
		return postgresMigrationTableCatalog{}, false, err
	}
	indexes, err := readPostgresMigrationIndexes(ctx, executor, catalog.oid)
	if err != nil {
		return postgresMigrationTableCatalog{}, false, err
	}
	catalog.columns = columns
	if catalog.attributeSlots < len(columns) || catalog.attributeSlots > postgresMigrationMaxAttributeSlots {
		return postgresMigrationTableCatalog{}, false, postgresMigrationCapability(
			fmt.Sprintf(
				"PostgreSQL application table %s has %d physical attribute slots for %d visible columns",
				table,
				catalog.attributeSlots,
				len(columns),
			),
			errPostgresMigrationPhysicalDrift,
		)
	}
	catalog.constraints = constraints
	catalog.indexes = indexes
	sequences, err := readPostgresMigrationSequences(ctx, executor, catalog.oid)
	if err != nil {
		return postgresMigrationTableCatalog{}, false, err
	}
	catalog.sequences = sequences
	return catalog, true, nil
}

func readPostgresMigrationColumns(
	ctx context.Context,
	executor migrationSQLExecutor,
	tableOID int64,
) (columns []postgresMigrationColumnCatalog, resultErr error) {
	rows, err := executor.QueryContext(
		ctx,
		`SELECT "a"."attnum"::integer, "a"."attname", "tn"."nspname", "t"."typname", `+
			`"a"."atttypmod", "a"."attnotnull", "a"."attidentity"::text, `+
			`"a"."attgenerated"::text, ("d"."oid" IS NOT NULL), `+
			`("a"."attcollation" = "t"."typcollation") `+
			`FROM "pg_catalog"."pg_attribute" AS "a" `+
			`JOIN "pg_catalog"."pg_type" AS "t" ON "t"."oid" = "a"."atttypid" `+
			`JOIN "pg_catalog"."pg_namespace" AS "tn" ON "tn"."oid" = "t"."typnamespace" `+
			`LEFT JOIN "pg_catalog"."pg_attrdef" AS "d" `+
			`ON "d"."adrelid" = "a"."attrelid" AND "d"."adnum" = "a"."attnum" `+
			`WHERE "a"."attrelid" = $1 AND "a"."attnum" > 0 AND NOT "a"."attisdropped" `+
			`ORDER BY "a"."attnum" LIMIT $2`,
		tableOID,
		postgresMigrationMaxAttributeSlots+1,
	)
	if err != nil {
		return nil, classifyPostgresMigrationIO(ctx, "inspect PostgreSQL application columns", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			resultErr = errors.Join(resultErr, classifyPostgresMigrationIO(ctx, "close PostgreSQL application column rows", closeErr))
		}
	}()
	for rows.Next() {
		if len(columns) >= postgresMigrationMaxAttributeSlots {
			return nil, postgresMigrationCapability("PostgreSQL application table exceeds the current column limit", errPostgresMigrationPhysicalDrift)
		}
		var column postgresMigrationColumnCatalog
		if err := rows.Scan(
			&column.attributeNumber,
			&column.name,
			&column.typeSchema,
			&column.typeName,
			&column.typeModifier,
			&column.notNull,
			&column.identity,
			&column.generated,
			&column.hasDefault,
			&column.defaultCollation,
		); err != nil {
			return nil, classifyPostgresMigrationIO(ctx, "scan PostgreSQL application column", err)
		}
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyPostgresMigrationIO(ctx, "iterate PostgreSQL application columns", err)
	}
	return columns, nil
}

func readPostgresMigrationConstraints(
	ctx context.Context,
	executor migrationSQLExecutor,
	tableOID int64,
) (constraints []postgresMigrationConstraintCatalog, resultErr error) {
	rows, err := executor.QueryContext(
		ctx,
		`SELECT "k"."oid"::bigint, "k"."conname", "k"."contype"::text, `+
			`"k"."condeferrable", "k"."condeferred", "k"."convalidated", `+
			`COALESCE("pg_catalog"."cardinality"("k"."conkey"), 0), `+
			`COALESCE("k"."conkey"[1]::integer, 0), "k"."confrelid"::bigint, `+
			`COALESCE("tn"."nspname", ''), COALESCE("tc"."relname", ''), `+
			`COALESCE("ta"."attname", ''), `+
			`COALESCE("pg_catalog"."cardinality"("k"."confkey"), 0), `+
			`COALESCE("k"."confkey"[1]::integer, 0), `+
			`"k"."confupdtype"::text, "k"."confdeltype"::text, "k"."confmatchtype"::text, `+
			`"k"."conindid"::bigint, `+
			`(SELECT COUNT(*) FROM "pg_catalog"."pg_trigger" AS "all_trigger" `+
			`WHERE "all_trigger"."tgconstraint" = "k"."oid" AND "all_trigger"."tgisinternal"), `+
			`(SELECT COUNT(*) FROM "pg_catalog"."pg_trigger" AS "enabled_trigger" `+
			`WHERE "enabled_trigger"."tgconstraint" = "k"."oid" AND "enabled_trigger"."tgisinternal" `+
			`AND "enabled_trigger"."tgenabled" = 'O') `+
			`FROM "pg_catalog"."pg_constraint" AS "k" `+
			`LEFT JOIN "pg_catalog"."pg_class" AS "tc" ON "tc"."oid" = "k"."confrelid" `+
			`LEFT JOIN "pg_catalog"."pg_namespace" AS "tn" ON "tn"."oid" = "tc"."relnamespace" `+
			`LEFT JOIN "pg_catalog"."pg_attribute" AS "ta" `+
			`ON "ta"."attrelid" = "k"."confrelid" AND "ta"."attnum" = "k"."confkey"[1] `+
			`WHERE "k"."conrelid" = $1 ORDER BY "k"."conname" LIMIT $2`,
		tableOID,
		postgresMigrationMaxFields+2,
	)
	if err != nil {
		return nil, classifyPostgresMigrationIO(ctx, "inspect PostgreSQL application constraints", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			resultErr = errors.Join(resultErr, classifyPostgresMigrationIO(ctx, "close PostgreSQL application constraint rows", closeErr))
		}
	}()
	for rows.Next() {
		if len(constraints) >= postgresMigrationMaxFields+1 {
			return nil, postgresMigrationCapability("PostgreSQL application table exceeds the current constraint limit", errPostgresMigrationPhysicalDrift)
		}
		var constraint postgresMigrationConstraintCatalog
		if err := rows.Scan(
			&constraint.oid,
			&constraint.name,
			&constraint.kind,
			&constraint.deferrable,
			&constraint.deferred,
			&constraint.validated,
			&constraint.sourceKeyCount,
			&constraint.sourceAttributeNumber,
			&constraint.targetOID,
			&constraint.targetSchema,
			&constraint.targetTable,
			&constraint.targetColumn,
			&constraint.targetKeyCount,
			&constraint.targetAttributeNumber,
			&constraint.updateAction,
			&constraint.deleteAction,
			&constraint.matchType,
			&constraint.indexOID,
			&constraint.internalTriggers,
			&constraint.enabledInternal,
		); err != nil {
			return nil, classifyPostgresMigrationIO(ctx, "scan PostgreSQL application constraint", err)
		}
		constraints = append(constraints, constraint)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyPostgresMigrationIO(ctx, "iterate PostgreSQL application constraints", err)
	}
	return constraints, nil
}

func readPostgresMigrationIndexes(
	ctx context.Context,
	executor migrationSQLExecutor,
	tableOID int64,
) (indexes []postgresMigrationIndexCatalog, resultErr error) {
	rows, err := executor.QueryContext(
		ctx,
		`SELECT "ic"."oid"::bigint, "ic"."relname", "i"."indisprimary", "i"."indisunique", `+
			`"i"."indisvalid", "i"."indisready", "i"."indislive", `+
			`"i"."indnkeyatts"::integer, "i"."indnatts"::integer, `+
			`COALESCE("i"."indkey"[0]::integer, 0), `+
			`("i"."indpred" IS NOT NULL), ("i"."indexprs" IS NOT NULL) `+
			`FROM "pg_catalog"."pg_index" AS "i" `+
			`JOIN "pg_catalog"."pg_class" AS "ic" ON "ic"."oid" = "i"."indexrelid" `+
			`WHERE "i"."indrelid" = $1 ORDER BY "ic"."relname" LIMIT $2`,
		tableOID,
		postgresMigrationMaxFields+2,
	)
	if err != nil {
		return nil, classifyPostgresMigrationIO(ctx, "inspect PostgreSQL application indexes", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			resultErr = errors.Join(resultErr, classifyPostgresMigrationIO(ctx, "close PostgreSQL application index rows", closeErr))
		}
	}()
	for rows.Next() {
		if len(indexes) >= postgresMigrationMaxFields+1 {
			return nil, postgresMigrationCapability("PostgreSQL application table exceeds the current index limit", errPostgresMigrationPhysicalDrift)
		}
		var index postgresMigrationIndexCatalog
		if err := rows.Scan(
			&index.oid,
			&index.name,
			&index.primary,
			&index.unique,
			&index.valid,
			&index.ready,
			&index.live,
			&index.keyCount,
			&index.totalCount,
			&index.firstAttributeNumber,
			&index.hasPredicate,
			&index.hasExpressions,
		); err != nil {
			return nil, classifyPostgresMigrationIO(ctx, "scan PostgreSQL application index", err)
		}
		indexes = append(indexes, index)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyPostgresMigrationIO(ctx, "iterate PostgreSQL application indexes", err)
	}
	return indexes, nil
}

func readPostgresMigrationSequences(
	ctx context.Context,
	executor migrationSQLExecutor,
	tableOID int64,
) (sequences []postgresMigrationSequenceCatalog, resultErr error) {
	rows, err := executor.QueryContext(
		ctx,
		`SELECT "sc"."oid"::bigint, "sn"."nspname", "sc"."relname", `+
			`"sc"."relkind"::text, "sc"."relpersistence"::text, `+
			`"tn"."nspname", "t"."typname", `+
			`"s"."seqstart", "s"."seqincrement", "s"."seqmax", "s"."seqmin", `+
			`"s"."seqcache", "s"."seqcycle", `+
			`"d"."refobjid"::bigint, "d"."refobjsubid"::integer, "d"."deptype"::text, `+
			`(SELECT COUNT(*) FROM "pg_catalog"."pg_depend" AS "all_d" `+
			`WHERE "all_d"."classid" = "d"."classid" AND "all_d"."objid" = "sc"."oid" `+
			`AND "all_d"."refclassid" = "d"."refclassid" AND "all_d"."refobjid" = $1) `+
			`FROM "pg_catalog"."pg_sequence" AS "s" `+
			`JOIN "pg_catalog"."pg_class" AS "sc" ON "sc"."oid" = "s"."seqrelid" `+
			`JOIN "pg_catalog"."pg_namespace" AS "sn" ON "sn"."oid" = "sc"."relnamespace" `+
			`JOIN "pg_catalog"."pg_type" AS "t" ON "t"."oid" = "s"."seqtypid" `+
			`JOIN "pg_catalog"."pg_namespace" AS "tn" ON "tn"."oid" = "t"."typnamespace" `+
			`JOIN "pg_catalog"."pg_depend" AS "d" ON "d"."objid" = "sc"."oid" `+
			`JOIN "pg_catalog"."pg_class" AS "dc" ON "dc"."oid" = "d"."classid" `+
			`JOIN "pg_catalog"."pg_namespace" AS "dn" ON "dn"."oid" = "dc"."relnamespace" `+
			`WHERE "dn"."nspname" = 'pg_catalog' AND "dc"."relname" = 'pg_class' `+
			`AND "d"."refclassid" = "d"."classid" AND "d"."refobjid" = $1 `+
			`AND "d"."deptype" = 'i' ORDER BY "sc"."relname" LIMIT $2`,
		tableOID,
		3,
	)
	if err != nil {
		return nil, classifyPostgresMigrationIO(ctx, "inspect PostgreSQL identity sequences", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			resultErr = errors.Join(resultErr, classifyPostgresMigrationIO(ctx, "close PostgreSQL identity sequence rows", closeErr))
		}
	}()
	for rows.Next() {
		if len(sequences) >= 2 {
			return nil, postgresMigrationCapability("PostgreSQL application table has too many identity sequences", errPostgresMigrationPhysicalDrift)
		}
		var sequence postgresMigrationSequenceCatalog
		if err := rows.Scan(
			&sequence.oid,
			&sequence.schema,
			&sequence.name,
			&sequence.kind,
			&sequence.persistence,
			&sequence.typeSchema,
			&sequence.typeName,
			&sequence.start,
			&sequence.increment,
			&sequence.maximum,
			&sequence.minimum,
			&sequence.cache,
			&sequence.cycle,
			&sequence.ownerTableOID,
			&sequence.ownerAttributeNumber,
			&sequence.dependencyType,
			&sequence.tableDependencyCount,
		); err != nil {
			return nil, classifyPostgresMigrationIO(ctx, "scan PostgreSQL identity sequence", err)
		}
		sequences = append(sequences, sequence)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyPostgresMigrationIO(ctx, "iterate PostgreSQL identity sequences", err)
	}
	return sequences, nil
}

func assertPostgresMigrationModelCatalog(
	catalog postgresMigrationTableCatalog,
	namespace string,
	model ir.Model,
	targets []migrationbackend.MigrationTarget,
) error {
	if err := assertPostgresMigrationOrdinaryTable(catalog, model.DBTable); err != nil {
		return err
	}
	if catalog.attributeSlots < len(catalog.columns) || catalog.attributeSlots > postgresMigrationMaxAttributeSlots {
		return postgresMigrationCatalogDrift(
			model.DBTable,
			fmt.Sprintf("has %d physical attribute slots for %d visible columns", catalog.attributeSlots, len(catalog.columns)),
		)
	}
	if len(catalog.columns) != len(model.Fields) {
		return postgresMigrationCatalogDrift(model.DBTable, fmt.Sprintf("has %d columns, want %d", len(catalog.columns), len(model.Fields)))
	}
	previousAttributeNumber := 0
	for index := range model.Fields {
		if catalog.columns[index].attributeNumber > catalog.attributeSlots {
			return postgresMigrationCatalogDrift(model.DBTable, "visible column exceeds the physical attribute-slot boundary")
		}
		if catalog.columns[index].attributeNumber <= previousAttributeNumber {
			return postgresMigrationCatalogDrift(model.DBTable, "non-dropped columns are not in increasing physical order")
		}
		previousAttributeNumber = catalog.columns[index].attributeNumber
		if err := assertPostgresMigrationColumnCatalog(catalog.columns[index], model.Fields[index]); err != nil {
			return postgresMigrationCatalogDrift(model.DBTable, err.Error())
		}
	}

	expectedConstraints := make(map[string]postgresMigrationConstraintCatalog, len(targets)+1)
	primaryKey, err := postgresMigrationPrimaryKey(model)
	if err != nil {
		return postgresMigrationIntentIntegrity("validate PostgreSQL model catalog primary key authority", err)
	}
	primaryName, err := postgresPrimaryKeyConstraintName(model.DBTable)
	if err != nil {
		return postgresMigrationIntentIntegrity("derive PostgreSQL catalog primary key name", err)
	}
	primaryAttribute := postgresMigrationCatalogAttributeNumber(catalog, primaryKey.Column)
	if primaryAttribute == 0 {
		return postgresMigrationCatalogDrift(model.DBTable, "does not contain the sealed primary-key column")
	}
	if err := assertPostgresMigrationIdentitySequence(catalog, namespace, model, primaryKey, primaryAttribute); err != nil {
		return err
	}
	expectedConstraints[primaryName] = postgresMigrationConstraintCatalog{
		name: primaryName, kind: "p", validated: true, sourceKeyCount: 1,
		sourceAttributeNumber: primaryAttribute,
	}
	for index := range targets {
		target := targets[index]
		name, err := postgresForeignKeyConstraintName(model.DBTable, target.SourceField.Column)
		if err != nil {
			return postgresMigrationIntentIntegrity("derive PostgreSQL catalog foreign key name", err)
		}
		expectedConstraints[name] = postgresMigrationConstraintCatalog{
			name: name, kind: "f", validated: true, sourceKeyCount: 1,
			sourceAttributeNumber: postgresMigrationCatalogAttributeNumber(catalog, target.SourceField.Column),
			targetSchema:          namespace, targetTable: target.TargetModel.DBTable, targetKeyCount: 1,
			targetColumn: target.TargetKey.Column,
			updateAction: "a", deleteAction: "a", matchType: "s",
			internalTriggers: 4, enabledInternal: 4,
		}
	}
	if len(catalog.constraints) != len(expectedConstraints) {
		return postgresMigrationCatalogDrift(model.DBTable, fmt.Sprintf("has %d constraints, want %d", len(catalog.constraints), len(expectedConstraints)))
	}
	var primaryConstraint postgresMigrationConstraintCatalog
	for index := range catalog.constraints {
		actual := catalog.constraints[index]
		expected, exists := expectedConstraints[actual.name]
		if !exists {
			return postgresMigrationCatalogDrift(model.DBTable, fmt.Sprintf("has unexpected constraint %q", actual.name))
		}
		if actual.kind != expected.kind || actual.deferrable || actual.deferred || actual.validated != expected.validated ||
			actual.sourceKeyCount != expected.sourceKeyCount || actual.sourceAttributeNumber != expected.sourceAttributeNumber {
			return postgresMigrationCatalogDrift(model.DBTable, fmt.Sprintf("constraint %q has an unsupported source shape", actual.name))
		}
		switch actual.kind {
		case "p":
			if actual.targetOID != 0 || actual.targetSchema != "" || actual.targetTable != "" || actual.targetKeyCount != 0 ||
				actual.targetAttributeNumber != 0 || actual.indexOID <= 0 || actual.internalTriggers != 0 || actual.enabledInternal != 0 {
				return postgresMigrationCatalogDrift(model.DBTable, fmt.Sprintf("primary key %q has an unsupported target/index shape", actual.name))
			}
			primaryConstraint = actual
		case "f":
			if actual.targetOID <= 0 || actual.targetSchema != expected.targetSchema || actual.targetTable != expected.targetTable ||
				actual.targetColumn != expected.targetColumn || actual.targetKeyCount != expected.targetKeyCount || actual.targetAttributeNumber <= 0 ||
				actual.updateAction != expected.updateAction || actual.deleteAction != expected.deleteAction ||
				actual.matchType != expected.matchType || actual.indexOID <= 0 ||
				actual.internalTriggers != expected.internalTriggers || actual.enabledInternal != expected.enabledInternal {
				return postgresMigrationCatalogDrift(model.DBTable, fmt.Sprintf("foreign key %q has an unsupported target/action shape", actual.name))
			}
		default:
			return postgresMigrationCatalogDrift(model.DBTable, fmt.Sprintf("constraint %q has unsupported kind %q", actual.name, actual.kind))
		}
	}
	if primaryConstraint.oid == 0 {
		return postgresMigrationCatalogDrift(model.DBTable, "has no exact framework primary key")
	}
	if len(catalog.indexes) != 1 {
		return postgresMigrationCatalogDrift(model.DBTable, fmt.Sprintf("has %d indexes, want the exact primary index", len(catalog.indexes)))
	}
	index := catalog.indexes[0]
	if index.oid != primaryConstraint.indexOID || index.name != primaryConstraint.name || !index.primary || !index.unique ||
		!index.valid || !index.ready || !index.live || index.keyCount != 1 || index.totalCount != 1 ||
		index.firstAttributeNumber != primaryAttribute || index.hasPredicate || index.hasExpressions {
		return postgresMigrationCatalogDrift(model.DBTable, "primary index shape is outside the current profile")
	}
	return nil
}

func assertPostgresMigrationTargetCatalog(
	catalog postgresMigrationTableCatalog,
	namespace string,
	model ir.Model,
	targetKey ir.Field,
) error {
	if err := assertPostgresMigrationOrdinaryTable(catalog, model.DBTable); err != nil {
		return err
	}
	attributeNumber := postgresMigrationCatalogAttributeNumber(catalog, targetKey.Column)
	if attributeNumber == 0 {
		return postgresMigrationCatalogDrift(model.DBTable, "does not contain the sealed target key column")
	}
	column, exists := postgresMigrationCatalogColumn(catalog, targetKey.Column)
	if !exists {
		return postgresMigrationCatalogDrift(model.DBTable, "does not contain the sealed target key column")
	}
	if err := assertPostgresMigrationColumnCatalog(column, targetKey); err != nil {
		return postgresMigrationCatalogDrift(model.DBTable, err.Error())
	}
	if err := assertPostgresMigrationIdentitySequence(catalog, namespace, model, targetKey, attributeNumber); err != nil {
		return err
	}
	primaryName, err := postgresPrimaryKeyConstraintName(model.DBTable)
	if err != nil {
		return postgresMigrationIntentIntegrity("derive PostgreSQL target catalog primary key name", err)
	}
	for index := range catalog.constraints {
		constraint := catalog.constraints[index]
		if constraint.name == primaryName && constraint.kind == "p" && !constraint.deferrable && !constraint.deferred &&
			constraint.validated && constraint.sourceKeyCount == 1 && constraint.sourceAttributeNumber == attributeNumber &&
			constraint.indexOID > 0 && constraint.internalTriggers == 0 && constraint.enabledInternal == 0 {
			for indexIndex := range catalog.indexes {
				candidate := catalog.indexes[indexIndex]
				if candidate.oid == constraint.indexOID && candidate.name == primaryName && candidate.primary && candidate.unique &&
					candidate.valid && candidate.ready && candidate.live && candidate.keyCount == 1 && candidate.totalCount == 1 &&
					candidate.firstAttributeNumber == attributeNumber && !candidate.hasPredicate && !candidate.hasExpressions {
					return nil
				}
			}
		}
	}
	return postgresMigrationCatalogDrift(model.DBTable, "does not contain the exact sealed target primary key")
}

func assertPostgresMigrationOrdinaryTable(catalog postgresMigrationTableCatalog, table string) error {
	if catalog.name != table || catalog.oid <= 0 || catalog.kind != "r" || catalog.persistence != "p" ||
		catalog.accessMethod != "heap" || catalog.isPartition || catalog.rowSecurity || catalog.forceRowSecurity ||
		catalog.hasSubclass || catalog.replicaIdentity != "d" || catalog.options != 0 || catalog.parentCount != 0 ||
		catalog.childCount != 0 || catalog.userTriggers != 0 || catalog.policies != 0 || catalog.rules != 0 {
		return postgresMigrationCatalogDrift(table, "physical table shape is outside the ordinary persistent current profile")
	}
	return nil
}

func assertPostgresMigrationColumnCatalog(
	actual postgresMigrationColumnCatalog,
	field ir.Field,
) error {
	if actual.attributeNumber <= 0 || actual.name != field.Column || actual.typeSchema != "pg_catalog" ||
		actual.generated != "" || actual.hasDefault || !actual.defaultCollation {
		return fmt.Errorf("column %q base shape is outside the current profile", field.Column)
	}
	switch field.Kind {
	case ir.FieldAuto:
		if actual.typeName != "int8" || actual.typeModifier != -1 || !actual.notNull || actual.identity != "d" {
			return fmt.Errorf("AutoField column %q has an unsupported physical shape", field.Column)
		}
	case ir.FieldChar:
		if actual.typeName != "varchar" || actual.typeModifier != field.MaxLength+4 || actual.notNull == field.Nullable || actual.identity != "" {
			return fmt.Errorf("CharField column %q has an unsupported physical shape", field.Column)
		}
	case ir.FieldBoolean:
		if actual.typeName != "bool" || actual.typeModifier != -1 || !actual.notNull || actual.identity != "" {
			return fmt.Errorf("BooleanField column %q has an unsupported physical shape", field.Column)
		}
	case ir.FieldForeignKey:
		if actual.typeName != "int8" || actual.typeModifier != -1 || actual.notNull == field.Nullable || actual.identity != "" {
			return fmt.Errorf("ForeignKey column %q has an unsupported physical shape", field.Column)
		}
	default:
		return fmt.Errorf("column %q has unsupported field kind %q", field.Column, field.Kind)
	}
	return nil
}

func assertPostgresMigrationIdentitySequence(
	catalog postgresMigrationTableCatalog,
	namespace string,
	model ir.Model,
	primaryKey ir.Field,
	primaryAttribute int,
) error {
	if len(catalog.sequences) != 1 {
		return postgresMigrationCatalogDrift(model.DBTable, fmt.Sprintf("has %d identity sequences, want 1", len(catalog.sequences)))
	}
	expectedName, err := postgresIdentitySequenceName(model.DBTable, primaryKey.Column)
	if err != nil {
		return postgresMigrationIntentIntegrity("derive PostgreSQL catalog identity sequence name", err)
	}
	sequence := catalog.sequences[0]
	if namespace != "" && sequence.schema != namespace {
		return postgresMigrationCatalogDrift(model.DBTable, "identity sequence is in a different schema")
	}
	if sequence.oid <= 0 || sequence.name != expectedName || sequence.kind != "S" || sequence.persistence != "p" ||
		sequence.typeSchema != "pg_catalog" || sequence.typeName != "int8" || sequence.start != 1 ||
		sequence.increment != 1 || sequence.maximum != math.MaxInt64 || sequence.minimum != 1 ||
		sequence.cache != 1 || sequence.cycle || sequence.ownerTableOID != catalog.oid ||
		sequence.ownerAttributeNumber != primaryAttribute || sequence.dependencyType != "i" ||
		sequence.tableDependencyCount != 1 {
		return postgresMigrationCatalogDrift(model.DBTable, "identity sequence shape or internal ownership is outside the current profile")
	}
	return nil
}

func postgresMigrationFieldAttributeNumber(model ir.Model, column string) int {
	for index := range model.Fields {
		if model.Fields[index].Column == column {
			return index + 1
		}
	}
	return 0
}

func postgresMigrationCatalogAttributeNumber(catalog postgresMigrationTableCatalog, column string) int {
	value, exists := postgresMigrationCatalogColumn(catalog, column)
	if !exists {
		return 0
	}
	return value.attributeNumber
}

func postgresMigrationCatalogColumn(
	catalog postgresMigrationTableCatalog,
	column string,
) (postgresMigrationColumnCatalog, bool) {
	for index := range catalog.columns {
		if catalog.columns[index].name == column {
			return catalog.columns[index], true
		}
	}
	return postgresMigrationColumnCatalog{}, false
}

func postgresMigrationCatalogDrift(table, detail string) error {
	return postgresMigrationCapability(
		fmt.Sprintf("PostgreSQL application table %q %s", table, detail),
		errPostgresMigrationPhysicalDrift,
	)
}

func sortedPostgresMigrationCatalogNames(models map[string]ir.Model) []string {
	names := make([]string, 0, len(models))
	for _, model := range models {
		names = append(names, model.DBTable)
	}
	sort.Strings(names)
	return names
}
