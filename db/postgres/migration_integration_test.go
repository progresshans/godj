package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	migrationdefinition "github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

var (
	postgresMigrationIntegrationAuthorKey  = migrations.MigrationKey{App: "authors", Name: "0001_author"}
	postgresMigrationIntegrationPostKey    = migrations.MigrationKey{App: "blog", Name: "0001_post"}
	postgresMigrationIntegrationFieldsKey  = migrations.MigrationKey{App: "blog", Name: "0002_fields"}
	postgresMigrationIntegrationPostFKKey  = migrations.MigrationKey{App: "blog", Name: "0003_author"}
	postgresMigrationIntegrationCommentKey = migrations.MigrationKey{
		App: "comments", Name: "0001_comment",
	}
)

func TestPostgresRevisionFencedMigrationIntegration(t *testing.T) {
	databaseURL := postgresIntegrationURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	schema := postgresMigrationIntegrationSchema(t, ctx, databaseURL)
	backend := openPostgresMigrationIntegrationBackend(t, ctx, databaseURL, schema)

	assertPostgresMigrationIntegrationHistory(t, ctx, backend, nil)
	loaded := postgresMigrationIntegrationDefinitions(t)
	state := postgresMigrationIntegrationMigrate(
		t,
		ctx,
		backend,
		loaded,
		migrations.LatestLifecycleRequest(),
	)
	assertPostgresMigrationIntegrationHistory(t, ctx, backend, postgresMigrationIntegrationAppliedHistory())
	assertPostgresMigrationIntegrationCatalog(t, ctx, backend, schema, state)
	assertPostgresMigrationIntegrationDisabledForeignKeyTriggerDrift(t, ctx, backend, schema, state)

	if err := backend.Close(); err != nil {
		t.Fatalf("close migrated PostgreSQL backend: %v", err)
	}
	backend = openPostgresMigrationIntegrationBackend(t, ctx, databaseURL, schema)
	assertPostgresMigrationIntegrationHistory(t, ctx, backend, postgresMigrationIntegrationAppliedHistory())
	assertPostgresMigrationIntegrationCatalog(t, ctx, backend, schema, state)
	noOpState := postgresMigrationIntegrationMigrate(
		t,
		ctx,
		backend,
		loaded,
		migrations.LatestLifecycleRequest(),
	)
	if !noOpState.Equal(state) {
		t.Fatalf("PostgreSQL no-op Latest state changed: before=%+v after=%+v", state, noOpState)
	}
	assertPostgresMigrationIntegrationHistory(t, ctx, backend, postgresMigrationIntegrationAppliedHistory())
	assertPostgresMigrationIntegrationCatalog(t, ctx, backend, schema, noOpState)

	withoutPostFK := postgresMigrationIntegrationMigrate(
		t,
		ctx,
		backend,
		loaded,
		migrations.TargetedLifecycleRequest(migrations.NamedTarget(postgresMigrationIntegrationFieldsKey)),
	)
	assertPostgresMigrationIntegrationHistory(t, ctx, backend, []migrationbackend.AppliedMigration{
		{App: "authors", Name: "0001_author"},
		{App: "blog", Name: "0001_post"},
		{App: "blog", Name: "0002_fields"},
		{App: "comments", Name: "0001_comment"},
	})
	assertPostgresMigrationIntegrationPostWithoutForeignKey(t, ctx, backend, schema, withoutPostFK)
	readdedPostFK := postgresMigrationIntegrationMigrate(
		t,
		ctx,
		backend,
		loaded,
		migrations.TargetedLifecycleRequest(migrations.NamedTarget(postgresMigrationIntegrationPostFKKey)),
	)
	assertPostgresMigrationIntegrationHistory(t, ctx, backend, postgresMigrationIntegrationAppliedHistory())
	assertPostgresMigrationIntegrationCatalog(t, ctx, backend, schema, readdedPostFK)
	withoutPostFK = postgresMigrationIntegrationMigrate(
		t,
		ctx,
		backend,
		loaded,
		migrations.TargetedLifecycleRequest(migrations.NamedTarget(postgresMigrationIntegrationFieldsKey)),
	)
	assertPostgresMigrationIntegrationPostWithoutForeignKey(t, ctx, backend, schema, withoutPostFK)

	postgresMigrationIntegrationMigrate(
		t,
		ctx,
		backend,
		loaded,
		migrations.TargetedLifecycleRequest(migrations.ZeroTarget("comments")),
	)
	assertPostgresMigrationIntegrationTableMissing(t, ctx, backend, schema, "comments_comment")
	postgresMigrationIntegrationMigrate(
		t,
		ctx,
		backend,
		loaded,
		migrations.TargetedLifecycleRequest(migrations.ZeroTarget("blog")),
	)
	assertPostgresMigrationIntegrationTableMissing(t, ctx, backend, schema, "blog_post")
	postgresMigrationIntegrationMigrate(
		t,
		ctx,
		backend,
		loaded,
		migrations.TargetedLifecycleRequest(migrations.ZeroTarget("authors")),
	)
	assertPostgresMigrationIntegrationTableMissing(t, ctx, backend, schema, "authors_author")
	assertPostgresMigrationIntegrationHistory(t, ctx, backend, nil)

	state = postgresMigrationIntegrationMigrate(
		t,
		ctx,
		backend,
		loaded,
		migrations.LatestLifecycleRequest(),
	)
	assertPostgresMigrationIntegrationHistory(t, ctx, backend, postgresMigrationIntegrationAppliedHistory())
	assertPostgresMigrationIntegrationCatalog(t, ctx, backend, schema, state)

	t.Run("two sessions report contention", func(t *testing.T) {
		first := postgresMigrationIntegrationSession(t, ctx, backend)
		second := postgresMigrationIntegrationSession(t, ctx, backend)
		assertPostgresMigrationIntegrationSessionHistory(t, ctx, first, postgresMigrationIntegrationAppliedHistory())
		assertPostgresMigrationIntegrationSessionHistory(t, ctx, second, postgresMigrationIntegrationAppliedHistory())

		transition := migrationbackend.HistoryTransition{
			Migration: migrationbackend.AppliedMigration{App: "fence", Name: "0001_hold"},
			Kind:      migrationbackend.HistoryTransitionApply,
		}
		intent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{}}
		firstTransaction, err := first.BeginMigration(ctx, transition, intent)
		if err != nil {
			t.Fatalf("begin first PostgreSQL migration fence: %v", err)
		}
		contendedTransaction, contendedErr := second.BeginMigration(ctx, transition, intent)
		if contendedTransaction != nil {
			_ = contendedTransaction.Rollback(ctx)
			t.Fatal("contended PostgreSQL migration returned a transaction")
		}
		assertPostgresMigrationIntegrationFenceError(
			t,
			contendedErr,
			migrationbackend.RevisionFenceFailureContended,
		)
		if err := firstTransaction.Rollback(ctx); err != nil {
			t.Fatalf("rollback first PostgreSQL migration fence: %v", err)
		}
	})

	t.Run("two sessions report stale snapshot", func(t *testing.T) {
		stale := postgresMigrationIntegrationSession(t, ctx, backend)
		assertPostgresMigrationIntegrationSessionHistory(t, ctx, stale, postgresMigrationIntegrationAppliedHistory())

		transition := migrationbackend.HistoryTransition{
			Migration: migrationbackend.AppliedMigration{App: "fence", Name: "0001_advance"},
			Kind:      migrationbackend.HistoryTransitionApply,
		}
		intent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{}}
		advancer := postgresMigrationIntegrationSession(t, ctx, backend)
		assertPostgresMigrationIntegrationSessionHistory(t, ctx, advancer, postgresMigrationIntegrationAppliedHistory())
		advanceTransaction, err := advancer.BeginMigration(ctx, transition, intent)
		if err != nil {
			t.Fatalf("begin PostgreSQL stale-snapshot advance: %v", err)
		}
		if err := advanceTransaction.RecordApplied(ctx, "fence", "0001_advance"); err != nil {
			t.Fatalf("record PostgreSQL stale-snapshot advance: %v", err)
		}
		outcome, err := advanceTransaction.CommitFenced(ctx)
		if err != nil || outcome.Durability != migrationbackend.CommitCommitted {
			t.Fatalf("commit PostgreSQL stale-snapshot advance = (%+v, %v)", outcome, err)
		}
		assertPostgresMigrationIntegrationHistory(t, ctx, backend, append(
			postgresMigrationIntegrationAppliedHistory(),
			migrationbackend.AppliedMigration{App: "fence", Name: "0001_advance"},
		))
		transaction, err := stale.BeginMigration(
			ctx,
			transition,
			intent,
		)
		if transaction != nil {
			_ = transaction.Rollback(ctx)
			t.Fatal("stale PostgreSQL migration returned a transaction")
		}
		assertPostgresMigrationIntegrationFenceError(t, err, migrationbackend.RevisionFenceFailureStale)
	})
}

func TestPostgresMigrationRejectsInitializedRevisionZeroIntegration(t *testing.T) {
	databaseURL := postgresIntegrationURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	schema := postgresMigrationIntegrationSchema(t, ctx, databaseURL)
	backend := openPostgresMigrationIntegrationBackend(t, ctx, databaseURL, schema)
	key := migrations.MigrationKey{App: "revisionzero", Name: "0001_entry"}
	loaded, report, err := migrationdefinition.Load(postgresMigrationIntegrationSource(
		t,
		key,
		nil,
		postgresMigrationIntegrationCreateModel(
			"revisionzero",
			"entry",
			"Entry",
			"revisionzero_entry",
			postgresMigrationIntegrationAutoField(),
		),
	))
	if err != nil {
		t.Fatalf("load revision-zero PostgreSQL migration definition: %v", err)
	}
	if report.DocumentsReceived != 1 || report.HeadersValidated != 1 || report.OperationsDecoded != 1 ||
		report.PlannerConstruction != 1 || report.DefinitionsPublished != 1 || report.DefinitionSetsPublished != 1 {
		t.Fatalf("revision-zero PostgreSQL migration definition report = %+v", report)
	}
	state := postgresMigrationIntegrationMigrate(t, ctx, backend, loaded, migrations.LatestLifecycleRequest())
	model, exists := state.Model("revisionzero", "entry")
	if !exists {
		t.Fatal("revision-zero PostgreSQL migration model is missing")
	}
	catalogBefore, present, err := loadPostgresMigrationTableCatalog(ctx, backend.database, schema, model.DBTable)
	if err != nil || !present {
		t.Fatalf("load revision-zero PostgreSQL catalog before corruption = present:%t error:%v", present, err)
	}

	revisionTable, err := quoteTable(schema, postgresMigrationRevisionTable)
	if err != nil {
		t.Fatal(err)
	}
	var epochBefore, fingerprintBefore []byte
	if err := backend.database.QueryRowContext(
		ctx,
		`SELECT "epoch", "history_fingerprint" FROM `+revisionTable+` WHERE "singleton" = 1`,
	).Scan(&epochBefore, &fingerprintBefore); err != nil {
		t.Fatalf("read PostgreSQL revision-zero token bytes: %v", err)
	}
	for _, corruption := range []struct {
		name      string
		statement string
		invalid   []byte
		restore   []byte
	}{
		{
			name:      "17-byte epoch",
			statement: `UPDATE ` + revisionTable + ` SET "epoch" = $1 WHERE "singleton" = 1`,
			invalid:   make([]byte, postgresMigrationRevisionEpochBytes+1),
			restore:   epochBefore,
		},
		{
			name:      "33-byte fingerprint",
			statement: `UPDATE ` + revisionTable + ` SET "history_fingerprint" = $1 WHERE "singleton" = 1`,
			invalid:   make([]byte, postgresMigrationHistoryDigestBytes+1),
			restore:   fingerprintBefore,
		},
	} {
		if _, err := backend.database.ExecContext(ctx, corruption.statement, corruption.invalid); err != nil {
			t.Fatalf("inject PostgreSQL %s: %v", corruption.name, err)
		}
		corruptHistory, readErr := backend.ReadAppliedMigrations(ctx)
		if len(corruptHistory) != 0 {
			t.Fatalf("PostgreSQL %s returned partial history: %v", corruption.name, corruptHistory)
		}
		assertPostgresMigrationIntegrationFenceError(
			t,
			readErr,
			migrationbackend.RevisionFenceFailureIntegrity,
		)
		if _, err := backend.database.ExecContext(ctx, corruption.statement, corruption.restore); err != nil {
			t.Fatalf("restore PostgreSQL %s: %v", corruption.name, err)
		}
	}
	result, err := backend.database.ExecContext(
		ctx,
		`UPDATE `+revisionTable+` SET "revision" = 0 WHERE "singleton" = 1`,
	)
	if err != nil {
		t.Fatalf("inject impossible PostgreSQL revision zero: %v", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil || rowsAffected != 1 {
		t.Fatalf("inject impossible PostgreSQL revision zero rows = %d, error=%v", rowsAffected, err)
	}

	history, err := backend.ReadAppliedMigrations(ctx)
	if len(history) != 0 {
		t.Fatalf("revision-zero PostgreSQL read returned partial history: %v", history)
	}
	assertPostgresMigrationIntegrationFenceError(t, err, migrationbackend.RevisionFenceFailureIntegrity)
	session, sessionErr := backend.OpenRevisionFencedSession(ctx)
	if sessionErr != nil || session == nil {
		t.Fatalf("open revision-zero PostgreSQL fenced session = (%T, %v)", session, sessionErr)
	}
	sessionHistory, sessionErr := session.ReadAppliedMigrations(ctx)
	if len(sessionHistory) != 0 {
		t.Fatalf("revision-zero PostgreSQL session returned partial history: %v", sessionHistory)
	}
	assertPostgresMigrationIntegrationFenceError(
		t,
		sessionErr,
		migrationbackend.RevisionFenceFailureIntegrity,
	)
	if err := session.Close(ctx); err != nil {
		t.Fatalf("close poisoned revision-zero PostgreSQL fenced session: %v", err)
	}

	var revisionAfter int64
	var epochAfter, fingerprintAfter []byte
	if err := backend.database.QueryRowContext(
		ctx,
		`SELECT "revision", "epoch", "history_fingerprint" FROM `+revisionTable+` WHERE "singleton" = 1`,
	).Scan(&revisionAfter, &epochAfter, &fingerprintAfter); err != nil {
		t.Fatalf("read PostgreSQL revision-zero metadata after rejection: %v", err)
	}
	if revisionAfter != 0 || !reflect.DeepEqual(epochAfter, epochBefore) ||
		!reflect.DeepEqual(fingerprintAfter, fingerprintBefore) {
		t.Fatalf(
			"PostgreSQL revision-zero metadata changed: revision=%d epoch_equal=%t fingerprint_equal=%t",
			revisionAfter,
			reflect.DeepEqual(epochAfter, epochBefore),
			reflect.DeepEqual(fingerprintAfter, fingerprintBefore),
		)
	}
	recorderTable, err := quoteTable(schema, postgresMigrationRecorderTable)
	if err != nil {
		t.Fatal(err)
	}
	var app, name string
	if err := backend.database.QueryRowContext(
		ctx,
		`SELECT "app", "name" FROM `+recorderTable,
	).Scan(&app, &name); err != nil {
		t.Fatalf("read PostgreSQL revision-zero recorder after rejection: %v", err)
	}
	if app != key.App || name != key.Name {
		t.Fatalf("PostgreSQL revision-zero recorder = %s.%s, want %s.%s", app, name, key.App, key.Name)
	}
	catalogAfter, present, err := loadPostgresMigrationTableCatalog(ctx, backend.database, schema, model.DBTable)
	if err != nil || !present || !reflect.DeepEqual(catalogAfter, catalogBefore) {
		t.Fatalf(
			"PostgreSQL revision-zero catalog changed after rejection = present:%t equal:%t error:%v",
			present,
			reflect.DeepEqual(catalogAfter, catalogBefore),
			err,
		)
	}

	// The transport query is capped at one row beyond the current 2,048-record
	// history limit. Corrupt exact-shape control data must fail without loading
	// an unbounded result or mutating the already-published application schema.
	if _, err := backend.database.ExecContext(
		ctx,
		`UPDATE `+revisionTable+` SET "revision" = 1 WHERE "singleton" = 1`,
	); err != nil {
		t.Fatalf("restore positive PostgreSQL revision for history-cap canary: %v", err)
	}
	if _, err := backend.database.ExecContext(
		ctx,
		`INSERT INTO `+recorderTable+` ("app", "name") `+
			`SELECT 'overflow' || "value"::text, '0001' `+
			`FROM "pg_catalog"."generate_series"(1, 2048) AS "generated"("value")`,
	); err != nil {
		t.Fatalf("inject over-limit PostgreSQL migration history: %v", err)
	}
	history, err = backend.ReadAppliedMigrations(ctx)
	if len(history) != 0 {
		t.Fatalf("over-limit PostgreSQL read returned partial history: %v", history)
	}
	assertPostgresMigrationIntegrationFenceError(t, err, migrationbackend.RevisionFenceFailureIntegrity)
	var recorderCount int
	if err := backend.database.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+recorderTable).Scan(&recorderCount); err != nil {
		t.Fatalf("count over-limit PostgreSQL migration history after rejection: %v", err)
	}
	if recorderCount != postgresMigrationHistoryRecordLimit+1 {
		t.Fatalf(
			"over-limit PostgreSQL migration history count = %d, want %d",
			recorderCount,
			postgresMigrationHistoryRecordLimit+1,
		)
	}
	catalogAfter, present, err = loadPostgresMigrationTableCatalog(ctx, backend.database, schema, model.DBTable)
	if err != nil || !present || !reflect.DeepEqual(catalogAfter, catalogBefore) {
		t.Fatalf(
			"over-limit PostgreSQL history rejection changed catalog = present:%t equal:%t error:%v",
			present,
			reflect.DeepEqual(catalogAfter, catalogBefore),
			err,
		)
	}
}

func TestPostgresMigrationRejectsInboundControlForeignKeyIntegration(t *testing.T) {
	databaseURL := postgresIntegrationURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	schema := postgresMigrationIntegrationSchema(t, ctx, databaseURL)
	backend := openPostgresMigrationIntegrationBackend(t, ctx, databaseURL, schema)
	key := migrations.MigrationKey{App: "inbound", Name: "0001_entry"}
	loaded, report, err := migrationdefinition.Load(postgresMigrationIntegrationSource(
		t,
		key,
		nil,
		postgresMigrationIntegrationCreateModel(
			"inbound",
			"entry",
			"Entry",
			"inbound_entry",
			postgresMigrationIntegrationAutoField(),
		),
	))
	if err != nil {
		t.Fatalf("load inbound-control-FK PostgreSQL migration definition: %v", err)
	}
	if report.DocumentsReceived != 1 || report.HeadersValidated != 1 || report.OperationsDecoded != 1 ||
		report.PlannerConstruction != 1 || report.DefinitionsPublished != 1 || report.DefinitionSetsPublished != 1 {
		t.Fatalf("inbound-control-FK PostgreSQL migration definition report = %+v", report)
	}
	state := postgresMigrationIntegrationMigrate(t, ctx, backend, loaded, migrations.LatestLifecycleRequest())
	model, exists := state.Model("inbound", "entry")
	if !exists {
		t.Fatal("inbound-control-FK PostgreSQL migration model is missing")
	}
	catalogBefore, present, err := loadPostgresMigrationTableCatalog(ctx, backend.database, schema, model.DBTable)
	if err != nil || !present {
		t.Fatalf("load inbound-control-FK PostgreSQL catalog = present:%t error:%v", present, err)
	}

	recorderTable, err := quoteTable(schema, postgresMigrationRecorderTable)
	if err != nil {
		t.Fatal(err)
	}
	revisionTable, err := quoteTable(schema, postgresMigrationRevisionTable)
	if err != nil {
		t.Fatal(err)
	}
	shadowTable, err := quoteTable(schema, "godj_test_inbound_shadow")
	if err != nil {
		t.Fatal(err)
	}
	shadowPrimaryKey, err := quoteIdentifier("godj_test_inbound_shadow_pkey")
	if err != nil {
		t.Fatal(err)
	}
	shadowForeignKey, err := quoteIdentifier("godj_test_inbound_shadow_fk")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.database.ExecContext(
		ctx,
		`CREATE TABLE `+shadowTable+` (`+
			`"app" VARCHAR(255) NOT NULL, "name" VARCHAR(255) NOT NULL, `+
			`CONSTRAINT `+shadowPrimaryKey+` PRIMARY KEY ("app", "name"), `+
			`CONSTRAINT `+shadowForeignKey+` FOREIGN KEY ("app", "name") `+
			`REFERENCES `+recorderTable+` ("app", "name") ON DELETE CASCADE)`,
	); err != nil {
		t.Fatalf("create inbound-control-FK PostgreSQL shadow table: %v", err)
	}
	if _, err := backend.database.ExecContext(
		ctx,
		`INSERT INTO `+shadowTable+` ("app", "name") VALUES ($1, $2)`,
		key.App,
		key.Name,
	); err != nil {
		t.Fatalf("insert inbound-control-FK PostgreSQL shadow row: %v", err)
	}
	var revisionBefore int64
	var fingerprintBefore []byte
	if err := backend.database.QueryRowContext(
		ctx,
		`SELECT "revision", "history_fingerprint" FROM `+revisionTable+` WHERE "singleton" = 1`,
	).Scan(&revisionBefore, &fingerprintBefore); err != nil {
		t.Fatalf("read inbound-control-FK PostgreSQL revision before rejection: %v", err)
	}

	history, err := backend.ReadAppliedMigrations(ctx)
	if len(history) != 0 {
		t.Fatalf("inbound-control-FK PostgreSQL read returned partial history: %v", history)
	}
	assertPostgresMigrationIntegrationFenceError(t, err, migrationbackend.RevisionFenceFailureIntegrity)
	if _, err := (migrations.Executor{Backend: backend}).Migrate(
		ctx,
		loaded,
		migrations.TargetedLifecycleRequest(migrations.ZeroTarget(key.App)),
	); err == nil {
		t.Fatal("inbound-control-FK PostgreSQL unapply error = nil")
	} else {
		assertPostgresMigrationIntegrationFenceError(t, err, migrationbackend.RevisionFenceFailureIntegrity)
	}

	var shadowCount int
	if err := backend.database.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+shadowTable).Scan(&shadowCount); err != nil {
		t.Fatalf("count inbound-control-FK PostgreSQL shadow rows: %v", err)
	}
	if shadowCount != 1 {
		t.Fatalf("inbound-control-FK PostgreSQL shadow rows = %d, want 1", shadowCount)
	}
	var app, name string
	if err := backend.database.QueryRowContext(
		ctx,
		`SELECT "app", "name" FROM `+recorderTable,
	).Scan(&app, &name); err != nil {
		t.Fatalf("read inbound-control-FK PostgreSQL recorder after rejection: %v", err)
	}
	if app != key.App || name != key.Name {
		t.Fatalf("inbound-control-FK PostgreSQL recorder = %s.%s, want %s.%s", app, name, key.App, key.Name)
	}
	var revisionAfter int64
	var fingerprintAfter []byte
	if err := backend.database.QueryRowContext(
		ctx,
		`SELECT "revision", "history_fingerprint" FROM `+revisionTable+` WHERE "singleton" = 1`,
	).Scan(&revisionAfter, &fingerprintAfter); err != nil {
		t.Fatalf("read inbound-control-FK PostgreSQL revision after rejection: %v", err)
	}
	if revisionAfter != revisionBefore || !reflect.DeepEqual(fingerprintAfter, fingerprintBefore) {
		t.Fatalf(
			"inbound-control-FK PostgreSQL revision changed = %d/%t, want %d/true",
			revisionAfter,
			reflect.DeepEqual(fingerprintAfter, fingerprintBefore),
			revisionBefore,
		)
	}
	catalogAfter, present, err := loadPostgresMigrationTableCatalog(ctx, backend.database, schema, model.DBTable)
	if err != nil || !present || !reflect.DeepEqual(catalogAfter, catalogBefore) {
		t.Fatalf(
			"inbound-control-FK PostgreSQL rejection changed catalog = present:%t equal:%t error:%v",
			present,
			reflect.DeepEqual(catalogAfter, catalogBefore),
			err,
		)
	}
}

func TestPostgresMigrationCreateThenAddInOneDefinitionIntegration(t *testing.T) {
	databaseURL := postgresIntegrationURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	schema := postgresMigrationIntegrationSchema(t, ctx, databaseURL)
	backend := openPostgresMigrationIntegrationBackend(t, ctx, databaseURL, schema)
	key := migrations.MigrationKey{App: "compound", Name: "0001_compound"}
	loaded, report, err := migrationdefinition.Load(postgresMigrationIntegrationSource(
		t,
		key,
		nil,
		postgresMigrationIntegrationCreateModel(
			"compound",
			"entry",
			"Entry",
			"compound_entry",
			postgresMigrationIntegrationAutoField(),
			postgresMigrationIntegrationCharField("title", "Title", "title", 120, false),
		),
		postgresMigrationIntegrationAddField(
			"compound",
			"entry",
			postgresMigrationIntegrationCharField("summary", "Summary", "summary", 200, true),
		),
	))
	if err != nil {
		t.Fatalf("load compound PostgreSQL migration definition: %v", err)
	}
	if report.DocumentsReceived != 1 || report.HeadersValidated != 1 || report.OperationsDecoded != 2 ||
		report.PlannerConstruction != 1 || report.DefinitionsPublished != 1 || report.DefinitionSetsPublished != 1 {
		t.Fatalf("compound PostgreSQL migration definition report = %+v", report)
	}
	state := postgresMigrationIntegrationMigrate(t, ctx, backend, loaded, migrations.LatestLifecycleRequest())
	assertPostgresMigrationIntegrationHistory(t, ctx, backend, []migrationbackend.AppliedMigration{{App: key.App, Name: key.Name}})
	model, exists := state.Model("compound", "entry")
	if !exists || len(model.Fields) != 3 || model.Fields[2].Column != "summary" || !model.Fields[2].Nullable {
		t.Fatalf("compound PostgreSQL migration state model = %+v, exists=%t", model, exists)
	}
	catalog, present, err := loadPostgresMigrationTableCatalog(ctx, backend.database, schema, model.DBTable)
	if err != nil || !present {
		t.Fatalf("load compound PostgreSQL migration catalog = present:%t error:%v", present, err)
	}
	if err := assertPostgresMigrationModelCatalog(catalog, schema, model, nil); err != nil {
		t.Fatalf("assert compound PostgreSQL migration catalog: %v", err)
	}

	state = postgresMigrationIntegrationMigrate(
		t,
		ctx,
		backend,
		loaded,
		migrations.TargetedLifecycleRequest(migrations.ZeroTarget(key.App)),
	)
	if _, exists := state.Model("compound", "entry"); exists {
		t.Fatalf("compound PostgreSQL migration model remains after reverse: %+v", state)
	}
	assertPostgresMigrationIntegrationHistory(t, ctx, backend, nil)
	assertPostgresMigrationIntegrationTableMissing(t, ctx, backend, schema, model.DBTable)
}

func TestPostgresMigrationRejectsNullableDefaultAddOnPopulatedTableIntegration(t *testing.T) {
	databaseURL := postgresIntegrationURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	schema := postgresMigrationIntegrationSchema(t, ctx, databaseURL)
	backend := openPostgresMigrationIntegrationBackend(t, ctx, databaseURL, schema)
	initialKey := migrations.MigrationKey{App: "defaults", Name: "0001_initial"}
	defaultKey := migrations.MigrationKey{App: "defaults", Name: "0002_nullable_default"}
	defaultField := postgresMigrationIntegrationCharField("summary", "Summary", "summary", 200, true)
	defaultField["default"] = map[string]any{
		"kind":   "string",
		"string": "backfilled",
	}
	loaded, report, err := migrationdefinition.Load(
		postgresMigrationIntegrationSource(
			t,
			initialKey,
			nil,
			postgresMigrationIntegrationCreateModel(
				"defaults",
				"entry",
				"Entry",
				"defaults_entry",
				postgresMigrationIntegrationAutoField(),
				postgresMigrationIntegrationCharField("title", "Title", "title", 120, false),
			),
		),
		postgresMigrationIntegrationSource(
			t,
			defaultKey,
			[]migrations.MigrationKey{initialKey},
			postgresMigrationIntegrationAddField("defaults", "entry", defaultField),
		),
	)
	if err != nil {
		t.Fatalf("load nullable-default PostgreSQL migration definitions: %v", err)
	}
	if report.DocumentsReceived != 2 || report.HeadersValidated != 2 || report.OperationsDecoded != 2 ||
		report.PlannerConstruction != 1 || report.DefinitionsPublished != 2 || report.DefinitionSetsPublished != 1 {
		t.Fatalf("nullable-default PostgreSQL migration definition report = %+v", report)
	}
	postgresMigrationIntegrationMigrate(
		t,
		ctx,
		backend,
		loaded,
		migrations.TargetedLifecycleRequest(migrations.NamedTarget(initialKey)),
	)
	qualified, err := quoteTable(schema, "defaults_entry")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.database.ExecContext(ctx, `INSERT INTO `+qualified+` ("title") VALUES ($1)`, "preserved"); err != nil {
		t.Fatalf("insert populated nullable-default source row: %v", err)
	}

	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); !migrationbackend.IsCapabilityError(err) {
		t.Fatalf("nullable-default AddField on populated PostgreSQL table error = %T %v, want capability", err, err)
	}
	assertPostgresMigrationIntegrationHistory(t, ctx, backend, []migrationbackend.AppliedMigration{{App: initialKey.App, Name: initialKey.Name}})
	catalog, present, err := loadPostgresMigrationTableCatalog(ctx, backend.database, schema, "defaults_entry")
	if err != nil || !present {
		t.Fatalf("load rolled-back nullable-default PostgreSQL catalog = present:%t error:%v", present, err)
	}
	if len(catalog.columns) != 2 || catalog.columns[0].name != "id" || catalog.columns[1].name != "title" {
		t.Fatalf("nullable-default PostgreSQL rejection left catalog = %+v", catalog.columns)
	}
	var rowCount int
	var title string
	if err := backend.database.QueryRowContext(
		ctx,
		`SELECT COUNT(*)::integer, MIN("title") FROM `+qualified,
	).Scan(&rowCount, &title); err != nil {
		t.Fatalf("read populated nullable-default source after rejection: %v", err)
	}
	if rowCount != 1 || title != "preserved" {
		t.Fatalf("nullable-default PostgreSQL rejection rows = count:%d title:%q", rowCount, title)
	}
}

func TestPostgresMigrationRecorderFailureRollsBackSchemaHistoryAndRevisionIntegration(t *testing.T) {
	databaseURL := postgresIntegrationURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	schema := postgresMigrationIntegrationSchema(t, ctx, databaseURL)
	backend := openPostgresMigrationIntegrationBackend(t, ctx, databaseURL, schema)
	initialKey := migrations.MigrationKey{App: "recorder", Name: "0001_initial"}
	failingKey := migrations.MigrationKey{App: "recorder", Name: "0002_summary"}
	loaded, report, err := migrationdefinition.Load(
		postgresMigrationIntegrationSource(
			t,
			initialKey,
			nil,
			postgresMigrationIntegrationCreateModel(
				"recorder",
				"entry",
				"Entry",
				"recorder_entry",
				postgresMigrationIntegrationAutoField(),
				postgresMigrationIntegrationCharField("title", "Title", "title", 120, false),
			),
		),
		postgresMigrationIntegrationSource(
			t,
			failingKey,
			[]migrations.MigrationKey{initialKey},
			postgresMigrationIntegrationAddField(
				"recorder",
				"entry",
				postgresMigrationIntegrationCharField("summary", "Summary", "summary", 200, true),
			),
		),
	)
	if err != nil {
		t.Fatalf("load recorder-failure PostgreSQL definitions: %v", err)
	}
	if report.DocumentsReceived != 2 || report.HeadersValidated != 2 || report.OperationsDecoded != 2 ||
		report.PlannerConstruction != 1 || report.DefinitionsPublished != 2 || report.DefinitionSetsPublished != 1 {
		t.Fatalf("recorder-failure PostgreSQL definition report = %+v", report)
	}
	initialState := postgresMigrationIntegrationMigrate(
		t,
		ctx,
		backend,
		loaded,
		migrations.TargetedLifecycleRequest(migrations.NamedTarget(initialKey)),
	)
	assertPostgresMigrationIntegrationHistory(
		t,
		ctx,
		backend,
		[]migrationbackend.AppliedMigration{{App: initialKey.App, Name: initialKey.Name}},
	)

	revisionTable, err := quoteTable(schema, postgresMigrationRevisionTable)
	if err != nil {
		t.Fatal(err)
	}
	var revisionBefore int64
	var fingerprintBefore []byte
	if err := backend.database.QueryRowContext(
		ctx,
		`SELECT "revision", "history_fingerprint" FROM `+revisionTable+` WHERE "singleton" = 1`,
	).Scan(&revisionBefore, &fingerprintBefore); err != nil {
		t.Fatalf("read PostgreSQL revision before recorder failure: %v", err)
	}

	recorderTable, err := quoteTable(schema, postgresMigrationRecorderTable)
	if err != nil {
		t.Fatal(err)
	}
	functionName, err := quoteTable(schema, "godj_test_reject_recorder")
	if err != nil {
		t.Fatal(err)
	}
	triggerName, err := quoteIdentifier("godj_test_reject_recorder")
	if err != nil {
		t.Fatal(err)
	}
	before, exists := initialState.Model("recorder", "entry")
	if !exists || len(before.Fields) != 2 {
		t.Fatalf("recorder-failure initial state = %+v, exists=%t", before, exists)
	}
	summary := ir.Field{
		Name: "summary", GoName: "Summary", Column: "summary", Kind: ir.FieldChar,
		Nullable: true, MaxLength: 200,
	}
	after := before.Clone()
	after.Fields = append(after.Fields, summary)
	transition := migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: failingKey.App, Name: failingKey.Name},
		Kind:      migrationbackend.HistoryTransitionApply,
	}
	intent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
		OperationIndex: 0,
		Kind:           migrationbackend.MigrationAddField,
		Before:         before,
		After:          after,
		Targets:        []migrationbackend.MigrationTarget{},
	}}}
	session, err := backend.OpenRevisionFencedSession(ctx)
	if err != nil {
		t.Fatalf("open recorder-failure PostgreSQL session: %v", err)
	}
	history, err := session.ReadAppliedMigrations(ctx)
	if err != nil || !reflect.DeepEqual(history, []migrationbackend.AppliedMigration{{App: initialKey.App, Name: initialKey.Name}}) {
		t.Fatalf("recorder-failure session history = %v, error=%v", history, err)
	}
	transaction, err := session.BeginMigration(ctx, transition, intent)
	if err != nil {
		t.Fatalf("begin recorder-failure PostgreSQL transaction: %v", err)
	}
	if err := transaction.AddField(ctx, before, summary); err != nil {
		t.Fatalf("execute recorder-failure PostgreSQL AddField: %v", err)
	}
	rawTransaction, ok := transaction.(*postgresRevisionFencedTransaction)
	if !ok {
		t.Fatalf("recorder-failure transaction = %T", transaction)
	}
	duringCatalog, present, err := loadPostgresMigrationTableCatalog(
		ctx,
		rawTransaction.connection,
		schema,
		"recorder_entry",
	)
	if err != nil || !present || len(duringCatalog.columns) != 3 || duringCatalog.columns[2].name != "summary" {
		t.Fatalf("recorder-failure in-transaction schema = present:%t columns:%+v error:%v", present, duringCatalog.columns, err)
	}
	var claimedRevision int64
	if err := rawTransaction.connection.QueryRowContext(
		ctx,
		`SELECT "revision" FROM `+revisionTable+` WHERE "singleton" = 1`,
	).Scan(&claimedRevision); err != nil || claimedRevision != revisionBefore+1 {
		t.Fatalf("recorder-failure claimed revision = %d, error=%v, want %d", claimedRevision, err, revisionBefore+1)
	}

	// Install the fault only after snapshot, fence claim, and public AddField on
	// the same pinned transaction. This reaches the literal recorder INSERT;
	// the function and trigger are themselves rolled back with that transaction.
	if _, err := rawTransaction.connection.ExecContext(
		ctx,
		`CREATE FUNCTION `+functionName+`() RETURNS trigger LANGUAGE plpgsql AS $godj$ `+
			`BEGIN IF NEW."app" = 'recorder' AND NEW."name" = '0002_summary' THEN `+
			`RAISE EXCEPTION 'forced recorder failure'; END IF; RETURN NEW; END $godj$`,
	); err != nil {
		t.Fatalf("create PostgreSQL recorder-failure function: %v", err)
	}
	if _, err := rawTransaction.connection.ExecContext(
		ctx,
		`CREATE TRIGGER `+triggerName+` BEFORE INSERT ON `+recorderTable+` `+
			`FOR EACH ROW EXECUTE FUNCTION `+functionName+`()`,
	); err != nil {
		t.Fatalf("create PostgreSQL recorder-failure trigger: %v", err)
	}

	recordErr := transaction.RecordApplied(ctx, failingKey.App, failingKey.Name)
	if recordErr == nil || !strings.Contains(recordErr.Error(), "SQLSTATE P0001") ||
		!strings.Contains(recordErr.Error(), "forced recorder failure") {
		t.Fatalf("forced PostgreSQL recorder error = %T %v, want P0001 sentinel", recordErr, recordErr)
	}
	outcome, commitErr := transaction.CommitFenced(ctx)
	if outcome.Durability != migrationbackend.CommitRolledBack || !errors.Is(commitErr, recordErr) {
		t.Fatalf("commit forced PostgreSQL recorder failure = (%+v, %v), want rolled back with %v", outcome, commitErr, recordErr)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatalf("close recorder-failure PostgreSQL session: %v", err)
	}
	assertPostgresMigrationIntegrationHistory(
		t,
		ctx,
		backend,
		[]migrationbackend.AppliedMigration{{App: initialKey.App, Name: initialKey.Name}},
	)
	catalog, present, err := loadPostgresMigrationTableCatalog(ctx, backend.database, schema, "recorder_entry")
	if err != nil || !present {
		t.Fatalf("load recorder-failure PostgreSQL catalog = present:%t error:%v", present, err)
	}
	if len(catalog.columns) != 2 || catalog.columns[0].name != "id" || catalog.columns[1].name != "title" {
		t.Fatalf("recorder failure left PostgreSQL schema mutation = %+v", catalog.columns)
	}
	var revisionAfter int64
	var fingerprintAfter []byte
	if err := backend.database.QueryRowContext(
		ctx,
		`SELECT "revision", "history_fingerprint" FROM `+revisionTable+` WHERE "singleton" = 1`,
	).Scan(&revisionAfter, &fingerprintAfter); err != nil {
		t.Fatalf("read PostgreSQL revision after recorder failure: %v", err)
	}
	if revisionAfter != revisionBefore || !reflect.DeepEqual(fingerprintAfter, fingerprintBefore) {
		t.Fatalf(
			"recorder failure changed PostgreSQL revision = before:%d/%x after:%d/%x",
			revisionBefore,
			fingerprintBefore,
			revisionAfter,
			fingerprintAfter,
		)
	}
	var faultFunctionCount int
	if err := backend.database.QueryRowContext(
		ctx,
		`SELECT COUNT(*)::integer FROM "pg_catalog"."pg_proc" AS "p" `+
			`JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "p"."pronamespace" `+
			`WHERE "n"."nspname" = $1 AND "p"."proname" = $2`,
		schema,
		"godj_test_reject_recorder",
	).Scan(&faultFunctionCount); err != nil || faultFunctionCount != 0 {
		t.Fatalf("rolled-back PostgreSQL recorder fault function count = %d, error=%v", faultFunctionCount, err)
	}

	state := postgresMigrationIntegrationMigrate(t, ctx, backend, loaded, migrations.LatestLifecycleRequest())
	assertPostgresMigrationIntegrationHistory(t, ctx, backend, []migrationbackend.AppliedMigration{
		{App: initialKey.App, Name: initialKey.Name},
		{App: failingKey.App, Name: failingKey.Name},
	})
	model, exists := state.Model("recorder", "entry")
	if !exists || len(model.Fields) != 3 || model.Fields[2].Column != "summary" {
		t.Fatalf("recorder-failure retry state = %+v, exists=%t", model, exists)
	}
}

func TestPostgresMigrationRejectsAddAfterDroppedAttributeSlotsAreExhaustedIntegration(t *testing.T) {
	databaseURL := postgresIntegrationURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	schema := postgresMigrationIntegrationSchema(t, ctx, databaseURL)
	backend := openPostgresMigrationIntegrationBackend(t, ctx, databaseURL, schema)
	initialKey := migrations.MigrationKey{App: "slots", Name: "0001_initial"}
	addKey := migrations.MigrationKey{App: "slots", Name: "0002_last_visible"}
	fields := make([]map[string]any, postgresMigrationMaxModelFields-1)
	fields[0] = postgresMigrationIntegrationAutoField()
	for index := 1; index < len(fields); index++ {
		name := fmt.Sprintf("value_%03d", index)
		fields[index] = postgresMigrationIntegrationCharField(name, "Value"+fmt.Sprintf("%03d", index), name, 1, true)
	}
	loaded, report, err := migrationdefinition.Load(
		postgresMigrationIntegrationSource(
			t,
			initialKey,
			nil,
			postgresMigrationIntegrationCreateModel("slots", "entry", "Entry", "slots_entry", fields...),
		),
		postgresMigrationIntegrationSource(
			t,
			addKey,
			[]migrations.MigrationKey{initialKey},
			postgresMigrationIntegrationAddField(
				"slots",
				"entry",
				postgresMigrationIntegrationCharField("last_visible", "LastVisible", "last_visible", 1, true),
			),
		),
	)
	if err != nil {
		t.Fatalf("load attribute-slot PostgreSQL definitions: %v", err)
	}
	if report.DocumentsReceived != 2 || report.HeadersValidated != 2 || report.OperationsDecoded != 2 ||
		report.PlannerConstruction != 1 || report.DefinitionsPublished != 2 || report.DefinitionSetsPublished != 1 {
		t.Fatalf("attribute-slot PostgreSQL definition report = %+v", report)
	}
	postgresMigrationIntegrationMigrate(
		t,
		ctx,
		backend,
		loaded,
		migrations.TargetedLifecycleRequest(migrations.NamedTarget(initialKey)),
	)

	qualifiedTable, err := quoteTable(schema, "slots_entry")
	if err != nil {
		t.Fatal(err)
	}
	tombstones := postgresMigrationMaxAttributeSlots - len(fields)
	addFragments := make([]string, tombstones)
	dropFragments := make([]string, tombstones)
	for index := 0; index < tombstones; index++ {
		column, err := quoteIdentifier(fmt.Sprintf("godj_test_tombstone_%04d", index))
		if err != nil {
			t.Fatal(err)
		}
		addFragments[index] = "ADD COLUMN " + column + " BOOLEAN NULL"
		dropFragments[index] = "DROP COLUMN " + column + " RESTRICT"
	}
	if _, err := backend.database.ExecContext(
		ctx,
		"ALTER TABLE "+qualifiedTable+" "+strings.Join(addFragments, ", "),
	); err != nil {
		t.Fatalf("fill PostgreSQL physical attribute slots: %v", err)
	}
	if _, err := backend.database.ExecContext(
		ctx,
		"ALTER TABLE "+qualifiedTable+" "+strings.Join(dropFragments, ", "),
	); err != nil {
		t.Fatalf("create PostgreSQL dropped-attribute tombstones: %v", err)
	}
	catalog, present, err := loadPostgresMigrationTableCatalog(ctx, backend.database, schema, "slots_entry")
	if err != nil || !present {
		t.Fatalf("load exhausted PostgreSQL attribute catalog = present:%t error:%v", present, err)
	}
	if len(catalog.columns) != len(fields) || catalog.attributeSlots != postgresMigrationMaxAttributeSlots {
		t.Fatalf(
			"exhausted PostgreSQL attribute catalog = visible:%d slots:%d, want visible:%d slots:%d",
			len(catalog.columns),
			catalog.attributeSlots,
			len(fields),
			postgresMigrationMaxAttributeSlots,
		)
	}

	if _, err := (migrations.Executor{Backend: backend}).Migrate(
		ctx,
		loaded,
		migrations.LatestLifecycleRequest(),
	); !migrationbackend.IsCapabilityError(err) {
		t.Fatalf("exhausted PostgreSQL attribute AddField error = %T %v, want capability", err, err)
	}
	assertPostgresMigrationIntegrationHistory(
		t,
		ctx,
		backend,
		[]migrationbackend.AppliedMigration{{App: initialKey.App, Name: initialKey.Name}},
	)
	catalog, present, err = loadPostgresMigrationTableCatalog(ctx, backend.database, schema, "slots_entry")
	if err != nil || !present || len(catalog.columns) != len(fields) ||
		catalog.attributeSlots != postgresMigrationMaxAttributeSlots {
		t.Fatalf(
			"rejected PostgreSQL AddField changed attribute catalog = present:%t visible:%d slots:%d error:%v",
			present,
			len(catalog.columns),
			catalog.attributeSlots,
			err,
		)
	}
}

func postgresMigrationIntegrationDefinitions(t *testing.T) migrations.LoadedDefinitionSet {
	t.Helper()
	sources := []migrationdefinition.Source{
		postgresMigrationIntegrationSource(
			t,
			postgresMigrationIntegrationAuthorKey,
			nil,
			postgresMigrationIntegrationCreateModel(
				"authors",
				"author",
				"Author",
				"authors_author",
				postgresMigrationIntegrationAutoField(),
				postgresMigrationIntegrationCharField("name", "Name", "name", 120, false),
			),
		),
		postgresMigrationIntegrationSource(
			t,
			postgresMigrationIntegrationPostKey,
			[]migrations.MigrationKey{postgresMigrationIntegrationAuthorKey},
			postgresMigrationIntegrationCreateModel(
				"blog",
				"post",
				"Post",
				"blog_post",
				postgresMigrationIntegrationAutoField(),
				postgresMigrationIntegrationCharField("title", "Title", "title", 200, false),
			),
		),
		postgresMigrationIntegrationSource(
			t,
			postgresMigrationIntegrationFieldsKey,
			[]migrations.MigrationKey{postgresMigrationIntegrationPostKey},
			postgresMigrationIntegrationAddField(
				"blog",
				"post",
				postgresMigrationIntegrationCharField("summary", "Summary", "summary", 200, true),
			),
			postgresMigrationIntegrationAddField(
				"blog",
				"post",
				postgresMigrationIntegrationBooleanField("published", "Published", "published"),
			),
		),
		postgresMigrationIntegrationSource(
			t,
			postgresMigrationIntegrationPostFKKey,
			[]migrations.MigrationKey{postgresMigrationIntegrationFieldsKey},
			postgresMigrationIntegrationAddField(
				"blog",
				"post",
				postgresMigrationIntegrationForeignKeyField(
					"author", "Author", "author_id", "authors", "author", "posts", true,
				),
			),
		),
		postgresMigrationIntegrationSource(
			t,
			postgresMigrationIntegrationCommentKey,
			[]migrations.MigrationKey{postgresMigrationIntegrationAuthorKey},
			postgresMigrationIntegrationCreateModel(
				"comments",
				"comment",
				"Comment",
				"comments_comment",
				postgresMigrationIntegrationAutoField(),
				postgresMigrationIntegrationCharField("body", "Body", "body", 500, false),
				postgresMigrationIntegrationForeignKeyField(
					"author", "Author", "author_id", "authors", "author", "comments", false,
				),
			),
		),
	}
	loaded, report, err := migrationdefinition.Load(sources...)
	if err != nil {
		t.Fatalf("load PostgreSQL migration integration definitions: %v", err)
	}
	if report.DocumentsReceived != 5 || report.HeadersValidated != 5 || report.OperationsDecoded != 6 ||
		report.PlannerConstruction != 1 || report.DefinitionsPublished != 5 || report.DefinitionSetsPublished != 1 {
		t.Fatalf("PostgreSQL migration definition report = %+v", report)
	}
	return loaded
}

func postgresMigrationIntegrationSource(
	t *testing.T,
	key migrations.MigrationKey,
	dependencies []migrations.MigrationKey,
	operations ...map[string]any,
) migrationdefinition.Source {
	t.Helper()
	encodedDependencies := make([]map[string]string, len(dependencies))
	for index := range dependencies {
		encodedDependencies[index] = map[string]string{
			"app":  dependencies[index].App,
			"name": dependencies[index].Name,
		}
	}
	encodedOperations := make([]map[string]any, len(operations))
	copy(encodedOperations, operations)
	document, err := json.Marshal(map[string]any{
		"format_version": migrationdefinition.DefinitionFormatVersion,
		"producer": map[string]string{
			"name":    "postgres-integration",
			"version": "1",
		},
		"migration": map[string]any{
			"app":          key.App,
			"name":         key.Name,
			"dependencies": encodedDependencies,
			"operations":   encodedOperations,
		},
	})
	if err != nil {
		t.Fatalf("marshal PostgreSQL migration integration definition %s.%s: %v", key.App, key.Name, err)
	}
	return migrationdefinition.Source{
		SourceID: key.App + "/" + key.Name + ".godj.json",
		Document: document,
	}
}

func postgresMigrationIntegrationCreateModel(
	app,
	name,
	goName,
	table string,
	fields ...map[string]any,
) map[string]any {
	return map[string]any{
		"kind":      "create_model",
		"app_label": app,
		"model": map[string]any{
			"name":     name,
			"go_name":  goName,
			"db_table": table,
			"fields":   fields,
		},
	}
}

func postgresMigrationIntegrationAddField(app, model string, field map[string]any) map[string]any {
	return map[string]any{
		"kind":       "add_field",
		"app_label":  app,
		"model_name": model,
		"field":      field,
	}
}

func postgresMigrationIntegrationAutoField() map[string]any {
	return map[string]any{
		"name":        "id",
		"go_name":     "ID",
		"column":      "id",
		"kind":        "auto",
		"primary_key": true,
		"nullable":    false,
		"max_length":  0,
		"default":     nil,
	}
}

func postgresMigrationIntegrationCharField(
	name,
	goName,
	column string,
	maxLength int,
	nullable bool,
) map[string]any {
	return map[string]any{
		"name":        name,
		"go_name":     goName,
		"column":      column,
		"kind":        "char",
		"primary_key": false,
		"nullable":    nullable,
		"max_length":  maxLength,
		"default":     nil,
	}
}

func postgresMigrationIntegrationBooleanField(name, goName, column string) map[string]any {
	return map[string]any{
		"name":        name,
		"go_name":     goName,
		"column":      column,
		"kind":        "boolean",
		"primary_key": false,
		"nullable":    false,
		"max_length":  0,
		"default": map[string]any{
			"kind":    "boolean",
			"boolean": false,
		},
	}
}

func postgresMigrationIntegrationForeignKeyField(
	name,
	goName,
	column,
	targetApp,
	targetModel,
	reverse string,
	nullable bool,
) map[string]any {
	return map[string]any{
		"name":        name,
		"go_name":     goName,
		"column":      column,
		"kind":        "foreign_key",
		"primary_key": false,
		"nullable":    nullable,
		"max_length":  0,
		"default":     nil,
		"relation": map[string]any{
			"target": map[string]string{
				"app_label":  targetApp,
				"model_name": targetModel,
			},
			"cardinality": "many_to_one",
			"reverse": map[string]any{
				"name":     reverse,
				"disabled": false,
			},
			"on_delete": "protect",
		},
	}
}

func postgresMigrationIntegrationMigrate(
	t *testing.T,
	ctx context.Context,
	backend *Backend,
	loaded migrations.LoadedDefinitionSet,
	request migrations.LifecycleRequest,
) migrations.ProjectState {
	t.Helper()
	state, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, request)
	if err != nil {
		t.Fatalf("migrate PostgreSQL integration lifecycle: %v", err)
	}
	return state
}

func postgresMigrationIntegrationAppliedHistory() []migrationbackend.AppliedMigration {
	return []migrationbackend.AppliedMigration{
		{App: "authors", Name: "0001_author"},
		{App: "blog", Name: "0001_post"},
		{App: "blog", Name: "0002_fields"},
		{App: "blog", Name: "0003_author"},
		{App: "comments", Name: "0001_comment"},
	}
}

func assertPostgresMigrationIntegrationHistory(
	t *testing.T,
	ctx context.Context,
	backend *Backend,
	want []migrationbackend.AppliedMigration,
) {
	t.Helper()
	got, err := backend.ReadAppliedMigrations(ctx)
	if err != nil {
		t.Fatalf("read PostgreSQL migration integration history: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("PostgreSQL migration history = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("PostgreSQL migration history = %v, want %v", got, want)
		}
	}
}

func assertPostgresMigrationIntegrationCatalog(
	t *testing.T,
	ctx context.Context,
	backend *Backend,
	schema string,
	state migrations.ProjectState,
) {
	t.Helper()
	author, authorExists := state.Model("authors", "author")
	post, postExists := state.Model("blog", "post")
	comment, commentExists := state.Model("comments", "comment")
	if !authorExists || !postExists || !commentExists ||
		len(author.Fields) != 2 || len(post.Fields) != 5 || len(comment.Fields) != 3 {
		t.Fatalf(
			"PostgreSQL migration state model shapes = author:%+v/%t post:%+v/%t comment:%+v/%t",
			author,
			authorExists,
			post,
			postExists,
			comment,
			commentExists,
		)
	}
	postTarget := migrationbackend.MigrationTarget{
		SourceField: post.Fields[4],
		TargetModel: author,
		TargetKey:   author.Fields[0],
	}
	commentTarget := migrationbackend.MigrationTarget{
		SourceField: comment.Fields[2],
		TargetModel: author,
		TargetKey:   author.Fields[0],
	}
	checks := []struct {
		model   ir.Model
		table   string
		targets []migrationbackend.MigrationTarget
	}{
		{model: author, table: "authors_author"},
		{model: post, table: "blog_post", targets: []migrationbackend.MigrationTarget{postTarget}},
		{model: comment, table: "comments_comment", targets: []migrationbackend.MigrationTarget{commentTarget}},
	}
	for _, check := range checks {
		catalog, present, err := loadPostgresMigrationTableCatalog(ctx, backend.database, schema, check.table)
		if err != nil || !present {
			t.Fatalf("load PostgreSQL migration catalog %s: present=%t err=%v", check.table, present, err)
		}
		if err := assertPostgresMigrationModelCatalog(catalog, schema, check.model, check.targets); err != nil {
			t.Fatalf("verify PostgreSQL migration catalog %s: %v", check.table, err)
		}
	}
}

func assertPostgresMigrationIntegrationDisabledForeignKeyTriggerDrift(
	t *testing.T,
	ctx context.Context,
	backend *Backend,
	schema string,
	state migrations.ProjectState,
) {
	t.Helper()
	post, postExists := state.Model("blog", "post")
	author, authorExists := state.Model("authors", "author")
	if !postExists || !authorExists || len(post.Fields) != 5 || len(author.Fields) != 2 {
		t.Fatalf("PostgreSQL disabled-trigger drift state = post:%+v/%t author:%+v/%t", post, postExists, author, authorExists)
	}
	target := migrationbackend.MigrationTarget{
		SourceField: post.Fields[4],
		TargetModel: author,
		TargetKey:   author.Fields[0],
	}
	qualified, err := quoteTable(schema, post.DBTable)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.database.ExecContext(ctx, "ALTER TABLE "+qualified+" DISABLE TRIGGER ALL"); err != nil {
		t.Fatalf("disable PostgreSQL ForeignKey enforcement triggers: %v", err)
	}
	catalog, present, err := loadPostgresMigrationTableCatalog(ctx, backend.database, schema, post.DBTable)
	if err != nil || !present {
		t.Fatalf("load disabled-trigger PostgreSQL catalog = present:%t error:%v", present, err)
	}
	if err := assertPostgresMigrationModelCatalog(catalog, schema, post, []migrationbackend.MigrationTarget{target}); err == nil || !migrationbackend.IsCapabilityError(err) {
		t.Fatalf("disabled PostgreSQL ForeignKey trigger catalog error = %T %v, want capability", err, err)
	}
	if _, err := backend.database.ExecContext(ctx, "ALTER TABLE "+qualified+" ENABLE TRIGGER ALL"); err != nil {
		t.Fatalf("restore PostgreSQL ForeignKey enforcement triggers: %v", err)
	}
	catalog, present, err = loadPostgresMigrationTableCatalog(ctx, backend.database, schema, post.DBTable)
	if err != nil || !present {
		t.Fatalf("reload restored-trigger PostgreSQL catalog = present:%t error:%v", present, err)
	}
	if err := assertPostgresMigrationModelCatalog(catalog, schema, post, []migrationbackend.MigrationTarget{target}); err != nil {
		t.Fatalf("restored PostgreSQL ForeignKey trigger catalog: %v", err)
	}
}

func assertPostgresMigrationIntegrationPostWithoutForeignKey(
	t *testing.T,
	ctx context.Context,
	backend *Backend,
	schema string,
	state migrations.ProjectState,
) {
	t.Helper()
	post, exists := state.Model("blog", "post")
	if !exists || len(post.Fields) != 4 || post.Fields[3].Name != "published" {
		t.Fatalf("post state after ForeignKey reverse = %+v, exists=%t", post, exists)
	}
	catalog, present, err := loadPostgresMigrationTableCatalog(ctx, backend.database, schema, post.DBTable)
	if err != nil || !present {
		t.Fatalf("load post catalog after ForeignKey reverse: present=%t err=%v", present, err)
	}
	if err := assertPostgresMigrationModelCatalog(catalog, schema, post, nil); err != nil {
		t.Fatalf("verify post catalog after ForeignKey reverse: %v", err)
	}
}

func assertPostgresMigrationIntegrationTableMissing(
	t *testing.T,
	ctx context.Context,
	backend *Backend,
	schema,
	table string,
) {
	t.Helper()
	_, present, err := loadPostgresMigrationTableCatalog(ctx, backend.database, schema, table)
	if err != nil || present {
		t.Fatalf("PostgreSQL migration table %s missing = %t, err=%v", table, !present, err)
	}
}

func postgresMigrationIntegrationSession(
	t *testing.T,
	ctx context.Context,
	backend *Backend,
) migrationbackend.RevisionFencedSession {
	t.Helper()
	session, err := backend.OpenRevisionFencedSession(ctx)
	if err != nil {
		t.Fatalf("open PostgreSQL revision-fenced integration session: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := session.Close(cleanupCtx); err != nil {
			t.Errorf("close PostgreSQL revision-fenced integration session: %v", err)
		}
	})
	return session
}

func assertPostgresMigrationIntegrationSessionHistory(
	t *testing.T,
	ctx context.Context,
	session migrationbackend.RevisionFencedSession,
	want []migrationbackend.AppliedMigration,
) {
	t.Helper()
	got, err := session.ReadAppliedMigrations(ctx)
	if err != nil {
		t.Fatalf("read PostgreSQL revision-fenced integration session: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PostgreSQL revision-fenced session history = %v, want %v", got, want)
	}
}

func assertPostgresMigrationIntegrationFenceError(
	t *testing.T,
	err error,
	want migrationbackend.RevisionFenceFailureKind,
) {
	t.Helper()
	var fenceError *migrationbackend.RevisionFenceError
	if !errors.As(err, &fenceError) || fenceError == nil || fenceError.Kind != want {
		t.Fatalf("PostgreSQL migration fence error = %v, want kind %d", err, want)
	}
}

func postgresMigrationIntegrationSchema(t *testing.T, ctx context.Context, databaseURL string) string {
	t.Helper()
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect PostgreSQL migration integration database: %v", redactConnectionError(err))
	}
	schema := fmt.Sprintf("godj_migration_%d", time.Now().UnixNano())
	quotedSchema, err := quoteIdentifier(schema)
	if err != nil {
		_ = admin.Close(ctx)
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("create PostgreSQL migration integration schema: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+quotedSchema+" CASCADE"); err != nil {
			t.Errorf("drop PostgreSQL migration integration schema: %v", err)
		}
		if err := admin.Close(cleanupCtx); err != nil {
			t.Errorf("close PostgreSQL migration integration admin connection: %v", err)
		}
	})
	return schema
}

func openPostgresMigrationIntegrationBackend(
	t *testing.T,
	ctx context.Context,
	databaseURL,
	schema string,
) *Backend {
	t.Helper()
	backend, err := Open(ctx, Config{URL: databaseURL, Schema: schema})
	if err != nil {
		t.Fatalf("open PostgreSQL migration integration backend: %v", err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("close PostgreSQL migration integration backend: %v", err)
		}
	})
	return backend
}
