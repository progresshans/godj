package postgresproduct

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/conformance/relationdeleteproduct/authors"
	"github.com/progresshans/godj/conformance/relationdeleteproduct/blog"
	relationproject "github.com/progresshans/godj/conformance/relationdeleteproduct/project"
	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	migrationdefinition "github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

const (
	postgresRelationCanonicalAlphaTitle          = "Canonical Alpha"
	postgresRelationRestartHelperEnv             = "GODJ_POSTGRES_RELATION_RESTART_HELPER"
	postgresRelationRestartSchemaEnv             = "GODJ_POSTGRES_RELATION_RESTART_SCHEMA"
	postgresRelationRestartPostIDEnv             = "GODJ_POSTGRES_RELATION_RESTART_POST_ID"
	postgresRelationRestartAuthorIDEnv           = "GODJ_POSTGRES_RELATION_RESTART_AUTHOR_ID"
	postgresRelationRestartSchemaPrefix          = "godj_pg_relation_"
	postgresRelationRestartSuccessToken          = "godj-postgres-relation-restart-v1-ok"
	postgresRelationRestartFailurePrefix         = "godj-postgres-relation-restart-v1-error:"
	postgresRelationRestartMaximumOutput         = 16 << 10
	postgresRelationRestartHelperTimeout         = 30 * time.Second
	postgresRelationRestartSubprocessTimeout     = 45 * time.Second
	postgresRelationRestartSubprocessTestTimeout = 40 * time.Second
)

var (
	postgresRelationAuthorsKey = migrations.MigrationKey{App: "authors", Name: "0001_initial"}
	postgresRelationBlogKey    = migrations.MigrationKey{App: "blog", Name: "0001_initial"}
)

func TestGeneratedRelationPostgresE2E(t *testing.T) {
	if os.Getenv(postgresRelationRestartHelperEnv) == "1" {
		if failureCode := postgresRelationVerifyRestartedState(); failureCode != "" {
			t.Fatalf("%s%s", postgresRelationRestartFailurePrefix, failureCode)
		}
		if _, err := fmt.Fprintln(os.Stdout, postgresRelationRestartSuccessToken); err != nil {
			t.Fatalf("%swrite_success", postgresRelationRestartFailurePrefix)
		}
		return
	}

	databaseURL := os.Getenv("GODJ_TEST_POSTGRES_URL")
	if strings.TrimSpace(databaseURL) == "" {
		if os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" {
			t.Fatal("GODJ_REQUIRE_POSTGRES=1 requires GODJ_TEST_POSTGRES_URL")
		}
		t.Skip("GODJ_TEST_POSTGRES_URL is not configured; generated relation PostgreSQL E2E was not run")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect generated relation PostgreSQL E2E database: %v", postgresRelationRedactedConnectionError(err))
	}
	schema := fmt.Sprintf("%s%d", postgresRelationRestartSchemaPrefix, time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("create isolated generated relation PostgreSQL schema: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+quotedSchema+" CASCADE"); err != nil {
			t.Errorf("drop isolated generated relation PostgreSQL schema: %v", err)
		}
		if err := admin.Close(cleanupCtx); err != nil {
			t.Errorf("close generated relation PostgreSQL admin connection: %v", err)
		}
	})

	loaded := postgresRelationDefinitions(t)
	migrationBackend, err := postgres.Open(ctx, postgres.Config{URL: databaseURL, Schema: schema})
	if err != nil {
		t.Fatalf("open generated relation PostgreSQL migration backend: %v", err)
	}
	t.Cleanup(func() {
		if err := migrationBackend.Close(); err != nil {
			t.Errorf("close generated relation PostgreSQL migration backend: %v", err)
		}
	})
	state, err := (migrations.Executor{Backend: migrationBackend}).Migrate(
		ctx,
		loaded,
		migrations.LatestLifecycleRequest(),
	)
	if err != nil {
		_ = migrationBackend.Close()
		t.Fatalf("migrate generated relation PostgreSQL definitions: %v", err)
	}
	postgresRelationAssertState(t, state)
	wantHistory := []migrationbackend.AppliedMigration{
		{App: postgresRelationAuthorsKey.App, Name: postgresRelationAuthorsKey.Name},
		{App: postgresRelationBlogKey.App, Name: postgresRelationBlogKey.Name},
	}
	history, err := migrationBackend.ReadAppliedMigrations(ctx)
	if err != nil {
		_ = migrationBackend.Close()
		t.Fatalf("read generated relation PostgreSQL migration history: %v", err)
	}
	if !reflect.DeepEqual(history, wantHistory) {
		_ = migrationBackend.Close()
		t.Fatalf("generated relation PostgreSQL migration history = %v, want %v", history, wantHistory)
	}
	if err := migrationBackend.Close(); err != nil {
		t.Fatalf("close generated relation PostgreSQL backend after migration: %v", err)
	}

	runtimeBackend, err := postgres.Open(ctx, postgres.Config{URL: databaseURL, Schema: schema})
	if err != nil {
		t.Fatalf("reopen generated relation PostgreSQL backend: %v", err)
	}
	t.Cleanup(func() {
		if err := runtimeBackend.Close(); err != nil {
			t.Errorf("close reopened generated relation PostgreSQL backend: %v", err)
		}
	})

	ada, err := authors.AuthorObjects.Create(ctx, runtimeBackend, authors.NewAuthorCreate("Ada"))
	if err != nil {
		t.Fatalf("create Ada through generated PostgreSQL Manager: %v", err)
	}
	bob, err := authors.AuthorObjects.Create(ctx, runtimeBackend, authors.NewAuthorCreate("Bob"))
	if err != nil {
		t.Fatalf("create Bob through generated PostgreSQL Manager: %v", err)
	}
	alpha, err := blog.PostObjects.Create(
		ctx,
		runtimeBackend,
		blog.NewPostCreate("Alpha", ada.ID).WithReviewerID(bob.ID),
	)
	if err != nil {
		t.Fatalf("create related Alpha post through generated PostgreSQL Manager: %v", err)
	}
	beta, err := blog.PostObjects.Create(
		ctx,
		runtimeBackend,
		blog.NewPostCreate("Beta", ada.ID).WithReviewerIDNull(),
	)
	if err != nil {
		t.Fatalf("create nullable-relation Beta post through generated PostgreSQL Manager: %v", err)
	}
	if ada.ID <= 0 || bob.ID <= ada.ID || alpha.ID <= 0 || beta.ID <= alpha.ID ||
		alpha.AuthorID != ada.ID || alpha.ReviewerID == nil || *alpha.ReviewerID != bob.ID ||
		beta.AuthorID != ada.ID || beta.ReviewerID != nil {
		t.Fatalf("generated PostgreSQL relation writes = Ada:%+v Bob:%+v Alpha:%+v Beta:%+v", ada, bob, alpha, beta)
	}

	relations, err := relationproject.BindRelations()
	if err != nil {
		t.Fatalf("bind generated PostgreSQL relation predicates: %v", err)
	}
	models, err := relationproject.Using(runtimeBackend)
	if err != nil {
		t.Fatalf("bind generated relation project facade to PostgreSQL: %v", err)
	}
	posts, err := models.BlogPost.
		Filter(relations.BlogPost.Author.Name.Exact("Ada")).
		OrderBy(blog.PostFields.ID.Asc()).
		All(ctx)
	if err != nil {
		t.Fatalf("query generated PostgreSQL forward relation predicate: %v", err)
	}
	if len(posts) != 2 {
		t.Fatalf("generated PostgreSQL relation predicate returned %d posts, want 2", len(posts))
	}
	postgresRelationAssertPost(t, posts[0], alpha.ID, "Alpha", ada.ID, &bob.ID)
	postgresRelationAssertPost(t, posts[1], beta.ID, "Beta", ada.ID, nil)

	author, err := posts[0].Author(ctx)
	if err != nil {
		t.Fatalf("load generated PostgreSQL required Author wrapper: %v", err)
	}
	postgresRelationAssertAuthor(t, author, ada.ID, "Ada")
	reviewer, present, err := posts[0].Reviewer(ctx)
	if err != nil || !present || reviewer == nil {
		t.Fatalf("load generated PostgreSQL present Reviewer wrapper = (%#v, %t, %v)", reviewer, present, err)
	}
	postgresRelationAssertAuthor(t, reviewer, bob.ID, "Bob")
	if reviewer, present, err := posts[1].Reviewer(ctx); err != nil || present || reviewer != nil {
		t.Fatalf("load generated PostgreSQL absent Reviewer wrapper = (%#v, %t, %v), want (nil, false, nil)", reviewer, present, err)
	}

	authorEager, err := models.BlogPost.
		SelectRelated(models.BlogPost.Related.Author).
		OrderBy(blog.PostFields.ID.Asc()).
		All(ctx)
	if err != nil || len(authorEager) != 2 {
		t.Fatalf("generated PostgreSQL required SelectRelated = (%#v, %v), want two posts", authorEager, err)
	}
	for _, post := range authorEager {
		author, err := post.Author(ctx)
		if err != nil {
			t.Fatalf("read generated PostgreSQL eager Author wrapper: %v", err)
		}
		postgresRelationAssertAuthor(t, author, ada.ID, "Ada")
	}

	reviewerEager, err := models.BlogPost.
		SelectRelated(models.BlogPost.Related.Reviewer).
		OrderBy(blog.PostFields.ID.Asc()).
		All(ctx)
	if err != nil || len(reviewerEager) != 2 {
		t.Fatalf("generated PostgreSQL nullable SelectRelated = (%#v, %v), want two posts", reviewerEager, err)
	}
	reviewer, present, err = reviewerEager[0].Reviewer(ctx)
	if err != nil || !present || reviewer == nil {
		t.Fatalf("generated PostgreSQL eager present Reviewer = (%#v, %t, %v)", reviewer, present, err)
	}
	postgresRelationAssertAuthor(t, reviewer, bob.ID, "Bob")
	if reviewer, present, err := reviewerEager[1].Reviewer(ctx); err != nil || present || reviewer != nil {
		t.Fatalf("generated PostgreSQL eager absent Reviewer = (%#v, %t, %v), want (nil, false, nil)", reviewer, present, err)
	}

	posts[0].Title = "  " + postgresRelationCanonicalAlphaTitle + "  "
	posts[0].NormalizeTitle()
	posts[0].AuthorID = bob.ID
	posts[0].ReviewerID = nil
	if err := posts[0].Save(ctx); err != nil {
		t.Fatalf("save promoted scalar and relation mutations through generated PostgreSQL facade: %v", err)
	}
	if posts[0].Title != postgresRelationCanonicalAlphaTitle {
		t.Fatalf(
			"application-owned NormalizeTitle result = %q, want %q",
			posts[0].Title,
			postgresRelationCanonicalAlphaTitle,
		)
	}
	if err := runtimeBackend.Close(); err != nil {
		t.Fatalf("close generated relation PostgreSQL backend after canonical facade save: %v", err)
	}
	postgresRelationVerifyRestartInSubprocess(t, ctx, schema, alpha.ID, bob.ID)
	runtimeBackend, err = postgres.Open(ctx, postgres.Config{URL: databaseURL, Schema: schema})
	if err != nil {
		t.Fatalf("reopen generated relation PostgreSQL backend after canonical facade save: %v", err)
	}
	freshModels, err := relationproject.Using(runtimeBackend)
	if err != nil {
		t.Fatalf("rebind reopened generated relation PostgreSQL backend: %v", err)
	}
	freshAlpha, found, err := freshModels.BlogPost.
		Filter(blog.PostFields.ID.Exact(alpha.ID)).
		OrderBy(blog.PostFields.ID.Asc()).
		First(ctx)
	if err != nil || !found || freshAlpha == nil {
		t.Fatalf("reload canonical Alpha through reopened PostgreSQL backend = (%#v, %t, %v)", freshAlpha, found, err)
	}
	postgresRelationAssertPost(t, freshAlpha, alpha.ID, postgresRelationCanonicalAlphaTitle, bob.ID, nil)
	freshAuthor, err := freshAlpha.Author(ctx)
	if err != nil {
		t.Fatalf("load reconciled PostgreSQL Author after direct FK mutation: %v", err)
	}
	postgresRelationAssertAuthor(t, freshAuthor, bob.ID, "Bob")
	if freshReviewer, present, err := freshAlpha.Reviewer(ctx); err != nil || present || freshReviewer != nil {
		t.Fatalf("load reconciled absent PostgreSQL Reviewer = (%#v, %t, %v), want (nil, false, nil)", freshReviewer, present, err)
	}
}

func postgresRelationVerifyRestartInSubprocess(
	t *testing.T,
	ctx context.Context,
	schema string,
	postID int64,
	authorID int64,
) {
	t.Helper()
	childContext, cancel := context.WithTimeout(ctx, postgresRelationRestartSubprocessTimeout)
	defer cancel()

	command := exec.CommandContext(
		childContext,
		os.Args[0],
		"-test.run=^TestGeneratedRelationPostgresE2E$",
		"-test.count=1",
		"-test.timeout="+postgresRelationRestartSubprocessTestTimeout.String(),
	)
	command.Env = postgresRelationRestartEnvironment(schema, postID, authorID)
	command.WaitDelay = 5 * time.Second
	stdout := &postgresRelationBoundedOutput{limit: postgresRelationRestartMaximumOutput}
	stderr := &postgresRelationBoundedOutput{limit: postgresRelationRestartMaximumOutput}
	command.Stdout = stdout
	command.Stderr = stderr

	err := command.Run()
	if err != nil {
		t.Fatalf(
			"separate-process PostgreSQL relation restart verification failed: status=%s diagnostic=%s stdout_truncated=%t stderr_truncated=%t",
			postgresRelationRestartProcessStatus(childContext, err),
			postgresRelationRestartDiagnostic(stdout.String(), stderr.String()),
			stdout.truncated,
			stderr.truncated,
		)
	}
	if strings.Count(stdout.String(), postgresRelationRestartSuccessToken) != 1 ||
		strings.Contains(stderr.String(), postgresRelationRestartFailurePrefix) {
		t.Fatalf(
			"separate-process PostgreSQL relation restart verification returned an invalid success protocol: diagnostic=%s stdout_truncated=%t stderr_truncated=%t",
			postgresRelationRestartDiagnostic(stdout.String(), stderr.String()),
			stdout.truncated,
			stderr.truncated,
		)
	}
}

func postgresRelationVerifyRestartedState() (failureCode string) {
	databaseURL := os.Getenv("GODJ_TEST_POSTGRES_URL")
	if strings.TrimSpace(databaseURL) == "" {
		return "invalid_environment"
	}
	schema := os.Getenv(postgresRelationRestartSchemaEnv)
	if !postgresRelationRestartSchemaValid(schema) {
		return "invalid_schema"
	}
	postID, err := strconv.ParseInt(os.Getenv(postgresRelationRestartPostIDEnv), 10, 64)
	if err != nil || postID <= 0 {
		return "invalid_post_id"
	}
	authorID, err := strconv.ParseInt(os.Getenv(postgresRelationRestartAuthorIDEnv), 10, 64)
	if err != nil || authorID <= 0 {
		return "invalid_author_id"
	}

	ctx, cancel := context.WithTimeout(context.Background(), postgresRelationRestartHelperTimeout)
	defer cancel()
	backend, err := postgres.Open(ctx, postgres.Config{URL: databaseURL, Schema: schema})
	if err != nil {
		return "open_backend"
	}
	defer func() {
		if err := backend.Close(); err != nil && failureCode == "" {
			failureCode = "close_backend"
		}
	}()

	models, err := relationproject.Using(backend)
	if err != nil {
		return "bind_facade"
	}
	post, found, err := models.BlogPost.
		Filter(blog.PostFields.ID.Exact(postID)).
		OrderBy(blog.PostFields.ID.Asc()).
		First(ctx)
	if err != nil {
		return "load_post"
	}
	if !found || post == nil {
		return "post_missing"
	}
	rawPost, err := post.Unwrap()
	if err != nil {
		return "unwrap_post"
	}
	if rawPost.ID != postID || rawPost.Title != postgresRelationCanonicalAlphaTitle ||
		rawPost.AuthorID != authorID || rawPost.ReviewerID != nil {
		return "post_state_mismatch"
	}

	author, err := post.Author(ctx)
	if err != nil || author == nil {
		return "load_author"
	}
	rawAuthor, err := author.Unwrap()
	if err != nil {
		return "unwrap_author"
	}
	if rawAuthor.ID != authorID || rawAuthor.Name != "Bob" {
		return "author_state_mismatch"
	}
	reviewer, present, err := post.Reviewer(ctx)
	if err != nil {
		return "load_reviewer"
	}
	if present || reviewer != nil {
		return "reviewer_state_mismatch"
	}
	return ""
}

func postgresRelationRestartEnvironment(schema string, postID int64, authorID int64) []string {
	privateNames := map[string]struct{}{
		postgresRelationRestartHelperEnv:   {},
		postgresRelationRestartSchemaEnv:   {},
		postgresRelationRestartPostIDEnv:   {},
		postgresRelationRestartAuthorIDEnv: {},
	}
	environment := make([]string, 0, len(os.Environ())+len(privateNames))
	for _, entry := range os.Environ() {
		name := entry
		if separator := strings.IndexByte(entry, '='); separator >= 0 {
			name = entry[:separator]
		}
		if _, private := privateNames[name]; private {
			continue
		}
		environment = append(environment, entry)
	}
	return append(
		environment,
		postgresRelationRestartHelperEnv+"=1",
		postgresRelationRestartSchemaEnv+"="+schema,
		postgresRelationRestartPostIDEnv+"="+strconv.FormatInt(postID, 10),
		postgresRelationRestartAuthorIDEnv+"="+strconv.FormatInt(authorID, 10),
	)
}

func postgresRelationRestartSchemaValid(schema string) bool {
	if len(schema) > 63 || !strings.HasPrefix(schema, postgresRelationRestartSchemaPrefix) {
		return false
	}
	suffix := strings.TrimPrefix(schema, postgresRelationRestartSchemaPrefix)
	if len(suffix) < 8 || len(suffix) > 20 {
		return false
	}
	for _, character := range []byte(suffix) {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

type postgresRelationBoundedOutput struct {
	contents  []byte
	limit     int
	truncated bool
}

func (output *postgresRelationBoundedOutput) Write(value []byte) (int, error) {
	written := len(value)
	remaining := output.limit - len(output.contents)
	if remaining > 0 {
		if remaining > len(value) {
			remaining = len(value)
		}
		output.contents = append(output.contents, value[:remaining]...)
	}
	if remaining < len(value) {
		output.truncated = true
	}
	return written, nil
}

func (output *postgresRelationBoundedOutput) String() string {
	return string(output.contents)
}

func postgresRelationRestartProcessStatus(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return "canceled"
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return "exit_" + strconv.Itoa(exitError.ExitCode())
	}
	return "start_failed"
}

func postgresRelationRestartDiagnostic(outputs ...string) string {
	for _, output := range outputs {
		position := strings.Index(output, postgresRelationRestartFailurePrefix)
		if position < 0 {
			continue
		}
		remainder := output[position+len(postgresRelationRestartFailurePrefix):]
		fields := strings.Fields(remainder)
		if len(fields) > 0 && postgresRelationRestartFailureCodeValid(fields[0]) {
			return fields[0]
		}
	}
	return "unstructured_child_output"
}

func postgresRelationRestartFailureCodeValid(code string) bool {
	switch code {
	case "invalid_environment",
		"invalid_schema",
		"invalid_post_id",
		"invalid_author_id",
		"open_backend",
		"bind_facade",
		"load_post",
		"post_missing",
		"unwrap_post",
		"post_state_mismatch",
		"load_author",
		"unwrap_author",
		"author_state_mismatch",
		"load_reviewer",
		"reviewer_state_mismatch",
		"close_backend",
		"write_success":
		return true
	default:
		return false
	}
}

func postgresRelationDefinitions(t *testing.T) migrations.LoadedDefinitionSet {
	t.Helper()
	authorModel := (authors.AuthorDescriptor{}).Metadata()
	postModel := (blog.PostDescriptor{}).Metadata()
	authorsSource := postgresRelationDefinitionSource(
		t,
		postgresRelationAuthorsKey,
		nil,
		postgresRelationCreateModel(postgresRelationAuthorsKey.App, authorModel),
	)
	blogSource := postgresRelationDefinitionSource(
		t,
		postgresRelationBlogKey,
		[]migrations.MigrationKey{postgresRelationAuthorsKey},
		postgresRelationCreateModel(postgresRelationBlogKey.App, postModel),
	)
	loaded, report, err := migrationdefinition.Load(authorsSource, blogSource)
	if err != nil {
		t.Fatalf("load generated relation PostgreSQL definitions: %v", err)
	}
	if report.DocumentsReceived != 2 || report.HeadersValidated != 2 || report.OperationsDecoded != 2 ||
		report.PlannerConstruction != 1 || report.DefinitionsPublished != 2 || report.DefinitionSetsPublished != 1 {
		t.Fatalf("generated relation PostgreSQL definition report = %+v", report)
	}
	return loaded
}

func postgresRelationDefinitionSource(
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
	document, err := json.Marshal(map[string]any{
		"format_version": migrationdefinition.DefinitionFormatVersion,
		"producer": map[string]string{
			"name":    "postgres-generated-relation-e2e",
			"version": "1",
		},
		"migration": map[string]any{
			"app":          key.App,
			"name":         key.Name,
			"dependencies": encodedDependencies,
			"operations":   operations,
		},
	})
	if err != nil {
		t.Fatalf("encode generated relation PostgreSQL definition %s.%s: %v", key.App, key.Name, err)
	}
	return migrationdefinition.Source{
		SourceID: key.App + "/" + key.Name + ".godj.json",
		Document: document,
	}
}

func postgresRelationCreateModel(app string, model ir.Model) map[string]any {
	fields := make([]map[string]any, len(model.Fields))
	for index := range model.Fields {
		fields[index] = postgresRelationField(model.Fields[index])
	}
	return map[string]any{
		"kind":      "create_model",
		"app_label": app,
		"model": map[string]any{
			"name":     model.Name,
			"go_name":  model.GoName,
			"db_table": model.DBTable,
			"fields":   fields,
		},
	}
}

func postgresRelationField(field ir.Field) map[string]any {
	encoded := map[string]any{
		"name":        field.Name,
		"go_name":     field.GoName,
		"column":      field.Column,
		"kind":        string(field.Kind),
		"primary_key": field.PrimaryKey,
		"nullable":    field.Nullable,
		"max_length":  field.MaxLength,
		"default":     field.Default,
	}
	if field.Relation != nil {
		encoded["relation"] = map[string]any{
			"target": map[string]string{
				"app_label":  field.Relation.Target.AppLabel,
				"model_name": field.Relation.Target.ModelName,
			},
			"cardinality": string(field.Relation.Cardinality),
			"reverse": map[string]any{
				"name":     field.Relation.Reverse.Name,
				"disabled": field.Relation.Reverse.Disabled,
			},
			"on_delete": string(field.Relation.OnDelete),
		}
	}
	return encoded
}

func postgresRelationAssertState(t *testing.T, state migrations.ProjectState) {
	t.Helper()
	author, authorExists := state.Model("authors", "author")
	post, postExists := state.Model("blog", "post")
	if !authorExists || !reflect.DeepEqual(author, (authors.AuthorDescriptor{}).Metadata()) ||
		!postExists || !reflect.DeepEqual(post, (blog.PostDescriptor{}).Metadata()) {
		t.Fatalf(
			"generated relation PostgreSQL state = Author:%+v/%t Post:%+v/%t",
			author,
			authorExists,
			post,
			postExists,
		)
	}
}

func postgresRelationAssertPost(
	t *testing.T,
	post *relationproject.BlogPost,
	wantID int64,
	wantTitle string,
	wantAuthorID int64,
	wantReviewerID *int64,
) {
	t.Helper()
	raw, err := post.Unwrap()
	if err != nil {
		t.Fatalf("unwrap generated PostgreSQL Post: %v", err)
	}
	if raw.ID != wantID || raw.Title != wantTitle || raw.AuthorID != wantAuthorID ||
		!reflect.DeepEqual(raw.ReviewerID, wantReviewerID) {
		t.Fatalf(
			"generated PostgreSQL Post = %+v, want ID:%d Title:%q AuthorID:%d ReviewerID:%v",
			raw,
			wantID,
			wantTitle,
			wantAuthorID,
			wantReviewerID,
		)
	}
}

func postgresRelationAssertAuthor(
	t *testing.T,
	author *relationproject.AuthorsAuthor,
	wantID int64,
	wantName string,
) {
	t.Helper()
	raw, err := author.Unwrap()
	if err != nil {
		t.Fatalf("unwrap generated PostgreSQL Author: %v", err)
	}
	if raw.ID != wantID || raw.Name != wantName {
		t.Fatalf("generated PostgreSQL Author = %+v, want ID:%d Name:%q", raw, wantID, wantName)
	}
}

func postgresRelationRedactedConnectionError(err error) error {
	var structured interface{ SQLState() string }
	if errors.As(err, &structured) {
		return fmt.Errorf("PostgreSQL SQLSTATE %s", structured.SQLState())
	}
	return errors.New("PostgreSQL connection failed")
}
