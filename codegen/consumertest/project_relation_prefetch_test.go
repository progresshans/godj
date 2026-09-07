package codegen_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/codegen/internal/testschema"
)

func TestGeneratedProjectRelationPrefetchExactNineFileUnionCompiles(t *testing.T) {
	authors, blog := testschema.QueryRelation()
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

	command := generatedGoCommand(t.Context(), directory, "test", "-mod=mod", "./...")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("exact nine-file generated prefetch project did not compile: %v\n%s", err, output)
	}
}

func TestGeneratedProjectRelationPrefetchPrerequisiteUnionFailuresPreserveLastKnownGood(t *testing.T) {
	authors, blog := testschema.QueryRelation()
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
	command := generatedGoCommand(ctx, compileDirectory, "test", "-mod=mod", "./...")
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("compile candidate ten-file union: %w\n%s", err, output)
	}
	return nil
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
