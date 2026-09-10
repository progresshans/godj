package article_test

import (
	"context"
	_ "embed"
	"reflect"
	"testing"

	"github.com/progresshans/godj/db/sqlite"
	articlemodels "github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/migrations"
	migrationdefinition "github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/systemstate"
)

//go:embed migrations/0001_initial.godj.json
var articleProjectInitialDefinition []byte

func TestArticleStableMigrationRootFreshLatestAndReopenNoop(t *testing.T) {
	loaded, report, err := migrationdefinition.Load(
		migrationdefinition.Source{
			SourceID: "migrations/0001_initial.godj.json",
			Document: append([]byte(nil), articleProjectInitialDefinition...),
		},
		systemstate.InitialDefinitionSource(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.DocumentsReceived != 2 || report.HeadersValidated != 2 || report.OperationsDecoded != 4 ||
		report.PlannerConstruction != 1 || report.DefinitionsPublished != 2 || report.DefinitionSetsPublished != 1 {
		t.Fatalf("definition load report = %+v", report)
	}
	if sources := loaded.Sources(); len(sources) != 2 ||
		sources[0].SourceID != "migrations/0001_initial.godj.json" ||
		sources[1].SourceID != "systemstate/godj_system.0001_initial" {
		t.Fatalf("definition sources = %+v", sources)
	}

	ctx := context.Background()
	databasePath := t.TempDir() + "/article.sqlite3"
	firstBackend, err := sqlite.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	firstState, err := (migrations.Executor{Backend: firstBackend}).Migrate(
		ctx,
		loaded,
		migrations.LatestLifecycleRequest(),
	)
	if err != nil {
		_ = firstBackend.Close()
		t.Fatal(err)
	}
	created, err := articlemodels.ArticleObjects.Create(
		ctx,
		firstBackend,
		articlemodels.NewArticleCreate("Explicit migrate survives reopen"),
	)
	if err != nil {
		_ = firstBackend.Close()
		t.Fatal(err)
	}
	if created.ID != 1 {
		_ = firstBackend.Close()
		t.Fatalf("created Article ID = %d, want 1", created.ID)
	}
	if err := firstBackend.Close(); err != nil {
		t.Fatal(err)
	}

	secondBackend, err := sqlite.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := secondBackend.Close(); err != nil {
			t.Errorf("close reopened Article backend: %v", err)
		}
	}()
	secondState, err := (migrations.Executor{Backend: secondBackend}).Migrate(
		ctx,
		loaded,
		migrations.LatestLifecycleRequest(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(secondState, firstState) {
		t.Fatalf("reopened no-op state differs\nfirst=%+v\nsecond=%+v", firstState, secondState)
	}

	articles, err := articlemodels.ArticleObjects.Using(secondBackend).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(articles) != 1 || articles[0].ID != created.ID || articles[0].Title != created.Title {
		t.Fatalf("reopened Articles = %+v, want persisted row %+v", articles, created)
	}
	session, err := secondBackend.OpenRevisionFencedSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	records, readErr := session.ReadAppliedMigrations(ctx)
	closeErr := session.Close(ctx)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if len(records) != 2 || records[0].App != "godj_conformance" || records[0].Name != "0001_initial" ||
		records[1].App != "godj_system" || records[1].Name != "0001_initial" {
		t.Fatalf("applied migration history = %+v", records)
	}
}
