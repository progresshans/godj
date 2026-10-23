package postgres

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
)

const (
	postgresMigrationFenceHelperMarkerEnv = "GODJ_POSTGRES_MIGRATION_FENCE_HELPER"
	postgresMigrationFenceHelperURLEnv    = "GODJ_POSTGRES_MIGRATION_FENCE_HELPER_URL"
	postgresMigrationFenceHelperSchemaEnv = "GODJ_POSTGRES_MIGRATION_FENCE_HELPER_SCHEMA"

	postgresMigrationFenceHelperReady   byte = 'R'
	postgresMigrationFenceHelperRelease byte = 'X'

	postgresMigrationFenceProcessTimeout = 45 * time.Second
)

func TestPostgresRevisionFenceCrossProcessIntegration(t *testing.T) {
	databaseURL := postgresIntegrationURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), postgresMigrationFenceProcessTimeout)
	t.Cleanup(cancel)

	schema := postgresMigrationIntegrationSchema(t, ctx, databaseURL)
	backend := openPostgresMigrationIntegrationBackend(t, ctx, databaseURL, schema)
	session := postgresMigrationIntegrationSession(t, ctx, backend)
	assertPostgresMigrationIntegrationSessionHistory(t, ctx, session, nil)

	readyReader, readyWriter, err := os.Pipe()
	if err != nil {
		cancel()
		t.Fatalf("create PostgreSQL migration helper READY pipe: %v", err)
	}
	releaseReader, releaseWriter, err := os.Pipe()
	if err != nil {
		_ = readyReader.Close()
		_ = readyWriter.Close()
		cancel()
		t.Fatalf("create PostgreSQL migration helper RELEASE pipe: %v", err)
	}

	command := exec.CommandContext(
		ctx,
		os.Args[0],
		"-test.run=^TestPostgresRevisionFenceHelperProcess$",
		"-test.timeout="+postgresMigrationFenceProcessTimeout.String(),
	)
	command.Env = postgresMigrationFenceHelperEnvironment(databaseURL, schema)
	command.ExtraFiles = []*os.File{readyWriter, releaseReader}
	command.WaitDelay = 2 * time.Second
	var helperOutput bytes.Buffer
	command.Stdout = &helperOutput
	command.Stderr = &helperOutput

	started := false
	waited := false
	t.Cleanup(func() {
		_ = releaseWriter.Close()
		_ = readyReader.Close()
		_ = readyWriter.Close()
		_ = releaseReader.Close()
		cancel()
		if started && !waited {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	})
	for _, argument := range command.Args {
		if strings.Contains(argument, databaseURL) || strings.Contains(argument, schema) {
			t.Fatal("PostgreSQL migration fence helper URL or schema was placed in process arguments")
		}
	}

	if err := command.Start(); err != nil {
		t.Fatalf("start PostgreSQL migration fence helper process: %v", err)
	}
	started = true
	if err := readyWriter.Close(); err != nil {
		t.Fatalf("close parent copy of PostgreSQL migration helper READY writer: %v", err)
	}
	if err := releaseReader.Close(); err != nil {
		t.Fatalf("close parent copy of PostgreSQL migration helper RELEASE reader: %v", err)
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("PostgreSQL migration fence process context has no deadline")
	}
	if err := readyReader.SetReadDeadline(deadline); err != nil {
		t.Fatalf("bound PostgreSQL migration helper READY read: %v", err)
	}
	if err := releaseWriter.SetWriteDeadline(deadline); err != nil {
		t.Fatalf("bound PostgreSQL migration helper RELEASE write: %v", err)
	}

	var ready [1]byte
	if _, err := io.ReadFull(readyReader, ready[:]); err != nil {
		helperErr := command.Wait()
		waited = true
		t.Fatalf(
			"wait for PostgreSQL migration helper READY: %v; helper error: %v; output: %s",
			err,
			helperErr,
			redactPostgresMigrationFenceHelperOutput(helperOutput.String(), databaseURL),
		)
	}
	if ready[0] != postgresMigrationFenceHelperReady {
		t.Fatalf("PostgreSQL migration helper READY byte = %q", ready[0])
	}
	if err := readyReader.Close(); err != nil {
		t.Fatalf("close PostgreSQL migration helper READY reader: %v", err)
	}

	transition := migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "process_fence", Name: "0001_hold"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}
	transaction, beginErr := session.BeginMigration(
		ctx,
		transition,
		migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{}},
	)
	unexpectedTransaction := transaction != nil
	var unexpectedRollbackErr error
	if transaction != nil {
		rollbackCtx, rollbackCancel := context.WithTimeout(context.Background(), 5*time.Second)
		unexpectedRollbackErr = transaction.Rollback(rollbackCtx)
		rollbackCancel()
	}

	if _, err := releaseWriter.Write([]byte{postgresMigrationFenceHelperRelease}); err != nil {
		t.Fatalf("release PostgreSQL migration fence helper: %v", err)
	}
	if err := releaseWriter.Close(); err != nil {
		t.Fatalf("close PostgreSQL migration fence helper RELEASE writer: %v", err)
	}
	if err := command.Wait(); err != nil {
		waited = true
		t.Fatalf(
			"PostgreSQL migration fence helper did not rollback and exit cleanly: %v; output: %s",
			err,
			redactPostgresMigrationFenceHelperOutput(helperOutput.String(), databaseURL),
		)
	}
	waited = true
	if strings.Contains(helperOutput.String(), databaseURL) {
		t.Fatal("PostgreSQL migration fence helper output exposed the database URL")
	}

	if unexpectedTransaction {
		t.Fatalf(
			"cross-process contended PostgreSQL migration returned a transaction; rollback error: %s",
			redactPostgresMigrationFenceHelperOutput(
				postgresMigrationFenceErrorString(unexpectedRollbackErr),
				databaseURL,
			),
		)
	}
	var fenceError *migrationbackend.RevisionFenceError
	if !errors.As(beginErr, &fenceError) ||
		fenceError == nil ||
		fenceError.Kind != migrationbackend.RevisionFenceFailureContended {
		t.Fatalf(
			"cross-process PostgreSQL migration fence error = %s, want contention",
			redactPostgresMigrationFenceHelperOutput(postgresMigrationFenceErrorString(beginErr), databaseURL),
		)
	}
}

func TestPostgresRevisionFenceHelperProcess(t *testing.T) {
	if os.Getenv(postgresMigrationFenceHelperMarkerEnv) != "1" {
		t.Skip("PostgreSQL migration fence helper environment is not configured")
	}
	databaseURL := os.Getenv(postgresMigrationFenceHelperURLEnv)
	schema := os.Getenv(postgresMigrationFenceHelperSchemaEnv)
	if databaseURL == "" || schema == "" {
		t.Fatal("PostgreSQL migration fence helper URL or schema environment is missing")
	}
	if err := validateSchemaIdentifier(schema); err != nil {
		t.Fatalf("validate PostgreSQL migration fence helper schema: %v", err)
	}

	readyWriter := os.NewFile(uintptr(3), "postgres-migration-fence-ready")
	releaseReader := os.NewFile(uintptr(4), "postgres-migration-fence-release")
	if readyWriter == nil || releaseReader == nil {
		t.Fatal("PostgreSQL migration fence helper pipes are unavailable")
	}
	t.Cleanup(func() {
		_ = readyWriter.Close()
		_ = releaseReader.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), postgresMigrationFenceProcessTimeout)
	defer cancel()
	// ExtraFiles descriptors are not deadline-pollable on every supported
	// platform. The parent CommandContext and this helper's -test.timeout bound
	// both pipe waits, and the parent always kills and reaps an unfinished child.

	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect PostgreSQL migration fence helper: %v", redactConnectionError(err))
	}
	var transaction pgx.Tx
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if transaction != nil {
			if err := transaction.Rollback(cleanupCtx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
				t.Errorf("rollback PostgreSQL migration fence helper during cleanup: %v", redactConnectionError(err))
			}
		}
		if err := connection.Close(cleanupCtx); err != nil {
			t.Errorf("close PostgreSQL migration fence helper connection: %v", redactConnectionError(err))
		}
	})

	transaction, err = connection.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		t.Fatalf("begin PostgreSQL migration fence helper transaction: %v", redactConnectionError(err))
	}
	var locked bool
	if err := transaction.QueryRow(
		ctx,
		`SELECT "pg_catalog"."pg_try_advisory_xact_lock"($1)`,
		postgresMigrationAdvisoryLockKey(schema),
	).Scan(&locked); err != nil {
		t.Fatalf("acquire PostgreSQL migration fence in helper: %v", redactConnectionError(err))
	}
	if !locked {
		t.Fatal("PostgreSQL migration fence helper could not acquire its advisory lock")
	}
	if _, err := readyWriter.Write([]byte{postgresMigrationFenceHelperReady}); err != nil {
		t.Fatalf("signal PostgreSQL migration fence helper READY: %v", err)
	}
	if err := readyWriter.Close(); err != nil {
		t.Fatalf("close PostgreSQL migration fence helper READY writer: %v", err)
	}

	var release [1]byte
	if _, err := io.ReadFull(releaseReader, release[:]); err != nil {
		t.Fatalf("wait for PostgreSQL migration fence helper RELEASE: %v", err)
	}
	if release[0] != postgresMigrationFenceHelperRelease {
		t.Fatalf("PostgreSQL migration fence helper RELEASE byte = %q", release[0])
	}
	if err := releaseReader.Close(); err != nil {
		t.Fatalf("close PostgreSQL migration fence helper RELEASE reader: %v", err)
	}

	rollbackCtx, rollbackCancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = transaction.Rollback(rollbackCtx)
	rollbackCancel()
	if err != nil {
		t.Fatalf("rollback PostgreSQL migration fence helper transaction: %v", redactConnectionError(err))
	}
	transaction = nil
}

func postgresMigrationFenceHelperEnvironment(databaseURL, schema string) []string {
	currentEnvironment := os.Environ()
	environment := make([]string, 0, len(currentEnvironment)+3)
	for _, entry := range currentEnvironment {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case postgresMigrationFenceHelperMarkerEnv,
			postgresMigrationFenceHelperURLEnv,
			postgresMigrationFenceHelperSchemaEnv:
			continue
		default:
			environment = append(environment, entry)
		}
	}
	return append(
		environment,
		postgresMigrationFenceHelperMarkerEnv+"=1",
		postgresMigrationFenceHelperURLEnv+"="+databaseURL,
		postgresMigrationFenceHelperSchemaEnv+"="+schema,
	)
}

func redactPostgresMigrationFenceHelperOutput(output, databaseURL string) string {
	if databaseURL == "" {
		return output
	}
	return strings.ReplaceAll(output, databaseURL, "<redacted PostgreSQL URL>")
}

func postgresMigrationFenceErrorString(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}
