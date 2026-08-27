//go:build darwin || linux

package godj

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
)

func TestMigrationCommandExactSQLiteSnapshotAcceptsActualStates(t *testing.T) {
	t.Run("prefix", func(t *testing.T) {
		_, databasePath, history := migrationCommandExactSQLiteApply(t, migrationCommandSources()[:1])
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		snapshot, err := migrationCommandInspectExactSQLite(ctx, databasePath)
		if err != nil {
			t.Fatal(err)
		}
		if err := migrationCommandAssertExactSQLitePrefix(snapshot); err != nil {
			t.Fatal(err)
		}
		if len(history) != 1 || history[0] != (migrations.MigrationKey{App: migrationCommandApp, Name: migrationCommandPrefix}) {
			t.Fatalf("prefix history = %+v", history)
		}
	})

	t.Run("latest_and_noop", func(t *testing.T) {
		sources := migrationCommandSources()
		project, databasePath, history := migrationCommandExactSQLiteApply(t, sources)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		before, err := migrationCommandInspectExactSQLite(ctx, databasePath)
		if err != nil {
			t.Fatal(err)
		}
		if err := migrationCommandAssertExactSQLiteLatest(before, history); err != nil {
			t.Fatal(err)
		}
		beforeHash, err := migrationCommandExactSQLiteFileHash(databasePath)
		if err != nil {
			t.Fatal(err)
		}

		execution := project.run(ctx, migrationCommandSQLiteConfig(databasePath, sources, &migrationCommandTrace{}), nil, nil)
		if err := migrationCommandSuccess(execution); err != nil {
			t.Fatalf("second migration-command execution: %v", err)
		}
		after, err := migrationCommandInspectExactSQLite(ctx, databasePath)
		if err != nil {
			t.Fatal(err)
		}
		if err := migrationCommandAssertExactSQLiteLatest(after, history); err != nil {
			t.Fatal(err)
		}
		afterHash, err := migrationCommandExactSQLiteFileHash(databasePath)
		if err != nil {
			t.Fatal(err)
		}
		if err := migrationCommandAssertExactSQLiteNoop(before, after, beforeHash, afterHash); err != nil {
			t.Fatal(err)
		}
	})
}

func TestMigrationCommandExactSQLiteSnapshotRejectsDatabaseFalseGreens(t *testing.T) {
	_, basePath, history := migrationCommandExactSQLiteApply(t, migrationCommandSources())

	tests := []struct {
		name       string
		statements []string
		wantError  string
	}{
		{
			name:       "unexpected_table",
			statements: []string{`CREATE TABLE "unexpected_table" ("id" INTEGER NOT NULL PRIMARY KEY)`},
			wantError:  "table count",
		},
		{
			name:       "sqlite_like_but_not_internal_table",
			statements: []string{`CREATE TABLE "sqliteX_escape" ("id" INTEGER NOT NULL PRIMARY KEY)`},
			wantError:  "table count",
		},
		{
			name:       "manual_index",
			statements: []string{`CREATE INDEX "manual_index" ON "godj_command_prefix" ("id")`},
			wantError:  "schema object count",
		},
		{
			name:       "manual_view",
			statements: []string{`CREATE VIEW "manual_view" AS SELECT "id" FROM "godj_command_prefix"`},
			wantError:  "schema object count",
		},
		{
			name:       "manual_trigger",
			statements: []string{`CREATE TRIGGER "manual_trigger" AFTER INSERT ON "godj_command_prefix" BEGIN SELECT 1; END`},
			wantError:  "schema object count",
		},
		{
			name:       "unexpected_command_column",
			statements: []string{`ALTER TABLE "godj_command_prefix" ADD COLUMN "unexpected" TEXT`},
			wantError:  "column count",
		},
		{
			name: "control_definition_without_checks",
			statements: []string{
				`ALTER TABLE "godj_migration_revision" RENAME TO "godj_migration_revision_old"`,
				`CREATE TABLE "godj_migration_revision" (` +
					`"singleton" INTEGER NOT NULL PRIMARY KEY, ` +
					`"format_version" INTEGER NOT NULL, ` +
					`"epoch" BLOB NOT NULL, ` +
					`"revision" INTEGER NOT NULL, ` +
					`"history_fingerprint" BLOB NOT NULL)`,
				`INSERT INTO "godj_migration_revision" ` +
					`SELECT "singleton", "format_version", "epoch", "revision", "history_fingerprint" ` +
					`FROM "godj_migration_revision_old"`,
				`DROP TABLE "godj_migration_revision_old"`,
			},
			wantError: "definition differs",
		},
		{
			name: "recorder_definition_with_extra_constraint",
			statements: []string{
				`ALTER TABLE "godj_migrations" RENAME TO "godj_migrations_old"`,
				`CREATE TABLE "godj_migrations" (` +
					`"app" VARCHAR(255) NOT NULL, ` +
					`"name" VARCHAR(255) NOT NULL, ` +
					`PRIMARY KEY ("app", "name"), ` +
					`CHECK (length("app") > 0))`,
				`INSERT INTO "godj_migrations" SELECT "app", "name" FROM "godj_migrations_old"`,
				`DROP TABLE "godj_migrations_old"`,
			},
			wantError: "definition differs",
		},
		{
			name: "unexpected_control_row",
			statements: []string{
				`ALTER TABLE "godj_migration_revision" RENAME TO "godj_migration_revision_old"`,
				`CREATE TABLE "godj_migration_revision" (` +
					`"singleton" INTEGER NOT NULL PRIMARY KEY, ` +
					`"format_version" INTEGER NOT NULL, ` +
					`"epoch" BLOB NOT NULL, ` +
					`"revision" INTEGER NOT NULL, ` +
					`"history_fingerprint" BLOB NOT NULL)`,
				`INSERT INTO "godj_migration_revision" ` +
					`SELECT "singleton", "format_version", "epoch", "revision", "history_fingerprint" ` +
					`FROM "godj_migration_revision_old"`,
				`INSERT INTO "godj_migration_revision" ` +
					`("singleton", "format_version", "epoch", "revision", "history_fingerprint") ` +
					`VALUES (2, 1, randomblob(16), 3, zeroblob(32))`,
				`DROP TABLE "godj_migration_revision_old"`,
			},
			wantError: "revision row count",
		},
		{
			name:       "unexpected_history_row",
			statements: []string{`INSERT INTO "godj_migrations" ("app", "name") VALUES ('unexpected', '0001')`},
			wantError:  "history row count or identity differs",
		},
		{
			name:       "canonical_fingerprint_mismatch",
			statements: []string{`UPDATE "godj_migration_revision" SET "history_fingerprint" = zeroblob(32) WHERE "singleton" = 1`},
			wantError:  "fingerprint differs",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			databasePath := migrationCommandExactSQLiteCloneFile(t, basePath)
			migrationCommandExactSQLiteExec(t, databasePath, test.statements...)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			snapshot, err := migrationCommandInspectExactSQLite(ctx, databasePath)
			if err != nil {
				t.Fatal(err)
			}
			err = migrationCommandAssertExactSQLiteLatest(snapshot, history)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("exact latest error = %v, want substring %q", err, test.wantError)
			}
		})
	}
}

func TestMigrationCommandExactSQLiteSnapshotRejectsCapturedInvariantCorruption(t *testing.T) {
	_, databasePath, history := migrationCommandExactSQLiteApply(t, migrationCommandSources())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	baseline, err := migrationCommandInspectExactSQLite(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrationCommandAssertExactSQLiteLatest(baseline, history); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(*migrationCommandExactSQLiteSnapshot)
	}{
		{name: "revision_row_count", mutate: func(snapshot *migrationCommandExactSQLiteSnapshot) {
			snapshot.revision.rowCount = 2
		}},
		{name: "singleton", mutate: func(snapshot *migrationCommandExactSQLiteSnapshot) {
			snapshot.revision.singleton = 2
		}},
		{name: "format_version", mutate: func(snapshot *migrationCommandExactSQLiteSnapshot) {
			snapshot.revision.formatVersion = 2
		}},
		{name: "epoch_length", mutate: func(snapshot *migrationCommandExactSQLiteSnapshot) {
			snapshot.revision.epoch = snapshot.revision.epoch[:15]
		}},
		{name: "revision", mutate: func(snapshot *migrationCommandExactSQLiteSnapshot) {
			snapshot.revision.revision--
		}},
		{name: "fingerprint_length", mutate: func(snapshot *migrationCommandExactSQLiteSnapshot) {
			snapshot.revision.historyFingerprint = snapshot.revision.historyFingerprint[:31]
		}},
		{name: "fingerprint_value", mutate: func(snapshot *migrationCommandExactSQLiteSnapshot) {
			snapshot.revision.historyFingerprint[0] ^= 0xff
		}},
		{name: "column_default", mutate: func(snapshot *migrationCommandExactSQLiteSnapshot) {
			snapshot.tables[0].columns[0].defaultSet = true
			snapshot.tables[0].columns[0].defaultValue = "0"
		}},
		{name: "column_ordinal", mutate: func(snapshot *migrationCommandExactSQLiteSnapshot) {
			snapshot.tables[0].columns[0].ordinal = 1
		}},
		{name: "column_name", mutate: func(snapshot *migrationCommandExactSQLiteSnapshot) {
			snapshot.tables[0].columns[0].name = "wrong_id"
		}},
		{name: "column_declared_type", mutate: func(snapshot *migrationCommandExactSQLiteSnapshot) {
			snapshot.tables[0].columns[0].declaredType = "TEXT"
		}},
		{name: "column_not_null", mutate: func(snapshot *migrationCommandExactSQLiteSnapshot) {
			snapshot.tables[0].columns[0].notNull = 0
		}},
		{name: "column_primary_key", mutate: func(snapshot *migrationCommandExactSQLiteSnapshot) {
			snapshot.tables[0].columns[0].primaryKey = 0
		}},
		{name: "singleton_storage_type", mutate: func(snapshot *migrationCommandExactSQLiteSnapshot) {
			snapshot.revision.singletonType = "text"
		}},
		{name: "format_version_storage_type", mutate: func(snapshot *migrationCommandExactSQLiteSnapshot) {
			snapshot.revision.formatVersionType = "text"
		}},
		{name: "epoch_storage_type", mutate: func(snapshot *migrationCommandExactSQLiteSnapshot) {
			snapshot.revision.epochType = "text"
		}},
		{name: "revision_storage_type", mutate: func(snapshot *migrationCommandExactSQLiteSnapshot) {
			snapshot.revision.revisionType = "real"
		}},
		{name: "fingerprint_storage_type", mutate: func(snapshot *migrationCommandExactSQLiteSnapshot) {
			snapshot.revision.historyFingerprintType = "text"
		}},
		{name: "same_count_wrong_history_with_matching_fingerprint", mutate: func(snapshot *migrationCommandExactSQLiteSnapshot) {
			snapshot.history[0].Name = "9999_wrong"
			snapshot.revision.historyFingerprint = migrationCommandHistoryFingerprint(snapshot.history)
		}},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			corrupt := migrationCommandCloneExactSQLiteSnapshot(baseline)
			test.mutate(&corrupt)
			if err := migrationCommandAssertExactSQLiteLatest(corrupt, history); err == nil {
				t.Fatal("exact latest accepted corrupted snapshot")
			}
		})
	}
}

func TestMigrationCommandExactSQLiteInspectIsReadOnly(t *testing.T) {
	_, databasePath, history := migrationCommandExactSQLiteApply(t, migrationCommandSources())
	beforeHash, err := migrationCommandExactSQLiteFileHash(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	beforeInventory := migrationCommandExactSQLiteDirectoryInventory(t, filepath.Dir(databasePath))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	snapshot, err := migrationCommandInspectExactSQLite(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrationCommandAssertExactSQLiteLatest(snapshot, history); err != nil {
		t.Fatal(err)
	}
	afterHash, err := migrationCommandExactSQLiteFileHash(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	afterInventory := migrationCommandExactSQLiteDirectoryInventory(t, filepath.Dir(databasePath))
	if beforeHash != afterHash {
		t.Fatalf("read-only exact SQLite inspection changed database bytes: before=%x after=%x", beforeHash, afterHash)
	}
	if !slicesEqual(beforeInventory, afterInventory) {
		t.Fatalf("read-only exact SQLite inspection changed directory inventory: before=%v after=%v", beforeInventory, afterInventory)
	}
}

func TestMigrationCommandExactSQLiteSnapshotNoopRequiresSemanticAndByteIdentity(t *testing.T) {
	_, databasePath, history := migrationCommandExactSQLiteApply(t, migrationCommandSources())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	snapshot, err := migrationCommandInspectExactSQLite(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrationCommandAssertExactSQLiteLatest(snapshot, history); err != nil {
		t.Fatal(err)
	}
	digest, err := migrationCommandExactSQLiteFileHash(databasePath)
	if err != nil {
		t.Fatal(err)
	}

	changedSemantic := migrationCommandCloneExactSQLiteSnapshot(snapshot)
	changedSemantic.revision.epoch[0] ^= 0xff
	if err := migrationCommandCompareExactSQLite(snapshot, changedSemantic); err == nil {
		t.Fatal("semantic comparison accepted a changed revision epoch")
	}
	if err := migrationCommandAssertExactSQLiteNoop(snapshot, changedSemantic, digest, digest); err == nil {
		t.Fatal("no-op assertion accepted changed semantic state")
	}
	changedDigest := digest
	changedDigest[0] ^= 0xff
	if err := migrationCommandAssertExactSQLiteNoop(snapshot, snapshot, digest, changedDigest); err == nil {
		t.Fatal("no-op assertion accepted changed database bytes")
	}
}

func TestMigrationCommandExactSQLiteInspectRejectsInvalidInputs(t *testing.T) {
	if _, err := migrationCommandInspectExactSQLite(nil, "unused"); err == nil {
		t.Fatal("inspect accepted a nil context")
	}
	if _, err := migrationCommandInspectExactSQLite(context.Background(), ""); err == nil {
		t.Fatal("inspect accepted an empty path")
	}
	missing := filepath.Join(t.TempDir(), "missing.sqlite3")
	if _, err := migrationCommandInspectExactSQLite(context.Background(), missing); err == nil {
		t.Fatal("inspect accepted a missing database")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("inspect created missing database: %v", err)
	}
	if _, err := migrationCommandInspectExactSQLite(context.Background(), t.TempDir()); err == nil {
		t.Fatal("inspect accepted a directory")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := migrationCommandInspectExactSQLite(canceled, "unused"); err == nil {
		t.Fatal("inspect accepted a canceled context")
	}
	malformed := filepath.Join(t.TempDir(), "malformed.sqlite3")
	if err := os.WriteFile(malformed, []byte("not a SQLite database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := migrationCommandInspectExactSQLite(context.Background(), malformed); err == nil {
		t.Fatal("inspect accepted a malformed regular file")
	}
}

func migrationCommandExactSQLiteApply(
	t *testing.T,
	sources []definition.Source,
) (migrationCommandProject, string, []migrations.MigrationKey) {
	t.Helper()
	project, err := newMigrationCommandProject()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := project.close(); err != nil {
			t.Errorf("close migration-command exact SQLite project: %v", err)
		}
	})
	history, _, err := migrationCommandExpectedHistory(sources)
	if err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(project.root, "exact-snapshot.sqlite3")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	execution := project.run(ctx, migrationCommandSQLiteConfig(databasePath, sources, &migrationCommandTrace{}), nil, nil)
	if err := migrationCommandSuccess(execution); err != nil {
		t.Fatalf("apply migration-command exact SQLite fixture: %v", err)
	}
	return project, databasePath, history
}

func migrationCommandExactSQLiteCloneFile(t *testing.T, source string) string {
	t.Helper()
	payload, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "corrupt.sqlite3")
	if err := os.WriteFile(target, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	return target
}

func migrationCommandExactSQLiteExec(t *testing.T, path string, statements ...string) {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close corrupted exact SQLite database: %v", err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, statement := range statements {
		if _, err := database.ExecContext(ctx, statement); err != nil {
			t.Fatalf("execute exact SQLite corruption: %v", err)
		}
	}
}

func migrationCommandExactSQLiteDirectoryInventory(t *testing.T, directory string) []string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	inventory := make([]string, len(entries))
	for index, entry := range entries {
		inventory[index] = entry.Name()
	}
	return inventory
}

func migrationCommandCloneExactSQLiteSnapshot(
	snapshot migrationCommandExactSQLiteSnapshot,
) migrationCommandExactSQLiteSnapshot {
	clone := snapshot
	clone.objects = append([]migrationCommandExactSQLiteObject(nil), snapshot.objects...)
	clone.tables = append([]migrationCommandExactSQLiteTable(nil), snapshot.tables...)
	for index := range clone.tables {
		clone.tables[index].columns = append([]migrationCommandExactSQLiteColumn(nil), snapshot.tables[index].columns...)
	}
	clone.history = append([]migrations.MigrationKey(nil), snapshot.history...)
	clone.revision.epoch = append([]byte(nil), snapshot.revision.epoch...)
	clone.revision.historyFingerprint = append([]byte(nil), snapshot.revision.historyFingerprint...)
	return clone
}
