package codegen_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema"
)

func TestGeneratedModelFormSaveConsumer(t *testing.T) {
	definition, err := schema.Build(schema.Definition{AppLabel: "saveprobe", Models: []schema.Model{
		{Name: "label", GoName: "Label", Fields: []schema.Field{schema.CharField("code", "Code", 12)}},
		{Name: "article", GoName: "Article", Fields: []schema.Field{schema.CharField("title", "Title", 24), schema.CharField("hidden", "Hidden", 24, schema.Default("initial"))}, ManyToMany: []schema.ManyToManyField{
			schema.ManyToMany("labels", "Labels", schema.Target("saveprobe", "label"), schema.RelatedName("labeled_articles"), schema.ManyToManyBlank()),
			schema.ManyToMany("reviewers", "Reviewers", schema.Target("saveprobe", "label"), schema.RelatedName("reviewed_articles"), schema.ManyToManyBlank()),
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	const module = "example.com/godj-model-save"
	bundle, err := codegen.GenerateProject(codegen.ProjectSpec{Project: codegen.PackageSpec{PackageName: "project", ImportPath: module + "/project", Directory: "project"}, Apps: []codegen.AppSpec{{Alias: "models", Package: codegen.PackageSpec{PackageName: "models", ImportPath: module + "/models", Directory: "models"}, Schema: definition}}})
	if err != nil {
		t.Fatal(err)
	}
	root := newGeneratedModule(t, module)
	for _, file := range bundle.Files() {
		writeGeneratedTestFile(t, root, file.Path, file.Source())
	}
	var fixture struct {
		Cases []struct {
			Name string `json:"name"`
		} `json:"cases"`
	}
	for _, file := range []string{"consumer_test.go", "reference.json"} {
		content, err := os.ReadFile("testdata/modelsave/" + file)
		if err != nil {
			t.Fatal(err)
		}
		writeGeneratedTestFile(t, root, "consumer/"+file, content)
		if file == "reference.json" {
			if err := json.Unmarshal(content, &fixture); err != nil || len(fixture.Cases) != 15 {
				t.Fatal("native save fixture", err)
			}
		}
	}
	backends := []string{"sqlite"}
	if strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) != "" {
		backends = append(backends, "postgres")
	}
	required := []string{"TestGeneratedModelFormSave"}
	for _, backend := range backends {
		prefix := "TestGeneratedModelFormSave/" + backend
		required = append(required, prefix)
		for _, test := range fixture.Cases {
			required = append(required, prefix+"/"+test.Name)
		}
		for _, name := range []string{"transaction_guard_denied", "transaction_guard_stale", "transaction_choice_changed", "transaction_callback_failure_after_write", "expired_session", "canceled"} {
			required = append(required, prefix+"/"+name)
		}
	}
	command := generatedGoCommand(t.Context(), root, "test", "-json", "-mod=mod", "./consumer")
	assertGeneratedConsumerTests(t, runStrictGeneratedCommand(t, command), required...)
}
