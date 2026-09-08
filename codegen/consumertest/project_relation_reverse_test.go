package codegen_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/internal/testschema"
	"github.com/progresshans/godj/schema/ir"
)

func TestGeneratedProjectRelationReverseExactNineFileUnionCompiles(t *testing.T) {
	authors, blog := testschema.QueryRelation()
	const modulePath = "example.com/godj-relation-reverse-project"

	projectBinding, err := codegen.GenerateProjectBridge("project", []codegen.BridgePackage{
		{Alias: "authors", ImportPath: modulePath + "/authors"},
		{Alias: "blog", ImportPath: modulePath + "/blog"},
	})
	if err != nil {
		t.Fatalf("generate project binding: %v", err)
	}
	projectReverse, err := codegen.GenerateProjectRelationReverse("project", []codegen.RelationReversePackage{
		{Alias: "authors", ImportPath: modulePath + "/authors", Schema: authors},
		{Alias: "blog", ImportPath: modulePath + "/blog", Schema: blog},
	})
	if err != nil {
		t.Fatalf("generate project reverse companion: %v", err)
	}

	directory := newGeneratedModule(t, modulePath)
	generatedFiles := []struct {
		name string
		data []byte
	}{
		{name: "project/zz_godj_bindings.go", data: projectBinding},
		{name: "project/zz_godj_relation_reverse.go", data: projectReverse},
	}
	names := writeGeneratedAppFixture(t, directory, "authors", "authors", authors, appFixtureFeatures{object: true})
	names = append(names, writeGeneratedAppFixture(t, directory, "blog", "blog", blog, appFixtureFeatures{object: true})...)
	if count := len(names) + len(generatedFiles); count != 8 {
		t.Fatalf("generated inventory has %d files, want exact eight", count)
	}
	for _, file := range generatedFiles {
		writeGeneratedTestFile(t, directory, file.name, file.data)
	}
	writeGeneratedTestFile(t, directory, "project/relation_reverse_external_test.go", generatedRelationReverseExternalTest(modulePath))

	command := generatedGoCommand(t.Context(), directory, "test", "-mod=mod", "./...")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("exact eight-file generated reverse project did not compile: %v\n%s", err, output)
	}
}

func TestGeneratedProjectRelationReverseMissingObjectPrerequisitesFailWithoutReplacingLastKnownGood(t *testing.T) {
	authors, blog := testschema.QueryRelation()
	for _, omitted := range []string{"authors", "blog"} {
		t.Run(omitted, func(t *testing.T) {
			const modulePrefix = "example.com/godj-relation-reverse-missing-object-"
			publicationDirectory := t.TempDir()
			publicationPath := filepath.Join(publicationDirectory, "zz_godj_relation_reverse.go")
			lastKnownGood := []byte("package project\n\nconst LastKnownGood = true\n")
			if err := os.WriteFile(publicationPath, lastKnownGood, 0o644); err != nil {
				t.Fatalf("write last-known-good reverse output: %v", err)
			}

			candidateDirectory := writeProjectRelationReverseVariant(
				t,
				modulePrefix+omitted,
				[]namedRelationReverseSchema{
					{name: "authors", schema: authors},
					{name: "blog", schema: blog},
				},
				"",
				omitted,
			)
			command := generatedGoCommand(t.Context(), candidateDirectory, "test", "-mod=mod", "./...")
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatalf("reverse union without %s relation-object prerequisite unexpectedly compiled", omitted)
			}
			if !bytes.Contains(output, []byte("RelationObjectDescriptor")) ||
				!bytes.Contains(output, []byte("BindRelationStorage")) {
				t.Fatalf("missing %s object diagnostic lacks required interface/method fragments:\n%s", omitted, output)
			}

			got, err := os.ReadFile(publicationPath)
			if err != nil {
				t.Fatalf("read last-known-good reverse output: %v", err)
			}
			if !bytes.Equal(got, lastKnownGood) {
				t.Fatalf("failed %s candidate replaced last-known-good output:\n%s", omitted, got)
			}
		})
	}
}

func TestGeneratedProjectRelationReverseCurrentAndNoEdgeVariantsCompile(t *testing.T) {
	authors, blog := testschema.QueryRelation()

	t.Run("current reverse owner is object capable", func(t *testing.T) {
		const modulePath = "example.com/godj-relation-reverse-current"
		generated := generateProjectRelationReverseVariant(t, modulePath, []namedRelationReverseSchema{
			{name: "authors", schema: authors},
			{name: "blog", schema: blog},
		})
		for _, required := range [][]byte{
			[]byte("type AuthorsAuthorReverseRelations struct"),
			[]byte("Posts         AuthorsAuthorPostsReverseRelation"),
			[]byte("AuthorsAuthorReverseObjectFactory"),
			[]byte("AuthorsAuthorReverseObject struct"),
			[]byte("type ReverseObjects struct"),
			[]byte("func BindReverseObjects() (ReverseObjects, error)"),
			[]byte(`db "github.com/progresshans/godj/db"`),
			[]byte(`query "github.com/progresshans/godj/query"`),
		} {
			if !bytes.Contains(generated, required) {
				t.Fatalf("current reverse source does not contain %q:\n%s", required, generated)
			}
		}
		compileProjectRelationReverseVariant(t, modulePath, []namedRelationReverseSchema{
			{name: "authors", schema: authors},
			{name: "blog", schema: blog},
		}, `package project_test

import (
	"testing"

	project "example.com/godj-relation-reverse-current/project"
)

func TestCurrentReverse(t *testing.T) {
	relations, err := project.BindReverseRelations()
	if err != nil {
		t.Fatal(err)
	}
	_ = relations.AuthorsAuthor.Posts.Title.Exact("Alpha")
	if _, err := project.BindReverseObjects(); err != nil {
		t.Fatal(err)
	}
}
`)
	})

	t.Run("no reverse edges", func(t *testing.T) {
		const modulePath = "example.com/godj-relation-reverse-empty"
		generated := generateProjectRelationReverseVariant(t, modulePath, []namedRelationReverseSchema{
			{name: "authors", schema: authors},
		})
		for _, forbidden := range [][]byte{
			[]byte("AuthorsAuthorReverseRelations"),
			[]byte("AuthorsAuthorReverseObjectFactory"),
			[]byte(`db "github.com/progresshans/godj/db"`),
			[]byte(`query "github.com/progresshans/godj/query"`),
		} {
			if bytes.Contains(generated, forbidden) {
				t.Fatalf("no-edge reverse source contains %q:\n%s", forbidden, generated)
			}
		}
		compileProjectRelationReverseVariant(t, modulePath, []namedRelationReverseSchema{
			{name: "authors", schema: authors},
		}, `package project_test

import (
	"testing"

	project "example.com/godj-relation-reverse-empty/project"
)

func TestEmptyReverse(t *testing.T) {
	if _, err := project.BindReverseRelations(); err != nil {
		t.Fatal(err)
	}
	if _, err := project.BindReverseObjects(); err != nil {
		t.Fatal(err)
	}
}
`)
	})
}

type namedRelationReverseSchema struct {
	name   string
	schema ir.Schema
}

func generateProjectRelationReverseVariant(
	t *testing.T,
	modulePath string,
	schemas []namedRelationReverseSchema,
) []byte {
	t.Helper()
	packages := make([]codegen.RelationReversePackage, len(schemas))
	for index, candidate := range schemas {
		packages[index] = codegen.RelationReversePackage{
			Alias:      candidate.name,
			ImportPath: modulePath + "/" + candidate.name,
			Schema:     candidate.schema,
		}
	}
	generated, err := codegen.GenerateProjectRelationReverse("project", packages)
	if err != nil {
		t.Fatalf("generate reverse variant: %v", err)
	}
	return generated
}

func compileProjectRelationReverseVariant(
	t *testing.T,
	modulePath string,
	schemas []namedRelationReverseSchema,
	externalTest string,
) {
	t.Helper()
	directory := writeProjectRelationReverseVariant(t, modulePath, schemas, externalTest, "")
	command := generatedGoCommand(t.Context(), directory, "test", "-mod=mod", "./...")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated reverse variant did not compile: %v\n%s", err, output)
	}
}

func writeProjectRelationReverseVariant(
	t *testing.T,
	modulePath string,
	schemas []namedRelationReverseSchema,
	externalTest string,
	omitObjectFor string,
) string {
	t.Helper()
	directory := newGeneratedModule(t, modulePath)

	bridgePackages := make([]codegen.BridgePackage, len(schemas))
	reversePackages := make([]codegen.RelationReversePackage, len(schemas))
	for index, candidate := range schemas {
		importPath := modulePath + "/" + candidate.name
		bridgePackages[index] = codegen.BridgePackage{Alias: candidate.name, ImportPath: importPath}
		reversePackages[index] = codegen.RelationReversePackage{Alias: candidate.name, ImportPath: importPath, Schema: candidate.schema}

		writeGeneratedAppFixture(t, directory, candidate.name, candidate.name, candidate.schema,
			appFixtureFeatures{object: candidate.name != omitObjectFor})
	}
	bridge, err := codegen.GenerateProjectBridge("project", bridgePackages)
	if err != nil {
		t.Fatalf("generate variant bridge: %v", err)
	}
	reverse, err := codegen.GenerateProjectRelationReverse("project", reversePackages)
	if err != nil {
		t.Fatalf("generate variant reverse: %v", err)
	}
	writeGeneratedTestFile(t, directory, "project/zz_godj_bindings.go", bridge)
	writeGeneratedTestFile(t, directory, "project/zz_godj_relation_reverse.go", reverse)
	if externalTest != "" {
		writeGeneratedTestFile(t, directory, "project/relation_reverse_external_test.go", []byte(externalTest))
	}
	return directory
}

func generatedRelationReverseExternalTest(modulePath string) []byte {
	return []byte(fmt.Sprintf(`package project_test

import (
	"context"
	"errors"
	"testing"

	authors "%s/authors"
	blog "%s/blog"
	project "%s/project"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

type neverBackend struct{}

func (*neverBackend) Query(context.Context, query.Plan) (db.Rows, error) {
	return nil, errors.New("unexpected query")
}

func assertInvalidPlan(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
		t.Fatalf("error = %%v, want query invalid-plan", err)
	}
}

func TestGeneratedReverseSurface(t *testing.T) {
	relations, err := project.BindReverseRelations()
	if err != nil {
		t.Fatalf("BindReverseRelations() error = %%v", err)
	}
	typed := authors.AuthorObjects.Using(&neverBackend{}).Filter(
		relations.AuthorsAuthor.Posts.Title.Exact("Alpha"),
		relations.AuthorsAuthor.ReviewedPosts.Title.Exact("Gamma"),
	).Plan()
	dynamicPredicates, err := relations.AuthorsAuthor.ParseDynamic(nil, []orm.LookupInput{
		{Key: "posts__title", Value: "Alpha"},
		{Key: "reviewed_posts__title", Value: "Gamma"},
	})
	if err != nil {
		t.Fatalf("ParseDynamic() error = %%v", err)
	}
	dynamic := authors.AuthorObjects.Using(&neverBackend{}).Filter(dynamicPredicates...).Plan()
	if !typed.Equal(dynamic) {
		t.Fatalf("typed and dynamic reverse plans differ\ntyped = %%#v\ndynamic = %%#v", typed, dynamic)
	}

	objects, err := project.BindReverseObjects()
	if err != nil {
		t.Fatalf("BindReverseObjects() error = %%v", err)
	}
	author := authors.NewAuthorWithID(1)
	object, err := objects.AuthorsAuthor.From(&neverBackend{}, author)
	if err != nil {
		t.Fatalf("From() error = %%v", err)
	}
	model, err := object.Model()
	if err != nil || model.ID != 1 {
		t.Fatalf("Model() = %%#v, err=%%v", model, err)
	}
	posts1, err := object.Posts()
	if err != nil {
		t.Fatalf("Posts() error = %%v", err)
	}
	posts2, err := object.Posts()
	if err != nil || posts2 != posts1 {
		t.Fatalf("repeated Posts() = %%p, first=%%p, err=%%v", posts2, posts1, err)
	}
	reviewed, err := object.ReviewedPosts()
	if err != nil || reviewed == posts1 {
		t.Fatalf("ReviewedPosts() = %%p, posts=%%p, err=%%v", reviewed, posts1, err)
	}
	fresh, err := object.Fresh()
	if err != nil || fresh == nil || fresh == object {
		t.Fatalf("Fresh() = %%p, object=%%p, err=%%v", fresh, object, err)
	}
	freshPosts, err := fresh.Posts()
	if err != nil || freshPosts == posts1 {
		t.Fatalf("fresh Posts() = %%p, old=%%p, err=%%v", freshPosts, posts1, err)
	}

	copyValue := *object
	_, err = (&copyValue).Model()
	assertInvalidPlan(t, err)
	_, err = new(project.AuthorsAuthorReverseObject).Posts()
	assertInvalidPlan(t, err)
	var nilObject *project.AuthorsAuthorReverseObject
	_, err = nilObject.ReviewedPosts()
	assertInvalidPlan(t, err)
	if _, err := objects.AuthorsAuthor.From(nil, author); err == nil {
		t.Fatal("From() accepted nil backend")
	}
	var typedNil *neverBackend
	if _, err := objects.AuthorsAuthor.From(typedNil, author); err == nil {
		t.Fatal("From() accepted typed-nil backend")
	}

	var _ orm.Predicate[authors.Author] = relations.AuthorsAuthor.Posts.ID.Exact(1)
	var _ *orm.RelatedSet[blog.Post] = posts1
}
`, modulePath, modulePath, modulePath))
}

func TestGeneratedReverseSelectorCanMatchFactoryMethodOnDifferentReceiver(t *testing.T) {
	authors, blog := testschema.QueryRelation()
	blog.Models[0].Fields[2].Relation.Reverse.Name = "from"
	compileProjectRelationReverseVariant(t, "example.com/reverse-from", []namedRelationReverseSchema{
		{name: "authors", schema: authors},
		{name: "blog", schema: blog},
	}, `package project_test

import project "example.com/reverse-from/project"

var _ = project.AuthorsAuthorReverseObjectFactory.From
var _ = (*project.AuthorsAuthorReverseObject).From
`)
}
