package codegen_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/codegen/internal/testfixture"
	"github.com/progresshans/godj/internal/testschema"
	"github.com/progresshans/godj/schema/ir"
)

func TestGeneratedProjectRelationDeleteExactTwelveFileUnionCompilesAndBinds(t *testing.T) {
	authors, blog := testschema.QueryRelation()
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
		testfixture.TargetSourcePackages(modulePath, "people", "entries", authors, blog),
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

	command := generatedGoCommand(t.Context(), directory, "test", "-mod=mod", "./...")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated exact twelve-file relation delete union did not compile and bind: %v\n%s", err, output)
	}
}

func TestGeneratedProjectRelationDeleteFingerprintDriftFailsCold(t *testing.T) {
	authors, blog := testschema.QueryRelation()
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
		testfixture.TargetSourcePackages(modulePath, "authors", "blog", authors, blog),
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

	command := generatedGoCommand(t.Context(), directory, "test", "-mod=mod", "./...")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated stale-fingerprint relation delete gate failed: %v\n%s", err, output)
	}
}

func TestGeneratedProjectRelationDeleteRejectsAddedAndRemovedTargetsBeforeBinding(t *testing.T) {
	authors, blog := testschema.QueryRelation()
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

			deletePackages := testfixture.TargetSourcePackages(modulePath, "authors", "blog", authors, blog)
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

			command := generatedGoCommand(t.Context(), directory, "test", "-mod=mod", "./...")
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("generated target-set drift gate failed: %v\n%s", err, output)
			}
		})
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
		writeGeneratedAppFixture(t, directory, candidate.directory, candidate.packageName, candidate.schema, appFixtureFeatures{})
	}
}
