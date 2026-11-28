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

func TestGeneratedSlugConsumer(t *testing.T) {
	s, err := schema.Build(schema.Definition{AppLabel: "slug_reference", Models: []schema.Model{
		{Name: "contact", GoName: "Contact", DBTable: "slug_reference_article", Fields: []schema.Field{schema.CharField("label", "Label", 32), schema.SlugField("address", "Address", schema.Nullable(), schema.AllowUnicode(true))}},
		{Name: "message", GoName: "Message", Fields: []schema.Field{schema.ForeignKey("contact", "ContactID", schema.Target("slug_reference", "contact"), schema.RelatedName("messages"), schema.Protect)}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	const module = "example.com/godj-slug"
	projectSpec := codegen.ProjectSpec{Project: codegen.PackageSpec{PackageName: "project", ImportPath: module + "/project", Directory: "project"}, Apps: []codegen.AppSpec{{Alias: "models", Package: codegen.PackageSpec{PackageName: "models", ImportPath: module + "/models", Directory: "models"}, Schema: s}}}
	bundle, err := codegen.GenerateProject(projectSpec)
	if err != nil {
		t.Fatal(err)
	}
	again, err := codegen.GenerateProject(projectSpec)
	if err != nil || len(again.Files()) != len(bundle.Files()) {
		t.Fatal("slug generation is not deterministic", err)
	}
	for i, file := range bundle.Files() {
		other := again.Files()[i]
		if file.Path != other.Path || !bytes.Equal(file.Source(), other.Source()) {
			t.Fatal("slug metadata changed between generations")
		}
	}
	root := newGeneratedModule(t, module)
	for _, file := range bundle.Files() {
		writeGeneratedTestFile(t, root, file.Path, file.Source())
	}
	consumer, err := os.ReadFile("testdata/slug/consumer_test.go")
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/consumer_test.go", consumer)
	for _, backend := range []string{"sqlite", "postgres"} {
		fixture, err := os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "internal/slugtest/testdata/slug-django61-"+backend+".json"))
		if err != nil {
			t.Fatal(err)
		}
		writeGeneratedTestFile(t, root, "consumer/"+backend+"_reference.json", fixture)
	}
	command := generatedGoCommand(t.Context(), root, "test", "-json", "-mod=mod", "./consumer")
	required := []string{"TestGeneratedSlugStorageAndHistory", "TestGeneratedSlugStorageAndHistory/sqlite", "TestGeneratedSlugStorageAndHistory/sqlite/model_form_save", "TestGeneratedSlugInputProjection"}
	if strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) != "" {
		required = append(required, "TestGeneratedSlugStorageAndHistory/postgres", "TestGeneratedSlugStorageAndHistory/postgres/model_form_save")
	}
	assertGeneratedConsumerTests(t, runStrictGeneratedCommand(t, command), required...)
}
