package codegen_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema"
)

func TestGeneratedFileConsumer(t *testing.T) {
	s, err := schema.Build(schema.Definition{AppLabel: "file_reference", Models: []schema.Model{
		{Name: "document", GoName: "Document", Fields: []schema.Field{
			schema.CharField("title", "Title", 40, schema.Unique()),
			schema.FileField("file", "File", schema.MaxLength(40), schema.Blank(), schema.Default(""), schema.Unique()),
			schema.FileField("optional", "Optional", schema.Nullable(), schema.Blank()),
			schema.CharField("owner", "Owner", 40, schema.Default("alice")),
		}},
		{Name: "archive", GoName: "Archive", Fields: []schema.Field{schema.FileField("reference", "Reference")}},
		{Name: "link", GoName: "Link", Fields: []schema.Field{schema.ForeignKey("document", "DocumentID", schema.Target("file_reference", "document"), schema.RelatedName("links"), schema.Protect)}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	const module = "example.com/godj-files"
	spec := codegen.ProjectSpec{Project: codegen.PackageSpec{PackageName: "project", ImportPath: module + "/project", Directory: "project"}, Apps: []codegen.AppSpec{{Alias: "models", Package: codegen.PackageSpec{PackageName: "models", ImportPath: module + "/models", Directory: "models"}, Schema: s}}}
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	again, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	files, repeated := bundle.Files(), again.Files()
	if len(files) == 0 || len(files) != len(repeated) {
		t.Fatal("incomplete generated file bundle")
	}
	root := newGeneratedModule(t, module)
	for i, file := range files {
		if file.Path != repeated[i].Path || !bytes.Equal(file.Source(), repeated[i].Source()) {
			t.Fatal("FileField output is not deterministic")
		}
		writeGeneratedTestFile(t, root, file.Path, file.Source())
	}
	for _, name := range []string{"consumer_test.go", "formset_test.go", "serving_test.go"} {
		consumer, err := os.ReadFile("testdata/files/" + name)
		if err != nil {
			t.Fatal(err)
		}
		writeGeneratedTestFile(t, root, "consumer/"+name, consumer)
	}
	reference, err := os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "forms/model/testdata/model-file-django61.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/reference.json", reference)
	required := []string{"TestGeneratedFileProjection", "TestGeneratedFileStorageAndHistory", "TestGeneratedFileStorageAndHistory/sqlite", "TestGeneratedFileStorageAndHistory/sqlite/formset", "TestGeneratedFileStorageAndHistory/sqlite/serving", "TestGeneratedFileStorageAndHistory/sqlite/serving/filesystem", "TestGeneratedFileStorageAndHistory/sqlite/serving/memory"}
	if strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) != "" {
		required = append(required, "TestGeneratedFileStorageAndHistory/postgres", "TestGeneratedFileStorageAndHistory/postgres/formset", "TestGeneratedFileStorageAndHistory/postgres/serving", "TestGeneratedFileStorageAndHistory/postgres/serving/filesystem", "TestGeneratedFileStorageAndHistory/postgres/serving/memory")
	}
	command := generatedGoCommand(t.Context(), root, "test", "-json", "-mod=mod", "./consumer")
	assertGeneratedConsumerTests(t, runStrictGeneratedCommand(t, command), required...)
}
