package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
)

const (
	postgresMigrationRecorderTable      = "godj_migrations"
	postgresMigrationRevisionTable      = "godj_migration_revision"
	postgresMigrationRevisionFormat     = int32(1)
	postgresMigrationRevisionEpochBytes = 16
	postgresMigrationHistoryDigestBytes = sha256.Size
	// Current definition sets can publish at most 2,048 migration documents,
	// so a larger durable history cannot be consumed by the current lifecycle.
	postgresMigrationHistoryRecordLimit = 2048
	postgresMigrationRecorderPrimaryKey = "godj_migrations_pkey"
	postgresMigrationRevisionPrimaryKey = "godj_migration_revision_pkey"
)

type postgresMigrationRevisionToken struct {
	initialized bool
	epoch       [postgresMigrationRevisionEpochBytes]byte
	revision    int64
	fingerprint [postgresMigrationHistoryDigestBytes]byte
}

type postgresMigrationRevisionSnapshot struct {
	revisionPresent bool
	recorderPresent bool
	records         []migrationbackend.AppliedMigration
	token           postgresMigrationRevisionToken
}

type postgresMigrationControlColumn struct {
	attributeNumber  int
	name             string
	typeName         string
	notNull          bool
	identity         string
	generated        string
	hasDefault       bool
	dropped          bool
	inheritedCount   int
	local            bool
	defaultCollation bool
}

type postgresMigrationControlConstraint struct {
	name       string
	kind       string
	deferrable bool
	deferred   bool
	validated  bool
	key        string
}

type postgresMigrationControlTableProfile struct {
	kind            string
	persistence     string
	accessMethod    string
	isPartition     bool
	rowSecurity     bool
	forceSecurity   bool
	hasSubclass     bool
	replicaIdentity string
	options         int
	parentCount     int
	childCount      int
	triggers        int
	policies        int
	rules           int
}

type postgresMigrationControlIndex struct {
	name           string
	accessMethod   string
	primary        bool
	unique         bool
	exclusion      bool
	valid          bool
	ready          bool
	live           bool
	keyCount       int
	totalCount     int
	key            string
	hasPredicate   bool
	hasExpressions bool
}

var _ migrationbackend.AppliedMigrationReader = (*Backend)(nil)

// ReadAppliedMigrations reads the same current-only, fail-closed snapshot used
// by a revision-fenced session. It never creates or adopts control objects.
func (b *Backend) ReadAppliedMigrations(ctx context.Context) ([]migrationbackend.AppliedMigration, error) {
	if err := b.validateContext(ctx); err != nil {
		return nil, err
	}
	snapshot, err := readAtomicPostgresMigrationSnapshot(ctx, b)
	if err != nil {
		return nil, err
	}
	if !snapshot.revisionPresent && snapshot.recorderPresent {
		return nil, newPostgresRevisionFenceError(
			migrationbackend.RevisionFenceFailureAdoptionRequired,
			errors.New("PostgreSQL migration recorder exists without revision metadata; exclusive adoption is required"),
		)
	}
	return clonePostgresAppliedMigrations(snapshot.records), nil
}

func readAtomicPostgresMigrationSnapshot(
	ctx context.Context,
	backend *Backend,
) (postgresMigrationRevisionSnapshot, error) {
	connection, err := backend.database.Conn(ctx)
	if err != nil {
		return postgresMigrationRevisionSnapshot{}, classifyPostgresMigrationIO(ctx, "acquire atomic migration history snapshot connection", err)
	}
	if _, err := connection.ExecContext(
		ctx,
		"BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY NOT DEFERRABLE",
	); err != nil {
		return postgresMigrationRevisionSnapshot{}, errors.Join(
			classifyPostgresMigrationIO(ctx, "begin atomic migration history snapshot", err),
			discardPostgresMigrationConnection(connection),
		)
	}
	snapshot, snapshotErr := inspectPostgresMigrationSnapshot(ctx, connection, backend.schema)
	if snapshotErr != nil {
		_, cleanupErr := rollbackAndReleasePostgresMigrationConnection(ctx, connection)
		return postgresMigrationRevisionSnapshot{}, errors.Join(
			snapshotErr,
			cleanupErr,
		)
	}
	if _, err := connection.ExecContext(ctx, "COMMIT"); err != nil {
		// This read-only transaction performed no durable mutation, so a COMMIT
		// error is an I/O failure rather than a migration commit-unknown outcome.
		return postgresMigrationRevisionSnapshot{}, errors.Join(
			classifyPostgresMigrationIO(ctx, "commit atomic migration history snapshot", err),
			discardPostgresMigrationConnection(connection),
		)
	}
	if err := closeOrDiscardPostgresMigrationConnection(connection); err != nil {
		return postgresMigrationRevisionSnapshot{}, err
	}
	return snapshot, nil
}

func inspectPostgresMigrationSnapshot(
	ctx context.Context,
	executor migrationSQLExecutor,
	namespace string,
) (postgresMigrationRevisionSnapshot, error) {
	revisionPresent, err := postgresMigrationControlObjectPresent(
		ctx,
		executor,
		namespace,
		postgresMigrationRevisionTable,
	)
	if err != nil {
		return postgresMigrationRevisionSnapshot{}, err
	}
	recorderPresent, err := postgresMigrationControlObjectPresent(
		ctx,
		executor,
		namespace,
		postgresMigrationRecorderTable,
	)
	if err != nil {
		return postgresMigrationRevisionSnapshot{}, err
	}
	if !revisionPresent {
		// Any pre-existing recorder object belongs to an unfenced/unknown
		// history and therefore requires an explicit exclusive adoption path.
		// Do not try to reinterpret or repair its shape here.
		return postgresMigrationRevisionSnapshot{
			recorderPresent: recorderPresent,
			records:         []migrationbackend.AppliedMigration{},
			token: postgresMigrationRevisionToken{
				fingerprint: fingerprintPostgresMigrationHistory(nil),
			},
		}, nil
	}
	revisionPresent, err = inspectPostgresMigrationControlTable(
		ctx,
		executor,
		namespace,
		postgresMigrationRevisionTable,
		postgresMigrationRevisionColumns(),
		postgresMigrationControlConstraint{
			name: postgresMigrationRevisionPrimaryKey, kind: "p", validated: true, key: "1",
		},
		postgresMigrationRevisionIndex(),
	)
	if err != nil {
		return postgresMigrationRevisionSnapshot{}, err
	}
	if !revisionPresent {
		return postgresMigrationRevisionSnapshot{}, postgresRevisionIntegrity(
			"PostgreSQL migration revision control object disappeared during one atomic snapshot",
			nil,
		)
	}
	if !recorderPresent {
		return postgresMigrationRevisionSnapshot{}, postgresRevisionIntegrity(
			"PostgreSQL migration revision metadata exists without a recorder table",
			nil,
		)
	}
	recorderPresent, err = inspectPostgresMigrationControlTable(
		ctx,
		executor,
		namespace,
		postgresMigrationRecorderTable,
		postgresMigrationRecorderColumns(),
		postgresMigrationControlConstraint{
			name: postgresMigrationRecorderPrimaryKey, kind: "p", validated: true, key: "1,2",
		},
		postgresMigrationRecorderIndex(),
	)
	if err != nil {
		return postgresMigrationRevisionSnapshot{}, err
	}
	if !recorderPresent {
		return postgresMigrationRevisionSnapshot{}, postgresRevisionIntegrity(
			"PostgreSQL migration recorder control object disappeared during one atomic snapshot",
			nil,
		)
	}
	records, err := readPostgresMigrationRecorder(ctx, executor, namespace)
	if err != nil {
		return postgresMigrationRevisionSnapshot{}, err
	}
	token, err := readPostgresMigrationRevisionToken(ctx, executor, namespace)
	if err != nil {
		return postgresMigrationRevisionSnapshot{}, err
	}
	fingerprint := fingerprintPostgresMigrationHistory(records)
	if fingerprint != token.fingerprint {
		return postgresMigrationRevisionSnapshot{}, postgresRevisionIntegrity(
			"stored PostgreSQL migration history fingerprint does not match recorder identities",
			nil,
		)
	}
	return postgresMigrationRevisionSnapshot{
		revisionPresent: true,
		recorderPresent: true,
		records:         records,
		token:           token,
	}, nil
}

func postgresMigrationControlObjectPresent(
	ctx context.Context,
	executor migrationSQLExecutor,
	namespace,
	table string,
) (bool, error) {
	var present bool
	if err := executor.QueryRowContext(
		ctx,
		`SELECT EXISTS (`+
			`SELECT 1 `+
			`FROM "pg_catalog"."pg_class" AS "c" `+
			`JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "c"."relnamespace" `+
			`WHERE "n"."nspname" = $1 AND "c"."relname" = $2)`,
		namespace,
		table,
	).Scan(&present); err != nil {
		return false, classifyPostgresMigrationIO(ctx, "inspect PostgreSQL migration control presence "+table, err)
	}
	return present, nil
}

func postgresMigrationRecorderColumns() []postgresMigrationControlColumn {
	return []postgresMigrationControlColumn{
		{attributeNumber: 1, name: "app", typeName: "character varying(255)", notNull: true, local: true, defaultCollation: true},
		{attributeNumber: 2, name: "name", typeName: "character varying(255)", notNull: true, local: true, defaultCollation: true},
	}
}

func postgresMigrationRevisionColumns() []postgresMigrationControlColumn {
	return []postgresMigrationControlColumn{
		{attributeNumber: 1, name: "singleton", typeName: "smallint", notNull: true, local: true, defaultCollation: true},
		{attributeNumber: 2, name: "format_version", typeName: "integer", notNull: true, local: true, defaultCollation: true},
		{attributeNumber: 3, name: "epoch", typeName: "bytea", notNull: true, local: true, defaultCollation: true},
		{attributeNumber: 4, name: "revision", typeName: "bigint", notNull: true, local: true, defaultCollation: true},
		{attributeNumber: 5, name: "history_fingerprint", typeName: "bytea", notNull: true, local: true, defaultCollation: true},
	}
}

func postgresMigrationRecorderIndex() postgresMigrationControlIndex {
	return postgresMigrationControlIndex{
		name:         postgresMigrationRecorderPrimaryKey,
		accessMethod: "btree",
		primary:      true,
		unique:       true,
		valid:        true,
		ready:        true,
		live:         true,
		keyCount:     2,
		totalCount:   2,
		key:          "1 2",
	}
}

func postgresMigrationRevisionIndex() postgresMigrationControlIndex {
	return postgresMigrationControlIndex{
		name:         postgresMigrationRevisionPrimaryKey,
		accessMethod: "btree",
		primary:      true,
		unique:       true,
		valid:        true,
		ready:        true,
		live:         true,
		keyCount:     1,
		totalCount:   1,
		key:          "1",
	}
}

func expectedPostgresMigrationControlTableProfile() postgresMigrationControlTableProfile {
	return postgresMigrationControlTableProfile{
		kind:            "r",
		persistence:     "p",
		accessMethod:    "heap",
		replicaIdentity: "d",
	}
}

func inspectPostgresMigrationControlTable(
	ctx context.Context,
	executor migrationSQLExecutor,
	namespace,
	table string,
	expectedColumns []postgresMigrationControlColumn,
	expectedConstraint postgresMigrationControlConstraint,
	expectedIndex postgresMigrationControlIndex,
) (bool, error) {
	var profile postgresMigrationControlTableProfile
	err := executor.QueryRowContext(
		ctx,
		`SELECT "c"."relkind"::text, "c"."relpersistence"::text, COALESCE("am"."amname", ''), `+
			`"c"."relispartition", "c"."relrowsecurity", "c"."relforcerowsecurity", `+
			`"c"."relhassubclass", "c"."relreplident"::text, `+
			`COALESCE("pg_catalog"."array_length"("c"."reloptions", 1), 0), `+
			`(SELECT COUNT(*) FROM "pg_catalog"."pg_inherits" AS "parent" WHERE "parent"."inhrelid" = "c"."oid"), `+
			`(SELECT COUNT(*) FROM "pg_catalog"."pg_inherits" AS "child" WHERE "child"."inhparent" = "c"."oid"), `+
			`(SELECT COUNT(*) FROM "pg_catalog"."pg_trigger" AS "trigger" `+
			`WHERE "trigger"."tgrelid" = "c"."oid"), `+
			`(SELECT COUNT(*) FROM "pg_catalog"."pg_policy" AS "policy" WHERE "policy"."polrelid" = "c"."oid"), `+
			`(SELECT COUNT(*) FROM "pg_catalog"."pg_rewrite" AS "rule" WHERE "rule"."ev_class" = "c"."oid") `+
			`FROM "pg_catalog"."pg_class" AS "c" `+
			`JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "c"."relnamespace" `+
			`LEFT JOIN "pg_catalog"."pg_am" AS "am" ON "am"."oid" = "c"."relam" `+
			`WHERE "n"."nspname" = $1 AND "c"."relname" = $2`,
		namespace,
		table,
	).Scan(
		&profile.kind,
		&profile.persistence,
		&profile.accessMethod,
		&profile.isPartition,
		&profile.rowSecurity,
		&profile.forceSecurity,
		&profile.hasSubclass,
		&profile.replicaIdentity,
		&profile.options,
		&profile.parentCount,
		&profile.childCount,
		&profile.triggers,
		&profile.policies,
		&profile.rules,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, classifyPostgresMigrationIO(ctx, "inspect PostgreSQL migration control object "+table, err)
	}
	if err := validatePostgresMigrationControlTableProfile(table, profile); err != nil {
		return false, err
	}
	columns, err := readPostgresMigrationControlColumns(ctx, executor, namespace, table)
	if err != nil {
		return false, err
	}
	constraints, err := readPostgresMigrationControlConstraints(ctx, executor, namespace, table)
	if err != nil {
		return false, err
	}
	indexes, err := readPostgresMigrationControlIndexes(ctx, executor, namespace, table)
	if err != nil {
		return false, err
	}
	if err := validatePostgresMigrationControlShape(
		table,
		columns,
		expectedColumns,
		constraints,
		expectedConstraint,
		indexes,
		expectedIndex,
	); err != nil {
		return false, err
	}
	return true, nil
}

func validatePostgresMigrationControlTableProfile(
	table string,
	profile postgresMigrationControlTableProfile,
) error {
	expected := expectedPostgresMigrationControlTableProfile()
	if profile == expected {
		return nil
	}
	return postgresRevisionIntegrity(
		fmt.Sprintf("PostgreSQL migration control object %s profile=%+v, want=%+v", table, profile, expected),
		nil,
	)
}

func validatePostgresMigrationControlShape(
	table string,
	columns,
	expectedColumns []postgresMigrationControlColumn,
	constraints []postgresMigrationControlConstraint,
	expectedConstraint postgresMigrationControlConstraint,
	indexes []postgresMigrationControlIndex,
	expectedIndex postgresMigrationControlIndex,
) error {
	if len(columns) != len(expectedColumns) {
		return postgresRevisionIntegrity(
			fmt.Sprintf("PostgreSQL migration control table %s has %d columns, want %d", table, len(columns), len(expectedColumns)),
			nil,
		)
	}
	for index := range columns {
		if columns[index] != expectedColumns[index] {
			return postgresRevisionIntegrity(
				fmt.Sprintf(
					"PostgreSQL migration control table %s column[%d] shape=%+v, want=%+v",
					table,
					index,
					columns[index],
					expectedColumns[index],
				),
				nil,
			)
		}
	}
	if len(constraints) != 1 || constraints[0] != expectedConstraint {
		return postgresRevisionIntegrity(
			fmt.Sprintf("PostgreSQL migration control table %s constraints=%+v, want=[%+v]", table, constraints, expectedConstraint),
			nil,
		)
	}
	if len(indexes) != 1 || indexes[0] != expectedIndex {
		return postgresRevisionIntegrity(
			fmt.Sprintf("PostgreSQL migration control table %s indexes=%+v, want=[%+v]", table, indexes, expectedIndex),
			nil,
		)
	}
	return nil
}

func readPostgresMigrationControlColumns(
	ctx context.Context,
	executor migrationSQLExecutor,
	namespace,
	table string,
) (columns []postgresMigrationControlColumn, resultErr error) {
	rows, err := executor.QueryContext(
		ctx,
		`SELECT "a"."attnum"::integer, "a"."attname", `+
			`"pg_catalog"."format_type"("a"."atttypid", "a"."atttypmod"), `+
			`"a"."attnotnull", "a"."attidentity"::text, "a"."attgenerated"::text, `+
			`("d"."oid" IS NOT NULL), "a"."attisdropped", "a"."attinhcount"::integer, "a"."attislocal", `+
			`COALESCE("a"."attcollation" = "t"."typcollation", "a"."attcollation" = 0) `+
			`FROM "pg_catalog"."pg_class" AS "c" `+
			`JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "c"."relnamespace" `+
			`JOIN "pg_catalog"."pg_attribute" AS "a" ON "a"."attrelid" = "c"."oid" `+
			`LEFT JOIN "pg_catalog"."pg_type" AS "t" ON "t"."oid" = "a"."atttypid" `+
			`LEFT JOIN "pg_catalog"."pg_attrdef" AS "d" `+
			`ON "d"."adrelid" = "a"."attrelid" AND "d"."adnum" = "a"."attnum" `+
			`WHERE "n"."nspname" = $1 AND "c"."relname" = $2 `+
			`AND "a"."attnum" > 0 `+
			`ORDER BY "a"."attnum" LIMIT 6`,
		namespace,
		table,
	)
	if err != nil {
		return nil, classifyPostgresMigrationIO(ctx, "inspect PostgreSQL migration control columns", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			columns = nil
			resultErr = errors.Join(resultErr, classifyPostgresMigrationIO(ctx, "close PostgreSQL migration control columns", err))
		}
	}()
	for rows.Next() {
		var column postgresMigrationControlColumn
		if err := rows.Scan(
			&column.attributeNumber,
			&column.name,
			&column.typeName,
			&column.notNull,
			&column.identity,
			&column.generated,
			&column.hasDefault,
			&column.dropped,
			&column.inheritedCount,
			&column.local,
			&column.defaultCollation,
		); err != nil {
			return nil, classifyPostgresMigrationIO(ctx, "scan PostgreSQL migration control column", err)
		}
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyPostgresMigrationIO(ctx, "iterate PostgreSQL migration control columns", err)
	}
	return columns, nil
}

func readPostgresMigrationControlIndexes(
	ctx context.Context,
	executor migrationSQLExecutor,
	namespace,
	table string,
) (indexes []postgresMigrationControlIndex, resultErr error) {
	rows, err := executor.QueryContext(
		ctx,
		`SELECT "ic"."relname", COALESCE("am"."amname", ''), `+
			`"i"."indisprimary", "i"."indisunique", "i"."indisexclusion", `+
			`"i"."indisvalid", "i"."indisready", "i"."indislive", `+
			`"i"."indnkeyatts"::integer, "i"."indnatts"::integer, "i"."indkey"::text, `+
			`("i"."indpred" IS NOT NULL), ("i"."indexprs" IS NOT NULL) `+
			`FROM "pg_catalog"."pg_index" AS "i" `+
			`JOIN "pg_catalog"."pg_class" AS "c" ON "c"."oid" = "i"."indrelid" `+
			`JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "c"."relnamespace" `+
			`JOIN "pg_catalog"."pg_class" AS "ic" ON "ic"."oid" = "i"."indexrelid" `+
			`LEFT JOIN "pg_catalog"."pg_am" AS "am" ON "am"."oid" = "ic"."relam" `+
			`WHERE "n"."nspname" = $1 AND "c"."relname" = $2 `+
			`ORDER BY "ic"."relname" LIMIT 2`,
		namespace,
		table,
	)
	if err != nil {
		return nil, classifyPostgresMigrationIO(ctx, "inspect PostgreSQL migration control indexes", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			indexes = nil
			resultErr = errors.Join(resultErr, classifyPostgresMigrationIO(ctx, "close PostgreSQL migration control indexes", err))
		}
	}()
	for rows.Next() {
		if len(indexes) == 1 {
			return nil, postgresRevisionIntegrity(
				"PostgreSQL migration control table contains more than one index",
				nil,
			)
		}
		var index postgresMigrationControlIndex
		if err := rows.Scan(
			&index.name,
			&index.accessMethod,
			&index.primary,
			&index.unique,
			&index.exclusion,
			&index.valid,
			&index.ready,
			&index.live,
			&index.keyCount,
			&index.totalCount,
			&index.key,
			&index.hasPredicate,
			&index.hasExpressions,
		); err != nil {
			return nil, classifyPostgresMigrationIO(ctx, "scan PostgreSQL migration control index", err)
		}
		indexes = append(indexes, index)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyPostgresMigrationIO(ctx, "iterate PostgreSQL migration control indexes", err)
	}
	return indexes, nil
}

func readPostgresMigrationControlConstraints(
	ctx context.Context,
	executor migrationSQLExecutor,
	namespace,
	table string,
) (constraints []postgresMigrationControlConstraint, resultErr error) {
	rows, err := executor.QueryContext(
		ctx,
		`SELECT "k"."conname", "k"."contype"::text, "k"."condeferrable", `+
			`"k"."condeferred", "k"."convalidated", `+
			`"pg_catalog"."array_to_string"("k"."conkey", ',') `+
			`FROM "pg_catalog"."pg_constraint" AS "k" `+
			`JOIN "pg_catalog"."pg_class" AS "c" ON "c"."oid" = "k"."conrelid" `+
			`JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "c"."relnamespace" `+
			`WHERE "n"."nspname" = $1 AND "c"."relname" = $2 `+
			`ORDER BY "k"."conname" LIMIT 2`,
		namespace,
		table,
	)
	if err != nil {
		return nil, classifyPostgresMigrationIO(ctx, "inspect PostgreSQL migration control constraints", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			constraints = nil
			resultErr = errors.Join(resultErr, classifyPostgresMigrationIO(ctx, "close PostgreSQL migration control constraints", err))
		}
	}()
	for rows.Next() {
		if len(constraints) == 1 {
			return nil, postgresRevisionIntegrity(
				"PostgreSQL migration control table contains more than one constraint",
				nil,
			)
		}
		var constraint postgresMigrationControlConstraint
		if err := rows.Scan(
			&constraint.name,
			&constraint.kind,
			&constraint.deferrable,
			&constraint.deferred,
			&constraint.validated,
			&constraint.key,
		); err != nil {
			return nil, classifyPostgresMigrationIO(ctx, "scan PostgreSQL migration control constraint", err)
		}
		constraints = append(constraints, constraint)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyPostgresMigrationIO(ctx, "iterate PostgreSQL migration control constraints", err)
	}
	return constraints, nil
}

func readPostgresMigrationRecorder(
	ctx context.Context,
	executor migrationSQLExecutor,
	namespace string,
) (records []migrationbackend.AppliedMigration, resultErr error) {
	table, err := quoteTable(namespace, postgresMigrationRecorderTable)
	if err != nil {
		return nil, err
	}
	rows, err := executor.QueryContext(
		ctx,
		`SELECT "app", "name" FROM `+table+` ORDER BY "app", "name" LIMIT $1`,
		postgresMigrationHistoryRecordLimit+1,
	)
	if err != nil {
		return nil, classifyPostgresMigrationIO(ctx, "read PostgreSQL migration recorder", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			records = nil
			resultErr = errors.Join(resultErr, classifyPostgresMigrationIO(ctx, "close PostgreSQL migration recorder rows", err))
		}
	}()
	for rows.Next() {
		if err := validatePostgresMigrationHistoryRecordCount(len(records) + 1); err != nil {
			return nil, err
		}
		var record migrationbackend.AppliedMigration
		if err := rows.Scan(&record.App, &record.Name); err != nil {
			return nil, classifyPostgresMigrationIO(ctx, "scan PostgreSQL migration recorder", err)
		}
		if record.App == "" || record.Name == "" {
			return nil, postgresRevisionIntegrity("PostgreSQL migration recorder contains an empty identity", nil)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyPostgresMigrationIO(ctx, "iterate PostgreSQL migration recorder", err)
	}
	return records, nil
}

func readPostgresMigrationRevisionToken(
	ctx context.Context,
	executor migrationSQLExecutor,
	namespace string,
) (token postgresMigrationRevisionToken, resultErr error) {
	table, err := quoteTable(namespace, postgresMigrationRevisionTable)
	if err != nil {
		return postgresMigrationRevisionToken{}, err
	}
	rows, err := executor.QueryContext(
		ctx,
		`SELECT "singleton", "format_version", `+
			`"pg_catalog"."substring"("epoch", 1, 17), "revision", `+
			`"pg_catalog"."substring"("history_fingerprint", 1, 33) FROM `+table+
			` ORDER BY "singleton" LIMIT 2`,
	)
	if err != nil {
		return postgresMigrationRevisionToken{}, classifyPostgresMigrationIO(ctx, "read PostgreSQL migration revision metadata", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			token = postgresMigrationRevisionToken{}
			resultErr = errors.Join(resultErr, classifyPostgresMigrationIO(ctx, "close PostgreSQL migration revision rows", err))
		}
	}()
	rowCount := 0
	for rows.Next() {
		rowCount++
		if rowCount > 1 {
			return postgresMigrationRevisionToken{}, postgresRevisionIntegrity(
				"PostgreSQL migration revision metadata contains more than one row",
				nil,
			)
		}
		var (
			singleton     int16
			formatVersion int32
			revision      int64
			epoch         []byte
			fingerprint   []byte
		)
		if err := rows.Scan(&singleton, &formatVersion, &epoch, &revision, &fingerprint); err != nil {
			return postgresMigrationRevisionToken{}, classifyPostgresMigrationIO(ctx, "scan PostgreSQL migration revision metadata", err)
		}
		if singleton != 1 {
			return postgresMigrationRevisionToken{}, postgresRevisionIntegrity(
				fmt.Sprintf("PostgreSQL migration revision singleton is %d, want 1", singleton),
				nil,
			)
		}
		if formatVersion != postgresMigrationRevisionFormat {
			return postgresMigrationRevisionToken{}, postgresRevisionIntegrity(
				fmt.Sprintf("PostgreSQL migration revision format is %d, want %d", formatVersion, postgresMigrationRevisionFormat),
				nil,
			)
		}
		if len(epoch) != postgresMigrationRevisionEpochBytes {
			return postgresMigrationRevisionToken{}, postgresRevisionIntegrity(
				fmt.Sprintf("PostgreSQL migration revision epoch has %d bytes, want %d", len(epoch), postgresMigrationRevisionEpochBytes),
				nil,
			)
		}
		if err := validateInitializedPostgresMigrationRevision(revision); err != nil {
			return postgresMigrationRevisionToken{}, err
		}
		if len(fingerprint) != postgresMigrationHistoryDigestBytes {
			return postgresMigrationRevisionToken{}, postgresRevisionIntegrity(
				fmt.Sprintf("PostgreSQL migration history fingerprint has %d bytes, want %d", len(fingerprint), postgresMigrationHistoryDigestBytes),
				nil,
			)
		}
		token.initialized = true
		copy(token.epoch[:], epoch)
		token.revision = revision
		copy(token.fingerprint[:], fingerprint)
	}
	if err := rows.Err(); err != nil {
		return postgresMigrationRevisionToken{}, classifyPostgresMigrationIO(ctx, "iterate PostgreSQL migration revision metadata", err)
	}
	if rowCount != 1 {
		return postgresMigrationRevisionToken{}, postgresRevisionIntegrity(
			fmt.Sprintf("PostgreSQL migration revision metadata contains %d rows, want 1", rowCount),
			nil,
		)
	}
	return token, nil
}

func validateInitializedPostgresMigrationRevision(revision int64) error {
	if revision <= 0 {
		return postgresRevisionIntegrity(
			fmt.Sprintf("initialized PostgreSQL migration revision is %d, want a positive value", revision),
			nil,
		)
	}
	return nil
}

func validatePostgresMigrationHistoryRecordCount(count int) error {
	if count > postgresMigrationHistoryRecordLimit {
		return postgresRevisionIntegrity(
			fmt.Sprintf(
				"PostgreSQL migration history has at least %d records, current limit is %d",
				count,
				postgresMigrationHistoryRecordLimit,
			),
			nil,
		)
	}
	return nil
}

func postgresMigrationHistorySuccessor(
	records []migrationbackend.AppliedMigration,
	transition migrationbackend.HistoryTransition,
) ([]migrationbackend.AppliedMigration, error) {
	successor := clonePostgresAppliedMigrations(records)
	sortPostgresAppliedMigrations(successor)
	index := sort.Search(len(successor), func(index int) bool {
		return comparePostgresAppliedMigration(successor[index], transition.Migration) >= 0
	})
	if transition.Kind == migrationbackend.HistoryTransitionApply {
		if index < len(successor) && successor[index] == transition.Migration {
			return nil, postgresRevisionIntegrity(
				fmt.Sprintf("migration %s.%s is already applied", transition.Migration.App, transition.Migration.Name),
				nil,
			)
		}
		successor = append(successor, migrationbackend.AppliedMigration{})
		copy(successor[index+1:], successor[index:])
		successor[index] = transition.Migration
		if err := validatePostgresMigrationHistoryRecordCount(len(successor)); err != nil {
			return nil, err
		}
		return successor, nil
	}
	if transition.Kind != migrationbackend.HistoryTransitionUnapply {
		return nil, postgresRevisionIntegrity(fmt.Sprintf("migration transition kind %d is invalid", transition.Kind), nil)
	}
	if index >= len(successor) || successor[index] != transition.Migration {
		return nil, postgresRevisionIntegrity(
			fmt.Sprintf("migration %s.%s is not applied", transition.Migration.App, transition.Migration.Name),
			nil,
		)
	}
	return append(successor[:index:index], successor[index+1:]...), nil
}

func fingerprintPostgresMigrationHistory(records []migrationbackend.AppliedMigration) [sha256.Size]byte {
	canonical := clonePostgresAppliedMigrations(records)
	sortPostgresAppliedMigrations(canonical)
	hash := sha256.New()
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(canonical)))
	_, _ = hash.Write(length[:])
	for _, record := range canonical {
		for _, value := range []string{record.App, record.Name} {
			binary.BigEndian.PutUint64(length[:], uint64(len(value)))
			_, _ = hash.Write(length[:])
			_, _ = hash.Write([]byte(value))
		}
	}
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result
}

func clonePostgresAppliedMigrations(records []migrationbackend.AppliedMigration) []migrationbackend.AppliedMigration {
	if records == nil {
		return []migrationbackend.AppliedMigration{}
	}
	return append([]migrationbackend.AppliedMigration(nil), records...)
}

func sortPostgresAppliedMigrations(records []migrationbackend.AppliedMigration) {
	sort.Slice(records, func(left, right int) bool {
		return comparePostgresAppliedMigration(records[left], records[right]) < 0
	})
}

func comparePostgresAppliedMigration(left, right migrationbackend.AppliedMigration) int {
	if left.App < right.App {
		return -1
	}
	if left.App > right.App {
		return 1
	}
	if left.Name < right.Name {
		return -1
	}
	if left.Name > right.Name {
		return 1
	}
	return 0
}

func equalPostgresAppliedMigrations(left, right []migrationbackend.AppliedMigration) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func equalPostgresMigrationRevisionToken(left, right postgresMigrationRevisionToken) bool {
	return left.initialized == right.initialized &&
		left.epoch == right.epoch &&
		left.revision == right.revision &&
		left.fingerprint == right.fingerprint
}
