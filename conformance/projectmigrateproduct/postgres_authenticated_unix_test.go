//go:build darwin || linux

package projectmigrateproduct_test

import (
	"context"
	"fmt"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestGlobalMigrateAuthenticatedArticlePostgresRestartDurability(t *testing.T) {
	databaseURL := projectMigratePostgresTestURL(t)
	databaseSecrets := projectMigratePostgresSensitiveValues(t, databaseURL)
	repository := repositoryRoot(t)
	descriptor := filepath.Join(repository, "examples", "article", "godj.toml")
	globalBinary := projectMigratePostgresBuildGlobalGodj(t, repository)
	expectedCatalog := expectedArticleCatalog(t, repository)
	schema := projectMigratePostgresCreateSchema(t, databaseURL)
	workspaceBase := newWorkspaceBase(t)

	const username = "authenticated-postgres-restart-admin"
	password := fmt.Sprintf("authenticated-postgres-restart-password-%d-%d-9Xq", os.Getpid(), time.Now().UnixNano())
	values := environmentMap(projectMigratePostgresEnvironment(t, databaseURL, schema, workspaceBase))
	values[articleAdminUsernameEnv] = username
	values[articleAdminPasswordEnv] = password
	environment := sortedEnvironment(values)
	projectMigratePostgresAssertEnvironment(t, environment, databaseURL, schema)

	sensitive := append(append([]string(nil), databaseSecrets...), schema, password)
	outputCanaries := []string{username}
	artifactRoots := []string{filepath.Dir(globalBinary), workspaceBase}

	migration := runMigrate(t, globalBinary, repository, descriptor, environment)
	projectMigratePostgresAssertVisibleSecretFree(t, migration.Stdout, migration.Stderr, sensitive)
	assertMigrateSuccess(t, migration, expectedCatalog, append(append([]string(nil), sensitive...), outputCanaries...)...)
	assertWorkspaceEmpty(t, workspaceBase)
	projectMigratePostgresAssertLatest(
		t,
		projectMigratePostgresInspect(t, databaseURL, schema),
		expectedCatalog,
		nil,
	)
	authenticatedRestartAssertMigratedSystemStateEmpty(
		t,
		authenticatedRestartInspectPostgres(t, databaseURL, schema),
	)
	projectMigratePostgresAssertStoredValuesSecretFree(t, databaseURL, schema, sensitive)
	projectMigratePostgresAssertArtifactsSecretFree(t, artifactRoots, sensitive)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal("create authenticated PostgreSQL restart cookie jar")
	}
	client := authenticatedRestartHTTPClient(jar)
	t.Cleanup(client.CloseIdleConnections)

	var phaseAState authenticatedRestartPhaseAState
	phaseA := authenticatedRestartRunServer(
		t,
		globalBinary,
		repository,
		descriptor,
		reserveLoopbackAddress(t, ""),
		environment,
		&sensitive,
		outputCanaries,
		func(baseURL string) error {
			var phaseErr error
			phaseAState, phaseErr = authenticatedRestartExercisePhaseA(
				client,
				jar,
				baseURL,
				username,
				password,
				&sensitive,
			)
			return phaseErr
		},
	)
	assertWorkspaceEmpty(t, workspaceBase)
	phaseASnapshot := authenticatedRestartInspectPostgres(t, databaseURL, schema)
	authenticatedRestartAssertPhaseAState(t, phaseASnapshot, username, password, phaseAState, sensitive)
	projectMigratePostgresAssertStoredValuesSecretFree(t, databaseURL, schema, sensitive)
	projectMigratePostgresAssertArtifactsSecretFree(t, artifactRoots, sensitive)

	phaseB := authenticatedRestartRunServer(
		t,
		globalBinary,
		repository,
		descriptor,
		reserveLoopbackAddress(t, ""),
		environment,
		&sensitive,
		outputCanaries,
		func(baseURL string) error {
			return authenticatedRestartExercisePhaseB(client, jar, baseURL, phaseAState, &sensitive)
		},
	)
	assertWorkspaceEmpty(t, workspaceBase)
	if phaseA.PID <= 0 || phaseB.PID <= 0 || phaseA.PID == phaseB.PID ||
		phaseA.PID == os.Getpid() || phaseB.PID == os.Getpid() {
		t.Fatalf(
			"authenticated PostgreSQL restart process identity = phase-a-valid:%t phase-b-valid:%t distinct:%t external:%t",
			phaseA.PID > 0,
			phaseB.PID > 0,
			phaseA.PID != phaseB.PID,
			phaseA.PID != os.Getpid() && phaseB.PID != os.Getpid(),
		)
	}
	phaseBSnapshot := authenticatedRestartInspectPostgres(t, databaseURL, schema)
	authenticatedRestartAssertCredentialUnchanged(t, phaseASnapshot.Credential, phaseBSnapshot.Credential)
	authenticatedRestartAssertPhaseBState(t, phaseBSnapshot, username, password, phaseAState, sensitive)
	projectMigratePostgresAssertStoredValuesSecretFree(t, databaseURL, schema, sensitive)
	projectMigratePostgresAssertArtifactsSecretFree(t, artifactRoots, sensitive)
}

func authenticatedRestartInspectPostgres(
	t *testing.T,
	databaseURL, schema string,
) authenticatedRestartDatabaseSnapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect authenticated restart PostgreSQL inspection: %v", projectMigratePostgresSafeError(err))
	}
	defer func() {
		if err := connection.Close(ctx); err != nil {
			t.Errorf("close authenticated restart PostgreSQL inspection: %v", projectMigratePostgresSafeError(err))
		}
	}()

	var snapshot authenticatedRestartDatabaseSnapshot
	articleRows, err := connection.Query(
		ctx,
		`SELECT "id", "title", "published", "summary" FROM `+
			pgx.Identifier{schema, "godj_conformance_article"}.Sanitize()+` ORDER BY "id"`,
	)
	if err != nil {
		t.Fatalf("query authenticated restart PostgreSQL Articles: %v", projectMigratePostgresSafeError(err))
	}
	for articleRows.Next() {
		var row authenticatedRestartPersistedArticle
		if err := articleRows.Scan(&row.ID, &row.Title, &row.Published, &row.Summary); err != nil {
			articleRows.Close()
			t.Fatalf("scan authenticated restart PostgreSQL Article: %v", projectMigratePostgresSafeError(err))
		}
		snapshot.Articles = append(snapshot.Articles, row)
	}
	if err := projectMigratePostgresCloseRows(articleRows); err != nil {
		t.Fatalf("finish authenticated restart PostgreSQL Article rows: %v", projectMigratePostgresSafeError(err))
	}

	sessionRows, err := connection.Query(
		ctx,
		`SELECT "digest", "payload" FROM `+
			pgx.Identifier{schema, "godj_system_session"}.Sanitize()+` ORDER BY "id"`,
	)
	if err != nil {
		t.Fatalf("query authenticated restart PostgreSQL sessions: %v", projectMigratePostgresSafeError(err))
	}
	for sessionRows.Next() {
		var row authenticatedRestartSessionRow
		if err := sessionRows.Scan(&row.Digest, &row.Payload); err != nil {
			sessionRows.Close()
			t.Fatalf("scan authenticated restart PostgreSQL session: %v", projectMigratePostgresSafeError(err))
		}
		snapshot.Sessions = append(snapshot.Sessions, row)
	}
	if err := projectMigratePostgresCloseRows(sessionRows); err != nil {
		t.Fatalf("finish authenticated restart PostgreSQL session rows: %v", projectMigratePostgresSafeError(err))
	}

	auditRows, err := connection.Query(
		ctx,
		`SELECT "id", "actor_id", "model", "object_id", "action", "changed_fields", "display_label" FROM `+
			pgx.Identifier{schema, "godj_system_audit"}.Sanitize()+` ORDER BY "id"`,
	)
	if err != nil {
		t.Fatalf("query authenticated restart PostgreSQL audit: %v", projectMigratePostgresSafeError(err))
	}
	for auditRows.Next() {
		var row authenticatedRestartAuditRow
		if err := auditRows.Scan(
			&row.Sequence,
			&row.ActorID,
			&row.Model,
			&row.ObjectID,
			&row.Action,
			&row.ChangedFields,
			&row.DisplayLabel,
		); err != nil {
			auditRows.Close()
			t.Fatalf("scan authenticated restart PostgreSQL audit: %v", projectMigratePostgresSafeError(err))
		}
		snapshot.Audits = append(snapshot.Audits, row)
	}
	if err := projectMigratePostgresCloseRows(auditRows); err != nil {
		t.Fatalf("finish authenticated restart PostgreSQL audit rows: %v", projectMigratePostgresSafeError(err))
	}

	credentialRows, err := connection.Query(
		ctx,
		`SELECT "id", "principal_id", "username", "encoded_password", "active", "permissions", "definition_digest" FROM `+
			pgx.Identifier{schema, "godj_system_credential"}.Sanitize()+` ORDER BY "id"`,
	)
	if err != nil {
		t.Fatalf("query authenticated restart PostgreSQL credential: %v", projectMigratePostgresSafeError(err))
	}
	for credentialRows.Next() {
		var row authenticatedRestartCredentialRow
		if err := credentialRows.Scan(
			&row.ID,
			&row.PrincipalID,
			&row.Username,
			&row.EncodedPassword,
			&row.Active,
			&row.Permissions,
			&row.DefinitionDigest,
		); err != nil {
			credentialRows.Close()
			t.Fatalf("scan authenticated restart PostgreSQL credential: %v", projectMigratePostgresSafeError(err))
		}
		snapshot.Credential = append(snapshot.Credential, row)
	}
	if err := projectMigratePostgresCloseRows(credentialRows); err != nil {
		t.Fatalf("finish authenticated restart PostgreSQL credential rows: %v", projectMigratePostgresSafeError(err))
	}
	return snapshot
}
