package codegen_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema/ir"
)

func TestGenerateProjectRelationPrefetchIsCanonicalAndByteLocked(t *testing.T) {
	t.Parallel()

	authors, blog := relationQueryGenerationSchemas()
	packages := relationReverseGenerationPackages(authors, blog)
	first, err := codegen.GenerateProjectRelationPrefetch("project", packages)
	if err != nil {
		t.Fatalf("GenerateProjectRelationPrefetch() error = %v", err)
	}
	second, err := codegen.GenerateProjectRelationPrefetch(
		"project",
		[]codegen.RelationReversePackage{packages[1], packages[0]},
	)
	if err != nil {
		t.Fatalf("GenerateProjectRelationPrefetch() permuted error = %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("project relation prefetch package order changed bytes\nfirst:\n%s\nsecond:\n%s", first, second)
	}

	want, err := os.ReadFile(filepath.Join("testdata", "relation_prefetch", "project.golden"))
	if err != nil {
		t.Fatalf("read project relation prefetch golden: %v\ngenerated:\n%s", err, first)
	}
	if !bytes.Equal(first, want) {
		t.Fatalf("project relation prefetch bytes drifted\ngot:\n%s\nwant:\n%s", first, want)
	}
	for _, fragment := range [][]byte{
		[]byte(`const GoDjProjectRelationPrefetchGeneratorVersion = "godj-codegen-rel-prefetch-project-v1"`),
		[]byte("type AuthorsAuthorReversePrefetches struct"),
		[]byte("objects       AuthorsAuthorReverseObjectFactory"),
		[]byte("posts         orm.ReversePrefetch[authors.Author, blog.Post]"),
		[]byte("reviewedPosts orm.ReversePrefetch[authors.Author, blog.Post]"),
		[]byte("func (_prefetches AuthorsAuthorReversePrefetches) Posts("),
		[]byte("func (_prefetches AuthorsAuthorReversePrefetches) ReviewedPosts("),
		[]byte("_snapshots := make([]authors.Author, len(_owners))"),
		[]byte("(authors.AuthorDescriptor{}).CloneModel(_owners[_index])"),
		[]byte("_prefetches.posts.Load(_ctx, _backend, _snapshots)"),
		[]byte("_prefetches.reviewedPosts.Load(_ctx, _backend, _snapshots)"),
		[]byte("_prefetches.objects.From(_backend, _snapshots[_index])"),
		[]byte("_object.posts = _sets[_index]"),
		[]byte("_object.reviewedPosts = _sets[_index]"),
		[]byte("type ReversePrefetches struct"),
		[]byte("func BindReversePrefetches() (ReversePrefetches, error)"),
		[]byte("_objects, _err := BindReverseObjects()"),
		[]byte("orm.BindReversePrefetch(_objects.AuthorsAuthor.posts)"),
		[]byte("orm.BindReversePrefetch(_objects.AuthorsAuthor.reviewedPosts)"),
	} {
		if !bytes.Contains(first, fragment) {
			t.Fatalf("project relation prefetch source does not contain %q:\n%s", fragment, first)
		}
	}
	loadIndex := bytes.Index(first, []byte(".Load(_ctx, _backend, _snapshots)"))
	fromIndex := bytes.Index(first, []byte(".From(_backend, _snapshots[_index])"))
	if loadIndex < 0 || fromIndex < 0 || loadIndex >= fromIndex {
		t.Fatalf("generated prefetch method did not call Load before wrapper From:\n%s", first)
	}
	for _, forbidden := range [][]byte{
		[]byte(`query "github.com/progresshans/godj/query"`),
		[]byte(`ir "github.com/progresshans/godj/schema/ir"`),
		[]byte("GoDjRelationSchema"),
		[]byte("ForeignKeyRelation"),
		[]byte(`"authors_author"`),
		[]byte(`"blog_post"`),
		[]byte(`"author_id"`),
		[]byte("panic("),
		[]byte("func init("),
	} {
		if bytes.Contains(first, forbidden) {
			t.Fatalf("project relation prefetch source contains forbidden schema replay %q:\n%s", forbidden, first)
		}
	}

	packages[0].Schema.Models[0].GoName = "Mutated"
	packages[1].Schema.Models[0].Fields[2].Relation.Reverse.Name = "mutated"
	if bytes.Contains(first, []byte("Mutated")) || bytes.Contains(first, []byte("mutated")) {
		t.Fatal("post-generation schema mutation changed generated project bytes")
	}
}

func TestGenerateProjectRelationPrefetchRejectsGeneratorOwnedInputsWithNilBytes(t *testing.T) {
	t.Parallel()

	authors, blog := relationQueryGenerationSchemas()
	valid := relationReverseGenerationPackages(authors, blog)
	tests := []struct {
		name     string
		pkg      string
		packages []codegen.RelationReversePackage
	}{
		{name: "invalid generated package", pkg: "bad-package", packages: valid},
		{name: "reserved context import path", pkg: "project", packages: relationPrefetchPackagesWithBlogIdentity(authors, blog, "blog", "context")},
		{name: "reserved objects private field", pkg: "project", packages: relationReversePackagesWithName(authors, blog, "objects")},
	}
	for _, alias := range []string{
		"db", "orm", "query", "ir", "bool", "error", "false", "nil", "true", "init", "for",
		"context", "make", "len",
	} {
		tests = append(tests, struct {
			name     string
			pkg      string
			packages []codegen.RelationReversePackage
		}{
			name:     "reserved alias " + alias,
			pkg:      "project",
			packages: relationPrefetchPackagesWithBlogIdentity(authors, blog, alias, "example.com/"+alias),
		})
	}
	for _, importPath := range []string{
		"github.com/progresshans/godj/db",
		"github.com/progresshans/godj/orm",
		"github.com/progresshans/godj/query",
		"github.com/progresshans/godj/schema/ir",
	} {
		tests = append(tests, struct {
			name     string
			pkg      string
			packages []codegen.RelationReversePackage
		}{
			name:     "reserved runtime import " + importPath,
			pkg:      "project",
			packages: relationPrefetchPackagesWithBlogIdentity(authors, blog, "blog", importPath),
		})
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			generated, err := codegen.GenerateProjectRelationPrefetch(test.pkg, test.packages)
			if err == nil {
				t.Fatalf("GenerateProjectRelationPrefetch() accepted invalid input:\n%s", generated)
			}
			if generated != nil {
				t.Fatalf("GenerateProjectRelationPrefetch() failure returned non-nil bytes %q", generated)
			}
		})
	}
}

func TestGenerateProjectRelationPrefetchPublishesCurrentOwnersAndHandlesEmptyProject(t *testing.T) {
	t.Parallel()

	authors, blog := relationQueryGenerationSchemas()
	current, err := codegen.GenerateProjectRelationPrefetch(
		"project",
		relationReverseGenerationPackages(authors, blog),
	)
	if err != nil {
		t.Fatalf("GenerateProjectRelationPrefetch() current error = %v", err)
	}
	for _, required := range [][]byte{
		[]byte(`const GoDjProjectRelationPrefetchGeneratorVersion = "godj-codegen-rel-prefetch-project-v1"`),
		[]byte("type AuthorsAuthorReversePrefetches struct"),
		[]byte("orm.ReversePrefetch"),
		[]byte("type ReversePrefetches struct"),
		[]byte("func BindReversePrefetches() (ReversePrefetches, error)"),
		[]byte("_objects, _err := BindReverseObjects()"),
	} {
		if !bytes.Contains(current, required) {
			t.Fatalf("current prefetch source does not contain %q:\n%s", required, current)
		}
	}

	empty, err := codegen.GenerateProjectRelationPrefetch("project", nil)
	if err != nil {
		t.Fatalf("GenerateProjectRelationPrefetch() empty error = %v", err)
	}
	if bytes.Contains(empty, []byte("import")) || !bytes.Contains(empty, []byte("BindReverseObjects()")) {
		t.Fatalf("empty prefetch source has invalid prerequisite/import shape:\n%s", empty)
	}
}

func TestGeneratedProjectRelationPrefetchExactNineFileUnionCompiles(t *testing.T) {
	authors, blog := relationQueryGenerationSchemas()
	const modulePath = "example.com/godj-relation-prefetch-project"
	schemas := []namedRelationReverseSchema{
		{name: "authors", schema: authors},
		{name: "blog", schema: blog},
	}
	directory, generated := writeProjectRelationPrefetchVariant(
		t,
		modulePath,
		schemas,
		generatedRelationPrefetchExternalTest(modulePath),
	)
	if !bytes.Contains(generated, []byte("AuthorsAuthorReversePrefetches")) {
		t.Fatalf("generated prefetch candidate omitted object-capable owner:\n%s", generated)
	}

	generatedCount := 0
	err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "zz_godj_") && strings.HasSuffix(entry.Name(), ".go") {
			generatedCount++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk generated ten-file union: %v", err)
	}
	if generatedCount != 9 {
		t.Fatalf("generated union has %d generated files, want exact nine", generatedCount)
	}

	command := exec.Command("go", "test", "-mod=mod", "./...")
	command.Dir = directory
	command.Env = generatedTestEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("exact nine-file generated prefetch project did not compile: %v\n%s", err, output)
	}
}

func TestGeneratedProjectRelationPrefetchPrerequisiteUnionFailuresPreserveLastKnownGood(t *testing.T) {
	authors, blog := relationQueryGenerationSchemas()
	schemas := []namedRelationReverseSchema{
		{name: "authors", schema: authors},
		{name: "blog", schema: blog},
	}

	for _, test := range []struct {
		name   string
		mutate func(*testing.T, string)
		want   []byte
	}{
		{
			name: "missing reverse companion",
			mutate: func(t *testing.T, directory string) {
				t.Helper()
				if err := os.Remove(filepath.Join(directory, "project", "zz_godj_relation_reverse.go")); err != nil {
					t.Fatalf("remove candidate reverse prerequisite: %v", err)
				}
			},
			want: []byte("undefined"),
		},
		{
			name: "incompatible reverse companion",
			mutate: func(t *testing.T, directory string) {
				t.Helper()
				writeGeneratedTestFile(
					t,
					directory,
					"project/zz_godj_relation_reverse.go",
					[]byte("package project\n\ntype ReverseObjects struct{}\n\nfunc BindReverseObjects() (ReverseObjects, error) { return ReverseObjects{}, nil }\n"),
				)
			},
			want: []byte("AuthorsAuthorReverseObject"),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			modulePath := "example.com/godj-relation-prefetch-" + strings.ReplaceAll(test.name, " ", "-")
			directory, generated := writeProjectRelationPrefetchVariant(t, modulePath, schemas, nil)
			if len(generated) == 0 {
				t.Fatal("pure prefetch generator returned no candidate bytes")
			}
			publicationPath := filepath.Join(directory, "project", "zz_godj_relation_prefetch.go")
			lastKnownGood := []byte("package project\n\nconst LastKnownGoodPrefetch = true\n")
			if err := os.WriteFile(publicationPath, lastKnownGood, 0o644); err != nil {
				t.Fatalf("write last-known-good prefetch output: %v", err)
			}

			test.mutate(t, directory)
			verified := false
			err := codegen.WriteFile(
				context.Background(),
				publicationPath,
				generated,
				codegen.WriteOptions{Verify: func(ctx context.Context, candidatePath string) error {
					verified = true
					return verifyProjectRelationPrefetchCandidate(
						ctx,
						t,
						directory,
						publicationPath,
						candidatePath,
					)
				}},
			)
			if err == nil {
				t.Fatalf("%s candidate unexpectedly compiled and replaced last-known-good output", test.name)
			}
			if !verified {
				t.Fatalf("%s publication did not invoke the union verifier: %v", test.name, err)
			}
			if !bytes.Contains([]byte(err.Error()), test.want) {
				t.Fatalf("%s compiler output lacks %q:\n%s", test.name, test.want, err)
			}
			got, err := os.ReadFile(publicationPath)
			if err != nil {
				t.Fatalf("read last-known-good prefetch output: %v", err)
			}
			if !bytes.Equal(got, lastKnownGood) {
				t.Fatalf("failed candidate replaced last-known-good output:\n%s", got)
			}
		})
	}
}

func verifyProjectRelationPrefetchCandidate(
	ctx context.Context,
	t *testing.T,
	directory string,
	targetPath string,
	candidatePath string,
) error {
	t.Helper()
	compileDirectory := t.TempDir()
	err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == directory || path == candidatePath {
			return nil
		}
		relative, err := filepath.Rel(directory, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(compileDirectory, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return err
		}
		return os.WriteFile(destination, contents, 0o644)
	})
	if err != nil {
		return fmt.Errorf("copy candidate union: %w", err)
	}
	candidate, err := os.ReadFile(candidatePath)
	if err != nil {
		return fmt.Errorf("read generated candidate: %w", err)
	}
	targetRelative, err := filepath.Rel(directory, targetPath)
	if err != nil {
		return fmt.Errorf("resolve candidate target: %w", err)
	}
	if err := os.WriteFile(filepath.Join(compileDirectory, targetRelative), candidate, 0o644); err != nil {
		return fmt.Errorf("place generated candidate in compile union: %w", err)
	}
	command := exec.CommandContext(ctx, "go", "test", "-mod=mod", "./...")
	command.Dir = compileDirectory
	command.Env = generatedTestEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("compile candidate ten-file union: %w\n%s", err, output)
	}
	return nil
}

func TestGenerateProjectRelationPrefetchPreservesReverseGoldenBytes(t *testing.T) {
	t.Parallel()

	authors, blog := relationQueryGenerationSchemas()
	packages := relationReverseGenerationPackages(authors, blog)
	before, err := codegen.GenerateProjectRelationReverse("project", packages)
	if err != nil {
		t.Fatalf("generate reverse before prefetch: %v", err)
	}
	if _, err := codegen.GenerateProjectRelationPrefetch("project", packages); err != nil {
		t.Fatalf("generate prefetch: %v", err)
	}
	after, err := codegen.GenerateProjectRelationReverse("project", packages)
	if err != nil {
		t.Fatalf("generate reverse after prefetch: %v", err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "relation_reverse", "project.golden"))
	if err != nil {
		t.Fatalf("read reverse golden: %v", err)
	}
	if !bytes.Equal(before, after) || !bytes.Equal(before, want) {
		t.Fatal("prefetch generation changed existing reverse companion bytes")
	}
}

func relationPrefetchPackagesWithBlogIdentity(
	authors ir.Schema,
	blog ir.Schema,
	alias string,
	importPath string,
) []codegen.RelationReversePackage {
	packages := relationReverseGenerationPackages(authors, blog)
	packages[1].Alias = alias
	packages[1].ImportPath = importPath
	return packages
}

func writeProjectRelationPrefetchVariant(
	t *testing.T,
	modulePath string,
	schemas []namedRelationReverseSchema,
	externalTest []byte,
) (string, []byte) {
	t.Helper()
	directory := writeProjectRelationReverseVariant(t, modulePath, schemas, "", "")
	packages := make([]codegen.RelationReversePackage, len(schemas))
	for index, candidate := range schemas {
		packages[index] = codegen.RelationReversePackage{
			Alias:      candidate.name,
			ImportPath: modulePath + "/" + candidate.name,
			Schema:     candidate.schema,
		}
	}
	prefetch, err := codegen.GenerateProjectRelationPrefetch("project", packages)
	if err != nil {
		t.Fatalf("generate prefetch variant: %v", err)
	}
	writeGeneratedTestFile(t, directory, "project/zz_godj_relation_prefetch.go", prefetch)
	if len(externalTest) > 0 {
		writeGeneratedTestFile(t, directory, "project/relation_prefetch_external_test.go", externalTest)
	}
	return directory, prefetch
}

func generatedRelationPrefetchExternalTest(modulePath string) []byte {
	return []byte(fmt.Sprintf(`package project_test

import (
	"context"
	"errors"
	"testing"

	authors "%s/authors"
	project "%s/project"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

type neverPrefetchBackend struct{}

func (*neverPrefetchBackend) Query(context.Context, query.Plan) (db.Rows, error) {
	return nil, errors.New("unexpected query")
}

func TestGeneratedPrefetchSurface(t *testing.T) {
	prefetches, err := project.BindReversePrefetches()
	if err != nil {
		t.Fatalf("BindReversePrefetches() error = %%v", err)
	}
	backend := &neverPrefetchBackend{}
	posts, err := prefetches.AuthorsAuthor.Posts(context.Background(), backend, []authors.Author{})
	if err != nil || posts == nil || len(posts) != 0 {
		t.Fatalf("Posts(empty) = %%#v, err=%%v", posts, err)
	}
	reviewed, err := prefetches.AuthorsAuthor.ReviewedPosts(context.Background(), backend, []authors.Author{})
	if err != nil || reviewed == nil || len(reviewed) != 0 {
		t.Fatalf("ReviewedPosts(empty) = %%#v, err=%%v", reviewed, err)
	}
	var _ []*project.AuthorsAuthorReverseObject = posts
}
`, modulePath, modulePath))
}
