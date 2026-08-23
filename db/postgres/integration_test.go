package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestPostgreSQLPhase1Integration(t *testing.T) {
	databaseURL := postgresIntegrationURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect PostgreSQL integration database: %v", redactConnectionError(err))
	}
	schema := fmt.Sprintf("godj_phase1_%d", time.Now().UnixNano())
	quotedSchema, err := quoteIdentifier(schema)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("create isolated schema: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+quotedSchema+" CASCADE"); err != nil {
			t.Errorf("drop isolated schema: %v", err)
		}
		if err := admin.Close(cleanupCtx); err != nil {
			t.Errorf("close PostgreSQL integration admin connection: %v", err)
		}
	})

	createPhase1Tables(t, ctx, admin, quotedSchema)
	// A caller-supplied replica role would suppress ordinary ForeignKey
	// triggers. It is outside the accepted URL surface and must fail before a
	// backend is published.
	forbiddenBackend, forbiddenErr := Open(ctx, Config{
		URL:    postgresIntegrationURLWithParameter(t, databaseURL, "session_replication_role", "replica"),
		Schema: schema,
	})
	if forbiddenBackend != nil {
		_ = forbiddenBackend.Close()
		t.Fatal("replica-role PostgreSQL URL published a backend")
	}
	if !errors.Is(forbiddenErr, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
		t.Fatalf("replica-role PostgreSQL URL error = %v, want backend invalid plan", forbiddenErr)
	}

	// Prove the current profile does not require SET permission on the SUSET
	// replication-role GUC. This application role owns no schema and receives
	// only ordinary runtime table/sequence privileges.
	role := fmt.Sprintf("godj_pg_app_%d", time.Now().UnixNano())
	quotedRole, err := quoteIdentifier(role)
	if err != nil {
		t.Fatal(err)
	}
	const rolePassword = "godj_test_password_17"
	if _, err := admin.Exec(
		ctx,
		"CREATE ROLE "+quotedRole+" LOGIN PASSWORD '"+rolePassword+"' "+
			"NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION",
	); err != nil {
		t.Fatalf("create least-privilege PostgreSQL integration role: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanupCtx, "DROP OWNED BY "+quotedRole); err != nil {
			t.Errorf("drop least-privilege PostgreSQL integration grants: %v", err)
		}
		if _, err := admin.Exec(cleanupCtx, "DROP ROLE "+quotedRole); err != nil {
			t.Errorf("drop least-privilege PostgreSQL integration role: %v", err)
		}
	})
	for _, statement := range []string{
		"GRANT USAGE ON SCHEMA " + quotedSchema + " TO " + quotedRole,
		"GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA " + quotedSchema + " TO " + quotedRole,
		"GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA " + quotedSchema + " TO " + quotedRole,
	} {
		if _, err := admin.Exec(ctx, statement); err != nil {
			t.Fatalf("grant least-privilege PostgreSQL integration access: %v", err)
		}
	}
	leastPrivilegeBackend, err := Open(ctx, Config{
		URL:    postgresIntegrationURLWithUser(t, databaseURL, role, rolePassword),
		Schema: schema,
	})
	if err != nil {
		t.Fatalf("open least-privilege PostgreSQL backend: %v", err)
	}
	leastRows, err := leastPrivilegeBackend.Query(
		ctx,
		query.NewPlan("authors_author", []query.FieldRef{
			query.NewFieldRef("id", "id", query.FieldInteger, false),
			query.NewFieldRef("name", "name", query.FieldString, false),
		}),
	)
	if err != nil {
		_ = leastPrivilegeBackend.Close()
		t.Fatalf("query through least-privilege PostgreSQL backend: %v", err)
	}
	leastNext := leastRows.Next()
	leastErr := leastRows.Err()
	if leastNext || leastErr != nil {
		_ = leastRows.Close()
		_ = leastPrivilegeBackend.Close()
		t.Fatalf("least-privilege PostgreSQL empty query = next:%t error:%v", leastNext, leastErr)
	}
	if err := leastRows.Close(); err != nil {
		_ = leastPrivilegeBackend.Close()
		t.Fatalf("close least-privilege PostgreSQL rows: %v", err)
	}
	if err := leastPrivilegeBackend.Close(); err != nil {
		t.Fatalf("close least-privilege PostgreSQL backend: %v", err)
	}

	// A server-side role default bypasses URL parsing and reaches AfterConnect.
	// Repeated rejection must close each unpublished physical connection and
	// must not expose the role URL or password through the returned error.
	if _, err := admin.Exec(ctx, "ALTER ROLE "+quotedRole+" SET session_replication_role = replica"); err != nil {
		t.Fatalf("set PostgreSQL integration role drift default: %v", err)
	}
	driftedRoleURL := postgresIntegrationURLWithUser(t, databaseURL, role, rolePassword)
	for attempt := 0; attempt < 4; attempt++ {
		driftedBackend, openErr := Open(ctx, Config{URL: driftedRoleURL, Schema: schema})
		if driftedBackend != nil {
			_ = driftedBackend.Close()
			t.Fatalf("drifted role attempt %d published a backend", attempt)
		}
		if openErr == nil {
			t.Fatalf("drifted role attempt %d error = nil", attempt)
		}
		for _, secret := range []string{role, rolePassword, driftedRoleURL} {
			if strings.Contains(openErr.Error(), secret) {
				t.Fatalf("drifted role attempt %d leaked credential material %q: %v", attempt, secret, openErr)
			}
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		var activeConnections int
		if err := admin.QueryRow(
			ctx,
			"SELECT count(*) FROM pg_catalog.pg_stat_activity WHERE usename = $1",
			role,
		).Scan(&activeConnections); err != nil {
			t.Fatalf("count rejected PostgreSQL physical sessions: %v", err)
		}
		if activeConnections == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("rejected PostgreSQL physical sessions still active = %d", activeConnections)
		}
		time.Sleep(25 * time.Millisecond)
	}
	if _, err := admin.Exec(ctx, "ALTER ROLE "+quotedRole+" RESET session_replication_role"); err != nil {
		t.Fatalf("reset PostgreSQL integration role drift default: %v", err)
	}

	// Role/database defaults are applied by the server at login. The current
	// profile owns durability and transaction defaults and must override weaker
	// role defaults before the connection is published.
	for _, setting := range []string{
		"synchronous_commit = off",
		"default_transaction_isolation = 'serializable'",
		"default_transaction_read_only = on",
		"default_transaction_deferrable = on",
	} {
		if _, err := admin.Exec(ctx, "ALTER ROLE "+quotedRole+" SET "+setting); err != nil {
			t.Fatalf("set PostgreSQL integration role runtime default %q: %v", setting, err)
		}
	}
	durableRoleBackend, err := Open(ctx, Config{URL: driftedRoleURL, Schema: schema})
	if err != nil {
		t.Fatalf("open role with weaker PostgreSQL durability default: %v", err)
	}
	var synchronousCommit, defaultTransactionLevel, defaultTransactionReadOnly, defaultTransactionDeferrable string
	if err := durableRoleBackend.database.QueryRowContext(
		ctx,
		`SELECT current_setting('synchronous_commit'), current_setting('default_transaction_isolation'), `+
			`current_setting('default_transaction_read_only'), current_setting('default_transaction_deferrable')`,
	).Scan(
		&synchronousCommit,
		&defaultTransactionLevel,
		&defaultTransactionReadOnly,
		&defaultTransactionDeferrable,
	); err != nil {
		_ = durableRoleBackend.Close()
		t.Fatalf("read PostgreSQL role runtime overrides: %v", err)
	}
	if synchronousCommit != "on" || defaultTransactionLevel != "read committed" ||
		defaultTransactionReadOnly != "off" || defaultTransactionDeferrable != "off" {
		_ = durableRoleBackend.Close()
		t.Fatalf(
			"PostgreSQL role runtime overrides = %q/%q/%q/%q, want on/read committed/off/off",
			synchronousCommit,
			defaultTransactionLevel,
			defaultTransactionReadOnly,
			defaultTransactionDeferrable,
		)
	}
	if err := durableRoleBackend.Atomic(ctx, func(session db.Session) error {
		transactionSession, ok := session.(*transactionSession)
		if !ok {
			return fmt.Errorf("Atomic session type = %T", session)
		}
		return transactionSession.transaction.QueryRowContext(
			ctx,
			`SELECT current_setting('transaction_isolation'), current_setting('transaction_read_only'), `+
				`current_setting('transaction_deferrable')`,
		).Scan(&defaultTransactionLevel, &defaultTransactionReadOnly, &defaultTransactionDeferrable)
	}); err != nil {
		_ = durableRoleBackend.Close()
		t.Fatalf("run Atomic through weaker PostgreSQL role defaults: %v", err)
	}
	if defaultTransactionLevel != "read committed" || defaultTransactionReadOnly != "off" ||
		defaultTransactionDeferrable != "off" {
		_ = durableRoleBackend.Close()
		t.Fatalf(
			"PostgreSQL Atomic transaction profile = %q/%q/%q, want read committed/off/off",
			defaultTransactionLevel,
			defaultTransactionReadOnly,
			defaultTransactionDeferrable,
		)
	}
	if err := durableRoleBackend.Close(); err != nil {
		t.Fatalf("close PostgreSQL role durability backend: %v", err)
	}
	for _, setting := range []string{
		"synchronous_commit",
		"default_transaction_isolation",
		"default_transaction_read_only",
		"default_transaction_deferrable",
	} {
		if _, err := admin.Exec(ctx, "ALTER ROLE "+quotedRole+" RESET "+setting); err != nil {
			t.Fatalf("reset PostgreSQL integration role runtime default %q: %v", setting, err)
		}
	}

	backend, err := Open(ctx, Config{URL: databaseURL, Schema: schema})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	if backend.profile.versionNumber/10000 != CurrentServerMajor ||
		backend.profile.timezone != "UTC" || backend.profile.searchPath != "pg_catalog" ||
		backend.profile.clientEncoding != "UTF8" || backend.profile.serverEncoding != "UTF8" ||
		backend.profile.standardConformingStrings != "on" || backend.profile.databaseEncoding != "UTF8" ||
		backend.profile.synchronousCommit != "on" || backend.profile.fsync != "on" ||
		backend.profile.defaultTransactionLevel != "read committed" ||
		backend.profile.defaultTransactionReadOnly != "off" ||
		backend.profile.defaultTransactionDeferrable != "off" ||
		backend.profile.fullPageWrites != "on" ||
		backend.profile.sessionReplicationRole != "origin" ||
		backend.profile.databaseLocaleProvider != "c" || backend.profile.databaseLocale.Valid ||
		backend.profile.databaseCollation != "C" || backend.profile.databaseCType != "C" ||
		backend.schema != schema {
		t.Fatalf("profile = %#v schema=%q", backend.profile, backend.schema)
	}

	t.Run("pool reuse discards replication-role drift", func(t *testing.T) {
		backend.database.SetMaxOpenConns(1)
		backend.database.SetMaxIdleConns(1)
		t.Cleanup(func() {
			backend.database.SetMaxOpenConns(0)
			backend.database.SetMaxIdleConns(2)
		})
		connection, err := backend.database.Conn(ctx)
		if err != nil {
			t.Fatalf("acquire PostgreSQL drift canary connection: %v", err)
		}
		if _, err := connection.ExecContext(ctx, "SET session_replication_role = replica"); err != nil {
			_ = connection.Close()
			t.Fatalf("set PostgreSQL drift canary replication role: %v", err)
		}
		if _, err := connection.ExecContext(ctx, "SET synchronous_commit = off"); err != nil {
			_ = connection.Close()
			t.Fatalf("set PostgreSQL drift canary synchronous commit: %v", err)
		}
		for _, statement := range []string{
			"SET default_transaction_isolation = 'serializable'",
			"SET default_transaction_read_only = on",
			"SET default_transaction_deferrable = on",
		} {
			if _, err := connection.ExecContext(ctx, statement); err != nil {
				_ = connection.Close()
				t.Fatalf("set PostgreSQL drift canary transaction default with %q: %v", statement, err)
			}
		}
		if err := connection.Close(); err != nil {
			t.Fatalf("return PostgreSQL drift canary connection: %v", err)
		}
		var role, synchronousCommit string
		var defaultTransactionLevel, defaultTransactionReadOnly, defaultTransactionDeferrable string
		if err := backend.database.QueryRowContext(
			ctx,
			`SELECT current_setting('session_replication_role'), current_setting('synchronous_commit'), `+
				`current_setting('default_transaction_isolation'), current_setting('default_transaction_read_only'), `+
				`current_setting('default_transaction_deferrable')`,
		).Scan(
			&role,
			&synchronousCommit,
			&defaultTransactionLevel,
			&defaultTransactionReadOnly,
			&defaultTransactionDeferrable,
		); err != nil {
			t.Fatalf("replace PostgreSQL drifted pooled connection: %v", err)
		}
		if role != "origin" {
			t.Fatalf("replacement PostgreSQL replication role = %q, want origin", role)
		}
		if synchronousCommit != "on" {
			t.Fatalf("replacement PostgreSQL synchronous commit = %q, want on", synchronousCommit)
		}
		if defaultTransactionLevel != "read committed" || defaultTransactionReadOnly != "off" ||
			defaultTransactionDeferrable != "off" {
			t.Fatalf(
				"replacement PostgreSQL transaction defaults = %q/%q/%q, want read committed/off/off",
				defaultTransactionLevel,
				defaultTransactionReadOnly,
				defaultTransactionDeferrable,
			)
		}
	})

	fields := phase1Fields()
	adaID := integrationInsert(t, ctx, backend, query.NewInsertPlanReturningKey(
		"authors_author",
		[]query.Assignment{query.NewAssignment(fields.authorName, query.String("Ada"))},
		fields.id,
	))
	bobID := integrationInsert(t, ctx, backend, query.NewInsertPlanReturningKey(
		"authors_author",
		[]query.Assignment{query.NewAssignment(fields.authorName, query.String("Bob"))},
		fields.id,
	))
	postID := integrationInsert(t, ctx, backend, phase1PostInsert(fields, "Hello 50%_Go", true, adaID, &bobID))
	secondPostID := integrationInsert(t, ctx, backend, phase1PostInsert(fields, "Second", false, adaID, nil))

	t.Run("composable Boolean expression query", func(t *testing.T) {
		titleMatch, err := query.NewExpression(query.NewCondition(fields.title, query.LookupIContains, query.String("50%_")))
		if err != nil {
			t.Fatal(err)
		}
		summaryMatch, err := query.NewExpression(query.NewCondition(fields.summary, query.LookupIContains, query.String("orm")))
		if err != nil {
			t.Fatal(err)
		}
		either, err := query.OrExpressions(titleMatch, summaryMatch)
		if err != nil {
			t.Fatal(err)
		}
		published, err := query.NewExpression(query.NewCondition(fields.published, query.LookupExact, query.Boolean(true)))
		if err != nil {
			t.Fatal(err)
		}
		excluded, err := query.NewExpression(query.NewCondition(fields.title, query.LookupIContains, query.String("draft")))
		if err != nil {
			t.Fatal(err)
		}
		excluded, err = query.NotExpression(excluded)
		if err != nil {
			t.Fatal(err)
		}
		where, err := query.AndExpressions(either, published, excluded)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := query.NewPlan("blog_post", []query.FieldRef{
			fields.id, fields.title, fields.published, fields.summary, fields.authorKey, fields.editorKey,
		}).WithWhere(where)
		if err != nil {
			t.Fatal(err)
		}
		projection, err := query.NewProjectionResult(fields.id, fields.title)
		if err != nil {
			t.Fatal(err)
		}
		plan, err = plan.WithResultShape(projection)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := backend.Query(ctx, plan)
		if err != nil {
			t.Fatal(err)
		}
		if got := collectTwoColumnIDs(t, rows); len(got) != 1 || got[0] != postID {
			t.Fatalf("Boolean expression IDs = %v, want [%d]", got, postID)
		}

		nullableExact, err := query.NewExpression(query.NewCondition(fields.summary, query.LookupExact, query.String("ORM")))
		if err != nil {
			t.Fatal(err)
		}
		nullableComplement, err := query.NotExpression(nullableExact)
		if err != nil {
			t.Fatal(err)
		}
		nullablePlan, err := query.NewPlan("blog_post", []query.FieldRef{
			fields.id, fields.title, fields.published, fields.summary, fields.authorKey, fields.editorKey,
		}).WithWhere(nullableComplement)
		if err != nil {
			t.Fatal(err)
		}
		nullablePlan, err = nullablePlan.WithResultShape(projection)
		if err != nil {
			t.Fatal(err)
		}
		nullablePlan = nullablePlan.WithOrderings(query.NewOrdering(fields.id, query.Ascending))
		rows, err = backend.Query(ctx, nullablePlan)
		if err != nil {
			t.Fatal(err)
		}
		if got := collectTwoColumnIDs(t, rows); len(got) != 2 || got[0] != postID || got[1] != secondPostID {
			t.Fatalf("nullable NOT IDs = %v, want [%d %d]", got, postID, secondPostID)
		}

		nullableEven, err := query.NotExpression(nullableComplement)
		if err != nil {
			t.Fatal(err)
		}
		nullableEvenPlan, err := query.NewPlan("blog_post", []query.FieldRef{
			fields.id, fields.title, fields.published, fields.summary, fields.authorKey, fields.editorKey,
		}).WithWhere(nullableEven)
		if err != nil {
			t.Fatal(err)
		}
		nullableEvenPlan, err = nullableEvenPlan.WithResultShape(projection)
		if err != nil {
			t.Fatal(err)
		}
		rows, err = backend.Query(ctx, nullableEvenPlan)
		if err != nil {
			t.Fatal(err)
		}
		if got := collectTwoColumnIDs(t, rows); len(got) != 0 {
			t.Fatalf("nullable double-NOT IDs = %v, want []", got)
		}

		nullableTriple, err := query.NotExpression(nullableEven)
		if err != nil {
			t.Fatal(err)
		}
		nullableTriplePlan, err := query.NewPlan("blog_post", []query.FieldRef{
			fields.id, fields.title, fields.published, fields.summary, fields.authorKey, fields.editorKey,
		}).WithWhere(nullableTriple)
		if err != nil {
			t.Fatal(err)
		}
		nullableTriplePlan, err = nullableTriplePlan.WithResultShape(projection)
		if err != nil {
			t.Fatal(err)
		}
		nullableTriplePlan = nullableTriplePlan.WithOrderings(query.NewOrdering(fields.id, query.Ascending))
		rows, err = backend.Query(ctx, nullableTriplePlan)
		if err != nil {
			t.Fatal(err)
		}
		if got := collectTwoColumnIDs(t, rows); len(got) != 2 || got[0] != postID || got[1] != secondPostID {
			t.Fatalf("nullable triple-NOT IDs = %v, want [%d %d]", got, postID, secondPostID)
		}
	})

	t.Run("scalar query update and delete", func(t *testing.T) {
		plan := query.NewPlan("blog_post", []query.FieldRef{
			fields.id, fields.title, fields.published, fields.summary, fields.authorKey, fields.editorKey,
		}).WithConditions(
			query.NewCondition(fields.title, query.LookupIContains, query.String("50%_")),
			query.NewCondition(fields.published, query.LookupExact, query.Boolean(true)),
		).WithOrderings(query.NewOrdering(fields.id, query.Ascending))
		rows, err := backend.Query(ctx, plan)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatalf("query returned no row: %v", rows.Err())
		}
		var gotID, gotAuthor int64
		var gotTitle string
		var gotPublished bool
		var gotSummary sql.NullString
		var gotEditor sql.NullInt64
		if err := rows.Scan(&gotID, &gotTitle, &gotPublished, &gotSummary, &gotAuthor, &gotEditor); err != nil {
			t.Fatal(err)
		}
		if gotID != postID || gotTitle != "Hello 50%_Go" || !gotPublished || gotSummary.Valid ||
			gotAuthor != adaID || !gotEditor.Valid || gotEditor.Int64 != bobID || rows.Next() {
			t.Fatalf("scalar row = id:%d title:%q published:%t summary:%#v author:%d editor:%#v", gotID, gotTitle, gotPublished, gotSummary, gotAuthor, gotEditor)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}

		updated, err := backend.Update(ctx, query.NewUpdatePlan(
			"blog_post",
			[]query.Assignment{query.NewAssignment(fields.summary, query.String("updated"))},
			fields.id,
			query.Integer(postID),
		))
		if err != nil || updated != 1 {
			t.Fatalf("Update() = (%d, %v)", updated, err)
		}
		deleted, err := backend.Delete(ctx, query.NewDeletePlan("blog_post", fields.id, query.Integer(secondPostID)))
		if err != nil || deleted != 1 {
			t.Fatalf("Delete() = (%d, %v)", deleted, err)
		}
	})

	t.Run("forward reverse and eager relation reads", func(t *testing.T) {
		forward, err := query.NewForwardRelationPath(
			ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
			"blog_post", "author", "author_id",
			ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
			"authors_author", "id", false, fields.authorName,
		)
		if err != nil {
			t.Fatal(err)
		}
		forwardRows, err := backend.Query(ctx, query.NewPlan("blog_post", []query.FieldRef{fields.id, fields.title, fields.authorKey}).WithConditions(
			query.NewRelatedCondition(forward, query.LookupExact, query.String("Ada")),
		))
		if err != nil {
			t.Fatal(err)
		}
		if got := collectForwardIDs(t, forwardRows); len(got) != 1 || got[0] != postID {
			t.Fatalf("forward IDs = %v", got)
		}

		reverse, err := query.NewReverseRelationPath(
			ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
			"blog_post", "author", "author_id",
			ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
			"authors_author", "id", "posts", false, fields.title,
		)
		if err != nil {
			t.Fatal(err)
		}
		reverseRows, err := backend.Query(ctx, query.NewPlan("authors_author", []query.FieldRef{fields.id, fields.authorName}).WithConditions(
			query.NewRelatedCondition(reverse, query.LookupExact, query.String("Hello 50%_Go")),
		))
		if err != nil {
			t.Fatal(err)
		}
		if got := collectTwoColumnIDs(t, reverseRows); len(got) != 1 || got[0] != adaID {
			t.Fatalf("reverse IDs = %v", got)
		}

		projection, err := query.NewForwardRelationProjection(
			ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
			"blog_post", fields.editorKey,
			ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
			"authors_author", fields.id,
			[]query.FieldRef{fields.id, fields.authorName},
		)
		if err != nil {
			t.Fatal(err)
		}
		projectionPlan, err := query.NewPlan("blog_post", []query.FieldRef{fields.id, fields.title, fields.editorKey}).
			WithConditions(query.NewCondition(fields.id, query.LookupExact, query.Integer(postID))).
			WithRelationProjection(projection)
		if err != nil {
			t.Fatal(err)
		}
		projectionRows, err := backend.Query(ctx, projectionPlan)
		if err != nil {
			t.Fatal(err)
		}
		defer projectionRows.Close()
		if !projectionRows.Next() {
			t.Fatalf("projection returned no row: %v", projectionRows.Err())
		}
		var rootID int64
		var rootTitle, editorName string
		var editorKey, editorID int64
		if err := projectionRows.Scan(&rootID, &rootTitle, &editorKey, &editorID, &editorName); err != nil {
			t.Fatal(err)
		}
		if rootID != postID || rootTitle != "Hello 50%_Go" || editorKey != bobID || editorID != bobID || editorName != "Bob" {
			t.Fatalf("projection = %d %q %d %d %q", rootID, rootTitle, editorKey, editorID, editorName)
		}
	})

	t.Run("generated key and SQLSTATE failures", func(t *testing.T) {
		_, err := backend.Insert(ctx, query.NewInsertPlan(
			"authors_author",
			[]query.Assignment{query.NewAssignment(fields.authorName, query.String("missing returning"))},
		))
		if !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeUnsupported}) {
			t.Fatalf("missing returning error = %v", err)
		}
		_, err = backend.Insert(ctx, query.NewInsertPlanReturningKey(
			"authors_author",
			[]query.Assignment{
				query.NewAssignment(fields.id, query.Integer(999)),
				query.NewAssignment(fields.authorName, query.String("explicit key")),
			},
			fields.id,
		))
		if !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeUnsupported}) {
			t.Fatalf("explicit key error = %v", err)
		}
		_, err = backend.Insert(ctx, phase1PostInsert(fields, "missing relation", false, 999999, nil))
		if !errors.Is(err, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeRelatedObjectMissing}) {
			t.Fatalf("foreign-key error = %v", err)
		}
	})

	t.Run("atomic lifecycle", func(t *testing.T) {
		var retained db.Session
		committedTitle := "atomic committed"
		if err := backend.Atomic(ctx, func(session db.Session) error {
			retained = session
			_, err := session.Insert(ctx, phase1PostInsert(fields, committedTitle, false, adaID, nil))
			return err
		}); err != nil {
			t.Fatalf("Atomic(commit) error = %v", err)
		}
		if count := integrationTitleCount(t, ctx, backend, fields, committedTitle); count != 1 {
			t.Fatalf("committed count = %d", count)
		}
		if _, err := retained.Insert(ctx, phase1PostInsert(fields, "expired", false, adaID, nil)); !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
			t.Fatalf("expired session error = %v", err)
		}

		rollbackSignal := errors.New("rollback requested")
		rolledBackTitle := "atomic rolled back"
		err := backend.Atomic(ctx, func(session db.Session) error {
			if _, err := session.Insert(ctx, phase1PostInsert(fields, rolledBackTitle, false, adaID, nil)); err != nil {
				return err
			}
			return rollbackSignal
		})
		if !errors.Is(err, rollbackSignal) || integrationTitleCount(t, ctx, backend, fields, rolledBackTitle) != 0 {
			t.Fatalf("rollback error = %v", err)
		}

		atomicCtx, atomicCancel := context.WithCancel(ctx)
		canceledTitle := "atomic canceled"
		err = backend.Atomic(atomicCtx, func(session db.Session) error {
			if _, err := session.Insert(atomicCtx, phase1PostInsert(fields, canceledTitle, false, adaID, nil)); err != nil {
				return err
			}
			atomicCancel()
			return nil
		})
		if !errors.Is(err, context.Canceled) || integrationTitleCount(t, ctx, backend, fields, canceledTitle) != 0 {
			t.Fatalf("canceled atomic error = %v", err)
		}
	})

	t.Run("concurrent queries and close", func(t *testing.T) {
		plan := query.NewPlan("authors_author", []query.FieldRef{fields.id, fields.authorName}).
			WithOrderings(query.NewOrdering(fields.id, query.Ascending))
		const workers = 24
		var wait sync.WaitGroup
		errorsSeen := make(chan error, workers)
		for range workers {
			wait.Add(1)
			go func() {
				defer wait.Done()
				rows, err := backend.Query(ctx, plan)
				if err != nil {
					errorsSeen <- err
					return
				}
				defer rows.Close()
				count := 0
				for rows.Next() {
					var id int64
					var name string
					if err := rows.Scan(&id, &name); err != nil {
						errorsSeen <- err
						return
					}
					count++
				}
				if err := rows.Err(); err != nil {
					errorsSeen <- err
					return
				}
				if count != 2 {
					errorsSeen <- fmt.Errorf("author count = %d", count)
				}
			}()
		}
		wait.Wait()
		close(errorsSeen)
		for err := range errorsSeen {
			t.Errorf("concurrent query: %v", err)
		}
		if err := backend.Close(); err != nil {
			t.Fatal(err)
		}
		if err := backend.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := backend.Query(ctx, plan); !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
			t.Fatalf("post-close query error = %v", err)
		}
	})
}

func postgresIntegrationURLWithParameter(t *testing.T, rawURL, name, value string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse PostgreSQL integration URL: %v", redactConnectionError(err))
	}
	queryValues := parsed.Query()
	queryValues.Set(name, value)
	parsed.RawQuery = queryValues.Encode()
	return parsed.String()
}

func postgresIntegrationURLWithUser(t *testing.T, rawURL, user, password string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse PostgreSQL integration URL: %v", redactConnectionError(err))
	}
	parsed.User = url.UserPassword(user, password)
	return parsed.String()
}

type integrationPhase1Fields struct {
	id         query.FieldRef
	authorName query.FieldRef
	title      query.FieldRef
	published  query.FieldRef
	summary    query.FieldRef
	authorKey  query.FieldRef
	editorKey  query.FieldRef
}

func phase1Fields() integrationPhase1Fields {
	return integrationPhase1Fields{
		id:         query.NewFieldRef("id", "id", query.FieldInteger, false),
		authorName: query.NewFieldRef("name", "name", query.FieldString, false),
		title:      query.NewFieldRef("title", "title", query.FieldString, false),
		published:  query.NewFieldRef("published", "published", query.FieldBoolean, false),
		summary:    query.NewFieldRef("summary", "summary", query.FieldString, true),
		authorKey:  query.NewFieldRef("author", "author_id", query.FieldInteger, false),
		editorKey:  query.NewFieldRef("editor", "editor_id", query.FieldInteger, true),
	}
}

func phase1PostInsert(fields integrationPhase1Fields, title string, published bool, authorID int64, editorID *int64) query.InsertPlan {
	editor := query.Null()
	if editorID != nil {
		editor = query.Integer(*editorID)
	}
	return query.NewInsertPlanReturningKey(
		"blog_post",
		[]query.Assignment{
			query.NewAssignment(fields.title, query.String(title)),
			query.NewAssignment(fields.published, query.Boolean(published)),
			query.NewAssignment(fields.summary, query.Null()),
			query.NewAssignment(fields.authorKey, query.Integer(authorID)),
			query.NewAssignment(fields.editorKey, editor),
		},
		fields.id,
	)
}

func createPhase1Tables(t *testing.T, ctx context.Context, admin *pgx.Conn, schema string) {
	t.Helper()
	statements := []string{
		`CREATE TABLE ` + schema + `."authors_author" (
			"id" BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
			"name" VARCHAR(100) NOT NULL
		)`,
		`CREATE TABLE ` + schema + `."blog_post" (
			"id" BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
			"title" VARCHAR(100) NOT NULL,
			"published" BOOLEAN NOT NULL,
			"summary" VARCHAR(100) NULL,
			"author_id" BIGINT NOT NULL REFERENCES ` + schema + `."authors_author" ("id"),
			"editor_id" BIGINT NULL REFERENCES ` + schema + `."authors_author" ("id")
		)`,
	}
	for _, statement := range statements {
		if _, err := admin.Exec(ctx, statement); err != nil {
			t.Fatalf("create Phase 1 table: %v", err)
		}
	}
}

func integrationInsert(t *testing.T, ctx context.Context, mutator db.Mutator, plan query.InsertPlan) int64 {
	t.Helper()
	identifier, err := mutator.Insert(ctx, plan)
	if err != nil {
		t.Fatalf("Insert() error = %v", err)
	}
	if identifier <= 0 {
		t.Fatalf("Insert() identifier = %d", identifier)
	}
	return identifier
}

func collectForwardIDs(t *testing.T, rows db.Rows) []int64 {
	t.Helper()
	defer rows.Close()
	var identifiers []int64
	for rows.Next() {
		var identifier int64
		var text string
		var relationID int64
		if err := rows.Scan(&identifier, &text, &relationID); err != nil {
			t.Fatal(err)
		}
		identifiers = append(identifiers, identifier)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return identifiers
}

func collectTwoColumnIDs(t *testing.T, rows db.Rows) []int64 {
	t.Helper()
	defer rows.Close()
	var identifiers []int64
	for rows.Next() {
		var identifier int64
		var text string
		if err := rows.Scan(&identifier, &text); err != nil {
			t.Fatal(err)
		}
		identifiers = append(identifiers, identifier)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return identifiers
}

func integrationTitleCount(t *testing.T, ctx context.Context, backend *Backend, fields integrationPhase1Fields, title string) int {
	t.Helper()
	rows, err := backend.Query(ctx, query.NewPlan("blog_post", []query.FieldRef{fields.id, fields.title}).WithConditions(
		query.NewCondition(fields.title, query.LookupExact, query.String(title)),
	))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id int64
		var storedTitle string
		if err := rows.Scan(&id, &storedTitle); err != nil {
			t.Fatal(err)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return count
}

func postgresIntegrationURL(t *testing.T) string {
	t.Helper()
	value := os.Getenv("GODJ_TEST_POSTGRES_URL")
	if value != "" {
		return value
	}
	if os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" {
		t.Fatal("GODJ_REQUIRE_POSTGRES=1 requires GODJ_TEST_POSTGRES_URL")
	}
	t.Skip("GODJ_TEST_POSTGRES_URL is not configured; PostgreSQL integration was not run")
	return ""
}
