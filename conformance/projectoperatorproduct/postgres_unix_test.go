//go:build darwin || linux

package projectoperatorproduct_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http/cookiejar"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	operatorattestation "github.com/progresshans/godj/conformance/projectoperatorproduct/attestation"
)

const (
	operatorPostgresTestURLEnvironment            = "GODJ_TEST_POSTGRES_URL"
	operatorPostgresRequiredEnvironment           = "GODJ_REQUIRE_POSTGRES"
	operatorPostgresAttestationCaptureEnvironment = "GODJ_PROJECT_OPERATOR_POSTGRES_ATTESTATION_CAPTURE"
)

var operatorPostgresSchemaSequence atomic.Uint64

func TestGlobalCreatesuperuserExternalPostgresAndSQLiteProduct(t *testing.T) {
	databaseURL := operatorPostgresTestURL(t)
	repository := operatorRepositoryRoot(t)
	sourceBefore, err := operatorattestation.ComputeSourceBinding(repository)
	if err != nil {
		t.Fatal("compute pre-run external operator source binding")
	}
	schema, fingerprint := operatorCreatePostgresSchema(t, databaseURL)
	operatorAssertPostgresSchemaSnapshotDetectsTriggerMutation(t, databaseURL, schema)
	project := newOperatorExternalProject(t)
	migrateMarker := project.marker(t, "postgres-migrate")
	environment := project.postgresEnvironment(t, databaseURL, schema, migrateMarker)
	migrateLeaf := project.runMigrate(t, environment, migrateMarker, nil)
	operatorAssertPostgresCounts(t, databaseURL, schema, 0, 0, 0)
	postgresSchemaBefore := operatorPostgresSchemaSnapshot(t, databaseURL, schema)

	password := operatorRandomPassword(t)
	defer clear(password)
	const username = "external-postgres-operator"
	provisionMarker := project.marker(t, "postgres-provision")
	provisionEnvironment := operatorEnvironment(environment, map[string]string{
		operatorMarkerEnvironment: provisionMarker,
		operatorHoldEnvironment:   "1000",
	})
	operatorAssertSecretFreeEnvironment(t, provisionEnvironment, password)
	provision := project.runProvision(t, provisionEnvironment, provisionMarker, username, password)
	operatorAssertPostgresCredential(t, databaseURL, schema, username, password)
	project.assertArtifactsExclude(t, password)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal("create PostgreSQL operator cookie jar")
	}
	client := operatorHTTPClient(jar)
	t.Cleanup(client.CloseIdleConnections)

	var authentication operatorAuthenticationState
	runtimeAMarker := project.marker(t, "postgres-runtime-a")
	runtimeAEnvironment := operatorEnvironment(environment, map[string]string{
		operatorMarkerEnvironment:       runtimeAMarker,
		operatorRunnerMarkerEnvironment: "",
		operatorHoldEnvironment:         "",
	})
	operatorAssertRuntimeEnvironment(t, runtimeAEnvironment, password)
	runtimeA := project.runRuntime(t, runtimeAEnvironment, runtimeAMarker, password, func(baseURL string) error {
		var phaseErr error
		authentication, phaseErr = operatorExercisePhaseA(client, jar, baseURL, username, password)
		return phaseErr
	})
	operatorAssertPostgresCounts(t, databaseURL, schema, 1, 1, 1)
	operatorAssertPostgresCredential(t, databaseURL, schema, username, password)

	runtimeBMarker := project.marker(t, "postgres-runtime-b")
	runtimeBEnvironment := operatorEnvironment(environment, map[string]string{
		operatorMarkerEnvironment:       runtimeBMarker,
		operatorRunnerMarkerEnvironment: "",
		operatorHoldEnvironment:         "",
	})
	operatorAssertRuntimeEnvironment(t, runtimeBEnvironment, password)
	runtimeB := project.runRuntime(t, runtimeBEnvironment, runtimeBMarker, password, func(baseURL string) error {
		return operatorExercisePhaseB(client, jar, baseURL, authentication)
	})
	operatorAssertPostgresCounts(t, databaseURL, schema, 1, 1, 2)
	operatorAssertPostgresCredential(t, databaseURL, schema, username, password)

	leafPIDs := []int{provision.leafPID, runtimeA.leafPID, runtimeB.leafPID}
	if !operatorDistinctPositivePIDs(leafPIDs...) ||
		operatorContainsPID(leafPIDs, os.Getpid()) || operatorContainsPID(leafPIDs, migrateLeaf) ||
		operatorContainsPID(leafPIDs, provision.globalPID) ||
		operatorContainsPID(leafPIDs, runtimeA.globalPID) ||
		operatorContainsPID(leafPIDs, runtimeB.globalPID) {
		t.Fatalf(
			"PostgreSQL operator leaf identity = provision:%d runtime-a:%d runtime-b:%d migrate:%d globals:%d/%d/%d test:%d",
			provision.leafPID, runtimeA.leafPID, runtimeB.leafPID, migrateLeaf,
			provision.globalPID, runtimeA.globalPID, runtimeB.globalPID, os.Getpid(),
		)
	}
	project.assertArtifactsExclude(t, password, []byte(authentication.sessionCookie), []byte(authentication.csrfCookie))
	project.assertWorkspaceEmpty(t)
	project.assertSourceUnchanged(t)
	rawSecretOccurrences := operatorPostgresRawSecretOccurrences(t, databaseURL, schema, password)
	if rawSecretOccurrences != 0 {
		t.Fatalf("external operator PostgreSQL forbidden secret sink occurrences = %d", rawSecretOccurrences)
	}
	postgresSchemaAfter := operatorPostgresSchemaSnapshot(t, databaseURL, schema)
	postgresSchemaDrift := !bytes.Equal(postgresSchemaBefore, postgresSchemaAfter)
	if postgresSchemaDrift {
		t.Fatal("external operator PostgreSQL physical schema drifted across runtime restart")
	}
	sqliteObserved := operatorRunSQLiteBackendProduct(t)
	sourceAfter, err := operatorattestation.ComputeSourceBinding(repository)
	if err != nil {
		t.Fatal("compute post-run external operator source binding")
	}
	if !sourceBefore.Equal(sourceAfter) {
		t.Fatal("external operator behavioral source changed during PostgreSQL product execution")
	}
	postgresqlObserved := operatorattestation.ObservedFacts{
		Backend:                             operatorattestation.BackendPostgreSQL,
		ProvisionProcesses:                  1,
		RuntimeProcesses:                    2,
		DistinctProcesses:                   int64(len(leafPIDs)),
		ProvisionCalls:                      1,
		CredentialRows:                      1,
		Provisioned:                         true,
		AdminAuthenticated:                  true,
		APIAuthenticated:                    true,
		DistinctProcessRestart:              runtimeA.leafPID != runtimeB.leafPID,
		ProvisionProcessDistinctFromRuntime: provision.leafPID != runtimeA.leafPID && provision.leafPID != runtimeB.leafPID,
		RestartRawSecretInput:               false,
		RestartStateLoss:                    0,
		SchemaDrift:                         postgresSchemaDrift,
		RawSecretOccurrences:                rawSecretOccurrences,
	}
	if capturePath := strings.TrimSpace(os.Getenv(operatorPostgresAttestationCaptureEnvironment)); capturePath != "" {
		if err := operatorattestation.WriteCapture(
			repository,
			capturePath,
			postgresqlObserved,
			sqliteObserved,
			fingerprint,
			sourceAfter,
		); err != nil {
			t.Fatal("write external operator PostgreSQL attestation capture")
		}
	}
}

func operatorPostgresTestURL(t *testing.T) string {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv(operatorPostgresTestURLEnvironment))
	if databaseURL != "" {
		return databaseURL
	}
	if os.Getenv(operatorPostgresRequiredEnvironment) == "1" {
		t.Fatalf("%s=1 requires %s", operatorPostgresRequiredEnvironment, operatorPostgresTestURLEnvironment)
	}
	t.Skip("GODJ_TEST_POSTGRES_URL is not configured; external operator PostgreSQL product E2E was not run")
	return ""
}

func operatorCreatePostgresSchema(
	t *testing.T,
	databaseURL string,
) (string, operatorattestation.PostgreSQLFingerprint) {
	t.Helper()
	sequence := operatorPostgresSchemaSequence.Add(1)
	schema := fmt.Sprintf("godj_operator_%d_%d_%d", os.Getpid(), time.Now().UnixNano(), sequence)
	if len(schema) > 63 {
		t.Fatal("generated operator PostgreSQL schema exceeds its identifier limit")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal("connect external operator PostgreSQL schema owner")
	}
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := connection.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		_ = connection.Close(ctx)
		t.Fatal("create external operator PostgreSQL schema")
	}
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		cleanup, err := pgx.Connect(cleanupContext, databaseURL)
		if err != nil {
			t.Error("connect external operator PostgreSQL cleanup")
			return
		}
		_, dropErr := cleanup.Exec(cleanupContext, "DROP SCHEMA "+quoted+" CASCADE")
		closeErr := cleanup.Close(cleanupContext)
		if errors.Join(dropErr, closeErr) != nil {
			t.Error("drop external operator PostgreSQL schema")
		}
	})
	if _, err := connection.Exec(ctx, "SET TIME ZONE 'UTC'"); err != nil {
		_ = connection.Close(ctx)
		t.Fatal("set external operator PostgreSQL fingerprint timezone")
	}
	var fingerprint operatorattestation.PostgreSQLFingerprint
	err = connection.QueryRow(ctx, `
		SELECT
			current_setting('server_version_num'),
			current_setting('server_encoding'),
			current_setting('client_encoding'),
			"d"."datlocprovider",
			COALESCE("d"."datlocale", '<null>'),
			"d"."datcollate",
			"d"."datctype",
			current_setting('TimeZone'),
			current_setting('standard_conforming_strings'),
			current_setting('synchronous_commit'),
			current_setting('default_transaction_isolation'),
			current_setting('default_transaction_read_only'),
			current_setting('default_transaction_deferrable'),
			current_setting('fsync'),
			current_setting('full_page_writes'),
			current_setting('session_replication_role')
		FROM "pg_catalog"."pg_database" AS "d"
		WHERE "d"."datname" = current_database()
	`).Scan(
		&fingerprint.ServerVersionNum,
		&fingerprint.ServerEncoding,
		&fingerprint.ClientEncoding,
		&fingerprint.LocaleProvider,
		&fingerprint.Locale,
		&fingerprint.Collation,
		&fingerprint.CharacterType,
		&fingerprint.TimeZone,
		&fingerprint.StandardConformingStrings,
		&fingerprint.SynchronousCommit,
		&fingerprint.DefaultIsolation,
		&fingerprint.DefaultReadOnly,
		&fingerprint.DefaultDeferrable,
		&fingerprint.FSync,
		&fingerprint.FullPageWrites,
		&fingerprint.SessionReplicationRole,
	)
	if err != nil {
		_ = connection.Close(ctx)
		t.Fatal("query external operator PostgreSQL fingerprint")
	}
	if err := operatorattestation.ValidatePostgreSQLFingerprint(fingerprint); err != nil {
		_ = connection.Close(ctx)
		t.Fatal("external operator PostgreSQL fingerprint differs from pinned 17.10 profile")
	}
	if err := connection.Close(ctx); err != nil {
		t.Fatal("close external operator PostgreSQL schema owner")
	}
	return schema, fingerprint
}

func operatorAssertPostgresCounts(t *testing.T, databaseURL, schema string, credentials, sessions, articles int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal("connect external operator PostgreSQL inspection")
	}
	defer connection.Close(ctx)
	prefix := pgx.Identifier{schema}.Sanitize() + "."
	queries := []struct {
		name  string
		query string
		want  int
	}{
		{name: "credential", query: `SELECT COUNT(*) FROM ` + prefix + `"godj_system_credential"`, want: credentials},
		{name: "session", query: `SELECT COUNT(*) FROM ` + prefix + `"godj_system_session"`, want: sessions},
		{name: "Article", query: `SELECT COUNT(*) FROM ` + prefix + `"godj_conformance_article"`, want: articles},
	}
	for _, item := range queries {
		var got int
		if err := connection.QueryRow(ctx, item.query).Scan(&got); err != nil {
			t.Fatalf("inspect external operator PostgreSQL %s count", item.name)
		}
		if got != item.want {
			t.Fatalf("external operator PostgreSQL %s count = %d, want %d", item.name, got, item.want)
		}
	}
}

func operatorAssertPostgresCredential(t *testing.T, databaseURL, schema, username string, password []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal("connect external operator PostgreSQL credential inspection")
	}
	defer connection.Close(ctx)
	prefix := pgx.Identifier{schema}.Sanitize() + "."
	var principal, storedUsername, encoded, permissions, digest string
	var active bool
	err = connection.QueryRow(ctx, `SELECT "principal_id", "username", "encoded_password", "active", "permissions", "definition_digest" FROM `+prefix+`"godj_system_credential"`).Scan(
		&principal, &storedUsername, &encoded, &active, &permissions, &digest,
	)
	if err != nil {
		t.Fatal("read external operator PostgreSQL credential")
	}
	if principal != "external-article-operator" || storedUsername != username || encoded == "" || !active || permissions == "" || !strings.HasPrefix(digest, "sha256:") {
		t.Fatal("external operator PostgreSQL credential semantic shape differs")
	}
	if bytes.Equal([]byte(encoded), password) || bytes.Contains([]byte(encoded), password) {
		t.Fatal("external operator PostgreSQL credential retained the raw password")
	}
	rows, err := connection.Query(ctx, `SELECT "digest", "payload" FROM `+prefix+`"godj_system_session"`)
	if err != nil {
		t.Fatal("read external operator PostgreSQL sessions")
	}
	defer rows.Close()
	for rows.Next() {
		var sessionDigest, payload string
		if err := rows.Scan(&sessionDigest, &payload); err != nil {
			t.Fatal("scan external operator PostgreSQL session")
		}
		if bytes.Contains([]byte(sessionDigest+payload), password) {
			t.Fatal("external operator PostgreSQL session retained the raw password")
		}
	}
	if rows.Err() != nil {
		t.Fatal("finish external operator PostgreSQL session inspection")
	}
}
