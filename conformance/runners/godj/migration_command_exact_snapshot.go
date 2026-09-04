//go:build darwin || linux

package godj

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"

	"github.com/progresshans/godj/migrations"
	_ "modernc.org/sqlite"
)

const (
	migrationCommandExactSQLiteRevisionTable = "godj_migration_revision"

	migrationCommandExactSQLiteRecorderDefinition = `CREATE TABLE "godj_migrations" (` +
		`"app" VARCHAR(255) NOT NULL, ` +
		`"name" VARCHAR(255) NOT NULL, ` +
		`PRIMARY KEY ("app", "name"))`
	migrationCommandExactSQLiteRevisionDefinition = `CREATE TABLE "godj_migration_revision" (` +
		`"singleton" INTEGER NOT NULL PRIMARY KEY CHECK ("singleton" = 1), ` +
		`"format_version" INTEGER NOT NULL CHECK ("format_version" > 0), ` +
		`"epoch" BLOB NOT NULL CHECK (typeof("epoch") = 'blob' AND length("epoch") = 16), ` +
		`"revision" INTEGER NOT NULL CHECK (typeof("revision") = 'integer' AND "revision" >= 0), ` +
		`"history_fingerprint" BLOB NOT NULL CHECK (typeof("history_fingerprint") = 'blob' AND length("history_fingerprint") = 32))`
)

type migrationCommandExactSQLiteColumn struct {
	ordinal      int
	name         string
	declaredType string
	notNull      int
	defaultSet   bool
	defaultValue string
	primaryKey   int
}

type migrationCommandExactSQLiteTable struct {
	name       string
	definition string
	columns    []migrationCommandExactSQLiteColumn
}

type migrationCommandExactSQLiteObject struct {
	objectType    string
	name          string
	definitionSet bool
	definition    string
}

type migrationCommandExactSQLiteRevision struct {
	rowCount               int64
	singleton              int64
	singletonType          string
	formatVersion          int64
	formatVersionType      string
	epoch                  []byte
	epochType              string
	revision               int64
	revisionType           string
	historyFingerprint     []byte
	historyFingerprintType string
}

type migrationCommandExactSQLiteSnapshot struct {
	objects  []migrationCommandExactSQLiteObject
	tables   []migrationCommandExactSQLiteTable
	history  []migrations.MigrationKey
	revision migrationCommandExactSQLiteRevision
}

// migrationCommandInspectExactSQLite opens an existing database read-only and
// captures every non-internal schema object plus every table's stored
// definition and ordered PRAGMA table_info shape. It also captures the complete
// migration history and the single revision-fence row without accepting a
// missing or malformed column as an empty value.
func migrationCommandInspectExactSQLite(
	ctx context.Context,
	path string,
) (snapshot migrationCommandExactSQLiteSnapshot, resultErr error) {
	if ctx == nil {
		return migrationCommandExactSQLiteSnapshot{}, errors.New("inspect exact migration-command SQLite database: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return migrationCommandExactSQLiteSnapshot{}, fmt.Errorf("inspect exact migration-command SQLite database: %w", err)
	}
	if path == "" {
		return migrationCommandExactSQLiteSnapshot{}, errors.New("inspect exact migration-command SQLite database: path is empty")
	}
	info, err := os.Stat(path)
	if err != nil {
		return migrationCommandExactSQLiteSnapshot{}, fmt.Errorf("inspect exact migration-command SQLite database file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return migrationCommandExactSQLiteSnapshot{}, errors.New("inspect exact migration-command SQLite database: path is not a regular file")
	}

	databaseURL := &url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	database, err := sql.Open("sqlite", databaseURL.String()+"?mode=ro")
	if err != nil {
		return migrationCommandExactSQLiteSnapshot{}, fmt.Errorf("open exact migration-command SQLite database: %w", err)
	}
	database.SetMaxOpenConns(1)
	defer func() {
		if err := database.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close exact migration-command SQLite database: %w", err))
		}
	}()
	if err := database.PingContext(ctx); err != nil {
		return migrationCommandExactSQLiteSnapshot{}, fmt.Errorf("ping exact migration-command SQLite database: %w", err)
	}

	objects, err := migrationCommandInspectExactSQLiteObjects(ctx, database)
	if err != nil {
		return migrationCommandExactSQLiteSnapshot{}, err
	}
	snapshot.objects = objects
	tables, err := migrationCommandInspectExactSQLiteTables(ctx, database)
	if err != nil {
		return migrationCommandExactSQLiteSnapshot{}, err
	}
	snapshot.tables = tables
	if migrationCommandExactSQLiteHasTable(tables, goDjMigrationRecordTable) {
		snapshot.history, err = migrationCommandInspectExactSQLiteHistory(ctx, database)
		if err != nil {
			return migrationCommandExactSQLiteSnapshot{}, err
		}
	}
	if migrationCommandExactSQLiteHasTable(tables, migrationCommandExactSQLiteRevisionTable) {
		snapshot.revision, err = migrationCommandInspectExactSQLiteRevisionRow(ctx, database)
		if err != nil {
			return migrationCommandExactSQLiteSnapshot{}, err
		}
	}
	return snapshot, nil
}

func migrationCommandInspectExactSQLiteObjects(
	ctx context.Context,
	database *sql.DB,
) (objects []migrationCommandExactSQLiteObject, resultErr error) {
	rows, err := database.QueryContext(
		ctx,
		`SELECT "type", "name", "sql" FROM "sqlite_schema" `+
			`WHERE lower(substr("name", 1, 7)) <> 'sqlite_' ORDER BY "type", "name"`,
	)
	if err != nil {
		return nil, fmt.Errorf("inspect exact migration-command SQLite schema objects: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			objects = nil
			resultErr = errors.Join(resultErr, fmt.Errorf("close exact migration-command SQLite schema object rows: %w", err))
		}
	}()
	for rows.Next() {
		var object migrationCommandExactSQLiteObject
		var definition sql.NullString
		if err := rows.Scan(&object.objectType, &object.name, &definition); err != nil {
			return nil, fmt.Errorf("scan exact migration-command SQLite schema object: %w", err)
		}
		object.definitionSet = definition.Valid
		object.definition = definition.String
		objects = append(objects, object)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate exact migration-command SQLite schema objects: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close exact migration-command SQLite schema object rows: %w", err)
	}
	return objects, nil
}

func migrationCommandInspectExactSQLiteTables(
	ctx context.Context,
	database *sql.DB,
) (tables []migrationCommandExactSQLiteTable, resultErr error) {
	rows, err := database.QueryContext(
		ctx,
		`SELECT "name", "sql" FROM "sqlite_schema" `+
			`WHERE "type" = 'table' AND lower(substr("name", 1, 7)) <> 'sqlite_' ORDER BY "name"`,
	)
	if err != nil {
		return nil, fmt.Errorf("inspect exact migration-command SQLite tables: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			tables = nil
			resultErr = errors.Join(resultErr, fmt.Errorf("close exact migration-command SQLite table rows: %w", err))
		}
	}()
	for rows.Next() {
		var table migrationCommandExactSQLiteTable
		var definition sql.NullString
		if err := rows.Scan(&table.name, &definition); err != nil {
			return nil, fmt.Errorf("scan exact migration-command SQLite table: %w", err)
		}
		if !definition.Valid {
			return nil, errors.New("inspect exact migration-command SQLite table: stored definition is null")
		}
		table.definition = definition.String
		tables = append(tables, table)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate exact migration-command SQLite tables: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close exact migration-command SQLite table rows: %w", err)
	}

	for index := range tables {
		columns, err := migrationCommandInspectExactSQLiteColumns(ctx, database, tables[index].name)
		if err != nil {
			return nil, err
		}
		tables[index].columns = columns
	}
	return tables, nil
}

func migrationCommandInspectExactSQLiteColumns(
	ctx context.Context,
	database *sql.DB,
	table string,
) (columns []migrationCommandExactSQLiteColumn, resultErr error) {
	rows, err := database.QueryContext(ctx, "PRAGMA table_info("+quoteSQLiteIdentifier(table)+")")
	if err != nil {
		return nil, fmt.Errorf("inspect exact migration-command SQLite columns: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			columns = nil
			resultErr = errors.Join(resultErr, fmt.Errorf("close exact migration-command SQLite column rows: %w", err))
		}
	}()
	for rows.Next() {
		var column migrationCommandExactSQLiteColumn
		var defaultValue sql.NullString
		if err := rows.Scan(
			&column.ordinal,
			&column.name,
			&column.declaredType,
			&column.notNull,
			&defaultValue,
			&column.primaryKey,
		); err != nil {
			return nil, fmt.Errorf("scan exact migration-command SQLite column: %w", err)
		}
		column.defaultSet = defaultValue.Valid
		column.defaultValue = defaultValue.String
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate exact migration-command SQLite columns: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close exact migration-command SQLite column rows: %w", err)
	}
	return columns, nil
}

func migrationCommandInspectExactSQLiteHistory(
	ctx context.Context,
	database *sql.DB,
) (history []migrations.MigrationKey, resultErr error) {
	rows, err := database.QueryContext(
		ctx,
		`SELECT "app", "name" FROM "godj_migrations" ORDER BY "app", "name"`,
	)
	if err != nil {
		return nil, fmt.Errorf("inspect exact migration-command SQLite history: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			history = nil
			resultErr = errors.Join(resultErr, fmt.Errorf("close exact migration-command SQLite history rows: %w", err))
		}
	}()
	for rows.Next() {
		var key migrations.MigrationKey
		if err := rows.Scan(&key.App, &key.Name); err != nil {
			return nil, fmt.Errorf("scan exact migration-command SQLite history: %w", err)
		}
		history = append(history, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate exact migration-command SQLite history: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close exact migration-command SQLite history rows: %w", err)
	}
	return history, nil
}

func migrationCommandInspectExactSQLiteRevisionRow(
	ctx context.Context,
	database *sql.DB,
) (revision migrationCommandExactSQLiteRevision, err error) {
	if err := database.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM "godj_migration_revision"`,
	).Scan(&revision.rowCount); err != nil {
		return migrationCommandExactSQLiteRevision{}, fmt.Errorf("count exact migration-command SQLite revision rows: %w", err)
	}
	if revision.rowCount == 0 {
		return revision, nil
	}
	if err := database.QueryRowContext(
		ctx,
		`SELECT `+
			`"singleton", typeof("singleton"), `+
			`"format_version", typeof("format_version"), `+
			`"epoch", typeof("epoch"), `+
			`"revision", typeof("revision"), `+
			`"history_fingerprint", typeof("history_fingerprint") `+
			`FROM "godj_migration_revision" ORDER BY "singleton" LIMIT 1`,
	).Scan(
		&revision.singleton,
		&revision.singletonType,
		&revision.formatVersion,
		&revision.formatVersionType,
		&revision.epoch,
		&revision.epochType,
		&revision.revision,
		&revision.revisionType,
		&revision.historyFingerprint,
		&revision.historyFingerprintType,
	); err != nil {
		return migrationCommandExactSQLiteRevision{}, fmt.Errorf("inspect exact migration-command SQLite revision row: %w", err)
	}
	return revision, nil
}

func migrationCommandAssertExactSQLitePrefix(snapshot migrationCommandExactSQLiteSnapshot) error {
	history := []migrations.MigrationKey{{App: migrationCommandApp, Name: migrationCommandPrefix}}
	return migrationCommandAssertExactSQLiteState(snapshot, []string{migrationCommandPrefixTable}, history)
}

func migrationCommandAssertExactSQLiteLatest(
	snapshot migrationCommandExactSQLiteSnapshot,
	history []migrations.MigrationKey,
) error {
	return migrationCommandAssertExactSQLiteState(
		snapshot,
		[]string{migrationCommandPrefixTable, migrationCommandMiddleTable, migrationCommandTailTable},
		history,
	)
}

func migrationCommandAssertExactSQLiteState(
	snapshot migrationCommandExactSQLiteSnapshot,
	commandTables []string,
	history []migrations.MigrationKey,
) error {
	wantHistory, err := migrationCommandExactSQLiteCanonicalHistory(history)
	if err != nil {
		return err
	}
	wantTableNames := append([]string(nil), commandTables...)
	wantTableNames = append(wantTableNames, migrationCommandExactSQLiteRevisionTable, goDjMigrationRecordTable)
	sort.Strings(wantTableNames)
	if len(snapshot.tables) != len(wantTableNames) {
		return fmt.Errorf("exact migration-command SQLite table count = %d, want %d", len(snapshot.tables), len(wantTableNames))
	}
	for index, wantName := range wantTableNames {
		if snapshot.tables[index].name != wantName {
			return fmt.Errorf("exact migration-command SQLite table[%d] does not match expected table set", index)
		}
	}
	if len(snapshot.objects) != len(wantTableNames) {
		return fmt.Errorf("exact migration-command SQLite schema object count = %d, want %d tables only", len(snapshot.objects), len(wantTableNames))
	}
	for index, wantName := range wantTableNames {
		object := snapshot.objects[index]
		if object.objectType != "table" || object.name != wantName || !object.definitionSet {
			return fmt.Errorf("exact migration-command SQLite schema object[%d] is not an expected table", index)
		}
	}
	if !migrationCommandKeysEqual(snapshot.history, wantHistory) {
		return fmt.Errorf("exact migration-command SQLite history row count or identity differs: got %d, want %d", len(snapshot.history), len(wantHistory))
	}
	if err := migrationCommandAssertExactSQLiteRevision(snapshot.revision, wantHistory); err != nil {
		return err
	}
	for _, table := range snapshot.tables {
		wantDefinition, wantColumns, err := migrationCommandExactSQLiteExpectedTable(table.name, commandTables)
		if err != nil {
			return err
		}
		if err := migrationCommandCompareExactSQLiteColumns(table.name, table.columns, wantColumns); err != nil {
			return err
		}
		if table.definition != wantDefinition {
			return fmt.Errorf("exact migration-command SQLite table %q definition differs", table.name)
		}
	}
	return nil
}

func migrationCommandAssertExactSQLiteRevision(
	revision migrationCommandExactSQLiteRevision,
	history []migrations.MigrationKey,
) error {
	if revision.rowCount != 1 {
		return fmt.Errorf("exact migration-command SQLite revision row count = %d, want 1", revision.rowCount)
	}
	if revision.singleton != 1 || revision.singletonType != "integer" {
		return errors.New("exact migration-command SQLite revision singleton differs")
	}
	if revision.formatVersion != 1 || revision.formatVersionType != "integer" {
		return errors.New("exact migration-command SQLite revision format differs")
	}
	if revision.epochType != "blob" || len(revision.epoch) != 16 {
		return fmt.Errorf("exact migration-command SQLite revision epoch shape differs: bytes=%d", len(revision.epoch))
	}
	if revision.revisionType != "integer" || revision.revision != int64(len(history)) {
		return fmt.Errorf("exact migration-command SQLite revision = %d, want %d", revision.revision, len(history))
	}
	wantFingerprint := migrationCommandHistoryFingerprint(history)
	if revision.historyFingerprintType != "blob" || len(revision.historyFingerprint) != sha256.Size {
		return fmt.Errorf("exact migration-command SQLite history fingerprint shape differs: bytes=%d", len(revision.historyFingerprint))
	}
	if !bytes.Equal(revision.historyFingerprint, wantFingerprint) {
		return errors.New("exact migration-command SQLite history fingerprint differs from canonical history")
	}
	return nil
}

func migrationCommandExactSQLiteExpectedTable(
	table string,
	commandTables []string,
) (string, []migrationCommandExactSQLiteColumn, error) {
	for _, commandTable := range commandTables {
		if table == commandTable {
			return "CREATE TABLE " + quoteSQLiteIdentifier(table) + ` ("id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT)`,
				[]migrationCommandExactSQLiteColumn{{
					ordinal: 0, name: "id", declaredType: "INTEGER", notNull: 1, primaryKey: 1,
				}}, nil
		}
	}
	switch table {
	case goDjMigrationRecordTable:
		return migrationCommandExactSQLiteRecorderDefinition, []migrationCommandExactSQLiteColumn{
			{ordinal: 0, name: "app", declaredType: "VARCHAR(255)", notNull: 1, primaryKey: 1},
			{ordinal: 1, name: "name", declaredType: "VARCHAR(255)", notNull: 1, primaryKey: 2},
		}, nil
	case migrationCommandExactSQLiteRevisionTable:
		return migrationCommandExactSQLiteRevisionDefinition, []migrationCommandExactSQLiteColumn{
			{ordinal: 0, name: "singleton", declaredType: "INTEGER", notNull: 1, primaryKey: 1},
			{ordinal: 1, name: "format_version", declaredType: "INTEGER", notNull: 1},
			{ordinal: 2, name: "epoch", declaredType: "BLOB", notNull: 1},
			{ordinal: 3, name: "revision", declaredType: "INTEGER", notNull: 1},
			{ordinal: 4, name: "history_fingerprint", declaredType: "BLOB", notNull: 1},
		}, nil
	default:
		return "", nil, fmt.Errorf("exact migration-command SQLite table %q has no expected schema", table)
	}
}

func migrationCommandCompareExactSQLiteColumns(
	table string,
	got []migrationCommandExactSQLiteColumn,
	want []migrationCommandExactSQLiteColumn,
) error {
	if len(got) != len(want) {
		return fmt.Errorf("exact migration-command SQLite table %q column count = %d, want %d", table, len(got), len(want))
	}
	for index := range want {
		if got[index] != want[index] {
			return fmt.Errorf("exact migration-command SQLite table %q column[%d] differs", table, index)
		}
	}
	return nil
}

// migrationCommandCompareExactSQLite compares every captured semantic value,
// including the revision epoch. It is suitable for checking that a later
// invocation did not replace an otherwise equivalent revision fence.
func migrationCommandCompareExactSQLite(
	before migrationCommandExactSQLiteSnapshot,
	after migrationCommandExactSQLiteSnapshot,
) error {
	if len(before.objects) != len(after.objects) {
		return errors.New("migration-command SQLite semantic schema object count changed")
	}
	for index := range before.objects {
		if before.objects[index] != after.objects[index] {
			return fmt.Errorf("migration-command SQLite semantic schema object[%d] changed", index)
		}
	}
	if len(before.tables) != len(after.tables) {
		return errors.New("migration-command SQLite semantic snapshot table count changed")
	}
	for index := range before.tables {
		left := before.tables[index]
		right := after.tables[index]
		if left.name != right.name || left.definition != right.definition {
			return fmt.Errorf("migration-command SQLite semantic table[%d] changed", index)
		}
		if err := migrationCommandCompareExactSQLiteColumns(left.name, left.columns, right.columns); err != nil {
			return fmt.Errorf("migration-command SQLite semantic snapshot changed: %w", err)
		}
	}
	if !migrationCommandKeysEqual(before.history, after.history) {
		return errors.New("migration-command SQLite semantic history changed")
	}
	if before.revision.rowCount != after.revision.rowCount ||
		before.revision.singleton != after.revision.singleton ||
		before.revision.singletonType != after.revision.singletonType ||
		before.revision.formatVersion != after.revision.formatVersion ||
		before.revision.formatVersionType != after.revision.formatVersionType ||
		before.revision.epochType != after.revision.epochType ||
		before.revision.revision != after.revision.revision ||
		before.revision.revisionType != after.revision.revisionType ||
		before.revision.historyFingerprintType != after.revision.historyFingerprintType ||
		!bytes.Equal(before.revision.epoch, after.revision.epoch) ||
		!bytes.Equal(before.revision.historyFingerprint, after.revision.historyFingerprint) {
		return errors.New("migration-command SQLite semantic revision state changed")
	}
	return nil
}

func migrationCommandExactSQLiteFileHash(path string) (digest [sha256.Size]byte, resultErr error) {
	if path == "" {
		return digest, errors.New("hash exact migration-command SQLite database: path is empty")
	}
	file, err := os.Open(path)
	if err != nil {
		return digest, fmt.Errorf("open exact migration-command SQLite database for hashing: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close exact migration-command SQLite database hash input: %w", err))
		}
	}()
	info, err := file.Stat()
	if err != nil {
		return digest, fmt.Errorf("stat exact migration-command SQLite database hash input: %w", err)
	}
	if !info.Mode().IsRegular() {
		return digest, errors.New("hash exact migration-command SQLite database: path is not a regular file")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return digest, fmt.Errorf("hash exact migration-command SQLite database: %w", err)
	}
	copy(digest[:], hash.Sum(nil))
	return digest, nil
}

func migrationCommandAssertExactSQLiteNoop(
	before migrationCommandExactSQLiteSnapshot,
	after migrationCommandExactSQLiteSnapshot,
	beforeFileHash [sha256.Size]byte,
	afterFileHash [sha256.Size]byte,
) error {
	if err := migrationCommandCompareExactSQLite(before, after); err != nil {
		return err
	}
	if beforeFileHash != afterFileHash {
		return errors.New("migration-command SQLite no-op changed database file bytes")
	}
	return nil
}

func migrationCommandExactSQLiteCanonicalHistory(
	history []migrations.MigrationKey,
) ([]migrations.MigrationKey, error) {
	canonical := append([]migrations.MigrationKey(nil), history...)
	sort.Slice(canonical, func(left, right int) bool {
		if canonical[left].App != canonical[right].App {
			return canonical[left].App < canonical[right].App
		}
		return canonical[left].Name < canonical[right].Name
	})
	for index, key := range canonical {
		if key.App == "" || key.Name == "" {
			return nil, errors.New("exact migration-command SQLite expected history contains an empty identity")
		}
		if index > 0 && canonical[index-1] == key {
			return nil, errors.New("exact migration-command SQLite expected history contains a duplicate identity")
		}
	}
	return canonical, nil
}

func migrationCommandExactSQLiteHasTable(tables []migrationCommandExactSQLiteTable, name string) bool {
	index := sort.Search(len(tables), func(index int) bool { return tables[index].name >= name })
	return index < len(tables) && tables[index].name == name
}
