package codegen_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema/ir"
)

func TestGenerateProjectRelationFacadeIsCanonicalAndByteLocked(t *testing.T) {
	t.Parallel()

	authors, blog := relationQueryGenerationSchemas()
	const modulePath = "example.com/godj-relation-facade"
	packages := relationFacadePackages(modulePath, authors, blog)
	first, err := codegen.GenerateProjectRelationFacade("project", packages)
	if err != nil {
		t.Fatalf("GenerateProjectRelationFacade() error = %v", err)
	}
	second, err := codegen.GenerateProjectRelationFacade(
		"project",
		[]codegen.RelationObjectPackage{packages[1], packages[0]},
	)
	if err != nil {
		t.Fatalf("GenerateProjectRelationFacade() package permutation error = %v", err)
	}
	reorderedBlog := blog.Clone()
	reorderedBlog.Models[0].Fields[2], reorderedBlog.Models[0].Fields[3] =
		reorderedBlog.Models[0].Fields[3], reorderedBlog.Models[0].Fields[2]
	third, err := codegen.GenerateProjectRelationFacade(
		"project",
		relationFacadePackages(modulePath, authors, reorderedBlog),
	)
	if err != nil {
		t.Fatalf("GenerateProjectRelationFacade() relation permutation error = %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("package ordering changed facade bytes\nfirst:\n%s\npackage permutation:\n%s", first, second)
	}
	if bytes.Equal(first, third) {
		t.Fatal("schema field-order drift did not change the canonical input hash")
	}
	if !bytes.Equal(facadeWithoutInputHash(first), facadeWithoutInputHash(third)) {
		t.Fatalf("field ordering changed facade surface beyond its provenance hash\nfirst:\n%s\nfield permutation:\n%s", first, third)
	}

	want, err := os.ReadFile(filepath.Join("testdata", "relation_facade", "project.golden"))
	if err != nil {
		t.Fatalf("read project relation facade golden: %v\ngenerated hex: %x", err, first)
	}
	if !bytes.Equal(first, want) {
		t.Fatalf("project relation facade bytes drifted\ngot:\n%s\nwant:\n%s", first, want)
	}

	for _, fragment := range [][]byte{
		[]byte(`const GoDjProjectRelationFacadeGeneratorVersion = "godj-codegen-rel-facade-project-v1"`),
		[]byte(`const GoDjProjectRelationFacadeInputSHA256 = "`),
		[]byte("type Backend interface {\n\tdb.Queryer\n\tdb.Mutator\n}"),
		[]byte("type Models struct {\n\tAuthorsAuthor AuthorsAuthorQuery\n\tBlogPost      BlogPostQuery\n}"),
		[]byte("func Using(_backend Backend) (Models, error)"),
		[]byte("func (_query BlogPostQuery) First(_ctx context.Context) (*BlogPost, bool, error)"),
		[]byte("func (_model *BlogPost) Author(_ctx context.Context) (*AuthorsAuthor, error)"),
		[]byte("func (_model *BlogPost) Reviewer(_ctx context.Context) (*AuthorsAuthor, bool, error)"),
		[]byte("type BlogPostRelationSelector interface"),
		[]byte("type BlogPostRelationSelectors struct"),
		[]byte("func (_query BlogPostQuery) SelectRelated(_selector BlogPostRelationSelector) BlogPostEagerQuery"),
		[]byte("func (_query BlogPostEagerQuery) All(_ctx context.Context) ([]*BlogPost, error)"),
		[]byte("_objects, _err := BindObjects()"),
		[]byte("_result.author = _state.objects.BlogPost.SelectRelated(_source).Author()"),
		[]byte("_result.reviewer = _state.objects.BlogPost.SelectRelated(_source).Reviewer()"),
	} {
		if !bytes.Contains(first, fragment) {
			t.Fatalf("generated facade source does not contain %q:\n%s", fragment, first)
		}
	}
	if bindIndex, nilIndex := bytes.Index(first, []byte("BindObjects()")), bytes.Index(first, []byte("relationFacadeNil(_backend)")); bindIndex < 0 || nilIndex < 0 || bindIndex >= nilIndex {
		t.Fatalf("BindObjects index %d must precede backend nil validation index %d", bindIndex, nilIndex)
	}
	wantExported := []string{
		"AuthorsAuthor",
		"AuthorsAuthorQuery",
		"Backend",
		"BlogPost",
		"BlogPostEagerQuery",
		"BlogPostQuery",
		"BlogPostRelationSelector",
		"BlogPostRelationSelectors",
		"GoDjProjectRelationFacadeGeneratorVersion",
		"GoDjProjectRelationFacadeInputSHA256",
		"Models",
		"Using",
	}
	if got := projectRelationFacadeExportedDeclarations(t, first); !slices.Equal(got, wantExported) {
		t.Fatalf("project relation facade exported declarations = %v, want %v", got, wantExported)
	}

	packages[1].Schema.Models[0].Fields[2].Relation.Target.AppLabel = "mutated"
	if bytes.Contains(first, []byte("mutated")) {
		t.Fatal("post-generation schema mutation changed generated facade bytes")
	}
}

func TestGenerateProjectRelationFacadeRejectsInvalidInputsBeforeBytes(t *testing.T) {
	t.Parallel()

	authors, blog := relationQueryGenerationSchemas()
	valid := relationFacadePackages("example.com/godj-relation-facade-invalid", authors, blog)
	reservedAlias := relationFacadePackages("example.com/godj-relation-facade-reserved", authors, blog)
	reservedAlias[0].Alias = "reflect"
	reservedPath := relationFacadePackages("example.com/godj-relation-facade-reserved", authors, blog)
	reservedPath[0].ImportPath = "reflect"
	unwrapCollision := blog.Clone()
	unwrapCollision.Models[0].Fields[2].GoName = "UnwrapID"

	for _, test := range []struct {
		name     string
		pkg      string
		packages []codegen.RelationObjectPackage
		contains string
	}{
		{name: "invalid package", pkg: "bad-package", packages: valid, contains: "package"},
		{name: "missing target", pkg: "project", packages: valid[1:], contains: "target"},
		{name: "reserved reflect alias", pkg: "project", packages: reservedAlias, contains: "reflect"},
		{name: "reserved reflect path", pkg: "project", packages: reservedPath, contains: "reflect"},
		{name: "wrapper Unwrap collision", pkg: "project", packages: relationFacadePackages("example.com/godj-relation-facade-unwrap", authors, unwrapCollision), contains: "Unwrap"},
		{name: "all-model surface collision", pkg: "project", packages: relationFacadeSurfaceCollisionPackages(), contains: "Models field ABC"},
	} {
		t.Run(test.name, func(t *testing.T) {
			generated, err := codegen.GenerateProjectRelationFacade(test.pkg, test.packages)
			if err == nil {
				t.Fatal("GenerateProjectRelationFacade() accepted invalid input")
			}
			if len(generated) != 0 {
				t.Fatalf("invalid input returned %d partial bytes", len(generated))
			}
			if !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("error %q does not identify %q", err, test.contains)
			}
		})
	}
}

func TestGeneratedProjectRelationFacadeBroadUniversesCompile(t *testing.T) {
	authors, blog := relationQueryGenerationSchemas()

	t.Run("unrelated multi-app multi-model", func(t *testing.T) {
		const modulePath = "example.com/godj-relation-facade-broad"
		multiAuthors := authors.Clone()
		multiAuthors.Models = append(multiAuthors.Models, ir.Model{
			Name: "profile", GoName: "Profile", DBTable: "authors_profile",
			Fields: []ir.Field{
				{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
				{Name: "label", GoName: "Label", Column: "label", Kind: ir.FieldChar, MaxLength: 80},
			},
		})
		tags := ir.Schema{
			FormatVersion: ir.FormatVersion,
			AppLabel:      "tags",
			Models: []ir.Model{{
				Name: "tag", GoName: "Tag", DBTable: "tags_tag",
				Fields: []ir.Field{
					{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
					{Name: "name", GoName: "Name", Column: "name", Kind: ir.FieldChar, MaxLength: 80},
				},
			}},
		}
		packages := []codegen.RelationObjectPackage{
			{Alias: "tags", ImportPath: modulePath + "/tags", Schema: tags},
			{Alias: "blog", ImportPath: modulePath + "/blog", Schema: blog},
			{Alias: "authors", ImportPath: modulePath + "/authors", Schema: multiAuthors},
		}
		directory, facade := writeGeneratedRelationFacadeUniverse(t, modulePath, packages, nil, nil)
		for _, fragment := range [][]byte{
			[]byte("AuthorsAuthor  AuthorsAuthorQuery"),
			[]byte("AuthorsProfile AuthorsProfileQuery"),
			[]byte("BlogPost       BlogPostQuery"),
			[]byte("TagsTag        TagsTagQuery"),
		} {
			if !bytes.Contains(facade, fragment) {
				t.Fatalf("broad facade does not contain %q:\n%s", fragment, facade)
			}
		}
		compileGeneratedRelationFacadeUniverse(t, directory)
	})

	t.Run("target also source", func(t *testing.T) {
		const modulePath = "example.com/godj-relation-facade-mutual"
		mutualAuthors, mutualBlog := mutualRelationGenerationSchemas()
		packages := relationFacadePackages(modulePath, mutualAuthors, mutualBlog)
		directory, _ := writeGeneratedRelationFacadeUniverse(
			t,
			modulePath,
			packages,
			nil,
			generatedRelationFacadeCrossSelectorTest(modulePath),
		)
		compileGeneratedRelationFacadeUniverse(t, directory)
	})

	t.Run("self edge", func(t *testing.T) {
		const modulePath = "example.com/godj-relation-facade-self"
		nodes := relationFacadeSelfSchema()
		packages := []codegen.RelationObjectPackage{{Alias: "nodes", ImportPath: modulePath + "/nodes", Schema: nodes}}
		directory, facade := writeGeneratedRelationFacadeUniverse(t, modulePath, packages, nil, nil)
		for _, fragment := range [][]byte{
			[]byte("NodesNode NodesNodeQuery"),
			[]byte("func (_model *NodesNode) Parent(_ctx context.Context) (*NodesNode, bool, error)"),
		} {
			if !bytes.Contains(facade, fragment) {
				t.Fatalf("self facade does not contain %q:\n%s", fragment, facade)
			}
		}
		compileGeneratedRelationFacadeUniverse(t, directory)
	})
}

func TestGeneratedProjectRelationFacadeInvalidStatesAndBindingPrecedence(t *testing.T) {
	authors, blog := relationQueryGenerationSchemas()

	t.Run("nil zero copied", func(t *testing.T) {
		const modulePath = "example.com/godj-relation-facade-invalid-state"
		directory, _ := writeGeneratedRelationFacadeUniverse(
			t,
			modulePath,
			relationFacadePackages(modulePath, authors, blog),
			nil,
			generatedRelationFacadeInvalidStateTest(modulePath),
		)
		compileGeneratedRelationFacadeUniverse(t, directory)
	})

	t.Run("binding cause precedes nil backend", func(t *testing.T) {
		const modulePath = "example.com/godj-relation-facade-binding-precedence"
		binding := []byte(`package project

import (
	"errors"

	"github.com/progresshans/godj/orm"
)

var relationFacadeBinderSentinel = errors.New("relation facade binder sentinel")

func Bind() (orm.ProjectBinding, error) {
	return orm.ProjectBinding{}, relationFacadeBinderSentinel
}
`)
		directory, _ := writeGeneratedRelationFacadeUniverse(
			t,
			modulePath,
			relationFacadePackages(modulePath, authors, blog),
			binding,
			[]byte(`package project

import (
	"context"
	"errors"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

var bindingPrecedenceIO int

type bindingPrecedenceBackend struct{}

func (*bindingPrecedenceBackend) Query(context.Context, query.Plan) (db.Rows, error) {
	bindingPrecedenceIO++
	return nil, nil
}

func (*bindingPrecedenceBackend) Insert(context.Context, query.InsertPlan) (int64, error) {
	bindingPrecedenceIO++
	return 0, nil
}

func (*bindingPrecedenceBackend) Update(context.Context, query.UpdatePlan) (int64, error) {
	bindingPrecedenceIO++
	return 0, nil
}

func (*bindingPrecedenceBackend) Delete(context.Context, query.DeletePlan) (int64, error) {
	bindingPrecedenceIO++
	return 0, nil
}

func TestBindingCausePrecedesNilBackend(t *testing.T) {
	_, err := Using(nil)
	if !errors.Is(err, relationFacadeBinderSentinel) {
		t.Fatalf("Using(nil) error = %v, want exact binder cause", err)
	}
	var typedNil *bindingPrecedenceBackend
	_, err = Using(typedNil)
	if !errors.Is(err, relationFacadeBinderSentinel) {
		t.Fatalf("Using(typed nil) error = %v, want exact binder cause", err)
	}
	if bindingPrecedenceIO != 0 {
		t.Fatalf("binding-precedence backend I/O = %d, want 0", bindingPrecedenceIO)
	}
}
`),
		)
		compileGeneratedRelationFacadeUniverse(t, directory)
	})
}

func TestProjectRelationFacadeFirstPublicationAndPrerequisitePreservation(t *testing.T) {
	t.Parallel()

	authors, blog := relationQueryGenerationSchemas()
	const modulePath = "example.com/godj-relation-facade-publication"
	packages := relationFacadePackages(modulePath, authors, blog)
	before := projectRelationFacadePrerequisiteBytes(t, modulePath, authors, blog, packages)
	candidate, err := codegen.GenerateProjectRelationFacade("project", packages)
	if err != nil {
		t.Fatalf("GenerateProjectRelationFacade() error = %v", err)
	}
	after := projectRelationFacadePrerequisiteBytes(t, modulePath, authors, blog, packages)
	for index := range before {
		if !bytes.Equal(before[index], after[index]) {
			t.Fatalf("facade generation changed prerequisite byte stream %d", index)
		}
	}

	directory := t.TempDir()
	path := filepath.Join(directory, "zz_godj_relation_facade.go")
	verifyCalls := 0
	err = codegen.WriteFile(context.Background(), path, candidate, codegen.WriteOptions{
		Check: true,
		Verify: func(context.Context, string) error {
			verifyCalls++
			return nil
		},
	})
	if !errors.Is(err, codegen.ErrDrift) || verifyCalls != 0 {
		t.Fatalf("missing Check = %v, verify calls = %d, want drift/0", err, verifyCalls)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing Check created target: %v", err)
	}

	verifyFailure := errors.New("candidate union rejected")
	err = codegen.WriteFile(context.Background(), path, candidate, codegen.WriteOptions{
		Verify: func(context.Context, string) error { return verifyFailure },
	})
	if !errors.Is(err, verifyFailure) {
		t.Fatalf("failed first publication error = %v, want verifier failure", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed first publication created target: %v", err)
	}

	err = codegen.WriteFile(context.Background(), path, candidate, codegen.WriteOptions{
		Verify: func(_ context.Context, candidatePath string) error {
			verifyCalls++
			got, readErr := os.ReadFile(candidatePath)
			if readErr != nil {
				return readErr
			}
			if !bytes.Equal(got, candidate) {
				return errors.New("candidate bytes changed")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("first publication error = %v", err)
	}
	if verifyCalls != 1 {
		t.Fatalf("write verifier calls = %d, want 1", verifyCalls)
	}
	verifyCalls = 0
	if err := codegen.WriteFile(context.Background(), path, candidate, codegen.WriteOptions{
		Check: true,
		Verify: func(context.Context, string) error {
			verifyCalls++
			return nil
		},
	}); err != nil || verifyCalls != 0 {
		t.Fatalf("exact Check = %v, verify calls = %d, want nil/0", err, verifyCalls)
	}

	replacement := bytes.Replace(candidate, []byte("backend is nil"), []byte("backend was nil"), 1)
	if bytes.Equal(replacement, candidate) {
		t.Fatal("replacement mutation did not change candidate")
	}
	err = codegen.WriteFile(context.Background(), path, replacement, codegen.WriteOptions{
		Verify: func(context.Context, string) error { return verifyFailure },
	})
	if !errors.Is(err, verifyFailure) {
		t.Fatalf("failed replacement error = %v, want verifier failure", err)
	}
	lastGood, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read last-good facade: %v", err)
	}
	if !bytes.Equal(lastGood, candidate) {
		t.Fatal("failed replacement changed last-good facade bytes")
	}
}

func relationFacadePackages(modulePath string, authors, blog ir.Schema) []codegen.RelationObjectPackage {
	return []codegen.RelationObjectPackage{
		{Alias: "authors", ImportPath: modulePath + "/authors", Schema: authors},
		{Alias: "blog", ImportPath: modulePath + "/blog", Schema: blog},
	}
}

func relationFacadeSurfaceCollisionPackages() []codegen.RelationObjectPackage {
	first := ir.Schema{
		FormatVersion: ir.FormatVersion,
		AppLabel:      "one",
		Models: []ir.Model{{
			Name: "bc", GoName: "BC", DBTable: "one_bc",
			Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
		}},
	}
	second := ir.Schema{
		FormatVersion: ir.FormatVersion,
		AppLabel:      "two",
		Models: []ir.Model{{
			Name: "c", GoName: "C", DBTable: "two_c",
			Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
		}},
	}
	return []codegen.RelationObjectPackage{
		{Alias: "a", ImportPath: "example.com/godj-relation-facade-collision/a", Schema: first},
		{Alias: "aB", ImportPath: "example.com/godj-relation-facade-collision/ab", Schema: second},
	}
}

func relationFacadeSelfSchema() ir.Schema {
	return ir.Schema{
		FormatVersion: ir.RelationFormatVersion,
		AppLabel:      "nodes",
		Models: []ir.Model{{
			Name: "node", GoName: "Node", DBTable: "nodes_node",
			Fields: []ir.Field{
				{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
				{
					Name: "parent", GoName: "ParentID", Column: "parent_id", Kind: ir.FieldForeignKey, Nullable: true,
					Relation: &ir.ForeignKeyRelation{
						Target: ir.ModelIdentity{AppLabel: "nodes", ModelName: "node"}, Cardinality: ir.RelationManyToOne,
						Reverse: ir.ReverseRelation{Name: "children"}, OnDelete: ir.DeleteSetNull,
					},
				},
			},
		}},
	}
}

func writeGeneratedRelationFacadeUniverse(
	t *testing.T,
	modulePath string,
	packages []codegen.RelationObjectPackage,
	bindingOverride, projectTest []byte,
) (string, []byte) {
	t.Helper()
	directory := t.TempDir()
	writeGeneratedTestFile(t, directory, "go.mod", []byte(fmt.Sprintf(`module %s

go 1.26.0

require github.com/progresshans/godj v0.0.0

replace github.com/progresshans/godj => %s
`, modulePath, filepath.ToSlash(codegenRepositoryRoot(t)))))

	bridgePackages := make([]codegen.BridgePackage, len(packages))
	for index, candidate := range packages {
		directoryName := strings.TrimPrefix(candidate.ImportPath, modulePath+"/")
		if directoryName == candidate.ImportPath || directoryName == "" || strings.Contains(directoryName, "..") {
			t.Fatalf("fixture import path %q is not confined to module %q", candidate.ImportPath, modulePath)
		}
		bridgePackages[index] = codegen.BridgePackage{Alias: candidate.Alias, ImportPath: candidate.ImportPath}
		main := mustGeneratedCode(t, candidate.Alias+" main", func() ([]byte, error) {
			return codegen.Generate(candidate.Alias, candidate.Schema)
		})
		metadata := mustGeneratedCode(t, candidate.Alias+" metadata", func() ([]byte, error) {
			return codegen.GenerateRelationMetadata(candidate.Alias, candidate.Schema)
		})
		object := mustGeneratedCode(t, candidate.Alias+" object", func() ([]byte, error) {
			return codegen.GenerateRelationObject(candidate.Alias, candidate.Schema)
		})
		projection := mustGeneratedCode(t, candidate.Alias+" projection", func() ([]byte, error) {
			return codegen.GenerateRelationProjection(candidate.Alias, candidate.Schema)
		})
		writeGeneratedTestFile(t, directory, directoryName+"/zz_godj_generated.go", main)
		writeGeneratedTestFile(t, directory, directoryName+"/zz_godj_relation.go", metadata)
		if candidate.Schema.FormatVersion == ir.RelationFormatVersion {
			relationQuery := mustGeneratedCode(t, candidate.Alias+" query", func() ([]byte, error) {
				return codegen.GenerateRelationQuery(candidate.Alias, candidate.Schema)
			})
			writeGeneratedTestFile(t, directory, directoryName+"/zz_godj_relation_query.go", relationQuery)
		}
		writeGeneratedTestFile(t, directory, directoryName+"/zz_godj_relation_object.go", object)
		writeGeneratedTestFile(t, directory, directoryName+"/zz_godj_relation_projection.go", projection)
	}

	binding := bindingOverride
	if binding == nil {
		binding = mustGeneratedCode(t, "project binding", func() ([]byte, error) {
			return codegen.GenerateProjectBridge("project", bridgePackages)
		})
	}
	object := mustGeneratedCode(t, "project object", func() ([]byte, error) {
		return codegen.GenerateProjectRelationObject("project", packages)
	})
	selectRelated := mustGeneratedCode(t, "project select related", func() ([]byte, error) {
		return codegen.GenerateProjectRelationSelectRelated("project", packages)
	})
	facade := mustGeneratedCode(t, "project relation facade", func() ([]byte, error) {
		return codegen.GenerateProjectRelationFacade("project", packages)
	})
	writeGeneratedTestFile(t, directory, "project/zz_godj_binding.go", binding)
	writeGeneratedTestFile(t, directory, "project/zz_godj_relation_object.go", object)
	writeGeneratedTestFile(t, directory, "project/zz_godj_relation_select_related.go", selectRelated)
	writeGeneratedTestFile(t, directory, "project/zz_godj_relation_facade.go", facade)
	if projectTest != nil {
		writeGeneratedTestFile(t, directory, "project/relation_facade_test.go", projectTest)
	}
	return directory, facade
}

func compileGeneratedRelationFacadeUniverse(t *testing.T, directory string) {
	t.Helper()
	command := exec.Command("go", "test", "-mod=mod", "./...")
	command.Dir = directory
	command.Env = generatedTestEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated relation facade universe did not compile: %v\n%s", err, output)
	}
}

func generatedRelationFacadeInvalidStateTest(modulePath string) []byte {
	return []byte(fmt.Sprintf(`package project

import (
	"context"
	"errors"
	"testing"

	blog %q
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

type facadeBackend struct{ io int }

func (backend *facadeBackend) Query(context.Context, query.Plan) (db.Rows, error) {
	backend.io++
	return nil, errors.New("unexpected facade query")
}
func (backend *facadeBackend) Insert(context.Context, query.InsertPlan) (int64, error) {
	backend.io++
	return 0, errors.New("unexpected facade insert")
}
func (backend *facadeBackend) Update(context.Context, query.UpdatePlan) (int64, error) {
	backend.io++
	return 0, errors.New("unexpected facade update")
}
func (backend *facadeBackend) Delete(context.Context, query.DeletePlan) (int64, error) {
	backend.io++
	return 0, errors.New("unexpected facade delete")
}

var _ Backend = (*facadeBackend)(nil)
var _ Backend = (db.Session)(nil)

func assertFacadeError(t *testing.T, err error, category string) {
	t.Helper()
	if !errors.Is(err, &query.Error{Category: category, Code: query.CodeInvalidPlan}) {
		t.Fatalf("error = %%v, want %%s/invalid_plan", err, category)
	}
}

func TestGeneratedFacadeInvalidStates(t *testing.T) {
	_, err := Using(nil)
	assertFacadeError(t, err, query.CategoryBackend)
	var typedNil *facadeBackend
	_, err = Using(typedNil)
	assertFacadeError(t, err, query.CategoryBackend)

	backend := &facadeBackend{}
	models, err := Using(backend)
	if err != nil {
		t.Fatalf("Using() error = %%v", err)
	}
	if backend.io != 0 {
		t.Fatalf("Using() I/O = %%d, want 0", backend.io)
	}

	var zero Models
	_, err = zero.BlogPost.All(context.Background())
	assertFacadeError(t, err, query.CategoryQuery)
	var zeroEager BlogPostEagerQuery
	_, err = zeroEager.All(context.Background())
	assertFacadeError(t, err, query.CategoryQuery)
	var nilSelector BlogPostRelationSelector
	_, err = models.BlogPost.SelectRelated(nilSelector).All(context.Background())
	assertFacadeError(t, err, query.CategoryQuery)
	var typedNilSelector *blogPostRelationSelector
	_, err = models.BlogPost.SelectRelated(typedNilSelector).All(context.Background())
	assertFacadeError(t, err, query.CategoryQuery)
	zeroSelector := blogPostRelationSelector{}
	_, err = models.BlogPost.SelectRelated(zeroSelector).All(context.Background())
	assertFacadeError(t, err, query.CategoryQuery)
	corruptSelector := blogPostRelationSelector{state: models.BlogPost.state}
	_, err = models.BlogPost.SelectRelated(corruptSelector).All(context.Background())
	assertFacadeError(t, err, query.CategoryQuery)

	var nilPost *BlogPost
	_, err = nilPost.Unwrap()
	assertFacadeError(t, err, query.CategoryQuery)
	_, err = new(BlogPost).Unwrap()
	assertFacadeError(t, err, query.CategoryQuery)

	reviewerID := int64(2)
	wrapped, err := models.BlogPost.state.wrapBlogPost(blog.Post{ID: 1, Title: "post", AuthorID: 1, ReviewerID: &reviewerID})
	if err != nil {
		t.Fatalf("wrapBlogPost() error = %%v", err)
	}
	copyValue := *wrapped
	_, err = (&copyValue).Unwrap()
	assertFacadeError(t, err, query.CategoryQuery)
	first, err := wrapped.Unwrap()
	if err != nil {
		t.Fatalf("first Unwrap() error = %%v", err)
	}
	*first.ReviewerID = 99
	second, err := wrapped.Unwrap()
	if err != nil || second.ReviewerID == nil || *second.ReviewerID != 2 {
		t.Fatalf("second Unwrap() = %%#v, err=%%v", second, err)
	}
	if backend.io != 0 {
		t.Fatalf("invalid facade boundaries performed %%d I/O calls", backend.io)
	}
}
`, modulePath+"/blog"))
}

func generatedRelationFacadeCrossSelectorTest(_ string) []byte {
	return []byte(`package project

import (
	"context"
	"errors"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

type crossSelectorBackend struct{ io int }

func (backend *crossSelectorBackend) Query(context.Context, query.Plan) (db.Rows, error) {
	backend.io++
	return nil, errors.New("unexpected query")
}
func (*crossSelectorBackend) Insert(context.Context, query.InsertPlan) (int64, error) { return 0, nil }
func (*crossSelectorBackend) Update(context.Context, query.UpdatePlan) (int64, error) { return 0, nil }
func (*crossSelectorBackend) Delete(context.Context, query.DeletePlan) (int64, error) { return 0, nil }

func TestCrossModelSelectorFailsBeforeIO(t *testing.T) {
	backend := &crossSelectorBackend{}
	models, err := Using(backend)
	if err != nil {
		t.Fatalf("Using() error = %v", err)
	}
	_, err = models.BlogPost.selectRelated(models.AuthorsAuthor.Related.Manager).All(context.Background())
	if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
		t.Fatalf("cross selector error = %v", err)
	}
	if backend.io != 0 {
		t.Fatalf("cross selector I/O = %d, want 0", backend.io)
	}
}
`)
}

func projectRelationFacadePrerequisiteBytes(
	t *testing.T,
	modulePath string,
	authors, blog ir.Schema,
	packages []codegen.RelationObjectPackage,
) [][]byte {
	t.Helper()
	return [][]byte{
		mustGeneratedCode(t, "project binding", func() ([]byte, error) {
			return codegen.GenerateProjectBridge("project", []codegen.BridgePackage{
				{Alias: "authors", ImportPath: modulePath + "/authors"},
				{Alias: "blog", ImportPath: modulePath + "/blog"},
			})
		}),
		mustGeneratedCode(t, "project object", func() ([]byte, error) {
			return codegen.GenerateProjectRelationObject("project", packages)
		}),
		mustGeneratedCode(t, "project select related", func() ([]byte, error) {
			return codegen.GenerateProjectRelationSelectRelated("project", packages)
		}),
		mustGeneratedCode(t, "authors main", func() ([]byte, error) { return codegen.Generate("authors", authors) }),
		mustGeneratedCode(t, "blog main", func() ([]byte, error) { return codegen.Generate("blog", blog) }),
	}
}

func projectRelationFacadeExportedDeclarations(t *testing.T, source []byte) []string {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), "project_relation_facade.go", source, 0)
	if err != nil {
		t.Fatalf("parse generated relation facade source: %v", err)
	}
	exported := make([]string, 0)
	for _, declaration := range parsed.Decls {
		switch declaration := declaration.(type) {
		case *ast.FuncDecl:
			if declaration.Recv == nil && declaration.Name.IsExported() {
				exported = append(exported, declaration.Name.Name)
			}
		case *ast.GenDecl:
			for _, specification := range declaration.Specs {
				switch specification := specification.(type) {
				case *ast.ValueSpec:
					for _, name := range specification.Names {
						if name.IsExported() {
							exported = append(exported, name.Name)
						}
					}
				case *ast.TypeSpec:
					if specification.Name.IsExported() {
						exported = append(exported, specification.Name.Name)
					}
				}
			}
		}
	}
	slices.Sort(exported)
	return exported
}

func facadeWithoutInputHash(source []byte) []byte {
	lines := bytes.Split(source, []byte("\n"))
	for index := range lines {
		if bytes.HasPrefix(lines[index], []byte("const GoDjProjectRelationFacadeInputSHA256 = ")) {
			lines[index] = []byte("const GoDjProjectRelationFacadeInputSHA256 = \"<canonical-input>\"")
		}
	}
	return bytes.Join(lines, []byte("\n"))
}
