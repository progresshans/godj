package codegen_test

import (
	"context"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/internal/testschema"
	"github.com/progresshans/godj/schema/ir"
)

func TestGenerateProjectFullUnionCompilesAndMixedSnapshotFails(t *testing.T) {
	baseline, err := codegen.GenerateProject(sparseProjectBundleTestSpec())
	if err != nil {
		t.Fatalf("GenerateProject() error = %v", err)
	}
	baselineRoot := writeProjectBundleModule(t, baseline)
	if output, err := compileProjectBundleModule(t.Context(), baselineRoot); err != nil {
		t.Fatalf("full generated union does not compile: %v\n%s", err, output)
	}

	changedSpec := sparseProjectBundleTestSpec()
	changedSpec.Apps[1].Schema.Models[0].Fields[0].MaxLength++
	changed, err := codegen.GenerateProject(changedSpec)
	if err != nil {
		t.Fatalf("GenerateProject(changed) error = %v", err)
	}
	if changed.SnapshotSHA256() == baseline.SnapshotSHA256() {
		t.Fatal("schema change did not change project snapshot")
	}
	changedRoot := writeProjectBundleModule(t, changed)
	if output, err := compileProjectBundleModule(t.Context(), changedRoot); err != nil {
		t.Fatalf("changed generated union does not compile: %v\n%s", err, output)
	}

	for _, oldFile := range baseline.Files() {
		oldFile := oldFile
		t.Run(oldFile.Path, func(t *testing.T) {
			mixedRoot := writeProjectBundleModule(t, changed)
			writeGeneratedTestFile(t, mixedRoot, oldFile.Path, oldFile.Source())
			if output, err := compileProjectBundleModule(t.Context(), mixedRoot); err == nil {
				t.Fatalf("mixed snapshot unexpectedly compiled\n%s", output)
			}
		})
	}
}

func sparseProjectBundleTestSpec() codegen.ProjectSpec {
	spec := projectBundleTestSpec()
	for index := range spec.Apps {
		switch spec.Apps[index].Alias {
		case "authors":
			spec.Apps[index].Schema.Models = append(spec.Apps[index].Schema.Models,
				ir.Model{
					Name: "category", GoName: "Category",
					Fields: []ir.Field{
						{Name: "id", GoName: "ID", Kind: ir.FieldAuto, PrimaryKey: true},
						{Name: "name", GoName: "Name", Kind: ir.FieldChar, MaxLength: 80},
					},
				},
				ir.Model{
					Name: "profile", GoName: "Profile",
					Fields: []ir.Field{
						{Name: "id", GoName: "ID", Kind: ir.FieldAuto, PrimaryKey: true},
						{Name: "label", GoName: "Label", Kind: ir.FieldChar, MaxLength: 80},
					},
				},
			)
		case "blog":
			spec.Apps[index].Schema.Models[0].Fields = append(spec.Apps[index].Schema.Models[0].Fields, ir.Field{
				Name: "category", GoName: "CategoryID", Kind: ir.FieldForeignKey, Nullable: true,
				Relation: &ir.ForeignKeyRelation{
					Target:      ir.ModelIdentity{AppLabel: "authors", ModelName: "category"},
					Cardinality: ir.RelationManyToOne,
					Reverse:     ir.ReverseRelation{Name: "categorized_posts"},
					OnDelete:    ir.DeleteSetNull,
				},
			})
		}
	}
	return spec
}

func writeProjectBundleModule(t *testing.T, bundle codegen.GeneratedBundle) string {
	t.Helper()
	directory := newGeneratedModule(t, "example.com/godj-project-bundle")
	for _, file := range bundle.Files() {
		writeGeneratedTestFile(t, directory, file.Path, file.Source())
	}
	writeGeneratedTestFile(t, directory, "consumer/consumer.go", []byte(`package consumer

import project "example.com/godj-project-bundle/project"

var _ = project.GoDjProjectRelationFacadeGeneratorVersion
`))
	return directory
}

func compileProjectBundleModule(ctx context.Context, directory string) ([]byte, error) {
	command := generatedGoCommand(ctx, directory, "test", "./...")
	return command.CombinedOutput()
}

func projectBundleTestSpec() codegen.ProjectSpec {
	authors, blog := testschema.Bundle()
	return codegen.ProjectSpec{
		Project: codegen.PackageSpec{PackageName: "project", ImportPath: "example.com/godj-project-bundle/project", Directory: "project"},
		Apps: []codegen.AppSpec{
			{Alias: "blog", Package: codegen.PackageSpec{PackageName: "blog", ImportPath: "example.com/godj-project-bundle/blog", Directory: "blog"}, Schema: blog},
			{Alias: "authors", Package: codegen.PackageSpec{PackageName: "authors", ImportPath: "example.com/godj-project-bundle/authors", Directory: "authors"}, Schema: authors},
		},
	}
}
