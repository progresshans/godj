package postgres

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestCompileScalarUsesQualifiedTablesAndNumberedArguments(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	published := query.NewFieldRef("published", "published", query.FieldBoolean, false)
	summary := query.NewFieldRef("summary", "summary", query.FieldString, true)
	in, err := query.NewInCondition(id, []query.Value{query.Integer(3), query.Integer(8)})
	if err != nil {
		t.Fatal(err)
	}
	plan := query.NewPlan("news_article", []query.FieldRef{id, title, published, summary}).
		WithConditions(
			query.NewCondition(summary, query.LookupIsNull, query.Boolean(false)),
			query.NewCondition(title, query.LookupIContains, query.String(`50%_Go\SQL`)),
			in,
			query.NewCondition(published, query.LookupExact, query.Boolean(true)),
		).
		WithOrderings(query.NewOrdering(title, query.Descending), query.NewOrdering(id, query.Ascending))
	plan, err = plan.WithLimit(2)
	if err != nil {
		t.Fatal(err)
	}

	statement, arguments, err := compilePlan("godj_app", plan)
	if err != nil {
		t.Fatalf("compilePlan() error = %v", err)
	}
	wantSQL := `SELECT "id", "title", "published", "summary" FROM "godj_app"."news_article" WHERE "summary" IS NOT NULL AND "title" ILIKE $1 ESCAPE '\' AND "id" IN ($2, $3) AND "published" = $4 ORDER BY "title" DESC, "id" ASC LIMIT $5`
	if statement != wantSQL {
		t.Fatalf("SQL = %q\nwant  %q", statement, wantSQL)
	}
	wantArguments := []any{`%50\%\_Go\\SQL%`, int64(3), int64(8), true, int64(2)}
	if !reflect.DeepEqual(arguments, wantArguments) {
		t.Fatalf("arguments = %#v, want %#v", arguments, wantArguments)
	}
}

func TestCompileOneHopRelationsAndProjection(t *testing.T) {
	t.Parallel()

	authorIdentity := ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}
	postIdentity := ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	authorKey := query.NewFieldRef("author", "author_id", query.FieldInteger, false)
	authorID := query.NewFieldRef("id", "id", query.FieldInteger, false)
	authorName := query.NewFieldRef("name", "name", query.FieldString, false)

	forward, err := query.NewForwardRelationPath(
		postIdentity, "blog_post", "author", "author_id",
		authorIdentity, "authors_author", "id", false, authorName,
	)
	if err != nil {
		t.Fatal(err)
	}
	forwardPlan := query.NewPlan("blog_post", []query.FieldRef{id, title, authorKey}).WithConditions(
		query.NewRelatedCondition(forward, query.LookupExact, query.String("Ada")),
	)
	statement, arguments, err := compilePlan("godj_app", forwardPlan)
	if err != nil {
		t.Fatal(err)
	}
	wantForward := `SELECT "t0"."id", "t0"."title", "t0"."author_id" FROM "godj_app"."blog_post" AS "t0" INNER JOIN "godj_app"."authors_author" AS "t1" ON "t0"."author_id" = "t1"."id" WHERE "t1"."name" = $1`
	if statement != wantForward || !reflect.DeepEqual(arguments, []any{"Ada"}) {
		t.Fatalf("forward = %q %#v", statement, arguments)
	}

	reverse, err := query.NewReverseRelationPath(
		postIdentity, "blog_post", "author", "author_id",
		authorIdentity, "authors_author", "id", "posts", false, title,
	)
	if err != nil {
		t.Fatal(err)
	}
	reversePlan := query.NewPlan("authors_author", []query.FieldRef{authorID, authorName}).WithConditions(
		query.NewRelatedCondition(reverse, query.LookupExact, query.String("Hello")),
	)
	statement, arguments, err = compilePlan("godj_app", reversePlan)
	if err != nil {
		t.Fatal(err)
	}
	wantReverse := `SELECT "t0"."id", "t0"."name" FROM "godj_app"."authors_author" AS "t0" INNER JOIN "godj_app"."blog_post" AS "t1" ON "t0"."id" = "t1"."author_id" WHERE "t1"."title" = $1`
	if statement != wantReverse || !reflect.DeepEqual(arguments, []any{"Hello"}) {
		t.Fatalf("reverse = %q %#v", statement, arguments)
	}

	nullableAuthor := query.NewFieldRef("author", "author_id", query.FieldInteger, true)
	projection, err := query.NewForwardRelationProjection(
		postIdentity, "blog_post", nullableAuthor,
		authorIdentity, "authors_author", authorID,
		[]query.FieldRef{authorID, authorName},
	)
	if err != nil {
		t.Fatal(err)
	}
	projectionPlan, err := query.NewPlan("blog_post", []query.FieldRef{id, title, nullableAuthor}).WithRelationProjection(projection)
	if err != nil {
		t.Fatal(err)
	}
	statement, arguments, err = compilePlan("godj_app", projectionPlan)
	if err != nil {
		t.Fatal(err)
	}
	wantProjection := `SELECT "t0"."id", "t0"."title", "t0"."author_id", "t1"."id", "t1"."name" FROM "godj_app"."blog_post" AS "t0" LEFT OUTER JOIN "godj_app"."authors_author" AS "t1" ON "t0"."author_id" = "t1"."id"`
	if statement != wantProjection || len(arguments) != 0 {
		t.Fatalf("projection = %q %#v", statement, arguments)
	}
}

func TestCompileNullableRelationIsNullTrimsJoin(t *testing.T) {
	t.Parallel()

	post := ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}
	author := ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	authorKey := query.NewFieldRef("author", "author_id", query.FieldInteger, true)
	path, err := query.NewNullableForwardRelationIsNullPath(
		post, "blog_post", authorKey, author, "authors_author", "id",
	)
	if err != nil {
		t.Fatal(err)
	}
	plan := query.NewPlan("blog_post", []query.FieldRef{id, authorKey}).WithConditions(
		query.NewRelatedCondition(path, query.LookupIsNull, query.Boolean(true)),
	)
	statement, arguments, err := compilePlan("godj_app", plan)
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT "t0"."id", "t0"."author_id" FROM "godj_app"."blog_post" AS "t0" WHERE "t0"."author_id" IS NULL`
	if statement != want || len(arguments) != 0 {
		t.Fatalf("nullable isnull = %q %#v", statement, arguments)
	}
}

func TestCompilerRejectsUnsafeIdentifiersBeforeSQL(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	tests := []struct {
		name   string
		schema string
		table  string
	}{
		{name: "empty schema", schema: "", table: "article"},
		{name: "reserved schema", schema: "pg_temp", table: "article"},
		{name: "uppercase schema", schema: "GoDj", table: "article"},
		{name: "quoted table", schema: "godj", table: `article";drop`},
		{name: "long table", schema: "godj", table: strings.Repeat("a", 64)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := compilePlan(test.schema, query.NewPlan(test.table, []query.FieldRef{id}))
			if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
				t.Fatalf("error = %v, want query invalid_plan", err)
			}
		})
	}
}

func TestCompileWritesRequireGeneratedReturningKey(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	published := query.NewFieldRef("published", "published", query.FieldBoolean, false)
	assignments := []query.Assignment{
		query.NewAssignment(title, query.String("Hello")),
		query.NewAssignment(published, query.Boolean(false)),
	}
	statement, arguments, err := compileInsert(
		"godj_app",
		query.NewInsertPlanReturningKey("news_article", assignments, id),
	)
	if err != nil {
		t.Fatal(err)
	}
	want := `INSERT INTO "godj_app"."news_article" ("title", "published") VALUES ($1, $2) RETURNING "id"`
	if statement != want || !reflect.DeepEqual(arguments, []any{"Hello", false}) {
		t.Fatalf("insert = %q %#v", statement, arguments)
	}

	statement, arguments, err = compileInsert(
		"godj_app",
		query.NewInsertPlanReturningKey("only_id", nil, id),
	)
	if err != nil || statement != `INSERT INTO "godj_app"."only_id" DEFAULT VALUES RETURNING "id"` || len(arguments) != 0 {
		t.Fatalf("default insert = %q %#v, error = %v", statement, arguments, err)
	}

	_, _, err = compileInsert("godj_app", query.NewInsertPlan("news_article", assignments))
	assertUnsupported(t, err)
	_, _, err = compileInsert("godj_app", query.NewInsertPlanReturningKey(
		"news_article",
		append([]query.Assignment{query.NewAssignment(id, query.Integer(99))}, assignments...),
		id,
	))
	assertUnsupported(t, err)
	_, _, err = compileInsert("godj_app", query.NewInsertPlanReturningKey(
		"news_article", assignments, query.NewFieldRef("key", "id", query.FieldString, false),
	))
	assertInvalidPlan(t, err)
}

func TestCompileUpdateAndDelete(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	published := query.NewFieldRef("published", "published", query.FieldBoolean, false)
	summary := query.NewFieldRef("summary", "summary", query.FieldString, true)
	updateSQL, updateArguments, err := compileUpdate("godj_app", query.NewUpdatePlan(
		"news_article",
		[]query.Assignment{
			query.NewAssignment(published, query.Boolean(false)),
			query.NewAssignment(summary, query.Null()),
		},
		id,
		query.Integer(9),
	))
	if err != nil {
		t.Fatal(err)
	}
	wantUpdate := `UPDATE "godj_app"."news_article" SET "published" = $1, "summary" = $2 WHERE "id" = $3`
	if updateSQL != wantUpdate || !reflect.DeepEqual(updateArguments, []any{false, nil, int64(9)}) {
		t.Fatalf("update = %q %#v", updateSQL, updateArguments)
	}
	deleteSQL, deleteArguments, err := compileDelete(
		"godj_app",
		query.NewDeletePlan("news_article", id, query.Integer(9)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if deleteSQL != `DELETE FROM "godj_app"."news_article" WHERE "id" = $1` ||
		!reflect.DeepEqual(deleteArguments, []any{int64(9)}) {
		t.Fatalf("delete = %q %#v", deleteSQL, deleteArguments)
	}
}

func TestSQLSTATEClassificationIsStructuredAndConservative(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		pg       *pgconn.PgError
		category string
		code     string
	}{
		{name: "missing table", pg: &pgconn.PgError{Code: sqlStateUndefinedTable}, category: query.CategoryBackend, code: query.CodeMissingTable},
		{name: "required", pg: &pgconn.PgError{Code: sqlStateNotNullViolation, ColumnName: "title"}, category: query.CategoryIntegrity, code: query.CodeRequiredField},
		{name: "insert foreign key", pg: &pgconn.PgError{Code: sqlStateForeignKey, ColumnName: "author_id"}, category: query.CategoryIntegrity, code: query.CodeRelatedObjectMissing},
		{name: "too long", pg: &pgconn.PgError{Code: sqlStateStringTruncation, ColumnName: "title"}, category: query.CategoryField, code: query.CodeInvalidValue},
		{name: "primary key", pg: &pgconn.PgError{Code: sqlStateUniqueViolation, SchemaName: "godj_app", TableName: "article", ConstraintName: "article_pkey"}, category: query.CategoryIntegrity, code: query.CodeUniquePrimaryKey},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := classifyDatabaseError(context.Background(), "insert", "godj_app", "article", test.pg)
			if !errors.Is(err, &query.Error{Category: test.category, Code: test.code}) {
				t.Fatalf("error = %v, want %s/%s", err, test.category, test.code)
			}
			var cause *pgconn.PgError
			if !errors.As(err, &cause) || cause != test.pg {
				t.Fatalf("cause = %p, want %p", cause, test.pg)
			}
		})
	}

	foreignKeyError := &pgconn.PgError{Code: sqlStateForeignKey, ColumnName: "author_id"}
	for _, operation := range []string{"insert", "update"} {
		err := classifyDatabaseError(context.Background(), operation, "godj_app", "article", foreignKeyError)
		if !errors.Is(err, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeRelatedObjectMissing}) {
			t.Fatalf("%s foreign-key error = %v, want integrity/related_object_missing", operation, err)
		}
	}
	deleteError := classifyDatabaseError(context.Background(), "delete", "godj_app", "author", foreignKeyError)
	if !errors.Is(deleteError, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeProtectedForeignKey}) ||
		errors.Is(deleteError, &query.Error{Code: query.CodeRelatedObjectMissing}) {
		t.Fatalf("delete foreign-key error = %v, want only integrity/protected_foreign_key", deleteError)
	}
	queryError := classifyDatabaseError(context.Background(), "query", "godj_app", "article", foreignKeyError)
	if errors.Is(queryError, &query.Error{Code: query.CodeRelatedObjectMissing}) ||
		errors.Is(queryError, &query.Error{Code: query.CodeProtectedForeignKey}) {
		t.Fatalf("non-mutation foreign-key error was over-classified: %v", queryError)
	}

	otherUnique := &pgconn.PgError{
		Code: sqlStateUniqueViolation, SchemaName: "godj_app", TableName: "article", ConstraintName: "article_title_key",
	}
	err := classifyDatabaseError(context.Background(), "insert", "godj_app", "article", otherUnique)
	if errors.Is(err, &query.Error{Code: query.CodeUniquePrimaryKey}) {
		t.Fatalf("non-primary unique error was misclassified: %v", err)
	}
	var cause *pgconn.PgError
	if !errors.As(err, &cause) || cause != otherUnique {
		t.Fatalf("non-primary unique cause = %p, want %p", cause, otherUnique)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := classifyDatabaseError(canceled, "query", "godj_app", "article", &pgconn.PgError{Code: sqlStateQueryCanceled}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled classification = %v", err)
	}
}

func TestMigrationPrimaryKeyViolationClassificationUsesExplicitConstraint(t *testing.T) {
	t.Parallel()

	constraint, err := postgresPrimaryKeyConstraintName("article")
	if err != nil {
		t.Fatal(err)
	}
	postgresError := &pgconn.PgError{
		Code:           sqlStateUniqueViolation,
		SchemaName:     "godj_app",
		TableName:      "article",
		ConstraintName: constraint,
	}
	classified := classifyDatabaseError(context.Background(), "insert", "godj_app", "article", postgresError)
	if !errors.Is(classified, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeUniquePrimaryKey}) {
		t.Fatalf("explicit migration primary-key error = %v", classified)
	}
}

func TestConfigValidationAndConnectionErrorRedaction(t *testing.T) {
	t.Parallel()

	if err := validateConfig(Config{URL: "postgresql://user:secret@localhost/database", Schema: "godj_app"}); err != nil {
		t.Fatalf("valid config error = %v", err)
	}
	invalid := []Config{
		{},
		{URL: "mysql://localhost/database", Schema: "godj_app"},
		{URL: "postgresql://localhost", Schema: "godj_app"},
		{URL: "postgresql://localhost/database", Schema: "pg_catalog"},
		{URL: "postgresql://localhost/database", Schema: "Godj"},
		{URL: "postgresql://localhost/database", Schema: strings.Repeat("s", 64)},
	}
	for _, config := range invalid {
		if err := validateConfig(config); !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
			t.Fatalf("validateConfig(%#v) = %v", config, err)
		}
	}
	redacted := redactConnectionError(errors.New("connect postgresql://user:secret@localhost/database"))
	if strings.Contains(redacted.Error(), "secret") || strings.Contains(redacted.Error(), "user") {
		t.Fatalf("redacted error leaked URL material: %v", redacted)
	}
}

func TestCurrentConnectionConfigOverridesEveryRequiredRuntimeParameter(t *testing.T) {
	t.Parallel()

	connectionConfig, err := currentConnectionConfig(
		"postgresql://localhost/database?application_name=godj-test&client_encoding=LATIN1&default_transaction_deferrable=on&default_transaction_isolation=serializable&default_transaction_read_only=on&search_path=public&standard_conforming_strings=off&synchronous_commit=off&timezone=Asia%2FSeoul",
	)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"client_encoding":                "UTF8",
		"default_transaction_deferrable": "off",
		"default_transaction_isolation":  "read committed",
		"default_transaction_read_only":  "off",
		"search_path":                    "pg_catalog",
		"standard_conforming_strings":    "on",
		"synchronous_commit":             "on",
		"timezone":                       "UTC",
	}
	for parameter, value := range want {
		if got := connectionConfig.RuntimeParams[parameter]; got != value {
			t.Fatalf("RuntimeParams[%q] = %q, want %q", parameter, got, value)
		}
	}
	if _, exists := connectionConfig.RuntimeParams["session_replication_role"]; exists {
		t.Fatal("current connection config sends privileged session_replication_role startup parameter")
	}
	if got := connectionConfig.RuntimeParams["application_name"]; got != "godj-test" {
		t.Fatalf("RuntimeParams[application_name] = %q, want godj-test", got)
	}
}

func TestCurrentConnectionConfigRejectsUnsupportedStartupOverrides(t *testing.T) {
	t.Parallel()

	for _, rawURL := range []string{
		"postgresql://localhost/database?session_replication_role=replica",
		"postgresql://localhost/database?options=-c%20session_replication_role%3Dreplica",
		"postgresql://localhost/database?statement_timeout=1",
	} {
		if _, err := currentConnectionConfig(rawURL); err == nil {
			t.Fatalf("currentConnectionConfig(%q) error = nil", rawURL)
		}
	}
}

func TestCurrentServerProfileValidation(t *testing.T) {
	t.Parallel()

	current := serverProfile{
		versionNumber:                170010,
		timezone:                     "UTC",
		searchPath:                   "pg_catalog",
		clientEncoding:               "UTF8",
		serverEncoding:               "UTF8",
		standardConformingStrings:    "on",
		synchronousCommit:            "on",
		defaultTransactionLevel:      "read committed",
		defaultTransactionReadOnly:   "off",
		defaultTransactionDeferrable: "off",
		fsync:                        "on",
		fullPageWrites:               "on",
		sessionReplicationRole:       "origin",
		databaseEncoding:             "UTF8",
		databaseLocaleProvider:       "c",
		databaseCollation:            "C",
		databaseCType:                "C",
	}
	if err := validateServerProfile(current, true, "godj_app"); err != nil {
		t.Fatalf("current profile error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*serverProfile)
		exists bool
		code   string
	}{
		{name: "wrong major", exists: true, code: query.CodeUnsupported, mutate: func(profile *serverProfile) { profile.versionNumber = 160010 }},
		{name: "timezone", exists: true, code: query.CodeInvalidPlan, mutate: func(profile *serverProfile) { profile.timezone = "Asia/Seoul" }},
		{name: "search path", exists: true, code: query.CodeInvalidPlan, mutate: func(profile *serverProfile) { profile.searchPath = "public" }},
		{name: "client encoding", exists: true, code: query.CodeInvalidPlan, mutate: func(profile *serverProfile) { profile.clientEncoding = "LATIN1" }},
		{name: "server encoding", exists: true, code: query.CodeInvalidPlan, mutate: func(profile *serverProfile) { profile.serverEncoding = "LATIN1" }},
		{name: "standard conforming strings", exists: true, code: query.CodeInvalidPlan, mutate: func(profile *serverProfile) { profile.standardConformingStrings = "off" }},
		{name: "synchronous commit", exists: true, code: query.CodeInvalidPlan, mutate: func(profile *serverProfile) { profile.synchronousCommit = "off" }},
		{name: "default transaction isolation", exists: true, code: query.CodeInvalidPlan, mutate: func(profile *serverProfile) { profile.defaultTransactionLevel = "serializable" }},
		{name: "default transaction read only", exists: true, code: query.CodeInvalidPlan, mutate: func(profile *serverProfile) { profile.defaultTransactionReadOnly = "on" }},
		{name: "default transaction deferrable", exists: true, code: query.CodeInvalidPlan, mutate: func(profile *serverProfile) { profile.defaultTransactionDeferrable = "on" }},
		{name: "fsync", exists: true, code: query.CodeInvalidPlan, mutate: func(profile *serverProfile) { profile.fsync = "off" }},
		{name: "full page writes", exists: true, code: query.CodeInvalidPlan, mutate: func(profile *serverProfile) { profile.fullPageWrites = "off" }},
		{name: "session replication role", exists: true, code: query.CodeInvalidPlan, mutate: func(profile *serverProfile) { profile.sessionReplicationRole = "replica" }},
		{name: "database encoding", exists: true, code: query.CodeInvalidPlan, mutate: func(profile *serverProfile) { profile.databaseEncoding = "LATIN1" }},
		{name: "locale provider", exists: true, code: query.CodeInvalidPlan, mutate: func(profile *serverProfile) { profile.databaseLocaleProvider = "i" }},
		{name: "collation", exists: true, code: query.CodeInvalidPlan, mutate: func(profile *serverProfile) { profile.databaseCollation = "C.UTF-8" }},
		{name: "ctype", exists: true, code: query.CodeInvalidPlan, mutate: func(profile *serverProfile) { profile.databaseCType = "C.UTF-8" }},
		{name: "empty non-null database locale", exists: true, code: query.CodeInvalidPlan, mutate: func(profile *serverProfile) { profile.databaseLocale.Valid = true }},
		{name: "non-empty database locale", exists: true, code: query.CodeInvalidPlan, mutate: func(profile *serverProfile) {
			profile.databaseLocale.Valid = true
			profile.databaseLocale.String = "en-US"
		}},
		{name: "missing schema", exists: false, code: query.CodeInvalidPlan, mutate: func(*serverProfile) {}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			profile := current
			test.mutate(&profile)
			err := validateServerProfile(profile, test.exists, "godj_app")
			if !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: test.code}) {
				t.Fatalf("error = %v, want backend/%s", err, test.code)
			}
		})
	}
}

func assertInvalidPlan(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
		t.Fatalf("error = %v, want query invalid_plan", err)
	}
}

func assertUnsupported(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeUnsupported}) {
		t.Fatalf("error = %v, want backend unsupported_feature", err)
	}
}
