package compiletest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/conformance/relationdeleteproduct/fixture"
	"github.com/progresshans/godj/internal/projectgenerate"
)

const modulePath = "github.com/progresshans/godj"

const (
	relationFacadePhysicalBytes   = 171929
	relationFacadePhysicalDigest  = "c47cb33a2d426ce14122208e3498f6884ffb84ef96538af500f2b89bf7bfb1a0"
	relationFacadeGeneratedBytes  = 79814
	relationFacadeGeneratedDigest = "c7ddaf0f760987b71f743dab51dfc8b7af842031529999a8dd1d9d1cd246fd13"
)

var relationFacadePhysicalFiles = []string{
	".godj/generated-manifest.json",
	"authors/zz_godj_generated.go",
	"authors/zz_godj_relation.go",
	"authors/zz_godj_relation_object.go",
	"authors/zz_godj_relation_projection.go",
	"blog/zz_godj_generated.go",
	"blog/zz_godj_relation.go",
	"blog/zz_godj_relation_object.go",
	"blog/zz_godj_relation_projection.go",
	"cmd/projectrunner/main.go",
	"fixture/schema.go",
	"godj.toml",
	"observer.go",
	"product_test.go",
	"project/zz_godj_bindings.go",
	"project/zz_godj_relation_delete.go",
	"project/zz_godj_relation_facade.go",
	"project/zz_godj_relation_object.go",
	"project/zz_godj_relation_prefetch.go",
	"project/zz_godj_relation_query.go",
	"project/zz_godj_relation_reverse.go",
	"project/zz_godj_relation_select_related.go",
}

var relationFacadePhysicalDirectories = []string{
	".",
	".godj",
	"authors",
	"blog",
	"cmd",
	"cmd/projectrunner",
	"fixture",
	"project",
}

var relationFacadeGeneratedFiles = []string{
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

var relationFacadeCommandViewFiles = []string{
	"authors/zz_godj_generated.go",
	"authors/zz_godj_relation.go",
	"authors/zz_godj_relation_object.go",
	"authors/zz_godj_relation_projection.go",
	"blog/zz_godj_generated.go",
	"blog/zz_godj_relation.go",
	"blog/zz_godj_relation_object.go",
	"blog/zz_godj_relation_projection.go",
	"fixture/schema.go",
	"observer.go",
	"product_test.go",
	"project/zz_godj_bindings.go",
	"project/zz_godj_relation_delete.go",
	"project/zz_godj_relation_facade.go",
	"project/zz_godj_relation_object.go",
	"project/zz_godj_relation_prefetch.go",
	"project/zz_godj_relation_query.go",
	"project/zz_godj_relation_reverse.go",
	"project/zz_godj_relation_select_related.go",
}

func TestExternalConsumerCompiles(t *testing.T) {
	for _, fixture := range []string{
		"external_consumer.go.txt",
		"write_external_consumer.go.txt",
		"save_external_consumer.go.txt",
		"migration_external_consumer.go.txt",
		"migration_relation_external_consumer.go.txt",
		"migration_definition_external_consumer.go.txt",
		"project_external_consumer.go.txt",
		"relation_project/external_consumer.go.txt",
		"relation_query/external_consumer.go.txt",
		"relation_object/external_consumer.go.txt",
		"relation_reverse/external_consumer.go.txt",
		"relation_prefetch/external_consumer.go.txt",
		"relation_select_related/external_consumer.go.txt",
		"relation_delete/backend_external_consumer.go.txt",
		"relation_delete/generated_external_consumer.go.txt",
	} {
		result := compileFixture(t, fixture)
		if result.err != nil {
			t.Fatalf("external consumer %s did not compile: %v\n%s", fixture, result.err, result.output)
		}
	}

	verifyRelationFacadeProduction(t)
}

func verifyRelationFacadeProduction(t *testing.T) {
	t.Helper()

	root, err := filepath.EvalSymlinks(repositoryRoot(t))
	if err != nil {
		t.Fatalf("canonicalize repository root: %v", err)
	}
	testdataRoot, err := canonicalRelationFacadeTestdataRoot(root)
	if err != nil {
		t.Fatalf("validate relation facade testdata root: %v", err)
	}
	consumerBacking, err := canonicalRelationFacadeFixture(testdataRoot, "external_consumer.go.txt")
	if err != nil {
		t.Fatalf("validate relation facade external consumer backing: %v", err)
	}
	consumerSource, err := os.ReadFile(consumerBacking)
	if err != nil {
		t.Fatalf("read relation facade external consumer: %v", err)
	}
	if err := validateRelationFacadeConsumerSource(consumerSource); err != nil {
		t.Fatalf("validate relation facade consumer source: %v", err)
	}

	forbiddenConsumerMutations := []struct {
		label   string
		old     string
		new     string
		wantErr string
	}{
		{
			label:   "required clear surface",
			old:     "rawPost, err := post.Unwrap()",
			new:     "rawPost, err := post.ClearAuthor()",
			wantErr: `forbidden consumer selector "ClearAuthor"`,
		},
		{
			label:   "Delete surface",
			old:     "rawPost, err := post.Unwrap()",
			new:     "rawPost, err := post.Delete(ctx)",
			wantErr: `forbidden consumer selector "Delete"`,
		},
		{
			label:   "reverse surface",
			old:     "_, err = requiredTarget.Unwrap()",
			new:     "_, err = requiredTarget.Posts()",
			wantErr: `forbidden consumer selector "Posts"`,
		},
		{
			label:   "low-level Object exposure",
			old:     "var source *project.BlogPost = post",
			new:     "var source *project.BlogPostObject = post",
			wantErr: "forbidden consumer imported selector project.BlogPostObject",
		},
		{
			label:   "RelationAtomic capability",
			old:     "func compileSession(ctx context.Context, session db.Session) error {\n\tmodels, err := project.Using(session)",
			new:     "func compileSession(ctx context.Context, session db.Session) error {\n\tvar _ db.RelationAtomic\n\tmodels, err := project.Using(session)",
			wantErr: "forbidden consumer imported selector db.RelationAtomic",
		},
		{
			label:   "RelationMutator capability",
			old:     "func compileSession(ctx context.Context, session db.Session) error {\n\tmodels, err := project.Using(session)",
			new:     "func compileSession(ctx context.Context, session db.Session) error {\n\tvar _ db.RelationMutator\n\tmodels, err := project.Using(session)",
			wantErr: "forbidden consumer imported selector db.RelationMutator",
		},
	}
	for _, mutation := range forbiddenConsumerMutations {
		mutated := formatRelationFacadeMutation(t, replaceRelationFacadeToken(
			t,
			consumerSource,
			[]byte(mutation.old),
			[]byte(mutation.new),
		))
		if err := validateRelationFacadeConsumerSource(mutated); err == nil || !strings.Contains(err.Error(), mutation.wantErr) {
			t.Fatalf("%s AST mutation error = %v, want %q", mutation.label, err, mutation.wantErr)
		}
	}
	verifyRelationFacadeInventoryRejections(t)

	overlayBacking := filepath.Join(testdataRoot, "project_facade_spike.go.txt")
	requireRelationFacadeTargetAbsent(t, overlayBacking, "after production transition")

	fixtureRoot := filepath.Join(root, "conformance", "relationdeleteproduct")
	physicalBefore := readRelationFacadeInventory(t, fixtureRoot)
	verifyRelationFacadePhysicalInventory(t, physicalBefore)
	productSource := physicalBefore.files["project/zz_godj_relation_facade.go"]
	if err := validateRelationFacadeProductSource(productSource); err != nil {
		t.Fatalf("validate production relation facade exports: %v", err)
	}
	for _, mutation := range []struct {
		label string
		old   string
		new   string
	}{
		{
			label: "AuthorsAuthorQuery.New",
			old:   "func (_query AuthorsAuthorQuery) New(_value authors.Author) (*AuthorsAuthor, error)",
			new:   "func (_query AuthorsAuthorQuery) New(_value blog.Post) (*AuthorsAuthor, error)",
		},
		{
			label: "BlogPostQuery.New",
			old:   "func (_query BlogPostQuery) New(_value blog.Post) (*BlogPost, error)",
			new:   "func (_query BlogPostQuery) New(_value authors.Author) (*BlogPost, error)",
		},
		{
			label: "AuthorsAuthor.Save",
			old:   "func (_model *AuthorsAuthor) Save(_ctx context.Context) error",
			new:   "func (_model *AuthorsAuthor) Save(_ctx interface{}) error",
		},
		{
			label: "BlogPost.Save",
			old:   "func (_model *BlogPost) Save(_ctx context.Context) error",
			new:   "func (_model *BlogPost) Save(_ctx interface{}) error",
		},
		{
			label: "BlogPost.WithAuthor",
			old:   "func (_model *BlogPost) WithAuthor(_target *AuthorsAuthor) (*BlogPost, error)",
			new:   "func (_model *BlogPost) WithAuthor(_target *BlogPost) (*BlogPost, error)",
		},
		{
			label: "BlogPost.WithAuthorID",
			old:   "func (_model *BlogPost) WithAuthorID(_key int64) (*BlogPost, error)",
			new:   "func (_model *BlogPost) WithAuthorID(_key string) (*BlogPost, error)",
		},
		{
			label: "BlogPost.WithReviewer",
			old:   "func (_model *BlogPost) WithReviewer(_target *AuthorsAuthor) (*BlogPost, error)",
			new:   "func (_model *BlogPost) WithReviewer(_target *BlogPost) (*BlogPost, error)",
		},
		{
			label: "BlogPost.WithReviewerID",
			old:   "func (_model *BlogPost) WithReviewerID(_key int64) (*BlogPost, error)",
			new:   "func (_model *BlogPost) WithReviewerID(_key string) (*BlogPost, error)",
		},
		{
			label: "BlogPost.ClearReviewer",
			old:   "func (_model *BlogPost) ClearReviewer() (*BlogPost, error)",
			new:   "func (_model *BlogPost) ClearReviewer(_clear bool) (*BlogPost, error)",
		},
		{
			label: "AuthorsAuthorQuery.Distinct",
			old:   "func (_query AuthorsAuthorQuery) Distinct() AuthorsAuthorQuery",
			new:   "func (_query AuthorsAuthorQuery) Distinct() BlogPostQuery",
		},
		{
			label: "AuthorsAuthorQuery.Offset",
			old:   "func (_query AuthorsAuthorQuery) Offset(_offset int) (AuthorsAuthorQuery, error)",
			new:   "func (_query AuthorsAuthorQuery) Offset(_offset int64) (AuthorsAuthorQuery, error)",
		},
		{
			label: "AuthorsAuthorQuery.Count",
			old:   "func (_query AuthorsAuthorQuery) Count(_ctx context.Context) (int64, error)",
			new:   "func (_query AuthorsAuthorQuery) Count(_ctx context.Context) (int, error)",
		},
		{
			label: "SelectAuthorsAuthorInto",
			old:   "func SelectAuthorsAuthorInto[R any](_ctx context.Context, _source AuthorsAuthorQuery, _projection orm.Projection[authors.Author, R]) ([]R, error)",
			new:   "func SelectAuthorsAuthorInto[R any](_ctx context.Context, _source AuthorsAuthorQuery, _projection orm.Projection[blog.Post, R]) ([]R, error)",
		},
		{
			label: "AggregateBlogPostInto",
			old:   "func AggregateBlogPostInto[R any](_ctx context.Context, _source BlogPostQuery, _aggregate orm.Aggregate[blog.Post, R]) (R, error)",
			new:   "func AggregateBlogPostInto[R any](_ctx context.Context, _source BlogPostQuery, _aggregate orm.Aggregate[authors.Author, R]) (R, error)",
		},
	} {
		mutated := replaceRelationFacadeToken(t, productSource, []byte(mutation.old), []byte(mutation.new))
		if err := validateRelationFacadeProductSource(mutated); err == nil || !strings.Contains(err.Error(), "signature") {
			t.Fatalf("%s ABI mutation error = %v, want exact signature rejection", mutation.label, err)
		}
	}
	verifyRelationFacadeProductionGoList(t, root)

	directory := t.TempDir()
	goMod := fmt.Sprintf(`module example.com/godj-relation-facade-consumer

go 1.26.0

require %s v0.0.0

replace %s => %s
`, modulePath, modulePath, filepath.ToSlash(root))
	if err := os.WriteFile(filepath.Join(directory, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatalf("write relation facade go.mod: %v", err)
	}
	consumerPath := filepath.Join(directory, "consumer.go")
	if err := os.WriteFile(consumerPath, consumerSource, 0o644); err != nil {
		t.Fatalf("write relation facade consumer: %v", err)
	}

	productOutput := filepath.Join(directory, "relationdeleteproduct.test")
	productCompile := compileRelationFacadeProduct(t, root, productOutput)
	if productCompile.err != nil {
		t.Fatalf("relation facade physical current bundle product did not compile: %v\n%s", productCompile.err, productCompile.output)
	}
	productInfo, err := os.Lstat(productOutput)
	if err != nil {
		t.Fatalf("lstat relation facade compile-only product: %v", err)
	}
	if !productInfo.Mode().IsRegular() {
		t.Fatalf("relation facade compile-only product mode = %s, want regular file", productInfo.Mode())
	}

	positive := compileRelationFacadeConsumer(t, directory, "production.test")
	if positive.err != nil {
		t.Fatalf("production relation facade consumer did not compile without overlay: %v\n%s", positive.err, positive.output)
	}

	queryerMutation := formatRelationFacadeMutation(t, replaceRelationFacadeToken(
		t,
		consumerSource,
		[]byte("func compileRelationFacade(ctx context.Context, backend project.Backend) error"),
		[]byte("func compileRelationFacade(ctx context.Context, backend db.Queryer) error"),
	))
	verifyRelationFacadeCompileNegative(
		t,
		directory,
		consumerPath,
		queryerMutation,
		"queryer-negative.test",
		[]string{"db.Queryer", "project.Backend"},
	)

	predicateMutation := replaceRelationFacadeToken(
		t,
		consumerSource,
		[]byte(`blog.PostFields.Title.IContains("lazy")`),
		[]byte(`authors.AuthorFields.Name.IContains("lazy")`),
	)
	verifyRelationFacadeCompileNegative(
		t,
		directory,
		consumerPath,
		predicateMutation,
		"predicate-negative.test",
		[]string{"orm.Predicate[authors.Author]", "orm.Predicate[blog.Post]"},
	)

	orderingMutation := replaceRelationFacadeToken(
		t,
		consumerSource,
		[]byte("ordered := filtered.OrderBy(blog.PostFields.ID.Asc())"),
		[]byte("ordered := filtered.OrderBy(authors.AuthorFields.ID.Asc())"),
	)
	verifyRelationFacadeCompileNegative(
		t,
		directory,
		consumerPath,
		orderingMutation,
		"ordering-negative.test",
		[]string{"orm.Ordering[authors.Author]", "orm.Ordering[blog.Post]"},
	)

	selectorMutation := replaceRelationFacadeToken(
		t,
		consumerSource,
		[]byte("SelectRelated(models.BlogPost.Related.Author)"),
		[]byte("SelectRelated(blog.PostFields.ID)"),
	)
	verifyRelationFacadeCompileNegative(
		t,
		directory,
		consumerPath,
		selectorMutation,
		"selector-negative.test",
		[]string{"project.BlogPostRelationSelector", "orm.IntegerField[blog.Post]"},
	)

	clearAuthorMutation := formatRelationFacadeMutation(t, replaceRelationFacadeToken(
		t,
		consumerSource,
		[]byte("rawPost, err := post.Unwrap()"),
		[]byte("rawPost, err := post.ClearAuthor()"),
	))
	verifyRelationFacadeCompileNegative(
		t,
		directory,
		consumerPath,
		clearAuthorMutation,
		"clear-author-negative.test",
		[]string{"post.ClearAuthor undefined", "*project.BlogPost"},
	)

	deleteMutation := formatRelationFacadeMutation(t, replaceRelationFacadeToken(
		t,
		consumerSource,
		[]byte("rawPost, err := post.Unwrap()"),
		[]byte("rawPost, err := post.Delete(ctx)"),
	))
	verifyRelationFacadeCompileNegative(
		t,
		directory,
		consumerPath,
		deleteMutation,
		"delete-negative.test",
		[]string{"post.Delete undefined", "*project.BlogPost"},
	)

	reverseMutation := formatRelationFacadeMutation(t, replaceRelationFacadeToken(
		t,
		consumerSource,
		[]byte("_, err = requiredTarget.Unwrap()"),
		[]byte("_, err = requiredTarget.Posts()"),
	))
	verifyRelationFacadeCompileNegative(
		t,
		directory,
		consumerPath,
		reverseMutation,
		"reverse-negative.test",
		[]string{"requiredTarget.Posts undefined", "*project.AuthorsAuthor"},
	)

	objectMutation := formatRelationFacadeMutation(t, replaceRelationFacadeToken(
		t,
		consumerSource,
		[]byte("var source *project.BlogPost = post"),
		[]byte("var source *project.BlogPostObject = post"),
	))
	verifyRelationFacadeCompileNegative(
		t,
		directory,
		consumerPath,
		objectMutation,
		"object-negative.test",
		[]string{"*project.BlogPost", "*project.BlogPostObject"},
	)

	newValueMutation := replaceRelationFacadeToken(
		t,
		consumerSource,
		[]byte(`models.BlogPost.New(blog.Post{Title: "new post"})`),
		[]byte(`models.BlogPost.New(authors.Author{Name: "new post"})`),
	)
	verifyRelationFacadeCompileNegative(
		t,
		directory,
		consumerPath,
		newValueMutation,
		"new-value-negative.test",
		[]string{"authors.Author", "blog.Post"},
	)

	targetMutation := replaceRelationFacadeToken(
		t,
		consumerSource,
		[]byte("newPost, err = newPost.WithAuthor(newAuthor)"),
		[]byte("newPost, err = newPost.WithAuthor(newPost)"),
	)
	verifyRelationFacadeCompileNegative(
		t,
		directory,
		consumerPath,
		targetMutation,
		"relation-target-negative.test",
		[]string{"*project.BlogPost", "*project.AuthorsAuthor"},
	)

	identifierMutation := replaceRelationFacadeToken(
		t,
		consumerSource,
		[]byte("newPost, err = newPost.WithReviewerID(2)"),
		[]byte(`newPost, err = newPost.WithReviewerID("2")`),
	)
	verifyRelationFacadeCompileNegative(
		t,
		directory,
		consumerPath,
		identifierMutation,
		"relation-id-negative.test",
		[]string{"untyped string", "int64"},
	)

	if err := os.WriteFile(consumerPath, consumerSource, 0o644); err != nil {
		t.Fatalf("restore relation facade consumer: %v", err)
	}
	physicalAfter := readRelationFacadeInventory(t, fixtureRoot)
	verifyRelationFacadePhysicalInventory(t, physicalAfter)
	if !equalRelationFacadeFiles(physicalBefore.files, physicalAfter.files) {
		t.Fatal("relation facade physical fixture bytes changed during no-overlay compile")
	}
	afterConsumerBacking, err := canonicalRelationFacadeFixture(testdataRoot, "external_consumer.go.txt")
	if err != nil {
		t.Fatalf("revalidate relation facade external consumer backing: %v", err)
	}
	if afterConsumerBacking != consumerBacking {
		t.Fatalf("relation facade consumer backing path changed during compile: %q, want %q", afterConsumerBacking, consumerBacking)
	}
	requireRelationFacadeTargetAbsent(t, overlayBacking, "after no-overlay compiles")
}
func canonicalRelationFacadeTestdataRoot(root string) (string, error) {
	expected := filepath.Clean(filepath.Join(root, "internal", "compiletest", "testdata", "relation_facade"))
	info, err := os.Lstat(expected)
	if err != nil {
		return "", fmt.Errorf("lstat %s: %w", expected, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("relation facade testdata root is a symlink: %s", expected)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("relation facade testdata root mode = %s, want directory", info.Mode())
	}
	canonical, err := filepath.EvalSymlinks(expected)
	if err != nil {
		return "", fmt.Errorf("canonicalize %s: %w", expected, err)
	}
	if canonical != expected {
		return "", fmt.Errorf("relation facade testdata root canonical path = %s, want exact %s", canonical, expected)
	}
	return canonical, nil
}

func canonicalRelationFacadeFixture(testdataRoot, name string) (string, error) {
	if name == "" || filepath.Base(name) != name || name == "." || name == ".." {
		return "", fmt.Errorf("invalid relation facade fixture name %q", name)
	}
	expected := filepath.Clean(filepath.Join(testdataRoot, name))
	info, err := os.Lstat(expected)
	if err != nil {
		return "", fmt.Errorf("lstat %s: %w", expected, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("relation facade fixture is a symlink: %s", expected)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("relation facade fixture mode = %s, want regular file: %s", info.Mode(), expected)
	}
	canonical, err := filepath.EvalSymlinks(expected)
	if err != nil {
		return "", fmt.Errorf("canonicalize %s: %w", expected, err)
	}
	relative, err := filepath.Rel(testdataRoot, canonical)
	if err != nil {
		return "", fmt.Errorf("confine relation facade fixture %s: %w", canonical, err)
	}
	if filepath.IsAbs(relative) || relative != name || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("relation facade fixture canonical path %s escapes exact testdata root %s", canonical, testdataRoot)
	}
	if canonical != expected {
		return "", fmt.Errorf("relation facade fixture canonical path = %s, want exact %s", canonical, expected)
	}
	return canonical, nil
}

func compileRelationFacadeConsumer(t *testing.T, directory, outputName string) compileResult {
	t.Helper()

	arguments := []string{"test", "-c", "-mod=mod", "-o", filepath.Join(directory, outputName), "."}
	command := exec.CommandContext(t.Context(), "go", arguments...)
	command.Dir = directory
	command.Env = commandEnvironment()
	output, err := command.CombinedOutput()
	return compileResult{output: string(output), err: err}
}

func compileRelationFacadeProduct(t *testing.T, root, outputPath string) compileResult {
	t.Helper()

	command := exec.CommandContext(
		t.Context(),
		"go",
		"test",
		"-c",
		"-mod=readonly",
		"-o",
		outputPath,
		modulePath+"/conformance/relationdeleteproduct",
	)
	command.Dir = root
	command.Env = commandEnvironment()
	output, err := command.CombinedOutput()
	return compileResult{output: string(output), err: err}
}

func verifyRelationFacadeCompileNegative(
	t *testing.T,
	directory string,
	consumerPath string,
	source []byte,
	outputName string,
	wantFragments []string,
) {
	t.Helper()

	if err := os.WriteFile(consumerPath, source, 0o644); err != nil {
		t.Fatalf("write relation facade %s source: %v", outputName, err)
	}
	result := compileRelationFacadeConsumer(t, directory, outputName)
	if result.err == nil {
		t.Fatalf("relation facade %s unexpectedly compiled", outputName)
	}
	for _, fragment := range wantFragments {
		if !strings.Contains(result.output, fragment) {
			t.Fatalf("relation facade %s diagnostics do not contain %q:\n%s", outputName, fragment, result.output)
		}
	}
}

type relationFacadeInventory struct {
	files  map[string][]byte
	names  []string
	bytes  int
	digest string
}

func readRelationFacadeInventory(t *testing.T, root string) relationFacadeInventory {
	t.Helper()

	inventory, err := loadRelationFacadeInventory(root, relationFacadePhysicalDirectories, relationFacadePhysicalFiles)
	if err != nil {
		t.Fatalf("read relation facade physical inventory: %v", err)
	}
	return inventory
}

func loadRelationFacadeInventory(root string, wantDirectories, wantFiles []string) (relationFacadeInventory, error) {
	directories := make(map[string]bool, len(wantDirectories))
	for _, name := range wantDirectories {
		directories[name] = false
	}
	files := make(map[string][]byte, len(wantFiles))
	expectedFiles := make(map[string]bool, len(wantFiles))
	for _, name := range wantFiles {
		expectedFiles[name] = false
	}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("relation facade physical fixture contains symlink %s", path)
		}
		switch relative {
		case ".godj/generate.lock", ".godj/publication-journal.json":
			info, err := entry.Info()
			if err != nil {
				return fmt.Errorf("inspect relation facade lifecycle control %s: %w", relative, err)
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("relation facade lifecycle control %s is not a regular file (%s)", relative, info.Mode())
			}
			return nil
		case ".godj/transactions":
			if !entry.IsDir() {
				return fmt.Errorf("relation facade lifecycle control %s is not a directory", relative)
			}
			return filepath.SkipDir
		}
		if entry.IsDir() {
			if _, expected := directories[relative]; !expected {
				return fmt.Errorf("relation facade physical fixture contains unexpected directory %s", relative)
			}
			directories[relative] = true
			return nil
		}
		if err := validateRelationFacadeInventoryEntry(path, entry); err != nil {
			return err
		}
		if _, expected := expectedFiles[relative]; !expected {
			return fmt.Errorf("relation facade physical fixture contains unexpected Go entry %s", relative)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[relative] = content
		expectedFiles[relative] = true
		return nil
	})
	if err != nil {
		return relationFacadeInventory{}, err
	}
	for name, seen := range directories {
		if !seen {
			return relationFacadeInventory{}, fmt.Errorf("relation facade physical fixture is missing directory %s", name)
		}
	}
	for name, seen := range expectedFiles {
		if !seen {
			return relationFacadeInventory{}, fmt.Errorf("relation facade physical fixture is missing Go entry %s", name)
		}
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	contentBytes, digest := digestRelationFacadeFiles(files)
	return relationFacadeInventory{files: files, names: names, bytes: contentBytes, digest: digest}, nil
}

func TestRelationFacadePhysicalInventoryExcludesOnlyGenerationLifecycleControls(t *testing.T) {
	repository := repositoryRoot(t)
	fixtureRoot := filepath.Join(repository, "conformance", "relationdeleteproduct")
	baseline := readRelationFacadeInventory(t, fixtureRoot)

	root := t.TempDir()
	for _, directory := range relationFacadePhysicalDirectories {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(directory)), 0o755); err != nil {
			t.Fatalf("create relation facade inventory directory %s: %v", directory, err)
		}
	}
	for _, name := range relationFacadePhysicalFiles {
		filename := filepath.Join(root, filepath.FromSlash(name))
		if err := os.WriteFile(filename, baseline.files[name], 0o644); err != nil {
			t.Fatalf("copy relation facade inventory file %s: %v", name, err)
		}
	}

	spec, err := fixture.ProjectSpec(context.Background())
	if err != nil {
		t.Fatalf("load relation facade ProjectSpec: %v", err)
	}
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatalf("generate relation facade project bundle: %v", err)
	}
	verifierCalls := 0
	err = projectgenerate.Publish(
		context.Background(),
		root,
		bundle,
		projectgenerate.CandidateVerifyFunc(func(context.Context, string) error {
			verifierCalls++
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("publish unchanged relation facade bundle: %v", err)
	}
	if verifierCalls != 0 {
		t.Fatalf("unchanged relation facade publication verifier calls = %d, want 0", verifierCalls)
	}
	for _, control := range []struct {
		path      string
		directory bool
	}{
		{path: ".godj/generate.lock"},
		{path: ".godj/transactions", directory: true},
	} {
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(control.path)))
		if err != nil || info.IsDir() != control.directory || (!control.directory && !info.Mode().IsRegular()) {
			t.Fatalf("successful publication control %s = %v, %v", control.path, info, err)
		}
	}
	afterPublish, err := loadRelationFacadeInventory(root, relationFacadePhysicalDirectories, relationFacadePhysicalFiles)
	if err != nil {
		t.Fatalf("inventory after successful publication: %v", err)
	}
	assertRelationFacadeInventoryEqual(t, baseline, afterPublish, "successful publication controls")
	report, err := projectgenerate.Check(context.Background(), root, bundle)
	if err != nil || !report.Clean() {
		t.Fatalf("generate check with persistent successful controls: report=%#v error=%v", report, err)
	}

	journalPath := filepath.Join(root, ".godj", "publication-journal.json")
	if err := os.WriteFile(journalPath, []byte("interrupted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	transactionPath := filepath.Join(root, ".godj", "transactions", "interrupted", "stage")
	if err := os.MkdirAll(transactionPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(transactionPath, "candidate"), []byte("partial\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	afterInterruption, err := loadRelationFacadeInventory(root, relationFacadePhysicalDirectories, relationFacadePhysicalFiles)
	if err != nil {
		t.Fatalf("inventory with interrupted publication controls: %v", err)
	}
	assertRelationFacadeInventoryEqual(t, baseline, afterInterruption, "interrupted publication controls")
	report, err = projectgenerate.Check(context.Background(), root, bundle)
	if !errors.Is(err, projectgenerate.ErrGeneratedDrift) || !errors.Is(err, projectgenerate.ErrPublicationInterrupted) || !report.Interrupted || report.Clean() {
		t.Fatalf("generate check interrupted publication: report=%#v error=%v", report, err)
	}
	for _, path := range []string{".godj/publication-journal.json", ".godj/transactions/interrupted"} {
		if !relationFacadeHasDrift(report.Drifts, path, projectgenerate.DriftInterrupted) {
			t.Fatalf("generate check interrupted drifts = %#v, want %s", report.Drifts, path)
		}
	}

	if err := os.WriteFile(filepath.Join(root, ".godj", "rogue-state.json"), []byte("rogue\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRelationFacadeInventory(root, relationFacadePhysicalDirectories, relationFacadePhysicalFiles); err == nil || !strings.Contains(err.Error(), "unexpected non-Go entry") {
		t.Fatalf("inventory rogue .godj error = %v, want fail-closed non-Go rejection", err)
	}
}

func assertRelationFacadeInventoryEqual(t *testing.T, left, right relationFacadeInventory, label string) {
	t.Helper()
	if !slices.Equal(left.names, right.names) || left.bytes != right.bytes || left.digest != right.digest || !equalRelationFacadeFiles(left.files, right.files) {
		t.Fatalf("relation facade inventory changed under %s: before=%d/%s/%q after=%d/%s/%q", label, left.bytes, left.digest, left.names, right.bytes, right.digest, right.names)
	}
}

func relationFacadeHasDrift(drifts []projectgenerate.Drift, path string, kind projectgenerate.DriftKind) bool {
	for _, drift := range drifts {
		if drift.Path == path && drift.Kind == kind {
			return true
		}
	}
	return false
}

func validateRelationFacadeInventoryEntry(path string, entry fs.DirEntry) error {
	if entry.Type()&os.ModeSymlink != 0 {
		return fmt.Errorf("relation facade physical fixture contains symlink %s", path)
	}
	if entry.IsDir() {
		return nil
	}
	info, err := entry.Info()
	if err != nil {
		return fmt.Errorf("inspect relation facade physical entry %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("relation facade physical fixture contains non-regular entry %s (%s)", path, info.Mode())
	}
	if filepath.Ext(entry.Name()) != ".go" && entry.Name() != "godj.toml" && entry.Name() != "generated-manifest.json" {
		return fmt.Errorf("relation facade physical fixture contains unexpected non-Go entry %s", path)
	}
	return nil
}

func verifyRelationFacadeInventoryRejections(t *testing.T) {
	t.Helper()

	extraRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(extraRoot, "valid.go"), []byte("package fixture\n"), 0o644); err != nil {
		t.Fatalf("write strict-inventory Go entry: %v", err)
	}
	wantDirectories := []string{"."}
	wantFiles := []string{"valid.go"}
	if _, err := loadRelationFacadeInventory(extraRoot, wantDirectories, wantFiles); err != nil {
		t.Fatalf("strict inventory rejected regular Go entry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(extraRoot, "unexpected.txt"), []byte("unexpected\n"), 0o644); err != nil {
		t.Fatalf("write strict-inventory extra entry: %v", err)
	}
	if _, err := loadRelationFacadeInventory(extraRoot, wantDirectories, wantFiles); err == nil || !strings.Contains(err.Error(), "unexpected non-Go entry") {
		t.Fatalf("strict inventory extra-entry error = %v, want non-Go rejection", err)
	}

	extraGoRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(extraGoRoot, "valid.go"), []byte("package fixture\n"), 0o644); err != nil {
		t.Fatalf("write strict-inventory expected Go entry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(extraGoRoot, "extra.go"), []byte("package fixture\n"), 0o644); err != nil {
		t.Fatalf("write strict-inventory extra Go entry: %v", err)
	}
	if _, err := loadRelationFacadeInventory(extraGoRoot, wantDirectories, wantFiles); err == nil || !strings.Contains(err.Error(), "unexpected Go entry extra.go") {
		t.Fatalf("strict inventory extra-Go error = %v, want exact-entry rejection", err)
	}

	extraDirectoryRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(extraDirectoryRoot, "valid.go"), []byte("package fixture\n"), 0o644); err != nil {
		t.Fatalf("write strict-inventory directory fixture: %v", err)
	}
	if err := os.Mkdir(filepath.Join(extraDirectoryRoot, "unexpected"), 0o755); err != nil {
		t.Fatalf("write strict-inventory extra directory: %v", err)
	}
	if _, err := loadRelationFacadeInventory(extraDirectoryRoot, wantDirectories, wantFiles); err == nil || !strings.Contains(err.Error(), "unexpected directory unexpected") {
		t.Fatalf("strict inventory extra-directory error = %v, want exact-directory rejection", err)
	}

	for _, adversary := range []struct {
		name         string
		mode         fs.FileMode
		wantFragment string
	}{
		{name: "linked.go", mode: os.ModeSymlink, wantFragment: "contains symlink"},
		{name: "pipe.go", mode: os.ModeNamedPipe, wantFragment: "non-regular entry"},
	} {
		entry := relationFacadeAdversarialDirEntry{name: adversary.name, mode: adversary.mode}
		if err := validateRelationFacadeInventoryEntry(filepath.Join(extraRoot, adversary.name), entry); err == nil || !strings.Contains(err.Error(), adversary.wantFragment) {
			t.Fatalf("strict inventory %s error = %v, want %q", adversary.name, err, adversary.wantFragment)
		}
	}
}

type relationFacadeAdversarialDirEntry struct {
	name string
	mode fs.FileMode
}

func (entry relationFacadeAdversarialDirEntry) Name() string               { return entry.name }
func (entry relationFacadeAdversarialDirEntry) IsDir() bool                { return entry.mode.IsDir() }
func (entry relationFacadeAdversarialDirEntry) Type() fs.FileMode          { return entry.mode.Type() }
func (entry relationFacadeAdversarialDirEntry) Info() (fs.FileInfo, error) { return entry, nil }
func (entry relationFacadeAdversarialDirEntry) Size() int64                { return 0 }
func (entry relationFacadeAdversarialDirEntry) Mode() fs.FileMode          { return entry.mode }
func (entry relationFacadeAdversarialDirEntry) ModTime() time.Time         { return time.Time{} }
func (entry relationFacadeAdversarialDirEntry) Sys() any                   { return nil }

func verifyRelationFacadePhysicalInventory(t *testing.T, inventory relationFacadeInventory) {
	t.Helper()

	if !slices.Equal(inventory.names, relationFacadePhysicalFiles) {
		t.Fatalf("relation facade physical files = %q, want %q", inventory.names, relationFacadePhysicalFiles)
	}
	if inventory.bytes != relationFacadePhysicalBytes || inventory.digest != relationFacadePhysicalDigest {
		t.Fatalf("relation facade physical inventory = %d/%s, want %d/%s", inventory.bytes, inventory.digest, relationFacadePhysicalBytes, relationFacadePhysicalDigest)
	}
	generated := make(map[string][]byte, len(relationFacadeGeneratedFiles))
	for _, name := range relationFacadeGeneratedFiles {
		content, ok := inventory.files[name]
		if !ok {
			t.Fatalf("relation facade generated file %s is absent", name)
		}
		generated[name] = content
	}
	generatedBytes, generatedDigest := digestRelationFacadeFiles(generated)
	if len(generated) != 16 || generatedBytes != relationFacadeGeneratedBytes || generatedDigest != relationFacadeGeneratedDigest {
		t.Fatalf(
			"relation facade generated inventory = %d/%d/%s, want 16/%d/%s",
			len(generated),
			generatedBytes,
			generatedDigest,
			relationFacadeGeneratedBytes,
			relationFacadeGeneratedDigest,
		)
	}
}

func digestRelationFacadeFiles(files map[string][]byte) (int, string) {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	digest := sha256.New()
	contentBytes := 0
	for _, name := range names {
		content := files[name]
		contentBytes += len(content)
		_, _ = io.WriteString(digest, name)
		_, _ = digest.Write([]byte{0})
		_, _ = io.WriteString(digest, strconv.Itoa(len(content)))
		_, _ = digest.Write([]byte{0})
		_, _ = digest.Write(content)
	}
	return contentBytes, fmt.Sprintf("%x", digest.Sum(nil))
}

func equalRelationFacadeFiles(left, right map[string][]byte) bool {
	if len(left) != len(right) {
		return false
	}
	for name, content := range left {
		if !bytes.Equal(content, right[name]) {
			return false
		}
	}
	return true
}

func requireRelationFacadeTargetAbsent(t *testing.T, path, phase string) {
	t.Helper()

	_, err := os.Lstat(path)
	if err == nil {
		t.Fatalf("retired relation facade overlay backing exists %s: %s", phase, path)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("lstat retired relation facade overlay backing %s: %v", phase, err)
	}
}

func replaceRelationFacadeToken(t *testing.T, source, oldToken, newToken []byte) []byte {
	t.Helper()

	if count := bytes.Count(source, oldToken); count != 1 {
		t.Fatalf("relation facade mutation token %q count = %d, want exact 1", oldToken, count)
	}
	return bytes.Replace(source, oldToken, newToken, 1)
}

func formatRelationFacadeMutation(t *testing.T, source []byte) []byte {
	t.Helper()

	formatted, err := format.Source(source)
	if err != nil {
		t.Fatalf("format relation facade adversarial mutation: %v", err)
	}
	return formatted
}

func verifyRelationFacadeProductionGoList(t *testing.T, root string) {
	t.Helper()

	command := exec.CommandContext(
		t.Context(),
		"go",
		"list",
		"-deps",
		"-test",
		"-json",
		"-mod=readonly",
		modulePath+"/conformance/relationdeleteproduct",
	)
	command.Dir = root
	command.Env = commandEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go list production relation facade package closure: %v\n%s", err, output)
	}
	type packageFiles struct {
		prefix    string
		goFiles   []string
		testFiles []string
	}
	wantPackages := map[string]packageFiles{
		modulePath + "/conformance/relationdeleteproduct": {
			goFiles:   []string{"observer.go"},
			testFiles: []string{"product_test.go"},
		},
		modulePath + "/conformance/relationdeleteproduct/authors": {
			prefix: "authors",
			goFiles: []string{
				"zz_godj_generated.go",
				"zz_godj_relation.go",
				"zz_godj_relation_object.go",
				"zz_godj_relation_projection.go",
			},
		},
		modulePath + "/conformance/relationdeleteproduct/blog": {
			prefix: "blog",
			goFiles: []string{
				"zz_godj_generated.go",
				"zz_godj_relation.go",
				"zz_godj_relation_object.go",
				"zz_godj_relation_projection.go",
			},
		},
		modulePath + "/conformance/relationdeleteproduct/fixture": {
			prefix:  "fixture",
			goFiles: []string{"schema.go"},
		},
		modulePath + "/conformance/relationdeleteproduct/project": {
			prefix: "project",
			goFiles: []string{
				"zz_godj_bindings.go",
				"zz_godj_relation_delete.go",
				"zz_godj_relation_facade.go",
				"zz_godj_relation_object.go",
				"zz_godj_relation_prefetch.go",
				"zz_godj_relation_query.go",
				"zz_godj_relation_reverse.go",
				"zz_godj_relation_select_related.go",
			},
		},
	}
	for path, files := range wantPackages {
		slices.Sort(files.goFiles)
		slices.Sort(files.testFiles)
		wantPackages[path] = files
	}
	seenPackages := make(map[string]bool, len(wantPackages))
	physicalFiles := make([]string, 0, len(relationFacadeCommandViewFiles))
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var listed struct {
			ImportPath   string
			Dir          string
			GoFiles      []string
			CgoFiles     []string
			TestGoFiles  []string
			XTestGoFiles []string
		}
		if err := decoder.Decode(&listed); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("decode production relation facade go list: %v", err)
		}
		want, expected := wantPackages[listed.ImportPath]
		if !expected {
			continue
		}
		if seenPackages[listed.ImportPath] {
			t.Fatalf("production relation facade go list repeated package %s", listed.ImportPath)
		}
		seenPackages[listed.ImportPath] = true
		listedDirectory, err := filepath.EvalSymlinks(listed.Dir)
		if err != nil {
			t.Fatalf("canonicalize production relation facade listed directory %s: %v", listed.ImportPath, err)
		}
		wantDirectory := filepath.Clean(filepath.Join(root, "conformance", "relationdeleteproduct", want.prefix))
		if listedDirectory != wantDirectory {
			t.Fatalf("production relation facade package %s directory = %s, want %s", listed.ImportPath, listedDirectory, wantDirectory)
		}
		goFiles := slices.Clone(listed.GoFiles)
		testFiles := slices.Clone(listed.TestGoFiles)
		slices.Sort(goFiles)
		slices.Sort(testFiles)
		if !slices.Equal(goFiles, want.goFiles) || !slices.Equal(testFiles, want.testFiles) {
			t.Fatalf("production relation facade package %s files = Go %q Test %q, want Go %q Test %q", listed.ImportPath, goFiles, testFiles, want.goFiles, want.testFiles)
		}
		if len(listed.CgoFiles) != 0 || len(listed.XTestGoFiles) != 0 {
			t.Fatalf("production relation facade package %s has unexpected Cgo/XTest files: %q/%q", listed.ImportPath, listed.CgoFiles, listed.XTestGoFiles)
		}
		for _, name := range append(goFiles, testFiles...) {
			physicalFiles = append(physicalFiles, filepath.ToSlash(filepath.Join(want.prefix, name)))
		}
	}
	if len(seenPackages) != len(wantPackages) {
		t.Fatalf("production relation facade package closure = %d packages, want exact %d", len(seenPackages), len(wantPackages))
	}
	slices.Sort(physicalFiles)
	if !slices.Equal(physicalFiles, relationFacadeCommandViewFiles) {
		t.Fatalf("production relation facade command-view files = %q, want exact %d %q", physicalFiles, len(relationFacadeCommandViewFiles), relationFacadeCommandViewFiles)
	}
}

func validateRelationFacadeProductSource(source []byte) error {
	file, err := parseRelationFacadeSource("zz_godj_relation_facade.go", source, "project")
	if err != nil {
		return err
	}

	wantTypes := map[string]bool{
		"Backend":                   true,
		"Models":                    true,
		"AuthorsAuthor":             true,
		"BlogPost":                  true,
		"AuthorsAuthorQuery":        true,
		"BlogPostQuery":             true,
		"BlogPostRelationSelector":  true,
		"BlogPostRelationSelectors": true,
		"BlogPostEagerQuery":        true,
	}
	wantConstants := map[string]bool{
		"GoDjProjectRelationFacadeGeneratorVersion": true,
		"GoDjProjectRelationFacadeInputSHA256":      true,
	}
	wantFunctions := map[string]bool{
		".Using":                      true,
		"AuthorsAuthor.Unwrap":        true,
		"AuthorsAuthor.Save":          true,
		"BlogPost.Unwrap":             true,
		"BlogPost.Save":               true,
		"BlogPost.WithAuthor":         true,
		"BlogPost.WithAuthorID":       true,
		"BlogPost.WithReviewer":       true,
		"BlogPost.WithReviewerID":     true,
		"BlogPost.ClearReviewer":      true,
		"BlogPost.Author":             true,
		"BlogPost.Reviewer":           true,
		"AuthorsAuthorQuery.New":      true,
		"AuthorsAuthorQuery.Filter":   true,
		"AuthorsAuthorQuery.OrderBy":  true,
		"AuthorsAuthorQuery.Distinct": true,
		"AuthorsAuthorQuery.Limit":    true,
		"AuthorsAuthorQuery.Offset":   true,
		"AuthorsAuthorQuery.Count":    true,
		"AuthorsAuthorQuery.First":    true,
		"AuthorsAuthorQuery.All":      true,
		".SelectAuthorsAuthorInto":    true,
		".AggregateAuthorsAuthorInto": true,
		"BlogPostQuery.New":           true,
		"BlogPostQuery.Filter":        true,
		"BlogPostQuery.OrderBy":       true,
		"BlogPostQuery.Distinct":      true,
		"BlogPostQuery.Limit":         true,
		"BlogPostQuery.Offset":        true,
		"BlogPostQuery.Count":         true,
		"BlogPostQuery.First":         true,
		"BlogPostQuery.All":           true,
		"BlogPostQuery.SelectRelated": true,
		".SelectBlogPostInto":         true,
		".AggregateBlogPostInto":      true,
		"BlogPostEagerQuery.Filter":   true,
		"BlogPostEagerQuery.OrderBy":  true,
		"BlogPostEagerQuery.Limit":    true,
		"BlogPostEagerQuery.All":      true,
	}
	wantABISignatures := map[string]string{
		"AuthorsAuthorQuery.New":      "func(_value authors.Author) (*AuthorsAuthor, error)",
		"BlogPostQuery.New":           "func(_value blog.Post) (*BlogPost, error)",
		"AuthorsAuthor.Save":          "func(_ctx context.Context) error",
		"BlogPost.Save":               "func(_ctx context.Context) error",
		"BlogPost.WithAuthor":         "func(_target *AuthorsAuthor) (*BlogPost, error)",
		"BlogPost.WithAuthorID":       "func(_key int64) (*BlogPost, error)",
		"BlogPost.WithReviewer":       "func(_target *AuthorsAuthor) (*BlogPost, error)",
		"BlogPost.WithReviewerID":     "func(_key int64) (*BlogPost, error)",
		"BlogPost.ClearReviewer":      "func() (*BlogPost, error)",
		"AuthorsAuthorQuery.Distinct": "func() AuthorsAuthorQuery",
		"AuthorsAuthorQuery.Offset":   "func(_offset int) (AuthorsAuthorQuery, error)",
		"AuthorsAuthorQuery.Count":    "func(_ctx context.Context) (int64, error)",
		"BlogPostQuery.Distinct":      "func() BlogPostQuery",
		"BlogPostQuery.Offset":        "func(_offset int) (BlogPostQuery, error)",
		"BlogPostQuery.Count":         "func(_ctx context.Context) (int64, error)",
	}
	wantGenericABISignatures := map[string]string{
		".SelectAuthorsAuthorInto":    "func(_ctx context.Context, _source AuthorsAuthorQuery, _projection orm.Projection[authors.Author, R]) ([]R, error)",
		".AggregateAuthorsAuthorInto": "func(_ctx context.Context, _source AuthorsAuthorQuery, _aggregate orm.Aggregate[authors.Author, R]) (R, error)",
		".SelectBlogPostInto":         "func(_ctx context.Context, _source BlogPostQuery, _projection orm.Projection[blog.Post, R]) ([]R, error)",
		".AggregateBlogPostInto":      "func(_ctx context.Context, _source BlogPostQuery, _aggregate orm.Aggregate[blog.Post, R]) (R, error)",
	}
	seenTypes := make(map[string]bool, len(wantTypes))
	seenConstants := make(map[string]bool, len(wantConstants))
	seenFunctions := make(map[string]bool, len(wantFunctions))
	seenABISignatures := make(map[string]bool, len(wantABISignatures))
	seenGenericABISignatures := make(map[string]bool, len(wantGenericABISignatures))
	for _, declaration := range file.Decls {
		switch declaration := declaration.(type) {
		case *ast.GenDecl:
			switch declaration.Tok {
			case token.IMPORT:
				continue
			case token.CONST:
				for _, specification := range declaration.Specs {
					value, ok := specification.(*ast.ValueSpec)
					if !ok {
						return fmt.Errorf("production relation facade const declaration contains %T", specification)
					}
					for _, name := range value.Names {
						if !ast.IsExported(name.Name) {
							continue
						}
						if !wantConstants[name.Name] || seenConstants[name.Name] {
							return fmt.Errorf("forbidden or duplicate production relation facade constant %q", name.Name)
						}
						seenConstants[name.Name] = true
					}
				}
			case token.TYPE:
				for _, specification := range declaration.Specs {
					typeSpecification, ok := specification.(*ast.TypeSpec)
					if !ok {
						return fmt.Errorf("production relation facade type declaration contains %T", specification)
					}
					name := typeSpecification.Name.Name
					if !ast.IsExported(name) {
						continue
					}
					if !wantTypes[name] || seenTypes[name] {
						return fmt.Errorf("forbidden or duplicate production relation facade type %q", name)
					}
					if typeSpecification.Assign.IsValid() || typeSpecification.TypeParams != nil {
						return fmt.Errorf("production relation facade type %q is an alias or generic declaration", name)
					}
					if err := validateRelationFacadeProductType(name, typeSpecification.Type); err != nil {
						return err
					}
					seenTypes[name] = true
				}
			case token.VAR:
				for _, specification := range declaration.Specs {
					value, ok := specification.(*ast.ValueSpec)
					if !ok {
						return fmt.Errorf("production relation facade var declaration contains %T", specification)
					}
					for _, name := range value.Names {
						if ast.IsExported(name.Name) {
							return fmt.Errorf("forbidden production relation facade exported variable %q", name.Name)
						}
					}
				}
			default:
				return fmt.Errorf("forbidden production relation facade declaration %s", declaration.Tok)
			}
		case *ast.FuncDecl:
			if !ast.IsExported(declaration.Name.Name) {
				continue
			}
			if declaration.Recv != nil && len(declaration.Recv.List) == 1 && !ast.IsExported(relationFacadeTypeName(declaration.Recv.List[0].Type)) {
				continue
			}
			key, err := relationFacadeFunctionKey(declaration)
			if err != nil {
				return err
			}
			if !wantFunctions[key] || seenFunctions[key] {
				return fmt.Errorf("forbidden or duplicate production relation facade function %q", key)
			}
			if identifier := relationFacadeForbiddenObjectIdentifier(declaration.Type); identifier != "" {
				return fmt.Errorf("production relation facade function %q exposes low-level %q", key, identifier)
			}
			if wantSignature, exact := wantABISignatures[key]; exact {
				if err := validateRelationFacadeExpressionSchema(declaration.Type, wantSignature); err != nil {
					return fmt.Errorf("production relation facade function %q signature: %w", key, err)
				}
				seenABISignatures[key] = true
			}
			if wantSignature, exact := wantGenericABISignatures[key]; exact {
				if err := validateRelationFacadeGenericFunctionSignature(declaration, wantSignature); err != nil {
					return fmt.Errorf("production relation facade function %q signature: %w", key, err)
				}
				seenGenericABISignatures[key] = true
			}
			seenFunctions[key] = true
		default:
			return fmt.Errorf("forbidden production relation facade declaration %T", declaration)
		}
	}
	if len(seenTypes) != len(wantTypes) || len(seenConstants) != len(wantConstants) || len(seenFunctions) != len(wantFunctions) ||
		len(seenABISignatures) != len(wantABISignatures) || len(seenGenericABISignatures) != len(wantGenericABISignatures) {
		return fmt.Errorf(
			"production relation facade exported declaration set is incomplete: types=%d/%d constants=%d/%d functions=%d/%d ABI signatures=%d/%d generic ABI signatures=%d/%d",
			len(seenTypes),
			len(wantTypes),
			len(seenConstants),
			len(wantConstants),
			len(seenFunctions),
			len(wantFunctions),
			len(seenABISignatures),
			len(wantABISignatures),
			len(seenGenericABISignatures),
			len(wantGenericABISignatures),
		)
	}
	return nil
}

func validateRelationFacadeGenericFunctionSignature(declaration *ast.FuncDecl, wantSignature string) error {
	if declaration.Type.TypeParams == nil || len(declaration.Type.TypeParams.List) != 1 {
		return fmt.Errorf("generic type parameter list is not exactly [R any]")
	}
	parameter := declaration.Type.TypeParams.List[0]
	if len(parameter.Names) != 1 || parameter.Names[0].Name != "R" {
		return fmt.Errorf("generic type parameter name is not exactly R")
	}
	if err := validateRelationFacadeExpressionSchema(parameter.Type, "any"); err != nil {
		return fmt.Errorf("generic type parameter R constraint: %w", err)
	}
	functionType := *declaration.Type
	functionType.TypeParams = nil
	return validateRelationFacadeExpressionSchema(&functionType, wantSignature)
}

func validateRelationFacadeProductType(name string, expression ast.Expr) error {
	if name == "Backend" {
		if err := validateRelationFacadeExpressionSchema(expression, "interface { db.Queryer; db.Mutator }"); err != nil {
			return fmt.Errorf("production relation facade Backend schema: %w", err)
		}
		return nil
	}
	if name == "BlogPostRelationSelector" {
		interfaceType, ok := expression.(*ast.InterfaceType)
		if !ok {
			return fmt.Errorf("production relation facade BlogPostRelationSelector is %T, want sealed interface", expression)
		}
		unexportedMethods := 0
		for _, field := range interfaceType.Methods.List {
			if len(field.Names) != 1 {
				return fmt.Errorf("production relation facade BlogPostRelationSelector contains embedded interface")
			}
			method := field.Names[0].Name
			if ast.IsExported(method) {
				return fmt.Errorf("production relation facade BlogPostRelationSelector exposes method %q", method)
			}
			unexportedMethods++
		}
		if unexportedMethods == 0 {
			return fmt.Errorf("production relation facade BlogPostRelationSelector is not sealed")
		}
		return nil
	}

	structure, ok := expression.(*ast.StructType)
	if !ok {
		return fmt.Errorf("production relation facade type %q is %T, want struct", name, expression)
	}
	wantFields := map[string]string{}
	switch name {
	case "Models":
		wantFields = map[string]string{
			"AuthorsAuthor": "AuthorsAuthorQuery",
			"BlogPost":      "BlogPostQuery",
		}
	case "BlogPostQuery":
		wantFields = map[string]string{"Related": "BlogPostRelationSelectors"}
	case "BlogPostRelationSelectors":
		wantFields = map[string]string{
			"Author":   "BlogPostRelationSelector",
			"Reviewer": "BlogPostRelationSelector",
		}
	}
	seenFields := make(map[string]bool, len(wantFields))
	for _, field := range structure.Fields.List {
		if len(field.Names) == 0 {
			return fmt.Errorf("production relation facade type %q contains anonymous embedded field", name)
		}
		for _, fieldName := range field.Names {
			if !ast.IsExported(fieldName.Name) {
				continue
			}
			wantType, expected := wantFields[fieldName.Name]
			if !expected || seenFields[fieldName.Name] {
				return fmt.Errorf("production relation facade type %q has forbidden or duplicate exported field %q", name, fieldName.Name)
			}
			if err := validateRelationFacadeExpressionSchema(field.Type, wantType); err != nil {
				return fmt.Errorf("production relation facade field %s.%s: %w", name, fieldName.Name, err)
			}
			seenFields[fieldName.Name] = true
		}
	}
	if len(seenFields) != len(wantFields) {
		return fmt.Errorf("production relation facade type %q exported fields = %d, want %d", name, len(seenFields), len(wantFields))
	}
	return nil
}

func relationFacadeForbiddenObjectIdentifier(root ast.Node) string {
	forbidden := ""
	ast.Inspect(root, func(node ast.Node) bool {
		if forbidden != "" {
			return false
		}
		identifier, ok := node.(*ast.Ident)
		if !ok {
			return true
		}
		if strings.HasSuffix(identifier.Name, "Object") || strings.HasSuffix(identifier.Name, "Objects") {
			forbidden = identifier.Name
			return false
		}
		return true
	})
	return forbidden
}
func validateRelationFacadeConsumerSource(source []byte) error {
	file, err := parseRelationFacadeSource("external_consumer.go", source, "facadeconsumer")
	if err != nil {
		return err
	}
	allowedImports := map[string]map[string]bool{
		"context": {"Context": true},
		modulePath + "/conformance/relationdeleteproduct/authors": {
			"Author":       true,
			"AuthorFields": true,
		},
		modulePath + "/conformance/relationdeleteproduct/blog": {
			"Post":       true,
			"PostFields": true,
		},
		modulePath + "/conformance/relationdeleteproduct/project": {
			"AuthorsAuthor":            true,
			"Backend":                  true,
			"BlogPost":                 true,
			"BlogPostRelationSelector": true,
			"Using":                    true,
		},
		modulePath + "/db": {
			"RelationSession": true,
			"Rows":            true,
			"Session":         true,
		},
		modulePath + "/query": {
			"DeletePlan": true,
			"InsertPlan": true,
			"Plan":       true,
			"UpdatePlan": true,
		},
	}
	if err := validateRelationFacadeImports(file, allowedImports, "consumer"); err != nil {
		return err
	}

	wantFunctions := map[string]string{
		"minimalBackend.Query":    "func(context.Context, query.Plan) (db.Rows, error)",
		"minimalBackend.Insert":   "func(context.Context, query.InsertPlan) (int64, error)",
		"minimalBackend.Update":   "func(context.Context, query.UpdatePlan) (int64, error)",
		"minimalBackend.Delete":   "func(context.Context, query.DeletePlan) (int64, error)",
		".compileRelationFacade":  "func(ctx context.Context, backend project.Backend) error",
		".compileMinimalBackend":  "func(ctx context.Context, backend *minimalBackend) error",
		".compileSession":         "func(ctx context.Context, session db.Session) error",
		".compileRelationSession": "func(ctx context.Context, session db.RelationSession) error",
	}
	seenFunctions := make(map[string]bool, len(wantFunctions))
	seenMinimalType := false
	seenBackendAssertions := false
	functions := make(map[string]*ast.FuncDecl, len(wantFunctions))
	for _, declaration := range file.Decls {
		switch declaration := declaration.(type) {
		case *ast.GenDecl:
			switch declaration.Tok {
			case token.IMPORT:
				continue
			case token.TYPE:
				if seenMinimalType || len(declaration.Specs) != 1 {
					return fmt.Errorf("forbidden or duplicate consumer type declaration")
				}
				typeSpecification, ok := declaration.Specs[0].(*ast.TypeSpec)
				if !ok || typeSpecification.Name.Name != "minimalBackend" || typeSpecification.Assign.IsValid() || typeSpecification.TypeParams != nil {
					return fmt.Errorf("forbidden consumer type declaration")
				}
				if err := validateRelationFacadeExpressionSchema(typeSpecification.Type, "struct{}"); err != nil {
					return fmt.Errorf("consumer minimalBackend type: %w", err)
				}
				seenMinimalType = true
			case token.VAR:
				if seenBackendAssertions {
					return fmt.Errorf("duplicate consumer backend assertions")
				}
				if err := validateRelationFacadeBackendAssertions(declaration); err != nil {
					return err
				}
				seenBackendAssertions = true
			default:
				return fmt.Errorf("forbidden consumer declaration %s", declaration.Tok)
			}
		case *ast.FuncDecl:
			key, err := relationFacadeFunctionKey(declaration)
			if err != nil {
				return err
			}
			wantSignature, expected := wantFunctions[key]
			if !expected || seenFunctions[key] {
				return fmt.Errorf("forbidden or duplicate consumer function %q", key)
			}
			if err := validateRelationFacadeExpressionSchema(declaration.Type, wantSignature); err != nil {
				return fmt.Errorf("consumer function signature %s: %w", key, err)
			}
			seenFunctions[key] = true
			functions[key] = declaration
		default:
			return fmt.Errorf("forbidden consumer declaration %T", declaration)
		}
	}
	if !seenMinimalType || !seenBackendAssertions || len(seenFunctions) != len(wantFunctions) {
		return fmt.Errorf(
			"consumer declaration set is incomplete: minimal=%t assertions=%t functions=%d/%d",
			seenMinimalType,
			seenBackendAssertions,
			len(seenFunctions),
			len(wantFunctions),
		)
	}

	for _, origin := range []struct {
		function string
		argument string
	}{
		{function: ".compileRelationFacade", argument: "backend"},
		{function: ".compileMinimalBackend", argument: "backend"},
		{function: ".compileSession", argument: "session"},
		{function: ".compileRelationSession", argument: "session"},
	} {
		if count := countRelationFacadeAssignments(
			functions[origin.function],
			token.DEFINE,
			[]string{"models", "err"},
			[]string{"project.Using(" + origin.argument + ")"},
		); count != 1 {
			return fmt.Errorf("consumer %s origin assignments = %d, want exact 1", origin.function, count)
		}
	}

	importSelectors := relationFacadeImportSelectors(file, allowedImports)
	allowedSelectors := map[string]bool{
		"All":            true,
		"Asc":            true,
		"Author":         true,
		"AuthorID":       true,
		"AuthorsAuthor":  true,
		"BlogPost":       true,
		"ClearReviewer":  true,
		"Filter":         true,
		"First":          true,
		"IContains":      true,
		"ID":             true,
		"Limit":          true,
		"Name":           true,
		"New":            true,
		"OrderBy":        true,
		"Related":        true,
		"Reviewer":       true,
		"Save":           true,
		"SelectRelated":  true,
		"Title":          true,
		"Unwrap":         true,
		"WithAuthor":     true,
		"WithAuthorID":   true,
		"WithReviewer":   true,
		"WithReviewerID": true,
	}
	wantWriteCalls := map[string]int{
		"models.AuthorsAuthor.New": 1,
		"models.BlogPost.New":      1,
		"newAuthor.Save":           1,
		"newPost.Save":             1,
		"newPost.WithAuthor":       1,
		"newPost.WithAuthorID":     1,
		"newPost.WithReviewer":     1,
		"newPost.WithReviewerID":   1,
		"newPost.ClearReviewer":    1,
	}
	seenWriteCalls := make(map[string]int, len(wantWriteCalls))
	projectUsingCalls := 0
	relatedAuthorTokens := 0
	relatedReviewerTokens := 0
	var validationErr error
	ast.Inspect(file, func(node ast.Node) bool {
		if validationErr != nil {
			return false
		}
		switch node := node.(type) {
		case *ast.GoStmt:
			validationErr = fmt.Errorf("consumer contains forbidden GoStmt")
			return false
		case *ast.DeferStmt:
			validationErr = fmt.Errorf("consumer contains forbidden DeferStmt")
			return false
		case *ast.FuncLit:
			validationErr = fmt.Errorf("consumer contains forbidden function literal")
			return false
		case *ast.CallExpr:
			if function, ok := node.Fun.(*ast.Ident); ok && function.Name != "len" {
				validationErr = fmt.Errorf("forbidden consumer bare call %q", function.Name)
				return false
			}
			if function, ok := node.Fun.(*ast.SelectorExpr); ok && function.Sel.Name == "Using" && relationFacadeSelectorPath(function.X) == "project" {
				projectUsingCalls++
			}
			if path := relationFacadeSelectorPath(node.Fun); wantWriteCalls[path] != 0 {
				seenWriteCalls[path]++
			}
		case *ast.SelectorExpr:
			if qualifier, ok := node.X.(*ast.Ident); ok {
				if allowed, imported := importSelectors[qualifier.Name]; imported {
					if !allowed[node.Sel.Name] {
						validationErr = fmt.Errorf("forbidden consumer imported selector %s.%s", qualifier.Name, node.Sel.Name)
						return false
					}
					return true
				}
			}
			if !allowedSelectors[node.Sel.Name] {
				validationErr = fmt.Errorf("forbidden consumer selector %q", node.Sel.Name)
				return false
			}
			path := relationFacadeSelectorPath(node)
			switch path {
			case "models.BlogPost.Related.Author":
				relatedAuthorTokens++
			case "models.BlogPost.Related.Reviewer":
				relatedReviewerTokens++
			}
		}
		return true
	})
	if validationErr != nil {
		return validationErr
	}
	if projectUsingCalls != 4 {
		return fmt.Errorf("consumer project.Using call sites = %d, want exact 4", projectUsingCalls)
	}
	for path, want := range wantWriteCalls {
		if got := seenWriteCalls[path]; got != want {
			return fmt.Errorf("consumer write call %s count = %d, want exact %d", path, got, want)
		}
	}
	if relatedAuthorTokens != 2 || relatedReviewerTokens != 2 {
		return fmt.Errorf(
			"consumer common relation selector tokens Author/Reviewer = %d/%d, want 2/2",
			relatedAuthorTokens,
			relatedReviewerTokens,
		)
	}
	if identifier := relationFacadeForbiddenObjectIdentifier(file); identifier != "" {
		return fmt.Errorf("consumer exposes forbidden low-level %q", identifier)
	}
	return nil
}

func validateRelationFacadeBackendAssertions(declaration *ast.GenDecl) error {
	wantValues := map[string]bool{
		"(*minimalBackend)(nil)":    true,
		"(db.Session)(nil)":         true,
		"(db.RelationSession)(nil)": true,
	}
	seenValues := make(map[string]bool, len(wantValues))
	for _, specification := range declaration.Specs {
		value, ok := specification.(*ast.ValueSpec)
		if !ok || len(value.Names) != 1 || value.Names[0].Name != "_" || len(value.Values) != 1 {
			return fmt.Errorf("consumer backend assertion has invalid shape")
		}
		if err := validateRelationFacadeExpressionSchema(value.Type, "project.Backend"); err != nil {
			return fmt.Errorf("consumer backend assertion type: %w", err)
		}
		formatted, err := formatRelationFacadeExpression(value.Values[0])
		if err != nil {
			return err
		}
		if !wantValues[formatted] || seenValues[formatted] {
			return fmt.Errorf("forbidden or duplicate consumer backend assertion %q", formatted)
		}
		seenValues[formatted] = true
	}
	if len(seenValues) != len(wantValues) {
		return fmt.Errorf("consumer backend assertions = %d, want %d", len(seenValues), len(wantValues))
	}
	return nil
}
func countRelationFacadeAssignments(function *ast.FuncDecl, operation token.Token, left, right []string) int {
	return countRelationFacadeAssignmentsIn(function.Body, operation, left, right)
}

func countRelationFacadeAssignmentsIn(root ast.Node, operation token.Token, left, right []string) int {
	count := 0
	ast.Inspect(root, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if ok && relationFacadeAssignmentMatches(assignment, operation, left, right) {
			count++
		}
		return true
	})
	return count
}

func relationFacadeAssignmentMatches(assignment *ast.AssignStmt, operation token.Token, left, right []string) bool {
	if assignment == nil || assignment.Tok != operation || len(assignment.Lhs) != len(left) || len(assignment.Rhs) != len(right) {
		return false
	}
	for index, want := range left {
		identifier, ok := assignment.Lhs[index].(*ast.Ident)
		if !ok || identifier.Name != want {
			return false
		}
	}
	for index, want := range right {
		if !relationFacadeExpressionMatches(assignment.Rhs[index], want) {
			return false
		}
	}
	return true
}

func relationFacadeExpressionMatches(expression ast.Expr, wantSource string) bool {
	want, err := parser.ParseExpr(wantSource)
	if err != nil {
		return false
	}
	gotFormatted, err := formatRelationFacadeExpression(expression)
	if err != nil {
		return false
	}
	wantFormatted, err := formatRelationFacadeExpression(want)
	return err == nil && gotFormatted == wantFormatted
}

func parseRelationFacadeSource(name string, source []byte, wantPackage string) (*ast.File, error) {
	formatted, err := format.Source(source)
	if err != nil {
		return nil, fmt.Errorf("format %s: %w", name, err)
	}
	if !bytes.Equal(formatted, source) {
		return nil, fmt.Errorf("%s is not gofmt-stable", name)
	}
	file, err := parser.ParseFile(token.NewFileSet(), name, source, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", name, err)
	}
	if file.Name.Name != wantPackage {
		return nil, fmt.Errorf("%s package = %q, want %q", name, file.Name.Name, wantPackage)
	}
	for _, group := range file.Comments {
		for _, comment := range group.List {
			if strings.HasPrefix(strings.TrimSpace(comment.Text), "//go:") {
				return nil, fmt.Errorf("%s contains forbidden Go directive", name)
			}
		}
	}
	return file, nil
}

func validateRelationFacadeExpressionSchema(expression ast.Expr, wantSource string) error {
	wantExpression, err := parser.ParseExpr(wantSource)
	if err != nil {
		return fmt.Errorf("parse expected schema %q: %w", wantSource, err)
	}
	got, err := formatRelationFacadeExpression(expression)
	if err != nil {
		return err
	}
	want, err := formatRelationFacadeExpression(wantExpression)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("got %q, want %q", got, want)
	}
	return nil
}

func formatRelationFacadeExpression(expression ast.Expr) (string, error) {
	var formatted bytes.Buffer
	if err := format.Node(&formatted, token.NewFileSet(), expression); err != nil {
		return "", fmt.Errorf("format relation facade AST schema: %w", err)
	}
	return formatted.String(), nil
}

func validateRelationFacadeImports(file *ast.File, allowed map[string]map[string]bool, label string) error {
	seen := make(map[string]bool, len(allowed))
	for _, specification := range file.Imports {
		if specification.Name != nil {
			return fmt.Errorf("forbidden %s import alias %q", label, specification.Name.Name)
		}
		path, err := strconv.Unquote(specification.Path.Value)
		if err != nil {
			return fmt.Errorf("decode %s import: %w", label, err)
		}
		if _, ok := allowed[path]; !ok {
			return fmt.Errorf("forbidden %s import %q", label, path)
		}
		if seen[path] {
			return fmt.Errorf("duplicate %s import %q", label, path)
		}
		seen[path] = true
	}
	if len(seen) != len(allowed) {
		return fmt.Errorf("%s import set = %d entries, want exact %d", label, len(seen), len(allowed))
	}
	return nil
}

func relationFacadeImportSelectors(file *ast.File, allowed map[string]map[string]bool) map[string]map[string]bool {
	selectors := make(map[string]map[string]bool, len(file.Imports))
	for _, specification := range file.Imports {
		path, err := strconv.Unquote(specification.Path.Value)
		if err != nil {
			continue
		}
		qualifier := filepath.Base(path)
		allowedSelectors, ok := allowed[path]
		if !ok {
			continue
		}
		selectors[qualifier] = make(map[string]bool, len(allowedSelectors))
		for name := range allowedSelectors {
			selectors[qualifier][name] = true
		}
	}
	return selectors
}

func relationFacadeFunctionKey(declaration *ast.FuncDecl) (string, error) {
	receiver := ""
	if declaration.Recv != nil {
		if len(declaration.Recv.List) != 1 {
			return "", fmt.Errorf("relation facade function %s has invalid receiver", declaration.Name.Name)
		}
		receiver = relationFacadeTypeName(declaration.Recv.List[0].Type)
		if receiver == "" {
			return "", fmt.Errorf("relation facade function %s has unsupported receiver", declaration.Name.Name)
		}
	}
	return receiver + "." + declaration.Name.Name, nil
}

func relationFacadeTypeName(expression ast.Expr) string {
	switch expression := expression.(type) {
	case *ast.Ident:
		return expression.Name
	case *ast.StarExpr:
		return relationFacadeTypeName(expression.X)
	case *ast.IndexExpr:
		return relationFacadeTypeName(expression.X)
	case *ast.IndexListExpr:
		return relationFacadeTypeName(expression.X)
	default:
		return ""
	}
}

func relationFacadeSelectorPath(expression ast.Expr) string {
	switch expression := expression.(type) {
	case *ast.Ident:
		return expression.Name
	case *ast.SelectorExpr:
		prefix := relationFacadeSelectorPath(expression.X)
		if prefix == "" {
			return expression.Sel.Name
		}
		return prefix + "." + expression.Sel.Name
	default:
		return ""
	}
}

func TestTypedAPIMisuseDoesNotCompile(t *testing.T) {
	tests := []struct {
		name          string
		fixture       string
		wantFragments []string
	}{
		{
			name:    "predicate model mismatch",
			fixture: "predicate_model_mismatch.go.txt",
			wantFragments: []string{
				"models.ArticleFields.Title.Exact",
				"orm.Predicate[Other]",
			},
		},
		{
			name:    "predicate composition model mismatch",
			fixture: "predicate_composition_model_mismatch.go.txt",
			wantFragments: []string{
				"orm.Or",
				"orm.Predicate[Other]",
				"orm.Predicate[models.Article]",
			},
		},
		{
			name:    "predicate connector arity",
			fixture: "predicate_connector_arity.go.txt",
			wantFragments: []string{
				"not enough arguments in call to orm.And",
				"not enough arguments in call to orm.Or",
			},
		},
		{
			name:    "field reference model mismatch",
			fixture: "field_reference_model_mismatch.go.txt",
			wantFragments: []string{
				"orm.F(otherTitle)",
				"orm.FieldReference[models.Article, string]",
			},
		},
		{
			name:    "field reference kind mismatch",
			fixture: "field_reference_kind_mismatch.go.txt",
			wantFragments: []string{
				"orm.F(models.ArticleFields.ID)",
				"orm.FieldReference[models.Article, string]",
			},
		},
		{
			name:    "Boolean field reference is unsupported",
			fixture: "field_reference_boolean_unsupported.go.txt",
			wantFragments: []string{
				"models.ArticleFields.Published",
				"does not match orm.ReferenceField",
				"cannot infer M and V",
			},
		},
		{
			name:    "relation field reference is unsupported",
			fixture: "field_reference_relation_unsupported.go.txt",
			wantFragments: []string{
				"orm.RelatedStringField[Article]",
				"does not match orm.ReferenceField",
				"cannot infer M and V",
			},
		},
		{
			name:    "descriptor model mismatch",
			fixture: "descriptor_model_mismatch.go.txt",
			wantFragments: []string{
				"orm.ModelDescriptor[Other]",
				"wrong type for method",
			},
		},
		{
			name:    "descriptor clone is required",
			fixture: "descriptor_clone_missing.go.txt",
			wantFragments: []string{
				"orm.ModelDescriptor[CustomModel]",
				"missing method CloneModel",
			},
		},
		{
			name:    "isnull requires bool",
			fixture: "isnull_string.go.txt",
			wantFragments: []string{
				"cannot use \"true\"",
				"as bool value",
			},
		},
		{
			name:    "nullable exact requires value",
			fixture: "nullable_exact_pointer.go.txt",
			wantFragments: []string{
				"cannot use (*string)(nil)",
				"as string value",
			},
		},
		{
			name:    "icontains requires string",
			fixture: "icontains_integer.go.txt",
			wantFragments: []string{
				"cannot use 123",
				"as string value",
			},
		},
		{
			name:    "non-null field has no null builder",
			fixture: "write_title_null.go.txt",
			wantFragments: []string{
				"WithTitleNull undefined",
			},
		},
		{
			name:    "write scalar type is static",
			fixture: "write_wrong_scalar.go.txt",
			wantFragments: []string{
				"cannot use \"false\"",
				"as bool value",
			},
		},
		{
			name:    "write input model mismatch",
			fixture: "write_model_mismatch.go.txt",
			wantFragments: []string{
				"orm.CreateInput[Other]",
				"wrong type for method BuildCreate",
			},
		},
		{
			name:    "Save update field model mismatch",
			fixture: "save_field_model_mismatch.go.txt",
			wantFragments: []string{
				"cannot use orm.NewStringField[Other]",
				"orm.WritableField[models.Article]",
			},
		},
		{
			name:    "Save primary key is not writable",
			fixture: "save_primary_key_mask.go.txt",
			wantFragments: []string{
				"models.ArticleFields.ID",
				"orm.WritableField[models.Article]",
			},
		},
		{
			name:    "Save option model mismatch",
			fixture: "save_option_model_mismatch.go.txt",
			wantFragments: []string{
				"orm.ForceInsert[Other]()",
				"orm.SaveOption[models.Article]",
			},
		},
		{
			name:    "QuerySet Iterate callback model mismatch",
			fixture: "query_iterate_model_mismatch.go.txt",
			wantFragments: []string{
				"cannot use func(Other) error",
				"func(models.Article) error",
			},
		},
		{
			name:    "QuerySet terminal result model mismatch",
			fixture: "query_terminal_result_mismatch.go.txt",
			wantFragments: []string{
				"cannot use article",
				"as Other value",
			},
		},
		{
			name:    "related predicate source model mismatch",
			fixture: "relation_query/predicate_source_mismatch.go.txt",
			wantFragments: []string{
				"relations.BlogPost.Author.Name.Exact",
				"orm.Predicate[authors.Author]",
			},
		},
		{
			name:    "forward relation target field mismatch",
			fixture: "relation_query/target_field_mismatch.go.txt",
			wantFragments: []string{
				"blog.PostFields.Title",
				"orm.StringField[authors.Author]",
			},
		},
		{
			name:    "related integer exact requires integer",
			fixture: "relation_query/integer_value_mismatch.go.txt",
			wantFragments: []string{
				"cannot use \"1\"",
				"as int64 value",
			},
		},
		{
			name:    "relation object predicate keeps source model",
			fixture: "relation_object/predicate_source_mismatch.go.txt",
			wantFragments: []string{
				"objects.BlogPost.Reviewer.IsNull",
				"orm.Predicate[authors.Author]",
			},
		},
		{
			name:    "relation object factory requires source model",
			fixture: "relation_object/factory_source_mismatch.go.txt",
			wantFragments: []string{
				"cannot use author",
				"as blog.Post value",
			},
		},
		{
			name:    "relation object isnull requires bool",
			fixture: "relation_object/isnull_value_mismatch.go.txt",
			wantFragments: []string{
				"cannot use \"true\"",
				"as bool value",
			},
		},
		{
			name:    "reverse relation predicate keeps owner model",
			fixture: "relation_reverse/predicate_owner_mismatch.go.txt",
			wantFragments: []string{
				"relations.AuthorsAuthor.Posts.Title.Exact",
				"orm.Predicate[blog.Post]",
			},
		},
		{
			name:    "select-related source QuerySet keeps source model",
			fixture: "relation_select_related/source_queryset_mismatch.go.txt",
			wantFragments: []string{
				"cannot use authors.AuthorObjects.Using(backend)",
				"orm.QuerySet[blog.Post]",
			},
		},
		{
			name:    "select-related remains singular",
			fixture: "relation_select_related/multiple_selection.go.txt",
			wantFragments: []string{
				"Author().Reviewer undefined",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := compileFixture(t, test.fixture)
			if result.err == nil {
				t.Fatalf("fixture %s unexpectedly compiled", test.fixture)
			}
			for _, fragment := range test.wantFragments {
				if !strings.Contains(result.output, fragment) {
					t.Fatalf("compiler output for %s does not contain %q:\n%s", test.fixture, fragment, result.output)
				}
			}
		})
	}
}

func TestDirectPackageDependencyBoundaries(t *testing.T) {
	forbidden := []dependencyEdge{
		{from: modulePath + "/schema/ir", to: modulePath + "/orm"},
		{from: modulePath + "/query", to: modulePath + "/orm"},
		{from: modulePath + "/orm", to: modulePath + "/db/sqlite"},
		{from: modulePath + "/codegen", to: modulePath + "/examples/article/models"},
		{from: modulePath + "/examples/article/models", to: modulePath + "/codegen"},
		{from: modulePath + "/examples/article/modeldef", to: modulePath + "/examples/article/models"},
		{from: modulePath + "/examples/article/modeldef", to: modulePath + "/examples/article/project"},
		{from: modulePath + "/examples/article/cmd/projectrunner", to: modulePath + "/examples/article/models"},
		{from: modulePath + "/examples/article/cmd/projectrunner", to: modulePath + "/examples/article/project"},
		{from: modulePath + "/conformance/relationdeleteproduct/fixture", to: modulePath + "/conformance/relationdeleteproduct/authors"},
		{from: modulePath + "/conformance/relationdeleteproduct/fixture", to: modulePath + "/conformance/relationdeleteproduct/blog"},
		{from: modulePath + "/conformance/relationdeleteproduct/fixture", to: modulePath + "/conformance/relationdeleteproduct/project"},
		{from: modulePath + "/conformance/relationdeleteproduct/cmd/projectrunner", to: modulePath + "/conformance/relationdeleteproduct/authors"},
		{from: modulePath + "/conformance/relationdeleteproduct/cmd/projectrunner", to: modulePath + "/conformance/relationdeleteproduct/blog"},
		{from: modulePath + "/conformance/relationdeleteproduct/cmd/projectrunner", to: modulePath + "/conformance/relationdeleteproduct/project"},
		{from: modulePath + "/migrations", to: modulePath + "/migrations/definition"},
		{from: modulePath + "/internal/projectcheck", to: modulePath + "/internal/projectcheck/linked"},
		{from: modulePath + "/internal/projectcheck/linked", to: modulePath + "/internal/projectcheck"},
		{from: modulePath + "/migrations", to: modulePath + "/internal/projectcheck"},
		{from: modulePath + "/migrations", to: modulePath + "/internal/projectcheck/linked"},
		{from: modulePath + "/migrations/definition", to: modulePath + "/internal/projectcheck/linked"},
	}

	packages := make([]string, 0, len(forbidden))
	for _, edge := range forbidden {
		if !slices.Contains(packages, edge.from) {
			packages = append(packages, edge.from)
		}
	}

	root := repositoryRoot(t)
	arguments := append([]string{"list", "-json"}, packages...)
	command := exec.Command("go", arguments...)
	command.Dir = root
	command.Env = commandEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("load direct package imports: %v\n%s", err, output)
	}

	directImports := make(map[string][]string, len(packages))
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var listed struct {
			ImportPath string
			Imports    []string
		}
		err := decoder.Decode(&listed)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("decode go list output: %v", err)
		}
		directImports[listed.ImportPath] = listed.Imports
	}

	for _, edge := range forbidden {
		imports, ok := directImports[edge.from]
		if !ok {
			t.Errorf("go list did not return package %s", edge.from)
			continue
		}
		if slices.Contains(imports, edge.to) {
			t.Errorf("forbidden direct dependency exists: %s -> %s", edge.from, edge.to)
		}
	}
}

func TestProjectCheckDirectImportGraph(t *testing.T) {
	want := map[string][]string{
		modulePath + "/project": {
			modulePath + "/codegen",
			modulePath + "/internal/projectcheck/linked",
			modulePath + "/internal/projectgenerate/linked",
			modulePath + "/internal/projectgenerate/protocol",
		},
		modulePath + "/internal/projectcheck": {
			modulePath + "/codegen",
			modulePath + "/internal/projectcheck/protocol",
			modulePath + "/internal/projectgenerate",
			modulePath + "/internal/projectgenerate/protocol",
		},
		modulePath + "/internal/projectcheck/linked": {
			modulePath + "/internal/projectcheck/protocol",
			modulePath + "/migrations",
			modulePath + "/migrations/definition",
		},
		modulePath + "/internal/projectcheck/protocol": nil,
		modulePath + "/internal/projectgenerate": {
			modulePath + "/codegen",
		},
		modulePath + "/internal/projectgenerate/linked": {
			modulePath + "/codegen",
			modulePath + "/internal/projectgenerate/protocol",
		},
		modulePath + "/internal/projectgenerate/protocol": {
			modulePath + "/codegen",
			modulePath + "/internal/projectspec",
			modulePath + "/schema/ir",
		},
		modulePath + "/internal/projectspec": {
			modulePath + "/schema/ir",
		},
	}

	packages := make([]string, 0, len(want))
	for packagePath := range want {
		packages = append(packages, packagePath)
	}
	slices.Sort(packages)
	command := exec.Command("go", append([]string{"list", "-json"}, packages...)...)
	command.Dir = repositoryRoot(t)
	command.Env = commandEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("load project-check imports: %v\n%s", err, output)
	}

	seen := make(map[string]bool, len(want))
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var listed struct {
			ImportPath string
			Imports    []string
		}
		err := decoder.Decode(&listed)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("decode project-check package: %v", err)
		}
		required, expected := want[listed.ImportPath]
		if !expected {
			continue
		}
		seen[listed.ImportPath] = true
		for _, requiredImport := range required {
			if !slices.Contains(listed.Imports, requiredImport) {
				t.Errorf("%s does not import required package %s", listed.ImportPath, requiredImport)
			}
		}
		for _, imported := range listed.Imports {
			if !strings.HasPrefix(imported, modulePath+"/") {
				continue
			}
			if !slices.Contains(required, imported) {
				t.Errorf("%s has unexpected direct module import %s", listed.ImportPath, imported)
			}
		}
	}
	for packagePath := range want {
		if !seen[packagePath] {
			t.Errorf("go list did not return %s", packagePath)
		}
	}
}

type compileResult struct {
	output string
	err    error
}

func compileFixture(t *testing.T, fixture string) compileResult {
	t.Helper()

	root := repositoryRoot(t)
	source, err := os.ReadFile(filepath.Join(root, "internal", "compiletest", "testdata", fixture))
	if err != nil {
		t.Fatalf("read fixture %s: %v", fixture, err)
	}

	directory := t.TempDir()
	goMod := fmt.Sprintf(`module example.com/godj-compile-gate

go 1.26.0

require %s v0.0.0

replace %s => %s
`, modulePath, modulePath, filepath.ToSlash(root))
	if err := os.WriteFile(filepath.Join(directory, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatalf("write fixture go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "consumer.go"), source, 0o644); err != nil {
		t.Fatalf("write fixture source: %v", err)
	}

	command := exec.Command("go", "test", "-mod=mod", "./...")
	command.Dir = directory
	command.Env = commandEnvironment()
	output, err := command.CombinedOutput()
	return compileResult{output: string(output), err: err}
}

type dependencyEdge struct {
	from string
	to   string
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve compile test source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
}

func commandEnvironment() []string {
	environment := make([]string, 0, len(os.Environ())+3)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "GOFLAGS=") || strings.HasPrefix(entry, "GOWORK=") || strings.HasPrefix(entry, "GOTOOLCHAIN=") {
			continue
		}
		environment = append(environment, entry)
	}
	environment = append(environment, "GOFLAGS=", "GOWORK=off", "GOTOOLCHAIN=local")
	return environment
}
