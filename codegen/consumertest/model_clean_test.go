package codegen_test

import (
	"os"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema"
)

func TestGeneratedModelCleanConsumer(t *testing.T) {
	definition, err := schema.Build(schema.Definition{AppLabel: "model_clean_probe", Models: []schema.Model{{
		Name: "contact", GoName: "Contact", Fields: []schema.Field{
			schema.CharField("code", "Code", 12, schema.Unique()),
			schema.EmailField("email", "Email"),
			schema.IntegerField("counter", "Counter", schema.Default(int64(1))),
			schema.CharField("hidden", "Hidden", 24, schema.Default("initial"), schema.Unique()),
		}, UniqueConstraints: []schema.UniqueConstraint{{Name: "code_counter", Fields: []string{"code", "counter"}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	const module = "example.com/godj-model-clean"
	bundle, err := codegen.GenerateProject(codegen.ProjectSpec{Project: codegen.PackageSpec{PackageName: "project", ImportPath: module + "/project", Directory: "project"}, Apps: []codegen.AppSpec{{Alias: "models", Package: codegen.PackageSpec{PackageName: "models", ImportPath: module + "/models", Directory: "models"}, Schema: definition}}})
	if err != nil {
		t.Fatal(err)
	}
	root := newGeneratedModule(t, module)
	for _, file := range bundle.Files() {
		writeGeneratedTestFile(t, root, file.Path, file.Source())
	}
	for _, file := range []string{"consumer_test.go", "reference.json"} {
		content, err := os.ReadFile("testdata/modelclean/" + file)
		if err != nil {
			t.Fatal(err)
		}
		writeGeneratedTestFile(t, root, "consumer/"+file, content)
	}
	cases := []string{"normalize_selected", "overwrite_duplicate", "introduce_duplicate", "rewrite_excluded_hidden", "excluded_hidden_duplicate", "change_email_after_fields", "repair_invalid_field", "mutate_and_field_error", "mutate_nonfield_error", "existing_selected", "existing_hidden_change", "missing_default", "returns_mapping"}
	backends := []string{"sqlite"}
	if strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) != "" {
		backends = append(backends, "postgres")
	}
	required := []string{"TestGeneratedModelClean"}
	for _, backend := range backends {
		prefix := "TestGeneratedModelClean/" + backend
		required = append(required, prefix)
		for _, name := range cases {
			required = append(required, prefix+"/"+name)
		}
	}
	command := generatedGoCommand(t.Context(), root, "test", "-json", "-mod=mod", "./consumer")
	assertGeneratedConsumerTests(t, runStrictGeneratedCommand(t, command), required...)
}
