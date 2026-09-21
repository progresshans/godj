package codegen_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/conformance/nullableforwardproduct"
)

func TestGeneratedNestedEagerConsumer(t *testing.T) {
	schemas, err := nullableforwardproduct.NestedSchemas()
	if err != nil {
		t.Fatal(err)
	}
	const module = "example.com/godj-nested-eager"
	apps := make([]codegen.AppSpec, len(schemas))
	for i, s := range schemas {
		apps[i] = codegen.AppSpec{Alias: s.AppLabel, Package: codegen.PackageSpec{PackageName: s.AppLabel, ImportPath: module + "/" + s.AppLabel, Directory: s.AppLabel}, Schema: s}
	}
	bundle, err := codegen.GenerateProject(codegen.ProjectSpec{Project: codegen.PackageSpec{PackageName: "project", ImportPath: module + "/project", Directory: "project"}, Apps: apps})
	if err != nil {
		t.Fatal(err)
	}
	root := newGeneratedModule(t, module)
	for _, file := range bundle.Files() {
		writeGeneratedTestFile(t, root, file.Path, file.Source())
	}
	consumer, err := os.ReadFile("testdata/nestedeager/consumer_test.go")
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/consumer_test.go", consumer)
	failures, err := os.ReadFile("testdata/nestedeager/failures_test.go")
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/failures_test.go", failures)
	fixture, err := os.ReadFile("testdata/nestedforward/fixture_test.go")
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/fixture_test.go", bytes.ReplaceAll(fixture, []byte("example.com/godj-nested-forward"), []byte(module)))
	reference, err := os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "orm/testdata/nested-eager-django61.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/reference.json", reference)
	command := generatedGoCommand(t.Context(), root, "test", "-json", "-mod=mod", "./consumer")
	assertGeneratedConsumerTests(t, runStrictGeneratedCommand(t, command), "TestGeneratedNestedEagerReference", "TestGeneratedNestedEagerInvalidSelections", "TestGeneratedNestedEagerOwnership", "TestGeneratedNestedEagerFailures", "TestGeneratedNestedEagerSelectedGraph", "TestGeneratedNestedEagerAssignmentInvalidatesOnlyChangedEdge", "TestGeneratedNestedEagerChildAssignmentPreservesUnvisitedSibling")
	writeGeneratedTestFile(t, root, "wrong/wrong.go", []byte(`package wrong
import (
 "example.com/godj-nested-eager/blog"
 "example.com/godj-nested-eager/people"
 "example.com/godj-nested-eager/directory"
 "github.com/progresshans/godj/orm"
)
func bad(prefix orm.RelatedSelect[blog.Post,people.Person],suffix orm.RelatedSelect[directory.Team,directory.Organization]){
 _ = prefix.WithChildren(suffix)
}
`))
	output, err := generatedGoCommand(t.Context(), root, "build", "-mod=mod", "./wrong").CombinedOutput()
	if err == nil || !bytes.Contains(output, []byte("WithChildren")) || !bytes.Contains(output, []byte("prepareRelatedSelection")) {
		t.Fatalf("intermediate Go type mismatch was not rejected: %v\n%s", err, output)
	}
}
