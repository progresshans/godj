package codegen_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/codegen/internal/testschema"
	"github.com/progresshans/godj/schema/ir"
)

func TestGenerateProjectRelationPrefetchIsCanonicalAndByteLocked(t *testing.T) {
	t.Parallel()

	authors, blog := testschema.QueryRelation()
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

	authors, blog := testschema.QueryRelation()
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

	authors, blog := testschema.QueryRelation()
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

func TestGenerateProjectRelationPrefetchPreservesReverseGoldenBytes(t *testing.T) {
	t.Parallel()

	authors, blog := testschema.QueryRelation()
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
