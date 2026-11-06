package codegen_test

import (
	"os"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema"
)

func TestGeneratedModelFormDatabaseConsumer(t *testing.T) {
	definition, err := schema.Build(schema.Definition{AppLabel: "postclean_probe", Models: []schema.Model{{
		Name: "contact", GoName: "Contact", Fields: []schema.Field{
			schema.CharField("key", "Key", 8, schema.Unique()),
			schema.EmailField("address", "Address", schema.Blank(), schema.Choices(schema.Choice("legacy", "Legacy"), schema.Choice("a@example.com", "A"), schema.Choice("b@example.com", "B"))),
			schema.IntegerField("counter", "Counter", schema.Default(int64(1))),
		}, UniqueConstraints: []schema.UniqueConstraint{{Name: "address_counter", Fields: []string{"address", "counter"}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	const module = "example.com/godj-model-postclean"
	bundle, err := codegen.GenerateProject(codegen.ProjectSpec{Project: codegen.PackageSpec{PackageName: "project", ImportPath: module + "/project", Directory: "project"}, Apps: []codegen.AppSpec{{Alias: "models", Package: codegen.PackageSpec{PackageName: "models", ImportPath: module + "/models", Directory: "models"}, Schema: definition}}})
	if err != nil {
		t.Fatal(err)
	}
	root := newGeneratedModule(t, module)
	for _, file := range bundle.Files() {
		writeGeneratedTestFile(t, root, file.Path, file.Source())
	}
	for _, file := range []string{"consumer_test.go", "reference.json"} {
		content, err := os.ReadFile("testdata/modelpostclean/" + file)
		if err != nil {
			t.Fatal(err)
		}
		writeGeneratedTestFile(t, root, "consumer/"+file, content)
	}
	cases := []string{"valid", "choice_email_invalid", "choice_missing", "two_field_errors", "duplicate_and_email", "duplicate_tuple", "model_clean", "counter_error", "override_email", "override_choice", "override_blank", "optional_required_model", "subset", "excluded_extra", "omitted_default", "same_row"}
	backends := []string{"sqlite"}
	if strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) != "" {
		backends = append(backends, "postgres")
	}
	required := []string{"TestGeneratedModelFormDatabase"}
	for _, backend := range backends {
		prefix := "TestGeneratedModelFormDatabase/" + backend
		required = append(required, prefix)
		for _, name := range cases {
			required = append(required, prefix+"/"+name)
		}
	}
	command := generatedGoCommand(t.Context(), root, "test", "-json", "-mod=mod", "./consumer")
	assertGeneratedConsumerTests(t, runStrictGeneratedCommand(t, command), required...)
}
