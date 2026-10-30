package codegen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema"
)

func TestGeneratedURLConsumer(t *testing.T) {
	s, err := schema.Build(schema.Definition{AppLabel: "url_reference", Models: []schema.Model{
		{Name: "contact", GoName: "Contact", DBTable: "url_reference_contact", Fields: []schema.Field{schema.CharField("label", "Label", 32), schema.URLField("address", "Address", schema.Nullable(), schema.Unique())}},
		{Name: "message", GoName: "Message", Fields: []schema.Field{schema.ForeignKey("contact", "ContactID", schema.Target("url_reference", "contact"), schema.RelatedName("messages"), schema.Protect)}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	const module = "example.com/godj-url"
	bundle, err := codegen.GenerateProject(codegen.ProjectSpec{Project: codegen.PackageSpec{PackageName: "project", ImportPath: module + "/project", Directory: "project"}, Apps: []codegen.AppSpec{{Alias: "models", Package: codegen.PackageSpec{PackageName: "models", ImportPath: module + "/models", Directory: "models"}, Schema: s}}})
	if err != nil {
		t.Fatal(err)
	}
	root := newGeneratedModule(t, module)
	for _, file := range bundle.Files() {
		writeGeneratedTestFile(t, root, file.Path, file.Source())
	}
	consumer, err := os.ReadFile("testdata/url/consumer_test.go")
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/consumer_test.go", consumer)
	for _, backend := range []string{"sqlite", "postgres"} {
		fixture, err := os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "internal/urltest/testdata/url-django61-"+backend+".json"))
		if err != nil {
			t.Fatal(err)
		}
		writeGeneratedTestFile(t, root, "consumer/"+backend+"_reference.json", fixture)
	}
	command := generatedGoCommand(t.Context(), root, "test", "-json", "-mod=mod", "./consumer")
	required := []string{"TestGeneratedURLStorageAndHistory", "TestGeneratedURLStorageAndHistory/sqlite", "TestGeneratedURLStorageAndHistory/sqlite/model_form_save", "TestGeneratedURLInputProjection"}
	if strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) != "" {
		required = append(required, "TestGeneratedURLStorageAndHistory/postgres", "TestGeneratedURLStorageAndHistory/postgres/model_form_save")
	}
	assertGeneratedConsumerTests(t, runStrictGeneratedCommand(t, command), required...)
}
