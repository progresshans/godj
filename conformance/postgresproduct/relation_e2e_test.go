package postgresproduct

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
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

var (
	postgresRelationAuthorsKey = migrations.MigrationKey{App: "authors", Name: "0001_initial"}
	postgresRelationBlogKey    = migrations.MigrationKey{App: "blog", Name: "0001_initial"}
)

func TestGeneratedRelationPostgresE2E(t *testing.T) {
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
	schema := fmt.Sprintf("godj_pg_relation_%d", time.Now().UnixNano())
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
