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
		[]byte(`const GoDjProjectRelationFacadeGeneratorVersion = "godj-codegen-rel-facade-project-current-v2"`),
		[]byte(`const GoDjProjectRelationFacadeInputSHA256 = "`),
		[]byte("type Backend interface {\n\tdb.Queryer\n\tdb.Mutator\n}"),
		[]byte("type Models struct {\n\tAuthorsAuthor AuthorsAuthorQuery\n\tBlogPost      BlogPostQuery\n}"),
		[]byte("func Using(_backend Backend) (Models, error)"),
		[]byte("func (_query BlogPostQuery) Distinct() BlogPostQuery"),
		[]byte("func (_query BlogPostQuery) Offset(_offset int) (BlogPostQuery, error)"),
		[]byte("func (_query BlogPostQuery) Count(_ctx context.Context) (int64, error)"),
		[]byte("func SelectBlogPostInto[R any](_ctx context.Context, _source BlogPostQuery, _projection orm.Projection[blog.Post, R]) ([]R, error)"),
		[]byte("func AggregateBlogPostInto[R any](_ctx context.Context, _source BlogPostQuery, _aggregate orm.Aggregate[blog.Post, R]) (R, error)"),
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
	for _, targetKeySnapshot := range [][]byte{
		[]byte("_authorTarget.relationFacadePrimaryKey()"),
		[]byte("_reviewerTarget.relationFacadePrimaryKey()"),
	} {
		if count := bytes.Count(first, targetKeySnapshot); count != 1 {
			t.Fatalf("generated facade target-key snapshot %q count = %d, want exactly 1", targetKeySnapshot, count)
		}
	}
	if bindIndex, nilIndex := bytes.Index(first, []byte("BindObjects()")), bytes.Index(first, []byte("relationFacadeNil(_backend)")); bindIndex < 0 || nilIndex < 0 || bindIndex >= nilIndex {
		t.Fatalf("BindObjects index %d must precede backend nil validation index %d", bindIndex, nilIndex)
	}
	wantExported := []string{
		"AggregateAuthorsAuthorInto",
		"AggregateBlogPostInto",
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
		"SelectAuthorsAuthorInto",
		"SelectBlogPostInto",
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
	saveCollision := blog.Clone()
	saveCollision.Models[0].Fields[2].GoName = "SaveID"
	derivedMethodCollision := blog.Clone()
	derivedMethodCollision.Models[0].Fields[3].GoName = "WithAuthorID"

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
		{name: "wrapper Save collision", pkg: "project", packages: relationFacadePackages("example.com/godj-relation-facade-save", authors, saveCollision), contains: "Save"},
		{name: "derived method collision", pkg: "project", packages: relationFacadePackages("example.com/godj-relation-facade-derived", authors, derivedMethodCollision), contains: "WithAuthor"},
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
			FormatVersion: ir.CurrentFormatVersion,
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
		directory, facade := writeGeneratedRelationFacadeUniverse(
			t,
			modulePath,
			packages,
			nil,
			generatedRelationFacadeTypedResultCompileTest(modulePath),
		)
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

func TestGeneratedProjectRelationFacadeRejectsCrossModelTypedResultsAtCompileTime(t *testing.T) {
	t.Parallel()

	authors, blog := relationQueryGenerationSchemas()
	const modulePath = "example.com/godj-relation-facade-cross-result"
	directory, _ := writeGeneratedRelationFacadeUniverse(
		t,
		modulePath,
		relationFacadePackages(modulePath, authors, blog),
		nil,
		generatedRelationFacadeCrossModelResultCompileFailure(modulePath),
	)
	command := exec.Command("go", "test", "-mod=mod", "./...")
	command.Dir = directory
	command.Env = generatedTestEnvironment()
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("cross-model typed results unexpectedly compiled")
	}
	if !bytes.Contains(output, []byte("Projection[")) || !bytes.Contains(output, []byte("SelectBlogPostInto")) {
		t.Fatalf("cross-model compile failure did not identify the typed projection boundary: %v\n%s", err, output)
	}
	if !bytes.Contains(output, []byte("Aggregate[")) || !bytes.Contains(output, []byte("AggregateBlogPostInto")) {
		t.Fatalf("cross-model compile failure did not identify the typed aggregate boundary: %v\n%s", err, output)
	}
}

func TestProjectRelationFacadeRuntime(t *testing.T) {
	authors, blog := relationQueryGenerationSchemas()
	reordered := blog.Clone()
	reordered.Models[0].Fields[2], reordered.Models[0].Fields[3] = reordered.Models[0].Fields[3], reordered.Models[0].Fields[2]
	for _, test := range []struct {
		name string
		blog ir.Schema
	}{{name: "declared", blog: blog}, {name: "permuted fields", blog: reordered}} {
		t.Run(test.name, func(t *testing.T) {
			modulePath := "example.com/godj-relation-facade-write-" + strings.ReplaceAll(test.name, " ", "-")
			directory, facade := writeGeneratedRelationFacadeUniverse(
				t,
				modulePath,
				relationFacadePackages(modulePath, authors, test.blog),
				nil,
				generatedProjectRelationFacadeRuntimeTest(modulePath),
			)
			if !bytes.Contains(facade, []byte("func (_model *BlogPost) WithReviewerID(_key int64) (*BlogPost, error)")) ||
				bytes.Contains(facade, []byte("WithReviewerID(_key *int64)")) {
				t.Fatalf("nullable scalar setter did not use the non-pointer key contract:\n%s", facade)
			}
			compileGeneratedRelationFacadeUniverse(t, directory)
			writeGeneratedTestFile(t, directory, "project/pointer_key_compile_negative_test.go", []byte(`package project

func pointerKeyMustNotCompile(_model *BlogPost) {
	_key := int64(1)
	_, _ = _model.WithReviewerID(&_key)
}
`))
			command := exec.Command("go", "test", "-mod=mod", "./project")
			command.Dir = directory
			command.Env = generatedTestEnvironment()
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatal("nullable pointer scalar setter unexpectedly compiled")
			}
			if !bytes.Contains(output, []byte("*int64")) || !bytes.Contains(output, []byte("int64")) {
				t.Fatalf("pointer scalar compile-negative did not identify the key types: %v\n%s", err, output)
			}
		})
	}
}

func TestProjectRelationFacadeReservedImports(t *testing.T) {
	authors, blog := relationQueryGenerationSchemas()
	for _, reserved := range []string{"any", "context", "int", "int64", "iota", "ir", "reflect", "sync", "true"} {
		t.Run(reserved+" alias", func(t *testing.T) {
			packages := relationFacadePackages("example.com/godj-relation-facade-reserved-"+reserved, authors, blog)
			packages[0].Alias = reserved
			if generated, err := codegen.GenerateProjectRelationFacade("project", packages); err == nil || len(generated) != 0 {
				t.Fatalf("reserved alias %q = (%d bytes, %v)", reserved, len(generated), err)
			}
		})
	}
	for _, reserved := range []string{"context", "ir", "sync"} {
		t.Run(reserved+" path", func(t *testing.T) {
			packages := relationFacadePackages("example.com/godj-relation-facade-reserved-"+reserved, authors, blog)
			path := reserved
			if reserved == "ir" {
				path = "github.com/progresshans/godj/schema/ir"
			}
			packages[0].ImportPath = path
			if generated, err := codegen.GenerateProjectRelationFacade("project", packages); err == nil || len(generated) != 0 {
				t.Fatalf("reserved path %q = (%d bytes, %v)", reserved, len(generated), err)
			}
		})
	}
	t.Run("non-conflicting alias", func(t *testing.T) {
		packages := relationFacadePackages("example.com/godj-relation-facade-non-conflicting", authors, blog)
		packages[0].Alias = "domainapp"
		generated, err := codegen.GenerateProjectRelationFacade("project", packages)
		if err != nil {
			t.Fatalf("non-conflicting alias rejected: %v", err)
		}
		if len(generated) == 0 {
			t.Fatal("non-conflicting alias returned empty generated bytes")
		}
	})
}

func TestProjectRelationFacadeEagerCOW(t *testing.T) {
	authors, blog := relationQueryGenerationSchemas()
	const modulePath = "example.com/godj-relation-facade-write-eager"
	directory, _ := writeGeneratedRelationFacadeUniverse(
		t,
		modulePath,
		relationFacadePackages(modulePath, authors, blog),
		nil,
		generatedProjectRelationFacadeEagerCOWTest(modulePath),
	)
	compileGeneratedRelationFacadeUniverse(t, directory)
}

func TestProjectRelationFacadeEmptyUniverseCompiles(t *testing.T) {
	const modulePath = "example.com/godj-relation-facade-write-empty"
	directory, _ := writeGeneratedRelationFacadeUniverse(t, modulePath, nil, nil, nil)
	compileGeneratedRelationFacadeUniverse(t, directory)
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
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "one",
		Models: []ir.Model{{
			Name: "bc", GoName: "BC", DBTable: "one_bc",
			Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
		}},
	}
	second := ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
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
		FormatVersion: ir.CurrentFormatVersion,
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

func generatedProjectRelationFacadeRuntimeTest(modulePath string) []byte {
	return []byte(fmt.Sprintf(`package project

import (
	"context"
	"errors"
	"reflect"
	"testing"

	authors %q
	blog %q
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

type relationFacadeRow struct { values []any }

func (row relationFacadeRow) Scan(destinations ...any) error {
	if len(destinations) != len(row.values) { return errors.New("scan width mismatch") }
	for index, destination := range destinations {
		reflected := reflect.ValueOf(destination)
		if reflected.Kind() != reflect.Pointer { return errors.New("scan destination is not a pointer") }
		value := row.values[index]
		if value == nil { continue }
		reflected.Elem().Set(reflect.ValueOf(value).Convert(reflected.Elem().Type()))
	}
	return nil
}

type relationFacadeRows struct { rows [][]any; index int }

func (rows *relationFacadeRows) Next() bool { return rows.index < len(rows.rows) }
func (rows *relationFacadeRows) Scan(destinations ...any) error {
	if rows.index >= len(rows.rows) { return errors.New("scan after rows end") }
	err := (relationFacadeRow{values: rows.rows[rows.index]}).Scan(destinations...)
	rows.index++
	return err
}
func (*relationFacadeRows) Err() error { return nil }
func (*relationFacadeRows) Close() error { return nil }

type relationFacadeBackend struct {
	queries int
	inserts int
	updates int
	deletes int
	nextID int64
	authors map[int64]string
	posts map[int64]blog.Post
}

var relationFacadeForeignKeyFailure = errors.New("relation facade foreign key sentinel")

func newRelationFacadeBackend() *relationFacadeBackend {
	reviewer := int64(2)
	return &relationFacadeBackend{
		nextID: 40,
		authors: map[int64]string{0: "Zero", 1: "Ada", 2: "Bob", 3: "Cleo"},
		posts: map[int64]blog.Post{10: {ID: 10, Title: "Alpha", AuthorID: 1, ReviewerID: &reviewer}},
	}
}

func (backend *relationFacadeBackend) io() int { return backend.queries + backend.inserts + backend.updates + backend.deletes }

func (backend *relationFacadeBackend) Query(_ context.Context, plan query.Plan) (db.Rows, error) {
	backend.queries++
	switch plan.Table() {
	case "authors_author":
		for key, name := range backend.authors {
			return &relationFacadeRows{rows: [][]any{{key, name}}}, nil
		}
		return &relationFacadeRows{}, nil
	case "blog_post":
		for _, post := range backend.posts {
			return &relationFacadeRows{rows: [][]any{{post.ID, post.Title, post.AuthorID, post.ReviewerID}}}, nil
		}
		return &relationFacadeRows{}, nil
	default:
		return nil, errors.New("unexpected query table")
	}
}

func (backend *relationFacadeBackend) Insert(_ context.Context, plan query.InsertPlan) (int64, error) {
	backend.inserts++
	if plan.Table() == "authors_author" {
		backend.nextID++
		backend.authors[backend.nextID] = "saved"
		return backend.nextID, nil
	}
	if plan.Table() != "blog_post" { return 0, errors.New("unexpected insert table") }
	var post blog.Post
	for _, assignment := range plan.Assignments() {
		switch assignment.Field().Name() {
		case "title": post.Title, _ = assignment.Value().String()
		case "author": post.AuthorID, _ = assignment.Value().Integer()
		case "reviewer":
			if !assignment.Value().IsNull() { value, _ := assignment.Value().Integer(); post.ReviewerID = &value }
		}
	}
	if _, ok := backend.authors[post.AuthorID]; !ok { return 0, relationFacadeForeignKeyFailure }
	backend.nextID++
	post.ID = backend.nextID
	backend.posts[post.ID] = post
	return post.ID, nil
}

func (backend *relationFacadeBackend) Update(context.Context, query.UpdatePlan) (int64, error) { backend.updates++; return 1, nil }
func (backend *relationFacadeBackend) Delete(context.Context, query.DeletePlan) (int64, error) { backend.deletes++; return 1, nil }

func TestProjectRelationFacadePendingNoPKLaterKeyAndManualKey(t *testing.T) {
	ctx := context.Background()
	backend := newRelationFacadeBackend()
	models, err := Using(backend)
	if err != nil { t.Fatal(err) }
	unset, err := models.BlogPost.New(blog.Post{Title: "required unset"})
	if err != nil { t.Fatal(err) }
	beforeIO := backend.io()
	if _, err := unset.Author(ctx); !errors.Is(err, &query.Error{Category: query.CategoryField, Code: query.CodeRequiredField, Field: "author"}) || backend.io() != beforeIO { t.Fatalf("required-unset accessor = %%v, io=%%d want %%d", err, backend.io(), beforeIO) }
	if reviewer, present, err := unset.Reviewer(ctx); err != nil || present || reviewer != nil || backend.io() != beforeIO { t.Fatalf("new nullable-absent accessor = (%%p,%%v,%%v), io=%%d want %%d", reviewer, present, err, backend.io(), beforeIO) }
	if _, err := unset.Unwrap(); !errors.Is(err, &query.Error{Category: query.CategoryField, Code: query.CodeRequiredField, Field: "author"}) { t.Fatalf("required-unset Unwrap = %%v", err) }
	beforePosts := len(backend.posts)
	if err := unset.Save(ctx); !errors.Is(err, &query.Error{Category: query.CategoryField, Code: query.CodeRequiredField, Field: "author"}) || backend.io() != beforeIO || len(backend.posts) != beforePosts { t.Fatalf("required-unset Save = %%v, io=%%d posts=%%d", err, backend.io(), len(backend.posts)) }
	rawPresent, err := models.BlogPost.New(blog.Post{Title: "raw present", AuthorID: 1})
	if err != nil { t.Fatal(err) }
	if raw, err := rawPresent.Unwrap(); err != nil || raw.AuthorID != 1 { t.Fatalf("new raw nonzero presence = %%#v, %%v", raw, err) }
	loadedZero, err := models.BlogPost.state.wrapBlogPost(blog.Post{ID: 12, Title: "loaded zero", AuthorID: 0})
	if err != nil { t.Fatal(err) }
	if raw, err := loadedZero.Unwrap(); err != nil || raw.AuthorID != 0 { t.Fatalf("loaded zero presence = %%#v, %%v", raw, err) }
	beforeLoadedZero := backend.queries
	if _, err := loadedZero.Author(ctx); err != nil || backend.queries != beforeLoadedZero+1 { t.Fatalf("loaded zero accessor = %%v, queries=%%d want %%d", err, backend.queries, beforeLoadedZero+1) }
	explicitZero, err := unset.WithAuthorID(0)
	if err != nil { t.Fatal(err) }
	if err := explicitZero.Save(ctx); err != nil { t.Fatalf("explicit FK zero Save = %%v", err) }
	explicitZeroRaw, err := explicitZero.Unwrap()
	if err != nil || explicitZeroRaw.ID == 0 || explicitZeroRaw.AuthorID != 0 { t.Fatalf("explicit FK zero raw = %%#v, %%v", explicitZeroRaw, err) }
	zeroTarget, err := models.AuthorsAuthor.New(authors.NewAuthorWithID(0))
	if err != nil { t.Fatal(err) }
	zeroObjectSource, _ := models.BlogPost.New(blog.Post{Title: "object PK zero"})
	zeroObjectDerived, err := zeroObjectSource.WithAuthor(zeroTarget)
	if err != nil { t.Fatal(err) }
	beforeZeroTarget := backend.io()
	if got, err := zeroObjectDerived.Author(ctx); err != nil || got != zeroTarget || backend.io() != beforeZeroTarget { t.Fatalf("object PK zero accessor = %%p, %%v, io=%%d want %%d", got, err, backend.io(), beforeZeroTarget) }
	if !zeroObjectDerived.authorScalarPresent { t.Fatal("object PK zero did not establish source scalar presence") }
	beforeZeroInserts := backend.inserts
	if err := zeroObjectDerived.Save(ctx); err != nil || backend.inserts != beforeZeroInserts+1 { t.Fatalf("object PK zero Save = %%v, inserts=%%d want %%d", err, backend.inserts, beforeZeroInserts+1) }
	author, err := models.AuthorsAuthor.New(authors.Author{Name: "new"})
	if err != nil { t.Fatal(err) }
	post, err := models.BlogPost.New(blog.Post{Title: "draft"})
	if err != nil { t.Fatal(err) }
	derived, err := post.WithAuthor(author)
	if err != nil { t.Fatal(err) }
	beforePending := backend.io()
	got, err := derived.Author(ctx)
	if err != nil || got != author || backend.io() != beforePending { t.Fatalf("warm pending accessor = (%%p, %%v), io=%%d want %%d", got, err, backend.io(), beforePending) }
	if _, err := derived.Unwrap(); !errors.Is(err, &query.Error{Category: query.CategoryModelState, Code: query.CodeUnsavedRelatedObject}) { t.Fatalf("pending Unwrap error = %%v", err) }
	if err := derived.Save(ctx); !errors.Is(err, &query.Error{Category: query.CategoryModelState, Code: query.CodeUnsavedRelatedObject}) || backend.io() != beforePending { t.Fatalf("pending Save = %%v, io=%%d want %%d", err, backend.io(), beforePending) }
	if err := author.Save(ctx); err != nil { t.Fatal(err) }
	if err := derived.Save(ctx); err != nil { t.Fatal(err) }
	raw, err := derived.Unwrap()
	if err != nil || raw.ID == 0 || raw.AuthorID == 0 { t.Fatalf("reconciled source = %%#v, %%v", raw, err) }

	manual, err := models.AuthorsAuthor.New(authors.NewAuthorWithID(999))
	if err != nil { t.Fatal(err) }
	manualPost, _ := models.BlogPost.New(blog.Post{Title: "manual"})
	manualDerived, _ := manualPost.WithAuthor(manual)
	beforePosts = len(backend.posts)
	beforeInserts := backend.inserts
	err = manualDerived.Save(ctx)
	if !errors.Is(err, relationFacadeForeignKeyFailure) || errors.Is(err, &query.Error{Category: query.CategoryModelState, Code: query.CodeUnsavedRelatedObject}) || backend.inserts != beforeInserts+1 || len(backend.posts) != beforePosts { t.Fatalf("manual-key DB reach = %%v, inserts=%%d want %%d posts=%%d", err, backend.inserts, beforeInserts+1, len(backend.posts)) }
}

func TestProjectRelationFacadeSameChangedScalarAndUnrelatedCacheCOW(t *testing.T) {
	ctx := context.Background()
	backend := newRelationFacadeBackend()
	models, _ := Using(backend)
	author, _ := models.AuthorsAuthor.New(authors.NewAuthorWithID(1))
	reviewer, _ := models.AuthorsAuthor.New(authors.NewAuthorWithID(2))
	post, _ := models.BlogPost.New(blog.Post{Title: "cache"})
	post, _ = post.WithAuthor(author)
	post, _ = post.WithReviewer(reviewer)
	same, _ := post.WithAuthorID(1)
	got, _ := same.Author(ctx)
	if got != author || backend.queries != 0 { t.Fatalf("same scalar lost cache: got=%%p want=%%p queries=%%d", got, author, backend.queries) }
	changed, _ := same.WithAuthorID(3)
	before := backend.queries
	if _, err := changed.Author(ctx); err != nil { t.Fatal(err) }
	if backend.queries != before+1 { t.Fatalf("changed scalar queries=%%d want %%d", backend.queries, before+1) }
	before = backend.queries
	gotReviewer, present, err := changed.Reviewer(ctx)
	if err != nil || !present || gotReviewer != reviewer || backend.queries != before { t.Fatalf("unrelated reviewer cache = (%%p,%%v,%%v) queries=%%d", gotReviewer, present, err, backend.queries) }
	originalReviewer, present, err := post.Reviewer(ctx)
	if err != nil || !present || originalReviewer != reviewer { t.Fatalf("original reviewer changed = (%%p,%%v,%%v)", originalReviewer, present, err) }
	if post.authorCache == changed.authorCache || post.reviewerCache == changed.reviewerCache { t.Fatal("derived wrapper shared a mutable cache cell") }
	cleared, err := changed.ClearReviewer()
	if err != nil { t.Fatal(err) }
	before = backend.queries
	gotReviewer, present, err = cleared.Reviewer(ctx)
	if err != nil || present || gotReviewer != nil || backend.queries != before { t.Fatalf("cleared reviewer = (%%p,%%v,%%v), queries=%%d", gotReviewer, present, err, backend.queries) }
	raw, err := cleared.Unwrap()
	if err != nil || raw.ReviewerID != nil { t.Fatalf("cleared raw = %%#v, %%v", raw, err) }
	if err := cleared.Save(ctx); err != nil { t.Fatal(err) }
	unsavedReviewer, _ := models.AuthorsAuthor.New(authors.Author{Name: "pending reviewer"})
	pendingReviewer, _ := post.WithReviewer(unsavedReviewer)
	if _, err := pendingReviewer.Unwrap(); !errors.Is(err, &query.Error{Category: query.CategoryModelState, Code: query.CodeUnsavedRelatedObject}) { t.Fatalf("nullable pending Unwrap error = %%v", err) }
	before = backend.io()
	if err := pendingReviewer.Save(ctx); !errors.Is(err, &query.Error{Category: query.CategoryModelState, Code: query.CodeUnsavedRelatedObject}) || backend.io() != before { t.Fatalf("nullable pending Save = %%v, io=%%d want %%d", err, backend.io(), before) }
	clearedPending, err := pendingReviewer.ClearReviewer()
	if err != nil { t.Fatal(err) }
	before = backend.queries
	gotReviewer, present, err = clearedPending.Reviewer(ctx)
	if err != nil || present || gotReviewer != nil || backend.queries != before { t.Fatalf("pending then clear reviewer = (%%p,%%v,%%v), queries=%%d", gotReviewer, present, err, backend.queries) }
	clearedPendingRaw, err := clearedPending.Unwrap()
	if err != nil || clearedPendingRaw.ReviewerID != nil { t.Fatalf("cleared pending Unwrap = %%#v, %%v", clearedPendingRaw, err) }
}

func TestProjectRelationFacadeKeyPresentTargetMutationKeepsScalarAndInvalidates(t *testing.T) {
	ctx := context.Background()
	backend := newRelationFacadeBackend()
	models, _ := Using(backend)
	target, _ := models.AuthorsAuthor.New(authors.NewAuthorWithID(1))
	source, _ := models.BlogPost.New(blog.Post{Title: "pk mutation"})
	derived, _ := source.WithAuthor(target)
	(authors.AuthorDescriptor{}).SetPrimaryKey(&target.model, 3)
	if err := derived.Save(ctx); err != nil { t.Fatal(err) }
	raw, err := derived.Unwrap()
	if err != nil || raw.AuthorID != 1 { t.Fatalf("source followed mutated target key: %%#v, %%v", raw, err) }
	before := backend.queries
	got, err := derived.Author(ctx)
	if err != nil || backend.queries != before+1 || got == target { t.Fatalf("invalidated accessor = %%p, %%v queries=%%d", got, err, backend.queries) }
}

func TestProjectRelationFacadeCanonicalRelationPreflightOrder(t *testing.T) {
	ctx := context.Background()
	backend := newRelationFacadeBackend()
	models, _ := Using(backend)
	source, _ := models.BlogPost.New(blog.Post{Title: "two pending edges"})
	author, _ := models.AuthorsAuthor.New(authors.Author{Name: "pending author"})
	reviewer, _ := models.AuthorsAuthor.New(authors.Author{Name: "pending reviewer"})
	derived, _ := source.WithReviewer(reviewer)
	derived, _ = derived.WithAuthor(author)
	before := backend.io()
	err := derived.Save(ctx)
	if !errors.Is(err, &query.Error{Category: query.CategoryModelState, Code: query.CodeUnsavedRelatedObject, Field: "author"}) || backend.io() != before {
		t.Fatalf("canonical preflight = %%v, io=%%d want %%d", err, backend.io(), before)
	}

	requiredUnset, _ := models.BlogPost.New(blog.Post{Title: "required unset reviewer pending"})
	reviewerOnly, _ := requiredUnset.WithReviewer(reviewer)
	before = backend.io()
	err = reviewerOnly.Save(ctx)
	if !errors.Is(err, &query.Error{Category: query.CategoryModelState, Code: query.CodeUnsavedRelatedObject, Field: "reviewer"}) || backend.io() != before {
		t.Fatalf("pending target did not precede required unset: %%v, io=%%d want %%d", err, backend.io(), before)
	}
	if _, err := reviewerOnly.Unwrap(); !errors.Is(err, &query.Error{Category: query.CategoryModelState, Code: query.CodeUnsavedRelatedObject, Field: "reviewer"}) {
		t.Fatalf("pending Unwrap did not precede required unset: %%v", err)
	}

	lateAuthor, _ := models.AuthorsAuthor.New(authors.Author{Name: "late author"})
	laterReviewer, _ := models.AuthorsAuthor.New(authors.Author{Name: "later reviewer"})
	staged, _ := requiredUnset.WithAuthor(lateAuthor)
	staged, _ = staged.WithReviewer(laterReviewer)
	if err := lateAuthor.Save(ctx); err != nil { t.Fatal(err) }
	objectBefore := staged.object
	authorStateBefore, authorTargetBefore, authorPendingBefore, _ := staged.authorCache.snapshot()
	reviewerStateBefore, reviewerTargetBefore, reviewerPendingBefore, _ := staged.reviewerCache.snapshot()
	before = backend.io()
	err = staged.Save(ctx)
	if !errors.Is(err, &query.Error{Category: query.CategoryModelState, Code: query.CodeUnsavedRelatedObject, Field: "reviewer"}) || backend.io() != before {
		t.Fatalf("later pending validation = %%v, io=%%d want %%d", err, backend.io(), before)
	}
	authorStateAfter, authorTargetAfter, authorPendingAfter, _ := staged.authorCache.snapshot()
	reviewerStateAfter, reviewerTargetAfter, reviewerPendingAfter, _ := staged.reviewerCache.snapshot()
	if staged.model.AuthorID != 0 || staged.authorScalarPresent || staged.object != objectBefore ||
		authorStateAfter != authorStateBefore || authorTargetAfter != authorTargetBefore || authorPendingAfter != authorPendingBefore ||
		reviewerStateAfter != reviewerStateBefore || reviewerTargetAfter != reviewerTargetBefore || reviewerPendingAfter != reviewerPendingBefore {
		t.Fatalf("failed preflight partially published author: raw=%%d scalar=%%v", staged.model.AuthorID, staged.authorScalarPresent)
	}
}

func TestProjectRelationFacadeAllCacheTuplesPrecedeUnsavedTargets(t *testing.T) {
	ctx := context.Background()
	backend := newRelationFacadeBackend()
	models, _ := Using(backend)
	unsavedAuthor, _ := models.AuthorsAuthor.New(authors.Author{Name: "unsaved author"})
	unsavedReviewer, _ := models.AuthorsAuthor.New(authors.Author{Name: "unsaved reviewer"})
	savedAuthor, _ := models.AuthorsAuthor.New(authors.NewAuthorWithID(1))

	assertStructuralFirst := func(label string, candidate *BlogPost) {
		t.Helper()
		modelBefore := (blog.PostDescriptor{}).CloneWriteModel(candidate.model)
		_, primaryBefore := (blog.PostDescriptor{}).PrimaryKey(modelBefore)
		objectBefore := candidate.object
		authorCacheBefore := candidate.authorCache
		reviewerCacheBefore := candidate.reviewerCache
		authorScalarBefore := candidate.authorScalarPresent
		authorStateBefore := candidate.authorCache.state
		authorTargetBefore := candidate.authorCache.target
		authorPendingBefore := candidate.authorCache.pending
		reviewerStateBefore := candidate.reviewerCache.state
		reviewerTargetBefore := candidate.reviewerCache.target
		reviewerPendingBefore := candidate.reviewerCache.pending
		beforeIO := backend.io()
		err := candidate.Save(ctx)
		if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) || backend.io() != beforeIO {
			t.Fatalf("%%s Save = %%v, io=%%d want structural invalid_plan/I/O %%d", label, err, backend.io(), beforeIO)
		}
		_, primaryAfter := (blog.PostDescriptor{}).PrimaryKey(candidate.model)
		if candidate.model.ID != modelBefore.ID || candidate.model.Title != modelBefore.Title ||
			candidate.model.AuthorID != modelBefore.AuthorID ||
			(candidate.model.ReviewerID == nil) != (modelBefore.ReviewerID == nil) ||
			(candidate.model.ReviewerID != nil && *candidate.model.ReviewerID != *modelBefore.ReviewerID) ||
			primaryAfter != primaryBefore || candidate.authorScalarPresent != authorScalarBefore ||
			candidate.object != objectBefore || candidate.authorCache != authorCacheBefore || candidate.reviewerCache != reviewerCacheBefore ||
			candidate.authorCache.state != authorStateBefore || candidate.authorCache.target != authorTargetBefore || candidate.authorCache.pending != authorPendingBefore ||
			candidate.reviewerCache.state != reviewerStateBefore || candidate.reviewerCache.target != reviewerTargetBefore || candidate.reviewerCache.pending != reviewerPendingBefore {
			t.Fatalf("%%s structural preflight failure partially published source", label)
		}
	}

	source, _ := models.BlogPost.New(blog.Post{Title: "earlier unsaved later corrupt"})
	earlierUnsaved, _ := source.WithAuthor(unsavedAuthor)
	earlierUnsaved.reviewerCache = &relationFacadeRelationCache[AuthorsAuthor]{state: relationFacadeRelationUnassigned, pending: true}
	assertStructuralFirst("earlier unsaved author, later corrupt reviewer", earlierUnsaved)

	selfCorruptTarget := &AuthorsAuthor{state: source.state}
	selfCorrupt, _ := source.WithAuthor(unsavedAuthor)
	selfCorrupt.reviewerCache = &relationFacadeRelationCache[AuthorsAuthor]{
		state: relationFacadeRelationAssignedPresent, target: selfCorruptTarget,
	}
	assertStructuralFirst("earlier unsaved author, later corrupt reviewer self", selfCorrupt)

	otherModels, _ := Using(newRelationFacadeBackend())
	foreignReviewer, _ := otherModels.AuthorsAuthor.New(authors.NewAuthorWithID(2))
	originCorrupt, _ := source.WithAuthor(unsavedAuthor)
	originCorrupt.reviewerCache = &relationFacadeRelationCache[AuthorsAuthor]{
		state: relationFacadeRelationAssignedPresent, target: foreignReviewer,
	}
	assertStructuralFirst("earlier unsaved author, later foreign reviewer origin", originCorrupt)

	reverseSource, _ := models.BlogPost.New(blog.Post{Title: "earlier corrupt later unsaved"})
	reverse, _ := reverseSource.WithAuthor(savedAuthor)
	reverse, _ = reverse.WithReviewer(unsavedReviewer)
	reverse.authorCache = &relationFacadeRelationCache[AuthorsAuthor]{state: relationFacadeRelationAssignedPresent}
	assertStructuralFirst("earlier corrupt author, later unsaved reviewer", reverse)
}

func TestProjectRelationFacadeRebuildConstructionFailureDoesNotPublish(t *testing.T) {
	ctx := context.Background()
	backend := newRelationFacadeBackend()
	models, _ := Using(backend)
	source, _ := models.BlogPost.New(blog.Post{Title: "rebuild failure"})
	author, _ := models.AuthorsAuthor.New(authors.Author{Name: "later key"})
	staged, _ := source.WithAuthor(author)
	if err := author.Save(ctx); err != nil { t.Fatal(err) }
	modelBefore := (blog.PostDescriptor{}).CloneWriteModel(staged.model)
	_, primaryBefore := (blog.PostDescriptor{}).PrimaryKey(modelBefore)
	objectBefore := staged.object
	cacheBefore := staged.authorCache
	stateBefore, targetBefore, pendingBefore, _ := cacheBefore.snapshot()
	scalarBefore := staged.authorScalarPresent
	staged.state.objects.BlogPost = BlogPostObjectFactory{}
	beforeIO := backend.io()
	err := staged.Save(ctx)
	if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) || backend.io() != beforeIO {
		t.Fatalf("rebuild construction failure = %%v, io=%%d want %%d", err, backend.io(), beforeIO)
	}
	stateAfter, targetAfter, pendingAfter, _ := staged.authorCache.snapshot()
	_, primaryAfter := (blog.PostDescriptor{}).PrimaryKey(staged.model)
	if staged.model.ID != modelBefore.ID || staged.model.Title != modelBefore.Title || staged.model.AuthorID != modelBefore.AuthorID ||
		primaryAfter != primaryBefore || staged.object != objectBefore || staged.authorCache != cacheBefore ||
		staged.authorScalarPresent != scalarBefore || stateAfter != stateBefore || targetAfter != targetBefore || pendingAfter != pendingBefore {
		t.Fatalf("rebuild construction failure partially published source")
	}
}

func TestProjectRelationFacadeCorruptCacheTuplesFailBeforeIO(t *testing.T) {
	ctx := context.Background()
	backend := newRelationFacadeBackend()
	models, _ := Using(backend)
	author, _ := models.AuthorsAuthor.New(authors.NewAuthorWithID(1))
	source, _ := models.BlogPost.New(blog.Post{Title: "corrupt cache"})
	source, _ = source.WithAuthor(author)
	assertCorrupt := func(label string, cache *relationFacadeRelationCache[AuthorsAuthor]) {
		t.Helper()
		candidate, _ := source.relationFacadeDerived(source.model)
		candidate.authorCache = cache
		before := backend.io()
		if _, err := candidate.Unwrap(); !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) || backend.io() != before {
			t.Fatalf("%%s Unwrap = %%v, io=%%d want %%d", label, err, backend.io(), before)
		}
		if err := candidate.Save(ctx); !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) || backend.io() != before {
			t.Fatalf("%%s Save = %%v, io=%%d want %%d", label, err, backend.io(), before)
		}
		if _, err := candidate.Author(ctx); !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) || backend.io() != before {
			t.Fatalf("%%s Author = %%v, io=%%d want %%d", label, err, backend.io(), before)
		}
	}
	assertCorrupt("unassigned pending", &relationFacadeRelationCache[AuthorsAuthor]{state: relationFacadeRelationUnassigned, pending: true})
	assertCorrupt("absent target", &relationFacadeRelationCache[AuthorsAuthor]{state: relationFacadeRelationAssignedAbsent, target: author})
	assertCorrupt("present nil", &relationFacadeRelationCache[AuthorsAuthor]{state: relationFacadeRelationAssignedPresent})
}
`, modulePath+"/authors", modulePath+"/blog"))
}

func generatedProjectRelationFacadeEagerCOWTest(modulePath string) []byte {
	return []byte(fmt.Sprintf(`package project

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	authors %q
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

type eagerRows struct { index int }

func (rows *eagerRows) Next() bool { return rows.index == 0 }
func (rows *eagerRows) Scan(destinations ...any) error {
	rows.index++
	values := []any{int64(10), "Alpha", int64(1), int64(2), int64(2), "Bob"}
	for index, destination := range destinations {
		switch typed := destination.(type) {
		case *int64: *typed = values[index].(int64)
		case *string: *typed = values[index].(string)
		case *sql.NullInt64: typed.Int64, typed.Valid = values[index].(int64), true
		case *sql.NullString: typed.String, typed.Valid = values[index].(string), true
		default: return errors.New("unsupported eager scan destination")
		}
	}
	return nil
}
func (*eagerRows) Err() error { return nil }
func (*eagerRows) Close() error { return nil }

type eagerBackend struct { queries int }
func (backend *eagerBackend) Query(context.Context, query.Plan) (db.Rows, error) { backend.queries++; return &eagerRows{}, nil }
func (*eagerBackend) Insert(context.Context, query.InsertPlan) (int64, error) { return 0, nil }
func (*eagerBackend) Update(context.Context, query.UpdatePlan) (int64, error) { return 0, nil }
func (*eagerBackend) Delete(context.Context, query.DeletePlan) (int64, error) { return 0, nil }

func TestProjectRelationFacadeEagerSelectedCacheHasIndependentCOWCell(t *testing.T) {
	ctx := context.Background()
	backend := &eagerBackend{}
	models, err := Using(backend)
	if err != nil { t.Fatal(err) }
	posts, err := models.BlogPost.SelectRelated(models.BlogPost.Related.Reviewer).All(ctx)
	if err != nil || len(posts) != 1 || backend.queries != 1 { t.Fatalf("eager All = %%d, %%v queries=%%d", len(posts), err, backend.queries) }
	loaded := posts[0]
	state, reviewer, pending, err := loaded.reviewerCache.snapshot()
	if err != nil || state != relationFacadeRelationAssignedPresent || reviewer == nil || pending { t.Fatalf("eager cache = %%d, %%p, %%v, %%v", state, reviewer, pending, err) }
	author, _ := models.AuthorsAuthor.New(authors.NewAuthorWithID(1))
	derived, err := loaded.WithAuthor(author)
	if err != nil { t.Fatal(err) }
	before := backend.queries
	got, present, err := derived.Reviewer(ctx)
	if err != nil || !present || got != reviewer || backend.queries != before { t.Fatalf("derived eager reviewer = (%%p,%%v,%%v), queries=%%d", got, present, err, backend.queries) }
	if derived.reviewerCache == loaded.reviewerCache { t.Fatal("derived eager cache cell was shared") }
}
`, modulePath+"/authors"))
}

func generatedRelationFacadeInvalidStateTest(modulePath string) []byte {
	return []byte(fmt.Sprintf(`package project

import (
	"context"
	"errors"
	"testing"

	authors %q
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
	before := backend.io
	err = (&copyValue).Save(context.Background())
	assertFacadeError(t, err, query.CategoryQuery)
	if backend.io != before { t.Fatalf("copied wrapper Save I/O = %%d, want %%d", backend.io, before) }
	err = nilPost.Save(context.Background())
	assertFacadeError(t, err, query.CategoryQuery)

	created, err := models.BlogPost.New(blog.Post{Title: "new", AuthorID: 1})
	if err != nil { t.Fatal(err) }
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	before = backend.io
	if err := created.Save(cancelled); !errors.Is(err, context.Canceled) || backend.io != before { t.Fatalf("cancelled Save = %%v, I/O=%%d want %%d", err, backend.io, before) }

	other, err := Using(backend)
	if err != nil { t.Fatal(err) }
	target, err := models.AuthorsAuthor.New(authors.NewAuthorWithID(1))
	if err != nil { t.Fatal(err) }
	otherSource, err := other.BlogPost.New(blog.Post{Title: "origin"})
	if err != nil { t.Fatal(err) }
	before = backend.io
	_, err = otherSource.WithAuthor(target)
	assertFacadeError(t, err, query.CategoryQuery)
	if backend.io != before { t.Fatalf("cross-origin assignment I/O = %%d, want %%d", backend.io, before) }
	_, err = otherSource.WithAuthor(nil)
	assertFacadeError(t, err, query.CategoryQuery)
	if backend.io != before { t.Fatalf("nil-target assignment I/O = %%d, want %%d", backend.io, before) }
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
`, modulePath+"/authors", modulePath+"/blog"))
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

func generatedRelationFacadeTypedResultCompileTest(modulePath string) []byte {
	return []byte(fmt.Sprintf(`package project

import (
	"context"

	blog %q
	"github.com/progresshans/godj/orm"
)

type typedPostRow struct {
	ID int64
	Title string
}

type typedPostReport struct {
	Count int64
	LatestID orm.Optional[int64]
}

func compileTypedPostResultSurface(ctx context.Context, source BlogPostQuery) error {
	projection := orm.Project2(blog.PostFields.ID, blog.PostFields.Title, func(id int64, title string) typedPostRow {
		return typedPostRow{ID: id, Title: title}
	})
	if _, err := SelectBlogPostInto(ctx, source.Distinct(), projection); err != nil {
		return err
	}
	offset, err := source.Offset(1)
	if err != nil {
		return err
	}
	if _, err := offset.Count(ctx); err != nil {
		return err
	}
	aggregate := orm.Aggregate2(orm.CountRows[blog.Post](), orm.Max(blog.PostFields.ID), func(count int64, latestID orm.Optional[int64]) typedPostReport {
		return typedPostReport{Count: count, LatestID: latestID}
	})
	_, err = AggregateBlogPostInto(ctx, source, aggregate)
	return err
}
`, modulePath+"/blog"))
}

func generatedRelationFacadeCrossModelResultCompileFailure(modulePath string) []byte {
	return []byte(fmt.Sprintf(`package project

import (
	"context"

	authors %q
	"github.com/progresshans/godj/orm"
)

func crossModelTypedResultMustNotCompile(ctx context.Context, source BlogPostQuery) {
	projection := orm.Project1(authors.AuthorFields.ID, func(id int64) int64 { return id })
	_, _ = SelectBlogPostInto(ctx, source, projection)
	aggregate := orm.Aggregate1(orm.CountRows[authors.Author](), func(count int64) int64 { return count })
	_, _ = AggregateBlogPostInto(ctx, source, aggregate)
}
`, modulePath+"/authors"))
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
