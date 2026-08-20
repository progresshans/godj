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

func TestGenerateProjectRelationDeleteIsCanonicalAndByteLocked(t *testing.T) {
	t.Parallel()

	authors, blog := relationQueryGenerationSchemas()
	packages := relationDeletePackages("example.com/godj-relation-delete", "authors", "blog", authors, blog)
	first, err := codegen.GenerateProjectRelationDelete("project", packages)
	if err != nil {
		t.Fatalf("GenerateProjectRelationDelete() error = %v", err)
	}
	second, err := codegen.GenerateProjectRelationDelete(
		"project",
		[]codegen.RelationObjectPackage{packages[1], packages[0]},
	)
	if err != nil {
		t.Fatalf("GenerateProjectRelationDelete() permuted package error = %v", err)
	}
	reorderedBlog := blog.Clone()
	reorderedBlog.Models[0].Fields[2], reorderedBlog.Models[0].Fields[3] =
		reorderedBlog.Models[0].Fields[3], reorderedBlog.Models[0].Fields[2]
	third, err := codegen.GenerateProjectRelationDelete(
		"project",
		relationDeletePackages("example.com/godj-relation-delete", "authors", "blog", authors, reorderedBlog),
	)
	if err != nil {
		t.Fatalf("GenerateProjectRelationDelete() permuted edge error = %v", err)
	}
	if !bytes.Equal(first, second) || !bytes.Equal(first, third) {
		t.Fatalf("semantic input order changed relation delete bytes\nfirst:\n%s\npackage permutation:\n%s\nedge permutation:\n%s", first, second, third)
	}

	want, err := os.ReadFile(filepath.Join("testdata", "relation_delete", "project.golden"))
	if err != nil {
		t.Fatalf("read project relation delete golden: %v\ngenerated:\n%s", err, first)
	}
	if !bytes.Equal(first, want) {
		t.Fatalf("project relation delete bytes drifted\ngot:\n%s\nwant:\n%s", first, want)
	}

	for _, fragment := range [][]byte{
		[]byte(`const GoDjProjectRelationDeleteGeneratorVersion = "godj-codegen-rel-delete-project-v1"`),
		[]byte(`authors "example.com/godj-relation-delete/target"`),
		[]byte(`orm "github.com/progresshans/godj/orm"`),
		[]byte(`ir "github.com/progresshans/godj/schema/ir"`),
		[]byte(`query "github.com/progresshans/godj/query"`),
		[]byte("var _ orm.WriteDescriptor[authors.Author] = authors.AuthorDescriptor{}"),
		[]byte("type RelationDeleters struct"),
		[]byte("AuthorsAuthor orm.RelationDeleter[authors.Author]"),
		[]byte("func BindRelationDeleters() (RelationDeleters, error)"),
		[]byte("_binding, _err := Bind()"),
		[]byte("_targets := make(map[ir.ModelIdentity]struct{})"),
		[]byte("_targets[_relation.Target] = struct{}{}"),
		[]byte(`ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}`),
		[]byte("_deleter0, _err := orm.BindRelationDeleter("),
	} {
		if !bytes.Contains(first, fragment) {
			t.Fatalf("project relation delete source does not contain %q:\n%s", fragment, first)
		}
	}
	for _, forbidden := range [][]byte{
		[]byte(`blog "example.com/godj-relation-delete/source"`),
		[]byte("func BindAuthorsAuthor"),
		[]byte("func BindAuthor"),
		[]byte("func (_"),
		[]byte("panic("),
		[]byte("reflect."),
		[]byte("func init("),
	} {
		if bytes.Contains(first, forbidden) {
			t.Fatalf("project relation delete source contains forbidden %q:\n%s", forbidden, first)
		}
	}
	wantExported := []string{
		"BindRelationDeleters",
		"GoDjProjectRelationDeleteGeneratorVersion",
		"RelationDeleters",
	}
	if got := projectRelationDeleteExportedDeclarations(t, first); !slices.Equal(got, wantExported) {
		t.Fatalf("project relation delete exported declarations = %v, want %v", got, wantExported)
	}
	targetCheck := bytes.Index(first, []byte(`_targets[ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}]`))
	bindCall := bytes.Index(first, []byte("orm.BindRelationDeleter("))
	if targetCheck < 0 || bindCall < 0 || targetCheck >= bindCall {
		t.Fatalf("target-set validation index %d must precede binder index %d", targetCheck, bindCall)
	}
	if calls := bytes.Count(first, []byte("Bind()")); calls != 1 {
		t.Fatalf("generated project binding calls = %d, want exactly 1", calls)
	}

	packages[1].Schema.Models[0].Fields[2].Relation.Target.AppLabel = "mutated"
	if bytes.Contains(first, []byte("mutated")) {
		t.Fatal("post-generation schema mutation changed generated relation delete bytes")
	}
}

func TestGenerateProjectRelationDeleteLocksExactFingerprintV1AndSemanticDrift(t *testing.T) {
	t.Parallel()

	authors, blog := relationQueryGenerationSchemas()
	const modulePath = "example.com/godj-relation-delete-fingerprint"
	baseline := mustGeneratedCode(t, "relation delete fingerprint baseline", func() ([]byte, error) {
		return codegen.GenerateProjectRelationDelete(
			"project",
			relationDeletePackages(modulePath, "authors", "blog", authors, blog),
		)
	})
	const exactDigest = "eb6914dc35eb53e3df8c392f7a6dac52dc81f9bfd00910adf5fda3bcf99c9a58"
	if !bytes.Contains(baseline, []byte(`"`+exactDigest+`"`)) {
		t.Fatalf("relation delete fingerprint does not contain exact v1 digest %s:\n%s", exactDigest, baseline)
	}

	mutations := []struct {
		name string
		edit func(*ir.Schema, *ir.Schema)
	}{
		{name: "target table", edit: func(target, _ *ir.Schema) { target.Models[0].DBTable = "writer" }},
		{name: "target primary key column", edit: func(target, _ *ir.Schema) { target.Models[0].Fields[0].Column = "author_pk" }},
		{name: "source table", edit: func(_, source *ir.Schema) { source.Models[0].DBTable = "entry" }},
		{name: "source primary key column", edit: func(_, source *ir.Schema) { source.Models[0].Fields[0].Column = "post_pk" }},
		{name: "foreign key column", edit: func(_, source *ir.Schema) { source.Models[0].Fields[2].Column = "writer_id" }},
		{name: "delete policy", edit: func(_, source *ir.Schema) { source.Models[0].Fields[3].Relation.OnDelete = ir.DeleteProtect }},
		{name: "incoming edge count", edit: func(_, source *ir.Schema) { source.Models[0].Fields = source.Models[0].Fields[:3] }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			target := authors.Clone()
			source := blog.Clone()
			mutation.edit(&target, &source)
			candidate, err := codegen.GenerateProjectRelationDelete(
				"project",
				relationDeletePackages(modulePath, "authors", "blog", target, source),
			)
			if err != nil {
				t.Fatalf("GenerateProjectRelationDelete() semantic mutation error = %v", err)
			}
			if bytes.Equal(candidate, baseline) || bytes.Contains(candidate, []byte(`"`+exactDigest+`"`)) {
				t.Fatalf("semantic mutation retained baseline fingerprint:\n%s", candidate)
			}
		})
	}
}

func TestGenerateProjectRelationDeleteRejectsInvalidInputsBeforeBytes(t *testing.T) {
	t.Parallel()

	authors, blog := relationQueryGenerationSchemas()
	valid := relationDeletePackages("example.com/godj-relation-delete-invalid", "authors", "blog", authors, blog)
	unsupportedPolicy := blog.Clone()
	unsupportedPolicy.Models[0].Fields[2].Relation.OnDelete = ir.DeletePolicy("cascade")
	invalidSetNull := blog.Clone()
	invalidSetNull.Models[0].Fields[3].Nullable = false
	invalidTargetKey := authors.Clone()
	invalidTargetKey.Models[0].Fields[0].PrimaryKey = false
	reservedAlias := relationDeletePackages("example.com/godj-relation-delete-reserved", "query", "blog", authors, blog)
	reservedPath := relationDeletePackages("example.com/godj-relation-delete-reserved", "authors", "blog", authors, blog)
	reservedPath[0].ImportPath = "github.com/progresshans/godj/orm"
	for _, test := range []struct {
		name     string
		pkg      string
		packages []codegen.RelationObjectPackage
		contains string
	}{
		{name: "invalid generated package", pkg: "bad-package", packages: valid, contains: "package"},
		{name: "missing target package", pkg: "project", packages: valid[1:], contains: "target"},
		{name: "reserved import alias", pkg: "project", packages: reservedAlias, contains: "query"},
		{name: "reserved import path", pkg: "project", packages: reservedPath, contains: "github.com/progresshans/godj/orm"},
		{
			name: "unsupported delete policy", pkg: "project",
			packages: relationDeletePackages("example.com/godj-relation-delete-policy", "authors", "blog", authors, unsupportedPolicy),
			contains: "unsupported",
		},
		{
			name: "nonnullable set null", pkg: "project",
			packages: relationDeletePackages("example.com/godj-relation-delete-nullability", "authors", "blog", authors, invalidSetNull),
			contains: "invalid_nullability",
		},
		{
			name: "invalid target key", pkg: "project",
			packages: relationDeletePackages("example.com/godj-relation-delete-key", "authors", "blog", invalidTargetKey, blog),
			contains: "AutoField must be the primary key",
		},
		{
			name:     "aggregate field collision",
			pkg:      "project",
			packages: relationDeleteAggregateFieldCollisionPackages(),
			contains: "ABC",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			generated, err := codegen.GenerateProjectRelationDelete(test.pkg, test.packages)
			if err == nil {
				t.Fatal("GenerateProjectRelationDelete() accepted invalid input")
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

func TestGenerateProjectRelationDeleteAliasDiffersFromAppLabelAndZeroUniverse(t *testing.T) {
	t.Parallel()

	authors, blog := relationQueryGenerationSchemas()
	aliased, err := codegen.GenerateProjectRelationDelete(
		"project",
		relationDeletePackages("example.com/godj-relation-delete-alias", "people", "entries", authors, blog),
	)
	if err != nil {
		t.Fatalf("GenerateProjectRelationDelete() aliased error = %v", err)
	}
	for _, fragment := range [][]byte{
		[]byte(`people "example.com/godj-relation-delete-alias/target"`),
		[]byte("PeopleAuthor orm.RelationDeleter[people.Author]"),
		[]byte("PeopleAuthor: _deleter0"),
		[]byte(`ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}`),
	} {
		if !bytes.Contains(aliased, fragment) {
			t.Fatalf("aliased relation delete source does not contain %q:\n%s", fragment, aliased)
		}
	}

	zero, err := codegen.GenerateProjectRelationDelete("project", nil)
	if err != nil {
		t.Fatalf("GenerateProjectRelationDelete() zero error = %v", err)
	}
	for _, fragment := range [][]byte{
		[]byte("type RelationDeleters struct"),
		[]byte("_binding, _err := Bind()"),
		[]byte("if len(_binding.ForwardRelations()) != 0"),
		[]byte("return RelationDeleters{}, nil"),
	} {
		if !bytes.Contains(zero, fragment) {
			t.Fatalf("zero relation delete source does not contain %q:\n%s", fragment, zero)
		}
	}
	for _, forbidden := range [][]byte{[]byte("orm \""), []byte("ir \""), []byte("RelationDeleter[")} {
		if bytes.Contains(zero, forbidden) {
			t.Fatalf("zero relation delete source contains unused %q:\n%s", forbidden, zero)
		}
	}
	wantExported := []string{
		"BindRelationDeleters",
		"GoDjProjectRelationDeleteGeneratorVersion",
		"RelationDeleters",
	}
	if got := projectRelationDeleteExportedDeclarations(t, zero); !slices.Equal(got, wantExported) {
		t.Fatalf("zero relation delete exported declarations = %v, want %v", got, wantExported)
	}
	nonemptyZero, err := codegen.GenerateProjectRelationDelete(
		"project",
		[]codegen.RelationObjectPackage{{
			Alias: "people", ImportPath: "example.com/godj-relation-delete-zero/people", Schema: authors,
		}},
	)
	if err != nil {
		t.Fatalf("GenerateProjectRelationDelete() nonempty zero-target error = %v", err)
	}
	if !bytes.Equal(nonemptyZero, zero) {
		t.Fatalf("nonempty zero-target universe changed companion bytes\nempty:\n%s\nnonempty:\n%s", zero, nonemptyZero)
	}
}

func TestGenerateProjectRelationDeleteLeavesCurrentPrerequisitesStableAndPreservesLastGood(t *testing.T) {
	t.Parallel()

	authors, blog := relationQueryGenerationSchemas()
	const modulePath = "example.com/godj-relation-delete-current-stability"
	packages := relationDeletePackages(modulePath, "authors", "blog", authors, blog)
	before := projectRelationDeletePrerequisiteBytes(t, modulePath, authors, blog, packages)
	candidate, err := codegen.GenerateProjectRelationDelete("project", packages)
	if err != nil {
		t.Fatalf("GenerateProjectRelationDelete() error = %v", err)
	}
	after := projectRelationDeletePrerequisiteBytes(t, modulePath, authors, blog, packages)
	for index := range before {
		if !bytes.Equal(before[index], after[index]) {
			t.Fatalf("relation delete generation changed prerequisite byte stream %d", index)
		}
	}

	directory := t.TempDir()
	path := filepath.Join(directory, "zz_godj_relation_delete.go")
	lastGood := []byte("package committed\n\nconst LastGood = true\n")
	if err := os.WriteFile(path, lastGood, 0o644); err != nil {
		t.Fatalf("write last-good sentinel: %v", err)
	}
	verifyFailure := errors.New("union verification failed")
	err = codegen.WriteFile(context.Background(), path, candidate, codegen.WriteOptions{
		Verify: func(context.Context, string) error { return verifyFailure },
	})
	if !errors.Is(err, verifyFailure) {
		t.Fatalf("WriteFile() error = %v, want verifier failure", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read last-good sentinel: %v", err)
	}
	if !bytes.Equal(got, lastGood) {
		t.Fatalf("failed union verification changed last-good bytes: %q", got)
	}
}

func TestGeneratedProjectRelationDeleteExactTwelveFileUnionCompilesAndBinds(t *testing.T) {
	authors, blog := relationQueryGenerationSchemas()
	const modulePath = "example.com/godj-relation-delete-union"
	directory, files := writeGeneratedRelationSelectRelatedProject(
		t,
		modulePath,
		"people",
		"entries",
		authors,
		blog,
		false,
	)
	companion, err := codegen.GenerateProjectRelationDelete(
		"project",
		relationDeletePackages(modulePath, "people", "entries", authors, blog),
	)
	if err != nil {
		t.Fatalf("GenerateProjectRelationDelete() union error = %v", err)
	}
	const companionPath = "project/zz_godj_relation_delete.go"
	writeGeneratedTestFile(t, directory, companionPath, companion)
	files = append(files, companionPath)
	if len(files) != 12 {
		t.Fatalf("generated relation delete union has %d files, want exact 12: %v", len(files), files)
	}
	writeGeneratedTestFile(
		t,
		directory,
		"project/relation_delete_external_test.go",
		[]byte(fmt.Sprintf(`package project_test

import (
	"testing"

	project %q
)

func TestGeneratedRelationDeleteAggregateBinds(t *testing.T) {
	deleters, err := project.BindRelationDeleters()
	if err != nil {
		t.Fatalf("BindRelationDeleters() error = %%v", err)
	}
	_ = deleters.PeopleAuthor
}
`, modulePath+"/project")),
	)

	command := exec.Command("go", "test", "-mod=mod", "./...")
	command.Dir = directory
	command.Env = generatedTestEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated exact twelve-file relation delete union did not compile and bind: %v\n%s", err, output)
	}
}

func TestGeneratedProjectRelationDeleteFingerprintDriftFailsCold(t *testing.T) {
	authors, blog := relationQueryGenerationSchemas()
	const modulePath = "example.com/godj-relation-delete-stale-fingerprint"
	directory, _ := writeGeneratedRelationSelectRelatedProject(
		t,
		modulePath,
		"authors",
		"blog",
		authors,
		blog,
		false,
	)
	companion, err := codegen.GenerateProjectRelationDelete(
		"project",
		relationDeletePackages(modulePath, "authors", "blog", authors, blog),
	)
	if err != nil {
		t.Fatalf("GenerateProjectRelationDelete() stale fixture error = %v", err)
	}
	writeGeneratedTestFile(t, directory, "project/zz_godj_relation_delete.go", companion)

	changedBlog := blog.Clone()
	changedBlog.Models[0].Fields[3].Relation.OnDelete = ir.DeleteProtect
	changedMetadata, err := codegen.GenerateRelationMetadata("blog", changedBlog)
	if err != nil {
		t.Fatalf("generate changed source metadata: %v", err)
	}
	writeGeneratedTestFile(t, directory, "source/zz_godj_relation.go", changedMetadata)
	writeGeneratedTestFile(
		t,
		directory,
		"project/relation_delete_stale_external_test.go",
		[]byte(fmt.Sprintf(`package project_test

import (
	"errors"
	"reflect"
	"testing"

	project %q
	"github.com/progresshans/godj/query"
)

func TestGeneratedRelationDeleteRejectsStaleFingerprint(t *testing.T) {
	deleters, err := project.BindRelationDeleters()
	if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
		t.Fatalf("BindRelationDeleters() error = %%v, want query_error/invalid_plan", err)
	}
	if !reflect.DeepEqual(deleters, project.RelationDeleters{}) {
		t.Fatalf("failed relation deleter binding published %%#v", deleters)
	}
}
`, modulePath+"/project")),
	)

	command := exec.Command("go", "test", "-mod=mod", "./...")
	command.Dir = directory
	command.Env = generatedTestEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated stale-fingerprint relation delete gate failed: %v\n%s", err, output)
	}
}

func TestGeneratedProjectRelationDeleteRejectsAddedAndRemovedTargetsBeforeBinding(t *testing.T) {
	authors, blog := relationQueryGenerationSchemas()
	categories, reviews := relationDeleteAdditionalSchemas()
	for _, test := range []struct {
		name       string
		fullDelete bool
		fullBind   bool
	}{
		{name: "added target", fullDelete: false, fullBind: true},
		{name: "removed target", fullDelete: true, fullBind: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			modulePath := "example.com/godj-relation-delete-target-set-" + strings.ReplaceAll(test.name, " ", "-")
			directory, _ := writeGeneratedRelationSelectRelatedProject(
				t,
				modulePath,
				"authors",
				"blog",
				authors,
				blog,
				false,
			)
			writeGeneratedRelationDeleteAdditionalPackages(t, directory, categories, reviews)

			deletePackages := relationDeletePackages(modulePath, "authors", "blog", authors, blog)
			if test.fullDelete {
				deletePackages = append(deletePackages,
					codegen.RelationObjectPackage{Alias: "categories", ImportPath: modulePath + "/category", Schema: categories},
					codegen.RelationObjectPackage{Alias: "reviews", ImportPath: modulePath + "/review", Schema: reviews},
				)
			}
			companion, err := codegen.GenerateProjectRelationDelete("project", deletePackages)
			if err != nil {
				t.Fatalf("GenerateProjectRelationDelete() target-set fixture error = %v", err)
			}
			writeGeneratedTestFile(t, directory, "project/zz_godj_relation_delete.go", companion)

			if test.fullBind {
				binding, err := codegen.GenerateProjectBridge("project", []codegen.BridgePackage{
					{Alias: "authors", ImportPath: modulePath + "/target"},
					{Alias: "blog", ImportPath: modulePath + "/source"},
					{Alias: "categories", ImportPath: modulePath + "/category"},
					{Alias: "reviews", ImportPath: modulePath + "/review"},
				})
				if err != nil {
					t.Fatalf("generate expanded project binding: %v", err)
				}
				writeGeneratedTestFile(t, directory, "project/zz_godj_binding.go", binding)
			}
			writeGeneratedTestFile(
				t,
				directory,
				"project/relation_delete_target_set_external_test.go",
				[]byte(fmt.Sprintf(`package project_test

import (
	"errors"
	"strings"
	"testing"

	project %q
	"github.com/progresshans/godj/query"
)

func TestGeneratedRelationDeleteRejectsTargetSetDrift(t *testing.T) {
	_, err := project.BindRelationDeleters()
	if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
		t.Fatalf("BindRelationDeleters() error = %%v, want query_error/invalid_plan", err)
	}
	if !strings.Contains(err.Error(), "target set") {
		t.Fatalf("BindRelationDeleters() error = %%v, want target-set rejection before field binding", err)
	}
}
`, modulePath+"/project")),
			)

			command := exec.Command("go", "test", "-mod=mod", "./...")
			command.Dir = directory
			command.Env = generatedTestEnvironment()
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("generated target-set drift gate failed: %v\n%s", err, output)
			}
		})
	}
}

func relationDeletePackages(
	modulePath, targetAlias, sourceAlias string,
	authors, blog ir.Schema,
) []codegen.RelationObjectPackage {
	return []codegen.RelationObjectPackage{
		{Alias: targetAlias, ImportPath: modulePath + "/target", Schema: authors},
		{Alias: sourceAlias, ImportPath: modulePath + "/source", Schema: blog},
	}
}

func relationDeleteAggregateFieldCollisionPackages() []codegen.RelationObjectPackage {
	firstTarget := ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "one",
		Models: []ir.Model{{
			Name: "c", GoName: "C",
			Fields: []ir.Field{{Name: "id", GoName: "ID", Kind: ir.FieldAuto, PrimaryKey: true}},
		}},
	}
	secondTarget := ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "two",
		Models: []ir.Model{{
			Name: "bc", GoName: "BC",
			Fields: []ir.Field{{Name: "id", GoName: "ID", Kind: ir.FieldAuto, PrimaryKey: true}},
		}},
	}
	source := ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "source",
		Models: []ir.Model{{
			Name: "link", GoName: "Link",
			Fields: []ir.Field{
				{Name: "id", GoName: "ID", Kind: ir.FieldAuto, PrimaryKey: true},
				{
					Name: "first", GoName: "FirstID", Kind: ir.FieldForeignKey,
					Relation: &ir.ForeignKeyRelation{
						Target: ir.ModelIdentity{AppLabel: "one", ModelName: "c"}, Cardinality: ir.RelationManyToOne,
						Reverse: ir.ReverseRelation{Name: "first_links"}, OnDelete: ir.DeleteProtect,
					},
				},
				{
					Name: "second", GoName: "SecondID", Kind: ir.FieldForeignKey,
					Relation: &ir.ForeignKeyRelation{
						Target: ir.ModelIdentity{AppLabel: "two", ModelName: "bc"}, Cardinality: ir.RelationManyToOne,
						Reverse: ir.ReverseRelation{Name: "second_links"}, OnDelete: ir.DeleteProtect,
					},
				},
			},
		}},
	}
	return []codegen.RelationObjectPackage{
		{Alias: "aB", ImportPath: "example.com/godj-relation-delete-collision/one", Schema: firstTarget},
		{Alias: "a", ImportPath: "example.com/godj-relation-delete-collision/two", Schema: secondTarget},
		{Alias: "source", ImportPath: "example.com/godj-relation-delete-collision/source", Schema: source},
	}
}

func relationDeleteAdditionalSchemas() (ir.Schema, ir.Schema) {
	categories := ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "categories",
		Models: []ir.Model{{
			Name: "category", GoName: "Category",
			Fields: []ir.Field{
				{Name: "id", GoName: "ID", Kind: ir.FieldAuto, PrimaryKey: true},
				{Name: "label", GoName: "Label", Kind: ir.FieldChar, MaxLength: 80},
			},
		}},
	}
	reviews := ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "reviews",
		Models: []ir.Model{{
			Name: "review", GoName: "Review",
			Fields: []ir.Field{
				{Name: "id", GoName: "ID", Kind: ir.FieldAuto, PrimaryKey: true},
				{
					Name: "category", GoName: "CategoryID", Kind: ir.FieldForeignKey,
					Relation: &ir.ForeignKeyRelation{
						Target: ir.ModelIdentity{AppLabel: "categories", ModelName: "category"}, Cardinality: ir.RelationManyToOne,
						Reverse: ir.ReverseRelation{Name: "reviews"}, OnDelete: ir.DeleteProtect,
					},
				},
			},
		}},
	}
	return categories, reviews
}

func writeGeneratedRelationDeleteAdditionalPackages(
	t *testing.T,
	directory string,
	categories, reviews ir.Schema,
) {
	t.Helper()
	for _, candidate := range []struct {
		packageName string
		directory   string
		schema      ir.Schema
	}{
		{packageName: "categories", directory: "category", schema: categories},
		{packageName: "reviews", directory: "review", schema: reviews},
	} {
		main, err := codegen.Generate(candidate.packageName, candidate.schema)
		if err != nil {
			t.Fatalf("generate additional %s main: %v", candidate.packageName, err)
		}
		metadata, err := codegen.GenerateRelationMetadata(candidate.packageName, candidate.schema)
		if err != nil {
			t.Fatalf("generate additional %s metadata: %v", candidate.packageName, err)
		}
		writeGeneratedTestFile(t, directory, candidate.directory+"/zz_godj_generated.go", main)
		writeGeneratedTestFile(t, directory, candidate.directory+"/zz_godj_relation.go", metadata)
	}
}

func projectRelationDeletePrerequisiteBytes(
	t *testing.T,
	modulePath string,
	authors, blog ir.Schema,
	packages []codegen.RelationObjectPackage,
) [][]byte {
	t.Helper()
	return [][]byte{
		mustGeneratedCode(t, "project binding", func() ([]byte, error) {
			return codegen.GenerateProjectBridge("project", []codegen.BridgePackage{
				{Alias: "authors", ImportPath: modulePath + "/target"},
				{Alias: "blog", ImportPath: modulePath + "/source"},
			})
		}),
		mustGeneratedCode(t, "project object", func() ([]byte, error) {
			return codegen.GenerateProjectRelationObject("project", packages)
		}),
		mustGeneratedCode(t, "project select related", func() ([]byte, error) {
			return codegen.GenerateProjectRelationSelectRelated("project", packages)
		}),
		mustGeneratedCode(t, "target main", func() ([]byte, error) { return codegen.Generate("authors", authors) }),
		mustGeneratedCode(t, "source main", func() ([]byte, error) { return codegen.Generate("blog", blog) }),
	}
}

func projectRelationDeleteExportedDeclarations(t *testing.T, source []byte) []string {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), "project_relation_delete.go", source, 0)
	if err != nil {
		t.Fatalf("parse generated relation delete source: %v", err)
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
