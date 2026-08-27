package relationdeleteproduct

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/conformance/relationdeleteproduct/authors"
	"github.com/progresshans/godj/conformance/relationdeleteproduct/blog"
	"github.com/progresshans/godj/conformance/relationdeleteproduct/fixture"
	"github.com/progresshans/godj/conformance/relationdeleteproduct/project"
	"github.com/progresshans/godj/db"
	projectgenerateprotocol "github.com/progresshans/godj/internal/projectgenerate/protocol"
	"github.com/progresshans/godj/query"
	modernsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const relationDeletePolicyDigest = "eb6914dc35eb53e3df8c392f7a6dac52dc81f9bfd00910adf5fda3bcf99c9a58"

var relationDeleteBundleRoster = []string{
	"authors/zz_godj_generated.go",
	"authors/zz_godj_relation.go",
	"authors/zz_godj_relation_object.go",
	"authors/zz_godj_relation_projection.go",
	"blog/zz_godj_generated.go",
	"blog/zz_godj_relation.go",
	"blog/zz_godj_relation_object.go",
	"blog/zz_godj_relation_projection.go",
	"project/zz_godj_bindings.go",
	"project/zz_godj_relation_delete.go",
	"project/zz_godj_relation_facade.go",
	"project/zz_godj_relation_object.go",
	"project/zz_godj_relation_prefetch.go",
	"project/zz_godj_relation_query.go",
	"project/zz_godj_relation_reverse.go",
	"project/zz_godj_relation_select_related.go",
}

var (
	facadeBackendInvalidPlan = &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}
	facadeQueryInvalidPlan   = &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}
	facadeUnexpectedIO       = errors.New("unexpected project facade backend I/O")
)

type facadeMinimalBackend struct {
	calls int
}

var _ project.Backend = (*facadeMinimalBackend)(nil)

func (backend *facadeMinimalBackend) Query(context.Context, query.Plan) (db.Rows, error) {
	backend.calls++
	return nil, facadeUnexpectedIO
}

func (backend *facadeMinimalBackend) Insert(context.Context, query.InsertPlan) (int64, error) {
	backend.calls++
	return 0, facadeUnexpectedIO
}

func (backend *facadeMinimalBackend) Update(context.Context, query.UpdatePlan) (int64, error) {
	backend.calls++
	return 0, facadeUnexpectedIO
}

func (backend *facadeMinimalBackend) Delete(context.Context, query.DeletePlan) (int64, error) {
	backend.calls++
	return 0, facadeUnexpectedIO
}

func TestCheckedInGeneratedRelationDeleteProjectRegeneratesExactBundle(t *testing.T) {
	t.Parallel()

	spec, err := fixture.ProjectSpec(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	files := bundle.Files()
	if len(files) != 16 {
		t.Fatalf("generated bundle file count = %d, want exact 16", len(files))
	}
	gotFiles := make([]string, len(files))
	for index := range files {
		gotFiles[index] = files[index].Path
	}
	if !reflect.DeepEqual(gotFiles, relationDeleteBundleRoster) {
		t.Fatalf("generated bundle roster = %#v, want exact 16 %#v", gotFiles, relationDeleteBundleRoster)
	}

	root := relationDeleteProductDirectory(t)
	for _, file := range files {
		path := filepath.Join(root, filepath.FromSlash(file.Path))
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(contents, file.Source()) {
			t.Fatalf("checked-in generated file %s differs from project bundle", file.Path)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != file.Mode.Perm() {
			t.Fatalf("checked-in generated file %s mode = %v, want regular %v", file.Path, info.Mode(), file.Mode.Perm())
		}
	}
	checkedManifest, err := os.ReadFile(filepath.Join(root, codegen.GeneratedManifestPath))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(checkedManifest, bundle.Manifest()) {
		t.Fatal("checked-in generated manifest differs from project bundle")
	}

	regenerated, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	assertRelationDeleteBundlesEqual(t, bundle, regenerated)
	reorderedSpec := spec
	reorderedSpec.Apps = slices.Clone(spec.Apps)
	reorderedSpec.Apps[0], reorderedSpec.Apps[1] = reorderedSpec.Apps[1], reorderedSpec.Apps[0]
	reordered, err := codegen.GenerateProject(reorderedSpec)
	if err != nil {
		t.Fatal(err)
	}
	assertRelationDeleteBundlesEqual(t, bundle, reordered)

	mutableFiles := bundle.Files()
	originalFirst := files[0].Source()
	mutableFiles[0].Path = "mutated"
	mutableSource := mutableFiles[0].Source()
	mutableSource[0] ^= 0xff
	mutableManifest := bundle.Manifest()
	mutableManifest[0] ^= 0xff
	freshFiles := bundle.Files()
	if freshFiles[0].Path != relationDeleteBundleRoster[0] || !bytes.Equal(freshFiles[0].Source(), originalFirst) || !bytes.Equal(bundle.Manifest(), checkedManifest) {
		t.Fatal("GeneratedBundle retained caller mutation")
	}

	selectRelatedSource, err := os.ReadFile(filepath.Join(root, "project", "zz_godj_relation_select_related.go"))
	if err != nil {
		t.Fatal(err)
	}
	if project.GoDjProjectRelationSelectRelatedGeneratorVersion != codegen.ProjectRelationSelectRelatedGeneratorVersion {
		t.Fatalf("checked-in relation-delete select-related generator version = %q, want %q", project.GoDjProjectRelationSelectRelatedGeneratorVersion, codegen.ProjectRelationSelectRelatedGeneratorVersion)
	}
	if count := bytes.Count(selectRelatedSource, []byte("configurationErr error")); count != 2 {
		t.Fatalf("relation-delete typed select-related private configuration error fields = %d, want exact 2", count)
	}
	var checkedFiles []string
	for _, directory := range []string{"authors", "blog", "project"} {
		entries, err := os.ReadDir(filepath.Join(root, directory))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasPrefix(entry.Name(), "zz_godj_") && strings.HasSuffix(entry.Name(), ".go") {
				checkedFiles = append(checkedFiles, directory+"/"+entry.Name())
			}
		}
	}
	slices.Sort(checkedFiles)
	if !reflect.DeepEqual(checkedFiles, relationDeleteBundleRoster) {
		t.Fatalf("checked-in generated file inventory = %#v, want exact sixteen %#v", checkedFiles, relationDeleteBundleRoster)
	}

	deleteCandidate := bundleFileSource(t, bundle, "project/zz_godj_relation_delete.go")
	if !bytes.Contains(deleteCandidate, []byte(relationDeletePolicyDigest)) {
		t.Fatalf("generated relation-delete aggregate omits exact policy digest %s", relationDeletePolicyDigest)
	}
	if project.GoDjProjectRelationDeleteGeneratorVersion != codegen.ProjectRelationDeleteGeneratorVersion {
		t.Fatalf(
			"checked-in generator version = %q, want %q",
			project.GoDjProjectRelationDeleteGeneratorVersion,
			codegen.ProjectRelationDeleteGeneratorVersion,
		)
	}
	if _, err := project.BindRelationDeleters(); err != nil {
		t.Fatalf("BindRelationDeleters() error = %v", err)
	}
	if project.GoDjProjectRelationFacadeGeneratorVersion != codegen.ProjectRelationFacadeGeneratorVersion {
		t.Fatalf(
			"checked-in relation-facade generator version = %q, want %q",
			project.GoDjProjectRelationFacadeGeneratorVersion,
			codegen.ProjectRelationFacadeGeneratorVersion,
		)
	}
}

func TestProjectFacadeLazyWrappersCacheAndUnwrap(t *testing.T) {
	t.Parallel()

	ctx, product := openProvisionedFacadeFixture(t)
	models, err := project.Using(product.backend)
	if err != nil {
		t.Fatalf("project.Using() error = %v", err)
	}

	before := product.backend.QueryCount()
	author, found, err := models.AuthorsAuthor.
		OrderBy(authors.AuthorFields.ID.Asc()).
		First(ctx)
	if err != nil || !found {
		t.Fatalf("AuthorsAuthor.First() = (%#v, %t, %v), want found", author, found, err)
	}
	assertFacadeQueryDelta(t, product, before, 1, "AuthorsAuthor.First")
	var queriedAuthor *project.AuthorsAuthor = author
	rawAuthor, err := queriedAuthor.Unwrap()
	if err != nil || rawAuthor.ID != 1 || rawAuthor.Name != "Ada" {
		t.Fatalf("AuthorsAuthor.Unwrap() = (%#v, %v), want Ada", rawAuthor, err)
	}
	rawAuthor.Name = "changed clone"
	rawAuthorAgain, err := queriedAuthor.Unwrap()
	if err != nil || rawAuthorAgain.Name != "Ada" {
		t.Fatalf("AuthorsAuthor second Unwrap() = (%#v, %v), want independent Ada clone", rawAuthorAgain, err)
	}
	assertFacadeQueryDelta(t, product, before, 1, "AuthorsAuthor.Unwrap clone")

	before = product.backend.QueryCount()
	post, found, err := models.BlogPost.
		Filter(blog.PostFields.ID.Exact(10)).
		OrderBy(blog.PostFields.ID.Asc()).
		First(ctx)
	if err != nil || !found {
		t.Fatalf("BlogPost.First(id=10) = (%#v, %t, %v), want found", post, found, err)
	}
	assertFacadeQueryDelta(t, product, before, 1, "BlogPost.First")
	var queriedPost *project.BlogPost = post
	rawPost, err := queriedPost.Unwrap()
	if err != nil || rawPost.ID != 10 || rawPost.Title != "Alpha" || rawPost.ReviewerID == nil || *rawPost.ReviewerID != 2 {
		t.Fatalf("BlogPost.Unwrap() = (%#v, %v), want post 10", rawPost, err)
	}
	rawPost.Title = "changed clone"
	*rawPost.ReviewerID = 99
	rawPostAgain, err := queriedPost.Unwrap()
	if err != nil || rawPostAgain.Title != "Alpha" || rawPostAgain.ReviewerID == nil || *rawPostAgain.ReviewerID != 2 {
		t.Fatalf("BlogPost second Unwrap() = (%#v, %v), want independent post clone", rawPostAgain, err)
	}

	before = product.backend.QueryCount()
	relatedAuthor, err := post.Author(ctx)
	if err != nil {
		t.Fatalf("BlogPost.Author() error = %v", err)
	}
	assertFacadeQueryDelta(t, product, before, 1, "cold BlogPost.Author")
	var sameTargetType *project.AuthorsAuthor = relatedAuthor
	relatedRaw, err := sameTargetType.Unwrap()
	if err != nil || relatedRaw.ID != 1 || relatedRaw.Name != "Ada" {
		t.Fatalf("BlogPost.Author().Unwrap() = (%#v, %v), want Ada", relatedRaw, err)
	}
	before = product.backend.QueryCount()
	relatedAuthorAgain, err := post.Author(ctx)
	if err != nil {
		t.Fatalf("warm BlogPost.Author() error = %v", err)
	}
	relatedRawAgain, err := relatedAuthorAgain.Unwrap()
	if err != nil || relatedRawAgain.ID != 1 {
		t.Fatalf("warm BlogPost.Author().Unwrap() = (%#v, %v), want author 1", relatedRawAgain, err)
	}
	assertFacadeQueryDelta(t, product, before, 0, "warm BlogPost.Author")

	before = product.backend.QueryCount()
	reviewer, present, err := post.Reviewer(ctx)
	if err != nil || !present || reviewer == nil {
		t.Fatalf("BlogPost.Reviewer() = (%#v, %t, %v), want present", reviewer, present, err)
	}
	assertFacadeQueryDelta(t, product, before, 1, "cold present BlogPost.Reviewer")
	var reviewerTarget *project.AuthorsAuthor = reviewer
	reviewerRaw, err := reviewerTarget.Unwrap()
	if err != nil || reviewerRaw.ID != 2 || reviewerRaw.Name != "Bob" {
		t.Fatalf("BlogPost.Reviewer().Unwrap() = (%#v, %v), want Bob", reviewerRaw, err)
	}
	before = product.backend.QueryCount()
	if _, present, err := post.Reviewer(ctx); err != nil || !present {
		t.Fatalf("warm BlogPost.Reviewer() = (present %t, %v), want present", present, err)
	}
	assertFacadeQueryDelta(t, product, before, 0, "warm present BlogPost.Reviewer")

	before = product.backend.QueryCount()
	nullablePost, found, err := models.BlogPost.
		Filter(blog.PostFields.ID.Exact(11)).
		OrderBy(blog.PostFields.ID.Asc()).
		First(ctx)
	if err != nil || !found {
		t.Fatalf("BlogPost.First(id=11) = (%#v, %t, %v), want found", nullablePost, found, err)
	}
	assertFacadeQueryDelta(t, product, before, 1, "nullable BlogPost.First")
	before = product.backend.QueryCount()
	if reviewer, present, err := nullablePost.Reviewer(ctx); err != nil || present || reviewer != nil {
		t.Fatalf("nullable BlogPost.Reviewer() = (%#v, %t, %v), want (nil, false, nil)", reviewer, present, err)
	}
	if reviewer, present, err := nullablePost.Reviewer(ctx); err != nil || present || reviewer != nil {
		t.Fatalf("warm nullable BlogPost.Reviewer() = (%#v, %t, %v), want (nil, false, nil)", reviewer, present, err)
	}
	assertFacadeQueryDelta(t, product, before, 0, "nullable NULL BlogPost.Reviewer")

	before = product.backend.QueryCount()
	separatePost, found, err := models.BlogPost.
		Filter(blog.PostFields.ID.Exact(10)).
		OrderBy(blog.PostFields.ID.Asc()).
		First(ctx)
	if err != nil || !found {
		t.Fatalf("separate BlogPost.First(id=10) = (%#v, %t, %v), want found", separatePost, found, err)
	}
	assertFacadeQueryDelta(t, product, before, 1, "separate BlogPost.First")
	before = product.backend.QueryCount()
	if _, err := separatePost.Author(ctx); err != nil {
		t.Fatalf("separate BlogPost.Author() error = %v", err)
	}
	assertFacadeQueryDelta(t, product, before, 1, "separate source wrapper cold Author")
}

func TestProjectFacadeSelectRelatedCachesAndPreservesEvaluationOwnership(t *testing.T) {
	t.Parallel()

	ctx, product := openProvisionedFacadeFixture(t)
	models, err := project.Using(product.backend)
	if err != nil {
		t.Fatalf("project.Using() error = %v", err)
	}
	var authorSelector project.BlogPostRelationSelector = models.BlogPost.Related.Author
	var reviewerSelector project.BlogPostRelationSelector = models.BlogPost.Related.Reviewer

	filtered := models.BlogPost.
		Filter(blog.PostFields.Title.IContains("a")).
		OrderBy(blog.PostFields.ID.Asc())
	limited, err := filtered.Limit(3)
	if err != nil {
		t.Fatalf("BlogPostQuery.Limit() error = %v", err)
	}
	authorEager := limited.SelectRelated(authorSelector)
	authorEagerCopy := authorEager
	before := product.backend.QueryCount()
	posts, err := authorEager.All(ctx)
	if err != nil || len(posts) != 3 {
		t.Fatalf("author eager All() = (%#v, %v), want 3 posts", posts, err)
	}
	assertFacadeQueryDelta(t, product, before, 1, "cold author eager All")
	for _, expected := range []struct {
		postID   int64
		authorID int64
	}{{postID: 10, authorID: 1}, {postID: 11, authorID: 1}, {postID: 12, authorID: 3}} {
		post := facadePostByID(t, posts, expected.postID)
		before = product.backend.QueryCount()
		author, err := post.Author(ctx)
		if err != nil {
			t.Fatalf("eager post %d Author() error = %v", expected.postID, err)
		}
		raw, err := author.Unwrap()
		if err != nil || raw.ID != expected.authorID {
			t.Fatalf("eager post %d Author().Unwrap() = (%#v, %v), want author %d", expected.postID, raw, err, expected.authorID)
		}
		assertFacadeQueryDelta(t, product, before, 0, "author eager warm accessor")
	}
	before = product.backend.QueryCount()
	if repeated, err := authorEager.All(ctx); err != nil || len(repeated) != 3 {
		t.Fatalf("repeated author eager All() = (%#v, %v), want 3 posts", repeated, err)
	}
	if copied, err := authorEagerCopy.All(ctx); err != nil || len(copied) != 3 {
		t.Fatalf("copied author eager All() = (%#v, %v), want 3 posts", copied, err)
	}
	assertFacadeQueryDelta(t, product, before, 0, "repeated and copied author eager All")

	derived := authorEager.Filter(blog.PostFields.ID.Exact(12))
	before = product.backend.QueryCount()
	derivedPosts, err := derived.All(ctx)
	if err != nil || len(derivedPosts) != 1 {
		t.Fatalf("derived author eager All() = (%#v, %v), want one post", derivedPosts, err)
	}
	derivedRaw, err := derivedPosts[0].Unwrap()
	if err != nil || derivedRaw.ID != 12 {
		t.Fatalf("derived eager post Unwrap() = (%#v, %v), want post 12", derivedRaw, err)
	}
	assertFacadeQueryDelta(t, product, before, 1, "derived author eager All")
	before = product.backend.QueryCount()
	if originalAgain, err := authorEager.All(ctx); err != nil || len(originalAgain) != 3 {
		t.Fatalf("original eager after derived All() = (%#v, %v), want 3 posts", originalAgain, err)
	}
	assertFacadeQueryDelta(t, product, before, 0, "original eager remains warm")

	reviewerEager := models.BlogPost.
		SelectRelated(reviewerSelector).
		Filter(blog.PostFields.Title.IContains("a")).
		OrderBy(blog.PostFields.ID.Asc())
	reviewerEager, err = reviewerEager.Limit(3)
	if err != nil {
		t.Fatalf("BlogPostEagerQuery.Limit() error = %v", err)
	}
	before = product.backend.QueryCount()
	reviewerPosts, err := reviewerEager.All(ctx)
	if err != nil || len(reviewerPosts) != 3 {
		t.Fatalf("reviewer eager All() = (%#v, %v), want 3 posts", reviewerPosts, err)
	}
	assertFacadeQueryDelta(t, product, before, 1, "cold reviewer eager All")
	for _, expected := range []struct {
		postID     int64
		present    bool
		reviewerID int64
	}{{postID: 10, present: true, reviewerID: 2}, {postID: 11}, {postID: 12, present: true, reviewerID: 2}} {
		post := facadePostByID(t, reviewerPosts, expected.postID)
		before = product.backend.QueryCount()
		reviewer, present, err := post.Reviewer(ctx)
		if err != nil || present != expected.present || (present && reviewer == nil) || (!present && reviewer != nil) {
			t.Fatalf("eager post %d Reviewer() = (%#v, %t, %v), want present %t", expected.postID, reviewer, present, err, expected.present)
		}
		if present {
			raw, err := reviewer.Unwrap()
			if err != nil || raw.ID != expected.reviewerID {
				t.Fatalf("eager post %d Reviewer().Unwrap() = (%#v, %v), want reviewer %d", expected.postID, raw, err, expected.reviewerID)
			}
		}
		assertFacadeQueryDelta(t, product, before, 0, "reviewer eager warm accessor")
	}
}

func TestProjectFacadeInvalidValuesFailBeforeBackendIO(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	minimal := &facadeMinimalBackend{}
	if _, ok := any(minimal).(db.Atomic); ok {
		t.Fatal("minimal project Backend unexpectedly implements db.Atomic")
	}
	if _, ok := any(minimal).(db.RelationAtomic); ok {
		t.Fatal("minimal project Backend unexpectedly implements db.RelationAtomic")
	}
	if _, ok := any(minimal).(db.RelationMutator); ok {
		t.Fatal("minimal project Backend unexpectedly implements db.RelationMutator")
	}
	models, err := project.Using(minimal)
	if err != nil {
		t.Fatalf("project.Using(minimal Queryer+Mutator) error = %v", err)
	}
	if minimal.calls != 0 {
		t.Fatalf("project.Using(minimal) backend calls = %d, want 0", minimal.calls)
	}

	if _, err := project.Using(nil); !errors.Is(err, facadeBackendInvalidPlan) {
		t.Fatalf("project.Using(nil) error = %v, want backend_error/invalid_plan", err)
	}
	var typedNil *facadeMinimalBackend
	if _, err := project.Using(typedNil); !errors.Is(err, facadeBackendInvalidPlan) {
		t.Fatalf("project.Using(typed nil) error = %v, want backend_error/invalid_plan", err)
	}

	assertInvalid := func(label string, operation func() error) {
		t.Helper()
		before := minimal.calls
		err := operation()
		if !errors.Is(err, facadeQueryInvalidPlan) {
			t.Fatalf("%s error = %v, want query_error/invalid_plan", label, err)
		}
		if minimal.calls != before {
			t.Fatalf("%s backend calls = %d, want unchanged %d", label, minimal.calls, before)
		}
	}
	assertInvalid("zero Models BlogPost.All", func() error {
		_, err := (project.Models{}).BlogPost.All(ctx)
		return err
	})
	assertInvalid("zero Models AuthorsAuthor.First", func() error {
		_, _, err := (project.Models{}).AuthorsAuthor.First(ctx)
		return err
	})
	assertInvalid("zero BlogPostQuery.All", func() error {
		var value project.BlogPostQuery
		_, err := value.All(ctx)
		return err
	})
	assertInvalid("zero AuthorsAuthorQuery.All", func() error {
		var value project.AuthorsAuthorQuery
		_, err := value.All(ctx)
		return err
	})
	assertInvalid("zero BlogPostEagerQuery.All", func() error {
		var value project.BlogPostEagerQuery
		_, err := value.All(ctx)
		return err
	})
	assertInvalid("nil BlogPostRelationSelector", func() error {
		var selector project.BlogPostRelationSelector
		_, err := models.BlogPost.SelectRelated(selector).All(ctx)
		return err
	})

	ctx, product := openProvisionedFacadeFixture(t)
	validModels, err := project.Using(product.backend)
	if err != nil {
		t.Fatalf("project.Using(SQLite) error = %v", err)
	}
	post, found, err := validModels.BlogPost.
		Filter(blog.PostFields.ID.Exact(10)).
		OrderBy(blog.PostFields.ID.Asc()).
		First(ctx)
	if err != nil || !found {
		t.Fatalf("load valid BlogPost = (%#v, %t, %v)", post, found, err)
	}
	author, found, err := validModels.AuthorsAuthor.
		Filter(authors.AuthorFields.ID.Exact(1)).
		OrderBy(authors.AuthorFields.ID.Asc()).
		First(ctx)
	if err != nil || !found {
		t.Fatalf("load valid AuthorsAuthor = (%#v, %t, %v)", author, found, err)
	}

	assertSQLiteInvalid := func(label string, operation func() error) {
		t.Helper()
		before := product.backend.QueryCount()
		err := operation()
		if !errors.Is(err, facadeQueryInvalidPlan) {
			t.Fatalf("%s error = %v, want query_error/invalid_plan", label, err)
		}
		assertFacadeQueryDelta(t, product, before, 0, label)
	}
	var nilPost *project.BlogPost
	assertSQLiteInvalid("nil BlogPost.Unwrap", func() error { _, err := nilPost.Unwrap(); return err })
	assertSQLiteInvalid("nil BlogPost.Author", func() error { _, err := nilPost.Author(ctx); return err })
	assertSQLiteInvalid("nil BlogPost.Reviewer", func() error { _, _, err := nilPost.Reviewer(ctx); return err })
	var zeroPost project.BlogPost
	assertSQLiteInvalid("zero BlogPost.Unwrap", func() error { _, err := zeroPost.Unwrap(); return err })
	copiedPost := *post
	assertSQLiteInvalid("copied BlogPost.Unwrap", func() error { _, err := copiedPost.Unwrap(); return err })
	assertSQLiteInvalid("copied BlogPost.Author", func() error { _, err := copiedPost.Author(ctx); return err })

	var nilAuthor *project.AuthorsAuthor
	assertSQLiteInvalid("nil AuthorsAuthor.Unwrap", func() error { _, err := nilAuthor.Unwrap(); return err })
	var zeroAuthor project.AuthorsAuthor
	assertSQLiteInvalid("zero AuthorsAuthor.Unwrap", func() error { _, err := zeroAuthor.Unwrap(); return err })
	copiedAuthor := *author
	assertSQLiteInvalid("copied AuthorsAuthor.Unwrap", func() error { _, err := copiedAuthor.Unwrap(); return err })
}

func TestProjectFacadeUsesCallbackLocalSession(t *testing.T) {
	t.Parallel()

	ctx, product := openProvisionedFacadeFixture(t)
	before := product.backend.QueryCount()
	callbacks := 0
	err := product.backend.Atomic(ctx, func(session db.Session) error {
		callbacks++
		var backend project.Backend = session
		models, err := project.Using(backend)
		if err != nil {
			return err
		}
		post, found, err := models.BlogPost.
			Filter(blog.PostFields.ID.Exact(10)).
			OrderBy(blog.PostFields.ID.Asc()).
			First(ctx)
		if err != nil {
			return err
		}
		if !found {
			return errors.New("callback-local BlogPost query did not find post 10")
		}
		author, err := post.Author(ctx)
		if err != nil {
			return err
		}
		raw, err := author.Unwrap()
		if err != nil {
			return err
		}
		if raw.ID != 1 || raw.Name != "Ada" {
			return errors.New("callback-local relation returned the wrong author")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("callback-local facade transaction error = %v", err)
	}
	if callbacks != 1 {
		t.Fatalf("callback-local facade callback count = %d, want 1", callbacks)
	}
	assertFacadeQueryDelta(t, product, before, 2, "callback-local source and relation queries")
}

func TestObserveUnsavedRelatedTargetFailsBeforeExactOperationIO(t *testing.T) {
	t.Parallel()

	got, err := ObserveUnsavedRelatedTarget(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(got.Err, &query.Error{
		Category: query.CategoryModelState,
		Code:     query.CodeUnsavedRelatedObject,
		Field:    "author",
	}) {
		t.Fatalf("REL-002 error = %v, want model_state_error/unsaved_related_object field author", got.Err)
	}
	if !reflect.DeepEqual(got.Before, initialDatabaseState()) || !reflect.DeepEqual(got.After, got.Before) {
		t.Fatalf("REL-002 database before/after = %#v/%#v, want exact unchanged fixture", got.Before, got.After)
	}
	if !reflect.DeepEqual(got.Metrics, WriteMetrics{}) {
		t.Fatalf("REL-002 operation-local metrics = %#v, want exact zero", got.Metrics)
	}
}

func TestWriteRecorderBoundsAndOrdersEveryBackendOperation(t *testing.T) {
	t.Parallel()

	backend := &facadeMinimalBackend{}
	recorder := &recordingBackend{backend: backend}
	ctx := context.Background()
	if _, err := recorder.Query(ctx, query.Plan{}); !errors.Is(err, facadeUnexpectedIO) {
		t.Fatalf("recorded Query() error = %v, want delegated sentinel", err)
	}
	if _, err := recorder.Insert(ctx, query.InsertPlan{}); !errors.Is(err, facadeUnexpectedIO) {
		t.Fatalf("recorded Insert() error = %v, want delegated sentinel", err)
	}
	if _, err := recorder.Update(ctx, query.UpdatePlan{}); !errors.Is(err, facadeUnexpectedIO) {
		t.Fatalf("recorded Update() error = %v, want delegated sentinel", err)
	}
	if _, err := recorder.Delete(ctx, query.DeletePlan{}); !errors.Is(err, facadeUnexpectedIO) {
		t.Fatalf("recorded Delete() error = %v, want delegated sentinel", err)
	}
	want := WriteMetrics{
		QueryCount:     1,
		InsertCount:    1,
		UpdateCount:    1,
		DeleteCount:    1,
		StatementKinds: []string{OperationSelect, OperationInsert, OperationUpdate, OperationDelete},
	}
	if got := recorder.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("recorded backend metrics = %#v, want %#v", got, want)
	}
	if backend.calls != 4 {
		t.Fatalf("recorded backend delegated calls = %d, want 4", backend.calls)
	}
}

func TestProjectFacadePendingTargetSaveReconcilesAndPublishesBothRows(t *testing.T) {
	t.Parallel()

	ctx, product := openProvisionedFacadeFixture(t)
	recorder := &recordingBackend{backend: product.backend}
	models, err := project.Using(recorder)
	if err != nil {
		t.Fatal(err)
	}
	target, err := models.AuthorsAuthor.New(authors.Author{Name: "Dora"})
	if err != nil {
		t.Fatal(err)
	}
	original, err := models.BlogPost.New(blog.Post{Title: "Delta"})
	if err != nil {
		t.Fatal(err)
	}
	derived, err := original.WithAuthor(target)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := derived.Author(ctx); err != nil || got != target {
		t.Fatalf("pending Author() = (%p, %v), want exact assigned target %p", got, err, target)
	}
	if _, err := derived.Unwrap(); !errors.Is(err, &query.Error{
		Category: query.CategoryModelState,
		Code:     query.CodeUnsavedRelatedObject,
		Field:    "author",
	}) {
		t.Fatalf("pending Unwrap() error = %v, want unsaved author", err)
	}
	if err := derived.Save(ctx); !errors.Is(err, &query.Error{
		Category: query.CategoryModelState,
		Code:     query.CodeUnsavedRelatedObject,
		Field:    "author",
	}) {
		t.Fatalf("pending Save() error = %v, want unsaved author", err)
	}
	if got := recorder.snapshot(); !reflect.DeepEqual(got, WriteMetrics{}) {
		t.Fatalf("pending accessor/unwrap/save metrics = %#v, want exact zero", got)
	}
	if err := target.Save(ctx); err != nil {
		t.Fatalf("target Save() error = %v", err)
	}
	if got := recorder.snapshot(); got.InsertCount != 1 || !reflect.DeepEqual(got.StatementKinds, []string{OperationInsert}) {
		t.Fatalf("target Save() metrics = %#v, want one INSERT", got)
	}
	targetRaw, err := target.Unwrap()
	if err != nil || targetRaw.ID != 4 || targetRaw.Name != "Dora" {
		t.Fatalf("saved target Unwrap() = (%#v, %v), want ID 4 Dora", targetRaw, err)
	}
	if err := derived.Save(ctx); err != nil {
		t.Fatalf("reconciled source Save() error = %v", err)
	}
	if got := recorder.snapshot(); got.InsertCount != 2 || !reflect.DeepEqual(got.StatementKinds, []string{OperationInsert, OperationInsert}) {
		t.Fatalf("target+source Save() metrics = %#v, want two INSERTs", got)
	}
	derivedRaw, err := derived.Unwrap()
	if err != nil || derivedRaw.ID != 13 || derivedRaw.Title != "Delta" || derivedRaw.AuthorID != 4 || derivedRaw.ReviewerID != nil {
		t.Fatalf("reconciled source Unwrap() = (%#v, %v), want post 13 -> author 4", derivedRaw, err)
	}
	if got, err := derived.Author(ctx); err != nil || got != target {
		t.Fatalf("reconciled Author() = (%p, %v), want still-warm target %p", got, err, target)
	}
	if _, err := original.Unwrap(); !errors.Is(err, &query.Error{
		Category: query.CategoryField,
		Code:     query.CodeRequiredField,
		Field:    "author",
	}) {
		t.Fatalf("original source Unwrap() error = %v, want unchanged required author", err)
	}
	state, err := readState(ctx, product.backend)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Authors) != 4 || state.Authors[3] != (AuthorRow{ID: 4, Name: "Dora"}) ||
		len(state.Posts) != 4 || state.Posts[3] != (PostRow{ID: 13, Title: "Delta", AuthorID: 4}) {
		t.Fatalf("reconciled database state = %#v", state)
	}
}

func TestProjectFacadeExplicitScalarOverridesPendingAssignment(t *testing.T) {
	t.Parallel()

	ctx, product := openProvisionedFacadeFixture(t)
	recorder := &recordingBackend{backend: product.backend}
	models, err := project.Using(recorder)
	if err != nil {
		t.Fatal(err)
	}
	unsaved, err := models.AuthorsAuthor.New(authors.Author{Name: "Must Not Follow"})
	if err != nil {
		t.Fatal(err)
	}
	source, err := models.BlogPost.New(blog.Post{Title: "Explicit Override"})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := source.WithAuthor(unsaved)
	if err != nil {
		t.Fatal(err)
	}
	overridden, err := pending.WithAuthorID(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := overridden.Save(ctx); err != nil {
		t.Fatalf("explicit scalar override Save() error = %v", err)
	}
	if got := recorder.snapshot(); got.InsertCount != 1 || !reflect.DeepEqual(got.StatementKinds, []string{OperationInsert}) {
		t.Fatalf("explicit scalar override Save() metrics = %#v, want one INSERT", got)
	}
	raw, err := overridden.Unwrap()
	if err != nil || raw.ID != 13 || raw.AuthorID != 1 {
		t.Fatalf("explicit scalar override Unwrap() = (%#v, %v), want post 13 -> author 1", raw, err)
	}
	loaded, err := overridden.Author(ctx)
	if err != nil {
		t.Fatal(err)
	}
	loadedRaw, err := loaded.Unwrap()
	if err != nil || loadedRaw.ID != 1 || loadedRaw.Name != "Ada" || loaded == unsaved {
		t.Fatalf("explicit scalar override Author().Unwrap() = (%#v, %v), wrapper %p; want loaded Ada distinct from %p", loadedRaw, err, loaded, unsaved)
	}
	if got := recorder.snapshot(); got.QueryCount != 1 || got.InsertCount != 1 ||
		!reflect.DeepEqual(got.StatementKinds, []string{OperationInsert, OperationSelect}) {
		t.Fatalf("explicit scalar override load metrics = %#v, want INSERT then SELECT", got)
	}
	before := recorder.snapshot()
	if got, err := pending.Author(ctx); err != nil || got != unsaved {
		t.Fatalf("original pending Author() = (%p, %v), want exact unsaved %p", got, err, unsaved)
	}
	if err := pending.Save(ctx); !errors.Is(err, &query.Error{
		Category: query.CategoryModelState,
		Code:     query.CodeUnsavedRelatedObject,
		Field:    "author",
	}) {
		t.Fatalf("original pending Save() error = %v, want unchanged unsaved author", err)
	}
	if got := recorder.snapshot(); !reflect.DeepEqual(got, before) {
		t.Fatalf("original pending access/save changed metrics from %#v to %#v", before, got)
	}
}

func TestProjectFacadeManualZeroKeyReachesDatabaseAndFailedInsertIsRetryable(t *testing.T) {
	t.Parallel()

	ctx, product := openProvisionedFacadeFixture(t)
	recorder := &recordingBackend{backend: product.backend}
	models, err := project.Using(recorder)
	if err != nil {
		t.Fatal(err)
	}
	manualValue := authors.NewAuthorWithID(0)
	manualValue.Name = "Zero"
	manual, err := models.AuthorsAuthor.New(manualValue)
	if err != nil {
		t.Fatal(err)
	}
	source, err := models.BlogPost.New(blog.Post{Title: "Manual zero"})
	if err != nil {
		t.Fatal(err)
	}
	source, err = source.WithAuthor(manual)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := source.Unwrap()
	if err != nil || raw.AuthorID != 0 {
		t.Fatalf("manual-zero source Unwrap() = (%#v, %v), want explicit FK 0", raw, err)
	}
	firstErr := source.Save(ctx)
	if firstErr == nil {
		t.Fatal("manual-zero source Save() unexpectedly succeeded without target row")
	}
	if errors.Is(firstErr, &query.Error{Category: query.CategoryModelState, Code: query.CodeUnsavedRelatedObject}) ||
		errors.Is(firstErr, &query.Error{Category: query.CategoryField, Code: query.CodeRequiredField}) {
		t.Fatalf("manual-zero source Save() error = %v, must be a database constraint cause", firstErr)
	}
	var driverError *modernsqlite.Error
	if !errors.As(firstErr, &driverError) || driverError.Code() != sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY {
		t.Fatalf(
			"manual-zero source Save() error = %v, want preserved SQLite FOREIGN KEY cause code %d",
			firstErr,
			sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY,
		)
	}
	if got := recorder.snapshot(); got.InsertCount != 1 || got.QueryCount != 0 || !reflect.DeepEqual(got.StatementKinds, []string{OperationInsert}) {
		t.Fatalf("manual-zero failed Save() metrics = %#v, want one INSERT reach", got)
	}
	if state, err := readState(ctx, product.backend); err != nil || !reflect.DeepEqual(state, initialDatabaseState()) {
		t.Fatalf("manual-zero failure database = (%#v, %v), want unchanged", state, err)
	}
	if _, err := product.backend.ExecContext(ctx, `INSERT INTO "authors_author" ("id", "name") VALUES (0, 'Zero')`); err != nil {
		t.Fatalf("provision manual-zero target row: %v", err)
	}
	if err := source.Save(ctx); err != nil {
		t.Fatalf("retry source Save() after target provision error = %v", err)
	}
	if got := recorder.snapshot(); got.InsertCount != 2 || !reflect.DeepEqual(got.StatementKinds, []string{OperationInsert, OperationInsert}) {
		t.Fatalf("manual-zero retry metrics = %#v, want second INSERT", got)
	}
	raw, err = source.Unwrap()
	if err != nil || raw.ID != 13 || raw.AuthorID != 0 {
		t.Fatalf("manual-zero retry Unwrap() = (%#v, %v), want persisted post 13 -> 0", raw, err)
	}
}

func TestProjectFacadeAssignedTargetErrorsPrecedeRequiredScalarWithoutIO(t *testing.T) {
	t.Parallel()

	ctx, product := openProvisionedFacadeFixture(t)
	recorder := &recordingBackend{backend: product.backend}
	models, err := project.Using(recorder)
	if err != nil {
		t.Fatal(err)
	}
	requiredUnset, err := models.BlogPost.New(blog.Post{Title: "required"})
	if err != nil {
		t.Fatal(err)
	}
	if err := requiredUnset.Save(ctx); !errors.Is(err, &query.Error{
		Category: query.CategoryField,
		Code:     query.CodeRequiredField,
		Field:    "author",
	}) {
		t.Fatalf("required-unset Save() error = %v", err)
	}
	unsavedReviewer, err := models.AuthorsAuthor.New(authors.Author{Name: "Reviewer"})
	if err != nil {
		t.Fatal(err)
	}
	reviewerPending, err := requiredUnset.WithReviewer(unsavedReviewer)
	if err != nil {
		t.Fatal(err)
	}
	if err := reviewerPending.Save(ctx); !errors.Is(err, &query.Error{
		Category: query.CategoryModelState,
		Code:     query.CodeUnsavedRelatedObject,
		Field:    "reviewer",
	}) {
		t.Fatalf("pending-reviewer + required-unset Save() error = %v, want reviewer first", err)
	}
	unsavedAuthor, err := models.AuthorsAuthor.New(authors.Author{Name: "Author"})
	if err != nil {
		t.Fatal(err)
	}
	bothPending, err := reviewerPending.WithAuthor(unsavedAuthor)
	if err != nil {
		t.Fatal(err)
	}
	if err := bothPending.Save(ctx); !errors.Is(err, &query.Error{
		Category: query.CategoryModelState,
		Code:     query.CodeUnsavedRelatedObject,
		Field:    "author",
	}) {
		t.Fatalf("two pending relations Save() error = %v, want canonical author first", err)
	}
	if got := recorder.snapshot(); !reflect.DeepEqual(got, WriteMetrics{}) {
		t.Fatalf("preflight precedence metrics = %#v, want exact zero", got)
	}
}

func TestProjectFacadeScalarDerivationsPreserveOnlyValidWarmCaches(t *testing.T) {
	t.Parallel()

	ctx, product := openProvisionedFacadeFixture(t)
	recorder := &recordingBackend{backend: product.backend}
	models, err := project.Using(recorder)
	if err != nil {
		t.Fatal(err)
	}
	author, found, err := models.AuthorsAuthor.
		Filter(authors.AuthorFields.ID.Exact(1)).
		OrderBy(authors.AuthorFields.ID.Asc()).
		First(ctx)
	if err != nil || !found {
		t.Fatalf("load author 1 = (%p, %t, %v)", author, found, err)
	}
	reviewer, found, err := models.AuthorsAuthor.
		Filter(authors.AuthorFields.ID.Exact(2)).
		OrderBy(authors.AuthorFields.ID.Asc()).
		First(ctx)
	if err != nil || !found {
		t.Fatalf("load reviewer 2 = (%p, %t, %v)", reviewer, found, err)
	}
	source, err := models.BlogPost.New(blog.Post{Title: "cache"})
	if err != nil {
		t.Fatal(err)
	}
	source, err = source.WithAuthor(author)
	if err != nil {
		t.Fatal(err)
	}
	source, err = source.WithReviewer(reviewer)
	if err != nil {
		t.Fatal(err)
	}
	before := recorder.snapshot()
	same, err := source.WithAuthorID(1)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := same.Author(ctx); err != nil || got != author {
		t.Fatalf("same-scalar Author() = (%p, %v), want warm %p", got, err, author)
	}
	changed, err := same.WithAuthorID(3)
	if err != nil {
		t.Fatal(err)
	}
	changedAuthor, err := changed.Author(ctx)
	if err != nil {
		t.Fatal(err)
	}
	changedRaw, err := changedAuthor.Unwrap()
	if err != nil || changedRaw.ID != 3 {
		t.Fatalf("different-scalar Author().Unwrap() = (%#v, %v), want author 3", changedRaw, err)
	}
	if got, present, err := changed.Reviewer(ctx); err != nil || !present || got != reviewer {
		t.Fatalf("different-scalar unrelated Reviewer() = (%p, %t, %v), want warm %p", got, present, err, reviewer)
	}
	if got, err := source.Author(ctx); err != nil || got != author {
		t.Fatalf("original Author() = (%p, %v), want unchanged warm %p", got, err, author)
	}
	cleared, err := changed.ClearReviewer()
	if err != nil {
		t.Fatal(err)
	}
	if got, present, err := cleared.Reviewer(ctx); err != nil || present || got != nil {
		t.Fatalf("cleared Reviewer() = (%p, %t, %v), want absent", got, present, err)
	}
	if got, present, err := source.Reviewer(ctx); err != nil || !present || got != reviewer {
		t.Fatalf("original Reviewer() after clear = (%p, %t, %v), want warm %p", got, present, err, reviewer)
	}
	after := recorder.snapshot()
	if after.QueryCount-before.QueryCount != 1 || after.InsertCount != before.InsertCount ||
		after.UpdateCount != before.UpdateCount || after.DeleteCount != before.DeleteCount {
		t.Fatalf("cache derivation operation delta before=%#v after=%#v, want one SELECT for changed author only", before, after)
	}
}

func TestProjectFacadePromotedDirectMutationPersistsAndReconcilesSQLite(t *testing.T) {
	t.Parallel()

	ctx, product := openProvisionedFacadeFixture(t)
	recorder := &recordingBackend{backend: product.backend}
	models, err := project.Using(recorder)
	if err != nil {
		t.Fatal(err)
	}
	post, found, err := models.BlogPost.
		Filter(blog.PostFields.ID.Exact(10)).
		OrderBy(blog.PostFields.ID.Asc()).
		First(ctx)
	if err != nil || !found {
		t.Fatalf("load post 10 = (%p, %t, %v)", post, found, err)
	}
	originalAuthor, err := post.Author(ctx)
	if err != nil {
		t.Fatal(err)
	}
	originalReviewer, present, err := post.Reviewer(ctx)
	if err != nil || !present {
		t.Fatalf("warm post 10 Reviewer() = (%p, %t, %v), want present", originalReviewer, present, err)
	}
	if got := recorder.snapshot(); got.QueryCount != 3 || !reflect.DeepEqual(got.StatementKinds, []string{
		OperationSelect,
		OperationSelect,
		OperationSelect,
	}) {
		t.Fatalf("load and warm metrics = %#v, want three SELECTs", got)
	}

	post.Title = "  Alpha promoted  "
	post.NormalizeTitle()
	post.AuthorID = 3
	beforeAuthor := recorder.snapshot()
	changedAuthor, err := post.Author(ctx)
	if err != nil {
		t.Fatal(err)
	}
	changedAuthorRaw, err := changedAuthor.Unwrap()
	if err != nil || changedAuthorRaw.ID != 3 || changedAuthor == originalAuthor {
		t.Fatalf("direct AuthorID Author() = (%p, %#v, %v), want a newly loaded author 3", changedAuthor, changedAuthorRaw, err)
	}
	afterAuthor := recorder.snapshot()
	if afterAuthor.QueryCount-beforeAuthor.QueryCount != 1 || afterAuthor.InsertCount != beforeAuthor.InsertCount ||
		afterAuthor.UpdateCount != beforeAuthor.UpdateCount || afterAuthor.DeleteCount != beforeAuthor.DeleteCount {
		t.Fatalf("direct AuthorID operation delta before=%#v after=%#v, want one SELECT", beforeAuthor, afterAuthor)
	}
	if got, gotPresent, gotErr := post.Reviewer(ctx); gotErr != nil || !gotPresent || got != originalReviewer {
		t.Fatalf("unchanged Reviewer() after direct AuthorID = (%p, %t, %v), want warm %p", got, gotPresent, gotErr, originalReviewer)
	}
	if got := recorder.snapshot(); !reflect.DeepEqual(got, afterAuthor) {
		t.Fatalf("unchanged reviewer published backend I/O: before=%#v after=%#v", afterAuthor, got)
	}

	reviewerID := int64(1)
	post.ReviewerID = &reviewerID
	beforeReviewer := recorder.snapshot()
	changedReviewer, present, err := post.Reviewer(ctx)
	if err != nil || !present {
		t.Fatalf("direct ReviewerID Reviewer() = (%p, %t, %v), want present", changedReviewer, present, err)
	}
	changedReviewerRaw, err := changedReviewer.Unwrap()
	if err != nil || changedReviewerRaw.ID != 1 || changedReviewer == originalReviewer {
		t.Fatalf("direct ReviewerID Reviewer().Unwrap() = (%p, %#v, %v), want a newly loaded author 1", changedReviewer, changedReviewerRaw, err)
	}
	afterReviewer := recorder.snapshot()
	if afterReviewer.QueryCount-beforeReviewer.QueryCount != 1 || afterReviewer.InsertCount != beforeReviewer.InsertCount ||
		afterReviewer.UpdateCount != beforeReviewer.UpdateCount || afterReviewer.DeleteCount != beforeReviewer.DeleteCount {
		t.Fatalf("direct ReviewerID operation delta before=%#v after=%#v, want one SELECT", beforeReviewer, afterReviewer)
	}
	if got, gotErr := post.Author(ctx); gotErr != nil || got != changedAuthor {
		t.Fatalf("unchanged Author() after direct ReviewerID = (%p, %v), want warm %p", got, gotErr, changedAuthor)
	}
	if got := recorder.snapshot(); !reflect.DeepEqual(got, afterReviewer) {
		t.Fatalf("unchanged author published backend I/O: before=%#v after=%#v", afterReviewer, got)
	}

	if err := post.Save(ctx); err != nil {
		t.Fatalf("directly mutated post Save() error = %v", err)
	}
	savedRaw, err := post.Unwrap()
	if err != nil || savedRaw.ID != 10 || savedRaw.Title != "Alpha promoted" || savedRaw.AuthorID != 3 ||
		savedRaw.ReviewerID == nil || *savedRaw.ReviewerID != 1 {
		t.Fatalf("saved direct mutation Unwrap() = (%#v, %v), want post 10 normalized and related to authors 3/1", savedRaw, err)
	}
	afterSave := recorder.snapshot()
	if afterSave.QueryCount != 5 || afterSave.UpdateCount != 1 || afterSave.InsertCount != 0 || afterSave.DeleteCount != 0 ||
		!reflect.DeepEqual(afterSave.StatementKinds, []string{
			OperationSelect,
			OperationSelect,
			OperationSelect,
			OperationSelect,
			OperationSelect,
			OperationUpdate,
		}) {
		t.Fatalf("direct mutation Save() metrics = %#v, want five SELECTs then one UPDATE", afterSave)
	}
	if got, gotErr := post.Author(ctx); gotErr != nil || got != changedAuthor {
		t.Fatalf("saved Author() = (%p, %v), want warm %p", got, gotErr, changedAuthor)
	}
	if got, gotPresent, gotErr := post.Reviewer(ctx); gotErr != nil || !gotPresent || got != changedReviewer {
		t.Fatalf("saved Reviewer() = (%p, %t, %v), want warm %p", got, gotPresent, gotErr, changedReviewer)
	}
	if got := recorder.snapshot(); !reflect.DeepEqual(got, afterSave) {
		t.Fatalf("post-save warm relation access published backend I/O: before=%#v after=%#v", afterSave, got)
	}

	post.ID = 99
	beforePrimaryKeyFailure := recorder.snapshot()
	err = post.Save(ctx)
	if !errors.Is(err, &query.Error{
		Category: query.CategoryModelState,
		Code:     query.CodePrimaryKeyUpdateField,
		Field:    "id",
	}) {
		t.Fatalf("direct persisted primary-key mutation Save() error = %v, want model_state_error/primary_key_update_field", err)
	}
	if got := recorder.snapshot(); !reflect.DeepEqual(got, beforePrimaryKeyFailure) {
		t.Fatalf("direct primary-key mutation reached backend: before=%#v after=%#v", beforePrimaryKeyFailure, got)
	}
	post.ID = 10
	restoredRaw, err := post.Unwrap()
	if err != nil || restoredRaw.ID != 10 {
		t.Fatalf("restored primary key Unwrap() = (%#v, %v), want ID 10", restoredRaw, err)
	}

	fresh, found, err := models.BlogPost.
		Filter(blog.PostFields.ID.Exact(10)).
		OrderBy(blog.PostFields.ID.Asc()).
		First(ctx)
	if err != nil || !found {
		t.Fatalf("fresh reload post 10 = (%p, %t, %v)", fresh, found, err)
	}
	freshRaw, err := fresh.Unwrap()
	if err != nil || freshRaw.ID != 10 || freshRaw.Title != "Alpha promoted" || freshRaw.AuthorID != 3 ||
		freshRaw.ReviewerID == nil || *freshRaw.ReviewerID != 1 {
		t.Fatalf("fresh reload Unwrap() = (%#v, %v), want persisted post 10 mutation", freshRaw, err)
	}
	if got := recorder.snapshot(); got.QueryCount != 6 || got.UpdateCount != 1 || !reflect.DeepEqual(got.StatementKinds, []string{
		OperationSelect,
		OperationSelect,
		OperationSelect,
		OperationSelect,
		OperationSelect,
		OperationUpdate,
		OperationSelect,
	}) {
		t.Fatalf("complete direct mutation metrics = %#v, want six SELECTs and one UPDATE in exact order", got)
	}
}

func TestProjectFacadeEagerUnrelatedCacheSurvivesWriteDerivationWithoutSharingPublication(t *testing.T) {
	t.Parallel()

	ctx, product := openProvisionedFacadeFixture(t)
	recorder := &recordingBackend{backend: product.backend}
	models, err := project.Using(recorder)
	if err != nil {
		t.Fatal(err)
	}
	loadedRows, err := models.BlogPost.
		SelectRelated(models.BlogPost.Related.Reviewer).
		Filter(blog.PostFields.ID.Exact(10)).
		OrderBy(blog.PostFields.ID.Asc()).
		All(ctx)
	if err != nil || len(loadedRows) != 1 {
		t.Fatalf("eager reviewer load = (%#v, %v), want one post", loadedRows, err)
	}
	loaded := loadedRows[0]
	if got := recorder.snapshot(); got.QueryCount != 1 || !reflect.DeepEqual(got.StatementKinds, []string{OperationSelect}) {
		t.Fatalf("eager reviewer load metrics = %#v, want one SELECT", got)
	}
	reviewer, present, err := loaded.Reviewer(ctx)
	if err != nil || !present || reviewer == nil {
		t.Fatalf("eager loaded Reviewer() = (%p, %t, %v), want present", reviewer, present, err)
	}
	before := recorder.snapshot()
	author, found, err := models.AuthorsAuthor.
		Filter(authors.AuthorFields.ID.Exact(3)).
		OrderBy(authors.AuthorFields.ID.Asc()).
		First(ctx)
	if err != nil || !found {
		t.Fatalf("load replacement author 3 = (%p, %t, %v)", author, found, err)
	}
	derived, err := loaded.WithAuthor(author)
	if err != nil {
		t.Fatal(err)
	}
	afterAuthorLoad := recorder.snapshot()
	if afterAuthorLoad.QueryCount-before.QueryCount != 1 {
		t.Fatalf("replacement author load delta before=%#v after=%#v, want one SELECT", before, afterAuthorLoad)
	}
	if got, present, err := derived.Reviewer(ctx); err != nil || !present || got != reviewer {
		t.Fatalf("derived eager Reviewer() = (%p, %t, %v), want warm %p", got, present, err, reviewer)
	}
	if got, err := derived.Author(ctx); err != nil || got != author {
		t.Fatalf("derived assigned Author() = (%p, %v), want warm %p", got, err, author)
	}
	cleared, err := derived.ClearReviewer()
	if err != nil {
		t.Fatal(err)
	}
	if got, present, err := cleared.Reviewer(ctx); err != nil || present || got != nil {
		t.Fatalf("cleared derived Reviewer() = (%p, %t, %v), want absent", got, present, err)
	}
	if got, present, err := loaded.Reviewer(ctx); err != nil || !present || got != reviewer {
		t.Fatalf("original eager Reviewer() after derived clear = (%p, %t, %v), want warm %p", got, present, err, reviewer)
	}
	if got := recorder.snapshot(); !reflect.DeepEqual(got, afterAuthorLoad) {
		t.Fatalf("derived eager access/clear published backend I/O: before=%#v after=%#v", afterAuthorLoad, got)
	}
}

func TestProjectFacadeWriteAdversarialValuesFailBeforeBackendIO(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	minimal := &facadeMinimalBackend{}
	models, err := project.Using(minimal)
	if err != nil {
		t.Fatal(err)
	}
	source, err := models.BlogPost.New(blog.Post{Title: "adversarial"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := models.AuthorsAuthor.New(authors.Author{Name: "adversarial"})
	if err != nil {
		t.Fatal(err)
	}
	assertInvalid := func(label string, operation func() error) {
		t.Helper()
		before := minimal.calls
		if err := operation(); !errors.Is(err, facadeQueryInvalidPlan) {
			t.Fatalf("%s error = %v, want query_error/invalid_plan", label, err)
		}
		if minimal.calls != before {
			t.Fatalf("%s backend calls = %d, want unchanged %d", label, minimal.calls, before)
		}
	}
	var nilSource *project.BlogPost
	assertInvalid("nil source Save", func() error { return nilSource.Save(ctx) })
	assertInvalid("nil source WithAuthor", func() error { _, err := nilSource.WithAuthor(target); return err })
	assertInvalid("nil target WithAuthor", func() error { _, err := source.WithAuthor(nil); return err })
	copySource := *source
	assertInvalid("copied source Save", func() error { return (&copySource).Save(ctx) })
	assertInvalid("copied source WithAuthorID", func() error { _, err := (&copySource).WithAuthorID(1); return err })
	copyTarget := *target
	assertInvalid("copied target Save", func() error { return (&copyTarget).Save(ctx) })
	assertInvalid("copied target assignment", func() error { _, err := source.WithAuthor(&copyTarget); return err })
	otherMinimal := &facadeMinimalBackend{}
	otherModels, err := project.Using(otherMinimal)
	if err != nil {
		t.Fatal(err)
	}
	otherTarget, err := otherModels.AuthorsAuthor.New(authors.Author{Name: "other"})
	if err != nil {
		t.Fatal(err)
	}
	assertInvalid("cross-origin target", func() error { _, err := source.WithAuthor(otherTarget); return err })
	if otherMinimal.calls != 0 {
		t.Fatalf("cross-origin secondary backend calls = %d, want 0", otherMinimal.calls)
	}
	if err := target.Save(nil); !errors.Is(err, facadeQueryInvalidPlan) {
		t.Fatalf("nil-context target Save() error = %v, want query_error/invalid_plan", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := target.Save(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled target Save() error = %v, want context.Canceled", err)
	}
	if minimal.calls != 0 {
		t.Fatalf("adversarial write backend calls = %d, want exact zero", minimal.calls)
	}
}

func TestProjectFacadeCallbackLocalWritesRollbackDatabaseWithoutRewindingPublishedValues(t *testing.T) {
	t.Parallel()

	ctx, product := openProvisionedFacadeFixture(t)
	rollback := errors.New("rollback generated relation writes")
	var targetRaw authors.Author
	var sourceRaw blog.Post
	var metrics WriteMetrics
	err := product.backend.Atomic(ctx, func(session db.Session) error {
		recorder := &recordingBackend{backend: session}
		models, err := project.Using(recorder)
		if err != nil {
			return err
		}
		target, err := models.AuthorsAuthor.New(authors.Author{Name: "Rolled Back"})
		if err != nil {
			return err
		}
		if err := target.Save(ctx); err != nil {
			return err
		}
		source, err := models.BlogPost.New(blog.Post{Title: "Rolled Back"})
		if err != nil {
			return err
		}
		source, err = source.WithAuthor(target)
		if err != nil {
			return err
		}
		if err := source.Save(ctx); err != nil {
			return err
		}
		targetRaw, err = target.Unwrap()
		if err != nil {
			return err
		}
		sourceRaw, err = source.Unwrap()
		if err != nil {
			return err
		}
		metrics = recorder.snapshot()
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("callback-local write transaction error = %v, want rollback sentinel", err)
	}
	if targetRaw.ID != 4 || targetRaw.Name != "Rolled Back" || sourceRaw.ID != 13 ||
		sourceRaw.Title != "Rolled Back" || sourceRaw.AuthorID != 4 {
		t.Fatalf("values published before rollback = target %#v source %#v", targetRaw, sourceRaw)
	}
	wantMetrics := WriteMetrics{InsertCount: 2, StatementKinds: []string{OperationInsert, OperationInsert}}
	if !reflect.DeepEqual(metrics, wantMetrics) {
		t.Fatalf("callback-local write metrics = %#v, want %#v", metrics, wantMetrics)
	}
	state, stateErr := readState(ctx, product.backend)
	if stateErr != nil || !reflect.DeepEqual(state, initialDatabaseState()) {
		t.Fatalf("database after callback rollback = (%#v, %v), want exact initial state", state, stateErr)
	}
}

func TestObserveExecutesExactREL007AndREL008Cases(t *testing.T) {
	t.Parallel()

	got, err := Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	initial := initialDatabaseState()
	if !reflect.DeepEqual(got.Protect.Before, initial) || !reflect.DeepEqual(got.SetNull.Before, initial) {
		t.Fatalf("fresh fixture states = protect %#v set-null %#v, want %#v", got.Protect.Before, got.SetNull.Before, initial)
	}
	if got.Protect.Returned != 0 {
		t.Fatalf("REL-007 returned rows = %d, want 0", got.Protect.Returned)
	}
	var protected *query.ProtectedForeignKeyError
	if !errors.As(got.Protect.Err, &protected) || protected.ProtectedSourceRows() != 2 {
		t.Fatalf("REL-007 error = %v, want typed protected count 2", got.Protect.Err)
	}
	if !errors.Is(got.Protect.Err, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeProtectedForeignKey}) {
		t.Fatalf("REL-007 error = %v, want integrity_error/protected_foreign_key", got.Protect.Err)
	}
	if !reflect.DeepEqual(got.Protect.CallerBefore, CallerState{ID: 1, Name: "Ada", KeyPresent: true}) ||
		!reflect.DeepEqual(got.Protect.CallerAfter, got.Protect.CallerBefore) {
		t.Fatalf("REL-007 caller before/after = %#v/%#v", got.Protect.CallerBefore, got.Protect.CallerAfter)
	}
	if !reflect.DeepEqual(got.Protect.After, got.Protect.Before) {
		t.Fatalf("REL-007 database changed from %#v to %#v", got.Protect.Before, got.Protect.After)
	}
	wantProtectMetrics := DeleteMetrics{
		TransactionCount:    1,
		QueryCount:          1,
		OperationOrder:      []string{OperationQuery},
		MutationOrder:       []string{},
		MutationRows:        []MutationRow{},
		RelationSetNullRows: []int64{},
		DeleteRows:          []int64{},
	}
	if !reflect.DeepEqual(got.Protect.Metrics, wantProtectMetrics) {
		t.Fatalf("REL-007 metrics = %#v, want %#v", got.Protect.Metrics, wantProtectMetrics)
	}

	if got.SetNull.Returned != 1 || got.SetNull.Err != nil {
		t.Fatalf("REL-008 Delete() = (%d, %v), want (1, nil)", got.SetNull.Returned, got.SetNull.Err)
	}
	if !reflect.DeepEqual(got.SetNull.CallerBefore, CallerState{ID: 2, Name: "Bob", KeyPresent: true}) ||
		!reflect.DeepEqual(got.SetNull.CallerAfter, CallerState{Name: "Bob"}) {
		t.Fatalf("REL-008 caller before/after = %#v/%#v", got.SetNull.CallerBefore, got.SetNull.CallerAfter)
	}
	if !reflect.DeepEqual(got.SetNull.After, setNullDatabaseState()) {
		t.Fatalf("REL-008 final database state = %#v, want %#v", got.SetNull.After, setNullDatabaseState())
	}
	wantSetNullMetrics := DeleteMetrics{
		TransactionCount:     1,
		QueryCount:           1,
		RelationSetNullCount: 1,
		DeleteCount:          1,
		OperationOrder:       []string{OperationQuery, OperationRelationSetNull, OperationDelete},
		MutationOrder:        []string{OperationRelationSetNull, OperationDelete},
		MutationRows: []MutationRow{
			{Kind: OperationRelationSetNull, AffectedRows: 2},
			{Kind: OperationDelete, AffectedRows: 1},
		},
		RelationSetNullRows: []int64{2},
		DeleteRows:          []int64{1},
	}
	if !reflect.DeepEqual(got.SetNull.Metrics, wantSetNullMetrics) {
		t.Fatalf("REL-008 metrics = %#v, want %#v", got.SetNull.Metrics, wantSetNullMetrics)
	}

	wantPhysical := PhysicalSchema{
		ForeignKeysEnabled: 1,
		ForeignKeys: []ForeignKeyShape{
			{From: "author_id", ToTable: "authors_author", ToColumn: "id", OnDelete: "RESTRICT"},
			{From: "reviewer_id", ToTable: "authors_author", ToColumn: "id", OnDelete: "NO ACTION"},
		},
		ReviewerNullable: true,
	}
	if !reflect.DeepEqual(got.Protect.Schema, wantPhysical) || !reflect.DeepEqual(got.SetNull.Schema, wantPhysical) {
		t.Fatalf("physical schemas = protect %#v set-null %#v, want %#v", got.Protect.Schema, got.SetNull.Schema, wantPhysical)
	}
}

func TestREL007REL008PhysicalSchemaFalseGreenMutationsAreRejected(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		mutate func(*fixtureConfig)
	}{
		{name: "schema-level set null", mutate: func(config *fixtureConfig) { config.reviewerDeleteAction = "SET NULL" }},
		{name: "author cascade", mutate: func(config *fixtureConfig) { config.authorDeleteAction = "CASCADE" }},
		{name: "missing author foreign key", mutate: func(config *fixtureConfig) { config.omitAuthorForeignKey = true }},
		{name: "missing reviewer foreign key", mutate: func(config *fixtureConfig) { config.omitReviewerForeignKey = true }},
		{name: "trigger side effect", mutate: func(config *fixtureConfig) { config.addBlogTrigger = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := defaultFixtureConfig()
			test.mutate(&config)
			if _, err := observeDelete(context.Background(), 2, config); err == nil {
				t.Fatal("physical-schema mutation published a successful REL-008 observation")
			}
		})
	}
}

func TestREL008AdversarialDeleteFailureRollsBackAndPreservesCaller(t *testing.T) {
	t.Parallel()

	config := defaultFixtureConfig()
	config.addExternalProtect = true
	got, err := observeDelete(context.Background(), 2, config)
	if err != nil {
		t.Fatalf("collect adversarial REL-008 observation: %v", err)
	}
	if got.Returned != 0 || got.Err == nil {
		t.Fatalf("adversarial REL-008 Delete() = (%d, %v), want (0, error)", got.Returned, got.Err)
	}
	if errors.Is(got.Err, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeProtectedForeignKey}) {
		t.Fatalf("adversarial external DB constraint was misreported as declared PROTECT: %v", got.Err)
	}
	if !reflect.DeepEqual(got.CallerBefore, CallerState{ID: 2, Name: "Bob", KeyPresent: true}) ||
		!reflect.DeepEqual(got.CallerAfter, got.CallerBefore) {
		t.Fatalf("adversarial REL-008 caller before/after = %#v/%#v", got.CallerBefore, got.CallerAfter)
	}
	if !reflect.DeepEqual(got.Before, initialDatabaseState()) || !reflect.DeepEqual(got.After, got.Before) {
		t.Fatalf("adversarial REL-008 database before/after = %#v/%#v", got.Before, got.After)
	}
	wantMetrics := DeleteMetrics{
		TransactionCount:     1,
		QueryCount:           1,
		RelationSetNullCount: 1,
		DeleteCount:          1,
		OperationOrder:       []string{OperationQuery, OperationRelationSetNull, OperationDelete},
		MutationOrder:        []string{OperationRelationSetNull, OperationDelete},
		MutationRows: []MutationRow{
			{Kind: OperationRelationSetNull, AffectedRows: 2},
			{Kind: OperationDelete, AffectedRows: 0},
		},
		RelationSetNullRows: []int64{2},
		DeleteRows:          []int64{0},
	}
	if !reflect.DeepEqual(got.Metrics, wantMetrics) {
		t.Fatalf("adversarial REL-008 metrics = %#v, want %#v", got.Metrics, wantMetrics)
	}
}

func TestGeneratedAppsHaveNoAppEdgesAndObserverIsOracleBlind(t *testing.T) {
	t.Parallel()

	root := relationDeleteProductDirectory(t)
	for _, directory := range []string{"authors", "blog"} {
		entries, err := os.ReadDir(filepath.Join(root, directory))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasPrefix(entry.Name(), "zz_godj_") || !strings.HasSuffix(entry.Name(), ".go") {
				continue
			}
			for _, imported := range parsedImports(t, filepath.Join(root, directory, entry.Name())) {
				if strings.Contains(imported, "/relationdeleteproduct/authors") || strings.Contains(imported, "/relationdeleteproduct/blog") {
					t.Fatalf("generated app file %s/%s has app-to-app import %q", directory, entry.Name(), imported)
				}
			}
		}
	}
	observerPath := filepath.Join(root, "observer.go")
	for _, imported := range parsedImports(t, observerPath) {
		for _, forbidden := range []string{"/oracles/", "/static/", "/fixtures/", "relation-oracle", "not-implemented", "notimplemented"} {
			if strings.Contains(imported, forbidden) {
				t.Fatalf("relation-delete observer imports forbidden expected artifact %q", imported)
			}
		}
		if slices.Contains([]string{"embed", "io/fs", "os", "path/filepath"}, imported) {
			t.Fatalf("relation-delete observer imports file-reading package %q", imported)
		}
	}
	observerSource, err := os.ReadFile(observerPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"/oracles/", "/static/", "/fixtures/", "relation-oracle", "not-implemented", "notimplemented"} {
		if bytes.Contains(observerSource, []byte(forbidden)) {
			t.Fatalf("relation-delete observer source names forbidden expected artifact %q", forbidden)
		}
	}

	const rootImport = "github.com/progresshans/godj/conformance/relationdeleteproduct/"
	command := exec.Command("go", "list", "-json", rootImport+"authors", rootImport+"blog", rootImport+"project")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		t.Fatalf("go list relation-delete project: %v", err)
	}
	type listedPackage struct {
		ImportPath string
		Imports    []string
		Deps       []string
	}
	listed := make(map[string]listedPackage, 3)
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var candidate listedPackage
		if err := decoder.Decode(&candidate); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		listed[candidate.ImportPath] = candidate
	}
	authorsPackage := listed[rootImport+"authors"]
	blogPackage := listed[rootImport+"blog"]
	projectPackage := listed[rootImport+"project"]
	if slices.Contains(authorsPackage.Imports, rootImport+"blog") || slices.Contains(authorsPackage.Deps, rootImport+"blog") {
		t.Fatalf("authors app reaches blog: imports=%#v deps=%#v", authorsPackage.Imports, authorsPackage.Deps)
	}
	if slices.Contains(blogPackage.Imports, rootImport+"authors") || slices.Contains(blogPackage.Deps, rootImport+"authors") {
		t.Fatalf("blog app reaches authors: imports=%#v deps=%#v", blogPackage.Imports, blogPackage.Deps)
	}
	for _, app := range []string{rootImport + "authors", rootImport + "blog"} {
		if !slices.Contains(projectPackage.Imports, app) || !slices.Contains(projectPackage.Deps, app) {
			t.Fatalf("project companion does not own app edge %q: imports=%#v deps=%#v", app, projectPackage.Imports, projectPackage.Deps)
		}
	}
}

func TestRelationDeleteDeclarationRunnerBootstrapsMissingAndBrokenGeneratedOutputs(t *testing.T) {
	t.Parallel()
	root := relationDeleteProductDirectory(t)
	repository := filepath.Clean(filepath.Join(root, "..", ".."))
	const rootImport = "github.com/progresshans/godj/conformance/relationdeleteproduct/"
	list := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", rootImport+"cmd/projectrunner")
	list.Dir = repository
	list.Env = relationDeleteOfflineGoEnvironment()
	output, err := list.CombinedOutput()
	if err != nil {
		t.Fatalf("list relation-delete declaration runner: %v\n%s", err, output)
	}
	dependencies := strings.Fields(string(output))
	for _, forbidden := range []string{rootImport + "authors", rootImport + "blog", rootImport + "project"} {
		if slices.Contains(dependencies, forbidden) {
			t.Fatalf("relation-delete declaration runner imports generated package %s", forbidden)
		}
	}

	generated := make([]string, 0, len(relationDeleteBundleRoster))
	for _, path := range relationDeleteBundleRoster {
		generated = append(generated, filepath.Join(root, filepath.FromSlash(path)))
	}
	t.Run("missing", func(t *testing.T) {
		replacements := make(map[string]string, len(generated))
		for _, path := range generated {
			replacements[path] = ""
		}
		assertRelationDeleteRunnerBuildAndSpec(t, repository, replacements)
	})
	t.Run("broken", func(t *testing.T) {
		broken := filepath.Join(t.TempDir(), "broken.go")
		if err := os.WriteFile(broken, []byte("package authors\nfunc broken( {\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		assertRelationDeleteRunnerBuildAndSpec(t, repository, map[string]string{generated[0]: broken})
	})
}

func assertRelationDeleteRunnerBuildAndSpec(t *testing.T, repository string, replacements map[string]string) {
	t.Helper()
	overlay := filepath.Join(t.TempDir(), "overlay.json")
	document, err := json.Marshal(struct {
		Replace map[string]string `json:"Replace"`
	}{Replace: replacements})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(overlay, document, 0o644); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "projectrunner")
	build := exec.Command("go", "build", "-overlay="+overlay, "-o", binary, "./conformance/relationdeleteproduct/cmd/projectrunner")
	build.Dir = repository
	build.Env = relationDeleteOfflineGoEnvironment()
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build relation-delete declaration runner: %v\n%s", err, output)
	}

	command := exec.CommandContext(context.Background(), binary, projectgenerateprotocol.PrivateArgument)
	command.Stdin = bytes.NewReader(projectgenerateprotocol.RequestDocument())
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("run relation-delete declaration runner: %v; stderr=%q", err, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("relation-delete declaration runner stderr = %q", stderr.String())
	}
	response, failure, failed := projectgenerateprotocol.ParseResponse(stdout.Bytes(), true)
	if failed || failure != (projectgenerateprotocol.Failure{}) || !response.OK || len(response.ProjectSpec.Apps) != 2 {
		t.Fatalf("relation-delete declaration response = %+v, failure=%+v, failed=%v", response, failure, failed)
	}
}

func relationDeleteOfflineGoEnvironment() []string {
	result := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "GOWORK=") || strings.HasPrefix(entry, "GOPROXY=") {
			continue
		}
		result = append(result, entry)
	}
	return append(result, "GOWORK=off", "GOPROXY=off")
}

func initialDatabaseState() DatabaseState {
	reviewer := int64(2)
	return DatabaseState{
		Authors: []AuthorRow{{ID: 1, Name: "Ada"}, {ID: 2, Name: "Bob"}, {ID: 3, Name: "Cleo"}},
		Posts: []PostRow{
			{ID: 10, Title: "Alpha", AuthorID: 1, ReviewerID: &reviewer},
			{ID: 11, Title: "Beta", AuthorID: 1},
			{ID: 12, Title: "Gamma", AuthorID: 3, ReviewerID: &reviewer},
		},
	}
}

func setNullDatabaseState() DatabaseState {
	return DatabaseState{
		Authors: []AuthorRow{{ID: 1, Name: "Ada"}, {ID: 3, Name: "Cleo"}},
		Posts: []PostRow{
			{ID: 10, Title: "Alpha", AuthorID: 1},
			{ID: 11, Title: "Beta", AuthorID: 1},
			{ID: 12, Title: "Gamma", AuthorID: 3},
		},
	}
}

func openProvisionedFacadeFixture(t *testing.T) (context.Context, *relationFixture) {
	t.Helper()

	ctx := context.Background()
	product, err := openFixture(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := product.close(); err != nil {
			t.Errorf("close project facade fixture: %v", err)
		}
	})
	if err := provision(ctx, product.backend, defaultFixtureConfig()); err != nil {
		t.Fatal(err)
	}
	return ctx, product
}

func assertFacadeQueryDelta(t *testing.T, product *relationFixture, before, want uint64, operation string) {
	t.Helper()

	got := product.backend.QueryCount() - before
	if got != want {
		t.Fatalf("%s query delta = %d, want %d", operation, got, want)
	}
}

func facadePostByID(t *testing.T, posts []*project.BlogPost, identifier int64) *project.BlogPost {
	t.Helper()

	for _, post := range posts {
		raw, err := post.Unwrap()
		if err != nil {
			t.Fatalf("unwrap eager BlogPost: %v", err)
		}
		if raw.ID == identifier {
			return post
		}
	}
	t.Fatalf("eager BlogPost %d is absent", identifier)
	return nil
}

func parsedImports(t *testing.T, path string) []string {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	imports := make([]string, len(parsed.Imports))
	for index, imported := range parsed.Imports {
		imports[index] = strings.Trim(imported.Path.Value, `"`)
	}
	return imports
}

func assertRelationDeleteBundlesEqual(t *testing.T, left, right codegen.GeneratedBundle) {
	t.Helper()
	if left.SnapshotSHA256() != right.SnapshotSHA256() || !bytes.Equal(left.Manifest(), right.Manifest()) {
		t.Fatal("project bundle snapshot or manifest is nondeterministic")
	}
	leftFiles := left.Files()
	rightFiles := right.Files()
	if len(leftFiles) != len(rightFiles) {
		t.Fatalf("project bundle file counts differ: %d != %d", len(leftFiles), len(rightFiles))
	}
	for index := range leftFiles {
		if leftFiles[index].Path != rightFiles[index].Path ||
			leftFiles[index].Owner != rightFiles[index].Owner ||
			leftFiles[index].SHA256 != rightFiles[index].SHA256 ||
			leftFiles[index].Mode != rightFiles[index].Mode ||
			!bytes.Equal(leftFiles[index].Source(), rightFiles[index].Source()) {
			t.Fatalf("project bundle file %d is nondeterministic", index)
		}
	}
}

func bundleFileSource(t *testing.T, bundle codegen.GeneratedBundle, path string) []byte {
	t.Helper()
	for _, file := range bundle.Files() {
		if file.Path == path {
			return file.Source()
		}
	}
	t.Fatalf("bundle file %s is absent", path)
	return nil
}

func relationDeleteProductDirectory(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate relation-delete product test source")
	}
	return filepath.Dir(source)
}
