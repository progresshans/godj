package codegen_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/progresshans/godj/schema"
)

func TestGeneratedScalarMembershipConsumer(t *testing.T) {
	definition, err := schema.Build(schema.Definition{AppLabel: "membership", Models: []schema.Model{{Name: "entry", GoName: "Entry", Fields: []schema.Field{
		schema.CharField("name", "Name", 20),
		schema.TextField("note", "Note", schema.Nullable()),
		schema.IntegerField("rank", "Rank", schema.Nullable()),
		schema.DateTimeField("at", "At", schema.Nullable()),
		schema.BooleanField("active", "Active", schema.Default(false)),
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	root := newGeneratedModule(t, "example.com/godj-membership")
	writeGeneratedAppFixture(t, root, "models", "models", definition, appFixtureFeatures{projection: true, object: true})
	consumer, err := os.ReadFile("testdata/membership/consumer_test.go")
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/consumer_test.go", consumer)
	reference, err := os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "orm/testdata/in-django61.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/reference.json", reference)
	command := generatedGoCommand(t.Context(), root, "test", "-json", "-mod=mod", "./consumer")
	assertGeneratedConsumerTests(t, runStrictGeneratedCommand(t, command),
		"TestGeneratedMembershipMatchesReference", "TestGeneratedMembershipEvaluationAndOwnership", "TestGeneratedMembershipRejectsInvalidInputs", "TestGeneratedMembershipSessionLifetime")
}
