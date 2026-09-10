package articleapp_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/articleapp"
)

func openArticleRepository(t *testing.T, name string) (context.Context, *sqlite.Backend, articleapp.Repository) {
	t.Helper()
	ctx := context.Background()
	backend, err := sqlite.OpenMemory(ctx, name)
	if err != nil {
		t.Fatalf("OpenMemory() error = %v", err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	if _, err := backend.ExecContext(ctx, `CREATE TABLE "godj_conformance_article" (
		"id" INTEGER PRIMARY KEY AUTOINCREMENT,
		"title" TEXT NOT NULL,
		"published" INTEGER NOT NULL,
		"summary" TEXT NULL
	)`); err != nil {
		t.Fatalf("create Article table: %v", err)
	}
	repository, err := articleapp.NewRepository(backend)
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	return ctx, backend, repository
}

func TestRepositoryProvidesNeutralArticlePersistenceAndNotFoundMarker(t *testing.T) {
	ctx := context.Background()
	backend, err := sqlite.OpenMemory(ctx, "article-app-repository")
	if err != nil {
		t.Fatalf("OpenMemory() error = %v", err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	if _, err := backend.ExecContext(ctx, `CREATE TABLE "godj_conformance_article" (
		"id" INTEGER PRIMARY KEY AUTOINCREMENT,
		"title" TEXT NOT NULL,
		"published" INTEGER NOT NULL,
		"summary" TEXT NULL
	)`); err != nil {
		t.Fatalf("create Article table: %v", err)
	}

	repository, err := articleapp.NewRepository(backend)
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	summary := "neutral"
	created, err := repository.Create(ctx, articleapp.Input{
		Title:     "Article",
		Published: true,
		Summary:   &summary,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	got, found, err := repository.Get(ctx, created.ID)
	if err != nil || !found || got.ID != created.ID || got.Summary == nil || *got.Summary != summary {
		t.Fatalf("Get() = %#v, found=%t, error=%v", got, found, err)
	}
	if _, err := repository.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := repository.Delete(ctx, created.ID); !errors.Is(err, articleapp.ErrNotFound) || !articleapp.IsCode(err, articleapp.CodeNotFound) {
		t.Fatalf("Delete(missing) error = %v, want neutral not_found", err)
	}
}

func TestNewRepositoryRejectsTypedNilBackend(t *testing.T) {
	var backend *sqlite.Backend
	if _, err := articleapp.NewRepository(backend); !articleapp.IsCode(err, articleapp.CodeInvalidInput) {
		t.Fatalf("NewRepository(typed nil) error = %v, want invalid_input", err)
	}
}

func TestArticlePermissionsRemainAppOwned(t *testing.T) {
	if articleapp.ArticleViewPermission != "godj_conformance.view_article" ||
		articleapp.ArticleAddPermission != "godj_conformance.add_article" ||
		articleapp.ArticleChangePermission != "godj_conformance.change_article" ||
		articleapp.ArticleDeletePermission != "godj_conformance.delete_article" {
		t.Fatal("Article permission constants changed")
	}
}

func TestRepositoryListCombinesClosedFilterSearchOrderingAndPaging(t *testing.T) {
	ctx, _, repository := openArticleRepository(t, "article-app-list-options")
	needleSummary := "needle appears only in the summary"
	first, err := repository.Create(ctx, articleapp.Input{
		Title:     "Alpha",
		Published: true,
		Summary:   &needleSummary,
	})
	if err != nil {
		t.Fatalf("Create(first) error = %v", err)
	}
	second, err := repository.Create(ctx, articleapp.Input{Title: "Needle Beta", Published: true})
	if err != nil {
		t.Fatalf("Create(second) error = %v", err)
	}
	third, err := repository.Create(ctx, articleapp.Input{Title: "Needle Gamma", Published: false})
	if err != nil {
		t.Fatalf("Create(third) error = %v", err)
	}
	fourth, err := repository.Create(ctx, articleapp.Input{Title: "Needle Delta", Published: true})
	if err != nil {
		t.Fatalf("Create(fourth) error = %v", err)
	}

	legacy, err := repository.List(ctx, articleapp.ListOptions{Search: "needle"})
	if err != nil {
		t.Fatalf("List(legacy zero options) error = %v", err)
	}
	if legacy.Total != 4 || len(legacy.Articles) != 4 || legacy.Articles[0].ID != first.ID || legacy.Limit != articleapp.DefaultPageSize {
		t.Fatalf("List(legacy zero options) = %#v, want title+summary, id asc, default limit", legacy)
	}

	page, err := repository.List(ctx, articleapp.ListOptions{
		Search:      "needle",
		Published:   articleapp.PublishedOnly,
		Ordering:    articleapp.IDDescending,
		SearchScope: articleapp.SearchTitleOnly,
		Offset:      1,
		Limit:       1,
	})
	if err != nil {
		t.Fatalf("List(combined options) error = %v", err)
	}
	if page.Total != 2 || len(page.Articles) != 1 || page.Articles[0].ID != second.ID ||
		page.Offset != 1 || page.Limit != 1 {
		t.Fatalf("List(combined options) = %#v, want second matching published row after descending offset", page)
	}
	if fourth.ID <= second.ID {
		t.Fatalf("fixture IDs = fourth %d, second %d, want fourth later", fourth.ID, second.ID)
	}

	unpublished, err := repository.List(ctx, articleapp.ListOptions{
		Search:      "needle",
		Published:   articleapp.UnpublishedOnly,
		SearchScope: articleapp.SearchTitleOnly,
	})
	if err != nil {
		t.Fatalf("List(unpublished) error = %v", err)
	}
	if unpublished.Total != 1 || len(unpublished.Articles) != 1 || unpublished.Articles[0].ID != third.ID {
		t.Fatalf("List(unpublished) = %#v, want ID %d", unpublished, third.ID)
	}
}

func TestRepositoryListRejectsInvalidClosedOptionsAndSearchBounds(t *testing.T) {
	ctx, _, repository := openArticleRepository(t, "article-app-list-validation")
	tests := map[string]articleapp.ListOptions{
		"negative offset":     {Offset: -1},
		"oversized limit":     {Limit: articleapp.MaximumPageSize + 1},
		"search byte bound":   {Search: strings.Repeat("x", articleapp.MaximumSearchBytes+1)},
		"search invalid utf8": {Search: string([]byte{0xff})},
		"search NUL":          {Search: "go\x00dj"},
		"published enum":      {Published: articleapp.PublishedFilter(255)},
		"ordering enum":       {Ordering: articleapp.IDOrdering(255)},
		"search scope enum":   {SearchScope: articleapp.SearchScope(255)},
	}
	for name, options := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := repository.List(ctx, options); !articleapp.IsCode(err, articleapp.CodeInvalidInput) {
				t.Fatalf("List(%#v) error = %v, want invalid_input", options, err)
			}
		})
	}
}

func TestRepositoryPatchDistinguishesSuppliedNullEmptyAndOmittedFields(t *testing.T) {
	ctx, backend, repository := openArticleRepository(t, "article-app-patch")
	initialSummary := "initial summary"
	created, err := repository.Create(ctx, articleapp.Input{
		Title:     "Initial",
		Published: false,
		Summary:   &initialSummary,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	titlePatch := (articleapp.Patch{}).WithTitle("Renamed")
	if !(articleapp.Patch{}).Empty() || titlePatch.Empty() {
		t.Fatal("Patch immutable builder or empty state is incorrect")
	}
	renamed, changed, err := repository.Patch(ctx, created.ID, titlePatch)
	if err != nil {
		t.Fatalf("Patch(title) error = %v", err)
	}
	if fmt.Sprint(changed) != "[title]" || renamed.Title != "Renamed" || renamed.Published ||
		renamed.Summary == nil || *renamed.Summary != initialSummary {
		t.Fatalf("Patch(title) = %#v, changed=%v", renamed, changed)
	}

	nulled, changed, err := repository.Patch(ctx, created.ID, (articleapp.Patch{}).WithSummaryNull())
	if err != nil {
		t.Fatalf("Patch(summary null) error = %v", err)
	}
	if fmt.Sprint(changed) != "[summary]" || nulled.Summary != nil || nulled.Title != "Renamed" {
		t.Fatalf("Patch(summary null) = %#v, changed=%v", nulled, changed)
	}

	empty, changed, err := repository.Patch(ctx, created.ID, (articleapp.Patch{}).WithSummary(""))
	if err != nil {
		t.Fatalf("Patch(empty summary) error = %v", err)
	}
	if fmt.Sprint(changed) != "[summary]" || empty.Summary == nil || *empty.Summary != "" {
		t.Fatalf("Patch(empty summary) = %#v, changed=%v", empty, changed)
	}

	if _, err := backend.ExecContext(ctx, `CREATE TRIGGER reject_article_updates
		BEFORE UPDATE ON "godj_conformance_article"
		BEGIN
			SELECT RAISE(FAIL, 'updates blocked');
		END`); err != nil {
		t.Fatalf("create update rejection trigger: %v", err)
	}
	unchanged, changed, err := repository.Patch(ctx, created.ID, articleapp.Patch{})
	if err != nil {
		t.Fatalf("Patch(empty no-op) error = %v", err)
	}
	if len(changed) != 0 || unchanged.Title != empty.Title || unchanged.Published != empty.Published ||
		unchanged.Summary == nil || *unchanged.Summary != "" {
		t.Fatalf("Patch(empty no-op) = %#v, changed=%v", unchanged, changed)
	}
	if _, err := backend.ExecContext(ctx, `DROP TRIGGER reject_article_updates`); err != nil {
		t.Fatalf("drop update rejection trigger: %v", err)
	}
}

func TestRepositoryPatchRejectsInvalidAndMissingWithoutMutation(t *testing.T) {
	ctx, _, repository := openArticleRepository(t, "article-app-patch-rejection")
	summary := "preserved"
	created, err := repository.Create(ctx, articleapp.Input{Title: "Original", Summary: &summary})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	invalidPatch := (articleapp.Patch{}).
		WithTitle(" \t ").
		WithPublished(true).
		WithSummary(strings.Repeat("x", 201))
	if _, _, err := repository.Patch(ctx, created.ID, invalidPatch); !articleapp.IsCode(err, articleapp.CodeInvalidInput) {
		t.Fatalf("Patch(invalid) error = %v, want invalid_input", err)
	}
	current, found, err := repository.Get(ctx, created.ID)
	if err != nil || !found || current.Title != "Original" || current.Published ||
		current.Summary == nil || *current.Summary != summary {
		t.Fatalf("Get(after invalid patch) = %#v, found=%t, error=%v", current, found, err)
	}

	if _, _, err := repository.Patch(ctx, created.ID+9999, (articleapp.Patch{}).WithTitle("Missing")); !errors.Is(err, articleapp.ErrNotFound) || !articleapp.IsCode(err, articleapp.CodeNotFound) {
		t.Fatalf("Patch(missing) error = %v, want not_found", err)
	}
	current, found, err = repository.Get(ctx, created.ID)
	if err != nil || !found || current.Title != "Original" || current.Published {
		t.Fatalf("Get(after missing patch) = %#v, found=%t, error=%v", current, found, err)
	}
}

func TestRepositoryPatchFailureRollsBackAllChangedFields(t *testing.T) {
	ctx, backend, repository := openArticleRepository(t, "article-app-patch-atomic")
	summary := "before"
	created, err := repository.Create(ctx, articleapp.Input{Title: "Before", Summary: &summary})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := backend.ExecContext(ctx, `CREATE TRIGGER reject_article_updates
		BEFORE UPDATE ON "godj_conformance_article"
		BEGIN
			SELECT RAISE(FAIL, 'forced patch failure');
		END`); err != nil {
		t.Fatalf("create update rejection trigger: %v", err)
	}

	patch := (articleapp.Patch{}).
		WithTitle("After").
		WithPublished(true).
		WithSummary("after")
	if _, _, err := repository.Patch(ctx, created.ID, patch); err == nil {
		t.Fatal("Patch(forced failure) error = nil")
	}
	current, found, err := repository.Get(ctx, created.ID)
	if err != nil || !found || current.Title != "Before" || current.Published ||
		current.Summary == nil || *current.Summary != summary {
		t.Fatalf("Get(after failed patch) = %#v, found=%t, error=%v", current, found, err)
	}
}

func TestRepositoryPatchChangedFieldsUseDeclaredOrder(t *testing.T) {
	ctx, _, repository := openArticleRepository(t, "article-app-patch-order")
	created, err := repository.Create(ctx, articleapp.Input{Title: "Before"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	updated, changed, err := repository.Patch(ctx, created.ID, (articleapp.Patch{}).
		WithSummary("after").
		WithPublished(true).
		WithTitle("After"))
	if err != nil {
		t.Fatalf("Patch() error = %v", err)
	}
	if fmt.Sprint(changed) != "[title published summary]" || updated.Title != "After" ||
		!updated.Published || updated.Summary == nil || *updated.Summary != "after" {
		t.Fatalf("Patch() = %#v, changed=%v", updated, changed)
	}
}

func TestRepositoryMutationHookReceivesDetachedDeterministicResultsAndSkipsNoOps(t *testing.T) {
	ctx, _, repository := openArticleRepository(t, "article-app-mutation-hook")
	var results []articleapp.MutationResult
	hooked := repository.WithMutationHook(func(_ context.Context, _ db.Session, result articleapp.MutationResult) error {
		results = append(results, result.Clone())
		// The callback receives a detached value; attempts to alter it cannot
		// rewrite the repository result or generated model state.
		result.Items[0].Article.Title = "forged"
		if result.Items[0].Article.Summary != nil {
			*result.Items[0].Article.Summary = "forged"
		}
		if len(result.Items[0].ChangedFields) > 0 {
			result.Items[0].ChangedFields[0] = "forged"
		}
		return nil
	})

	summary := "initial"
	created, err := hooked.Create(ctx, articleapp.Input{Title: "First", Summary: &summary})
	if err != nil || created.Title != "First" || created.Summary == nil || *created.Summary != "initial" {
		t.Fatalf("Create() = %#v, %v", created, err)
	}
	updated, changed, err := hooked.Update(ctx, created.ID, articleapp.Input{Title: "Second", Summary: &summary})
	if err != nil || fmt.Sprint(changed) != "[title]" || updated.Title != "Second" {
		t.Fatalf("Update() = %#v, changed=%v, error=%v", updated, changed, err)
	}
	patched, changed, err := hooked.Patch(ctx, created.ID, (articleapp.Patch{}).WithSummary("patched"))
	if err != nil || fmt.Sprint(changed) != "[summary]" || patched.Summary == nil || *patched.Summary != "patched" {
		t.Fatalf("Patch() = %#v, changed=%v, error=%v", patched, changed, err)
	}
	if _, changed, err := hooked.Update(ctx, created.ID, articleapp.Input{Title: patched.Title, Published: patched.Published, Summary: patched.Summary}); err != nil || len(changed) != 0 {
		t.Fatalf("Update(no-op) changed=%v, error=%v", changed, err)
	}
	if _, changed, err := hooked.Patch(ctx, created.ID, articleapp.Patch{}); err != nil || len(changed) != 0 {
		t.Fatalf("Patch(no-op) changed=%v, error=%v", changed, err)
	}

	other, err := repository.Create(ctx, articleapp.Input{Title: "Other"})
	if err != nil {
		t.Fatalf("Create(other) error = %v", err)
	}
	published, err := hooked.Publish(ctx, []int64{other.ID, created.ID, other.ID})
	if err != nil || fmt.Sprint(published.MatchedIDs) != fmt.Sprintf("[%d %d]", created.ID, other.ID) {
		t.Fatalf("Publish() = %#v, %v", published, err)
	}
	deleted, err := hooked.Delete(ctx, created.ID)
	if err != nil || deleted.ID != created.ID || deleted.Title != "Second" {
		t.Fatalf("Delete() = %#v, %v", deleted, err)
	}

	wantOperations := []articleapp.MutationOperation{
		articleapp.MutationCreate,
		articleapp.MutationUpdate,
		articleapp.MutationPatch,
		articleapp.MutationPublish,
		articleapp.MutationDelete,
	}
	if len(results) != len(wantOperations) {
		t.Fatalf("hook calls = %d, want %d: %#v", len(results), len(wantOperations), results)
	}
	for index, want := range wantOperations {
		if results[index].Operation != want {
			t.Fatalf("result[%d].Operation = %q, want %q", index, results[index].Operation, want)
		}
	}
	publishResult := results[3]
	if len(publishResult.Items) != 2 || publishResult.Items[0].Article.ID != created.ID ||
		publishResult.Items[1].Article.ID != other.ID ||
		fmt.Sprint(publishResult.Items[0].ChangedFields) != "[published]" {
		t.Fatalf("publish mutation result = %#v", publishResult)
	}
}

func TestRepositoryMutationHookFailureRollsBackEveryArticleKernel(t *testing.T) {
	ctx, _, repository := openArticleRepository(t, "article-app-mutation-hook-rollback")
	first, err := repository.Create(ctx, articleapp.Input{Title: "First"})
	if err != nil {
		t.Fatalf("Create(first) error = %v", err)
	}
	second, err := repository.Create(ctx, articleapp.Input{Title: "Second"})
	if err != nil {
		t.Fatalf("Create(second) error = %v", err)
	}
	hookFailure := errors.New("forced mutation hook failure")
	hookCalls := 0
	rejected := repository.WithMutationHook(func(context.Context, db.Session, articleapp.MutationResult) error {
		hookCalls++
		return hookFailure
	})

	if _, err := rejected.Create(ctx, articleapp.Input{Title: "Never committed"}); !errors.Is(err, hookFailure) {
		t.Fatalf("Create(hook failure) error = %v", err)
	}
	if _, _, err := rejected.Update(ctx, first.ID, articleapp.Input{Title: "Changed"}); !errors.Is(err, hookFailure) {
		t.Fatalf("Update(hook failure) error = %v", err)
	}
	if _, _, err := rejected.Patch(ctx, first.ID, (articleapp.Patch{}).WithPublished(true)); !errors.Is(err, hookFailure) {
		t.Fatalf("Patch(hook failure) error = %v", err)
	}
	if _, err := rejected.Delete(ctx, first.ID); !errors.Is(err, hookFailure) {
		t.Fatalf("Delete(hook failure) error = %v", err)
	}
	if _, err := rejected.Publish(ctx, []int64{second.ID, first.ID}); !errors.Is(err, hookFailure) {
		t.Fatalf("Publish(hook failure) error = %v", err)
	}
	if _, changed, err := rejected.Update(ctx, first.ID, articleapp.Input{Title: "First"}); err != nil || len(changed) != 0 {
		t.Fatalf("Update(no-op with rejecting hook) changed=%v, error=%v", changed, err)
	}
	if hookCalls != 5 {
		t.Fatalf("hook calls = %d, want 5 mutating kernels and no no-op call", hookCalls)
	}

	page, err := repository.List(ctx, articleapp.ListOptions{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if page.Total != 2 || len(page.Articles) != 2 || page.Articles[0].ID != first.ID ||
		page.Articles[0].Title != "First" || page.Articles[0].Published ||
		page.Articles[1].ID != second.ID || page.Articles[1].Published {
		t.Fatalf("rows after rejected hooks = %#v", page.Articles)
	}
}
